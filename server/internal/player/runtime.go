package player

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	mailv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/mail"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/actor"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

const (
	ProtocolVersion uint32 = 1
	LocalOwnerEpoch uint64 = 1
	DefaultZoneID          = "zone-local"
)

var (
	ErrNotOwner          = errors.New("not owner")
	ErrForbiddenTarget   = errors.New("target player differs from caller")
	ErrInvalidEnvelope   = errors.New("invalid websocket envelope")
	ErrUnsupportedAction = errors.New("unsupported action")
	ErrRuntimeClosed     = errors.New("player runtime is closed")
)

const (
	actorLifecycleLoading uint32 = iota
	actorLifecycleReady
	actorLifecycleFailed
	actorLifecycleClosing
	actorLifecycleEvicting
)

var errActorGone = errors.New("player actor is leaving")

// FarmViewDispatcher 接收 Actor mailbox 内已构造好的公开农场 Patch。
// 生产实现是 farmview.Dispatcher；业务路径不得查询访客或调用 Gate Push。
type FarmViewDispatcher interface {
	Enqueue(ownerPlayerID uint64, patch *wsv1.FarmViewPatch)
	Close()
}

// StealableNotifier best-effort notifies InfoSvr when a plot becomes stealable.
// Failures must not roll back Actor authority.
type StealableNotifier interface {
	NotifyOwnerPlotStealable(ctx context.Context, ownerPlayerID uint64, plotID uint32, notificationID string) error
}

// GiftMailer creates gift mails for SendFriendGift.
type GiftMailer interface {
	CreateGiftMail(ctx context.Context, request *mailv1.CreateGiftMailRequest) (*mailv1.CreateGiftMailResponse, error)
}

// PlayerPresence reports whether a player still has a live Gate connection.
type PlayerPresence interface {
	Has(playerID uint64) bool
}

// FarmObservers reports whether an owner's farm currently has live visitors.
type FarmObservers interface {
	HasVisitors(ownerPlayerID uint64, now time.Time) bool
}

type runtimeActor struct {
	mailbox           *actor.Mailbox
	state             *State
	persistedRevision uint64
	persistedToken    StoreToken

	lifecycle     atomic.Uint32
	activationErr atomic.Value // error; published before Failed is visible

	loadCtx    context.Context
	loadCancel context.CancelFunc

	// syncPending holds one pendingSyncStep per synchronous Saga step whose
	// mutation is in this Actor's memory but not yet proven durable (see
	// sync_persist.go). It is mailbox-owned, ephemeral and never persisted.
	syncPending map[string]pendingSyncStep

	// farmViewEpoch/farmViewSeq are ephemeral, in-memory only (see
	// BuildPublicFarmSnapshot): they identify this Actor incarnation to
	// friend-farm visitors and are never persisted or restored from a
	// checkpoint.
	farmViewEpoch []byte
	farmViewSeq   uint64

	// tickGeneration invalidates stale heap entries when a deadline is
	// rescheduled or cancelled. It is not persisted.
	tickGeneration atomic.Uint64

	// lastAccessAtMS is the last admitted external request/visit time.
	// Scheduler ticks must not update it. Units: Unix milliseconds.
	lastAccessAtMS atomic.Int64
	quickSummary   atomic.Pointer[FarmQuickSummary]
}

type DrainedPlayer struct {
	PlayerID           uint64 `json:"player_id"`
	OwnerEpoch         uint64 `json:"owner_epoch"`
	CheckpointRevision uint64 `json:"checkpoint_revision"`
}

// Runtime lazily activates one in-memory Actor per player. Without a store it
// retains the explicit development-only default-state behavior.
type Runtime struct {
	mu            sync.Mutex
	actors        map[uint64]*runtimeActor
	dirtyRevision map[uint64]uint64
	store         CheckpointStore
	pushForwarder PushForwarder
	farmView      FarmViewDispatcher
	stealable     StealableNotifier
	giftMailer    GiftMailer
	accountNamer  AccountNamer
	presence      PlayerPresence
	observers     FarmObservers
	quickInfo     FarmQuickInfoNotifier
	config        atomic.Pointer[ConfigSnapshot]
	now           atomic.Value    // stores func() time.Time
	randBPS       func() uint32   // 可注入；返回值会对 10000 取模（旧护主概率路径）
	randIntn      func(n int) int // 可注入；返回 [0,n)；护主罚款与投虫金币
	backgroundCtx context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	shardLocks    [routing.ShardCount]sync.RWMutex
	deadlines     *actorDeadlineBook
}

func NewRuntime() *Runtime {
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &Runtime{
		actors:        make(map[uint64]*runtimeActor),
		dirtyRevision: make(map[uint64]uint64),
		backgroundCtx: ctx,
		cancel:        cancel,
		deadlines:     newActorDeadlineBook(),
	}
	runtime.SetNow(time.Now)
	runtime.config.Store(NewDevelopmentConfigSnapshot())
	runtime.wg.Add(1)
	go runtime.runDeadlineScheduler(ctx)
	return runtime
}

func NewRuntimeWithStore(store CheckpointStore) (*Runtime, error) {
	if store == nil {
		return nil, errors.New("checkpoint store is required")
	}
	runtime := NewRuntime()
	runtime.store = store
	runtime.wg.Add(1)
	go runtime.runDirtyFlusher(runtime.backgroundCtx)
	return runtime, nil
}

func (r *Runtime) ReplaceConfig(snapshot *ConfigSnapshot) error {
	if snapshot == nil || snapshot.Version() == 0 {
		return errors.New("config snapshot is required")
	}
	r.config.Store(snapshot)
	return nil
}

// CurrentConfig exposes the live ConfigSnapshot read-only, for callers
// outside package player that need to resolve config-driven facts (e.g.
// cmd/zone resolving ConfigSnapshot.SoleStealableCrop before starting a
// steal interaction) without duplicating Runtime's config storage.
func (r *Runtime) CurrentConfig() *ConfigSnapshot {
	return r.config.Load()
}

// SetNow replaces the Runtime clock. Safe for concurrent use by the deadline
// scheduler and tests that inject fixed time.
func (r *Runtime) SetNow(fn func() time.Time) {
	if fn == nil {
		fn = time.Now
	}
	r.now.Store(fn)
}

func (r *Runtime) currentTime() time.Time {
	if fn, ok := r.now.Load().(func() time.Time); ok && fn != nil {
		return fn()
	}
	return time.Now()
}

func (r *Runtime) SetPushForwarder(forwarder PushForwarder) error {
	if forwarder == nil {
		return errors.New("push forwarder is required")
	}
	r.mu.Lock()
	r.pushForwarder = forwarder
	r.mu.Unlock()
	return nil
}

// SetFarmViewDispatcher 接入有界公开农场广播投递器。
// 未配置时仍会推进 farm_view_seq（保证快照正确），只是不对外推送。
func (r *Runtime) SetFarmViewDispatcher(dispatcher FarmViewDispatcher) error {
	if dispatcher == nil {
		return errors.New("farm view dispatcher is required")
	}
	r.mu.Lock()
	r.farmView = dispatcher
	r.mu.Unlock()
	return nil
}

func (r *Runtime) SetStealableNotifier(notifier StealableNotifier) {
	r.mu.Lock()
	r.stealable = notifier
	r.mu.Unlock()
}

func (r *Runtime) SetGiftMailer(mailer GiftMailer) {
	r.mu.Lock()
	r.giftMailer = mailer
	r.mu.Unlock()
}

// AccountNamer resolves a trusted display name for gift Outbox payloads.
type AccountNamer interface {
	AccountName(ctx context.Context, playerID uint64) (string, bool, error)
}

func (r *Runtime) SetAccountNamer(namer AccountNamer) {
	r.mu.Lock()
	r.accountNamer = namer
	r.mu.Unlock()
}

func (r *Runtime) SetPlayerPresence(presence PlayerPresence) {
	r.mu.Lock()
	r.presence = presence
	r.mu.Unlock()
}

func (r *Runtime) SetFarmObservers(observers FarmObservers) {
	r.mu.Lock()
	r.observers = observers
	r.mu.Unlock()
}

func (r *Runtime) markDirty(playerID, checkpointRevision uint64) {
	if r.store == nil {
		return
	}
	r.mu.Lock()
	if checkpointRevision > r.dirtyRevision[playerID] {
		r.dirtyRevision[playerID] = checkpointRevision
	}
	r.mu.Unlock()
}

func (r *Runtime) runDirtyFlusher(ctx context.Context) {
	defer r.wg.Done()
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = r.flushDirty(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (r *Runtime) flushDirty(ctx context.Context) error {
	r.mu.Lock()
	playerIDs := make([]uint64, 0, len(r.dirtyRevision))
	for playerID := range r.dirtyRevision {
		playerIDs = append(playerIDs, playerID)
	}
	r.mu.Unlock()
	for _, playerID := range playerIDs {
		if err := r.flushPlayer(ctx, playerID); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) flushPlayer(ctx context.Context, playerID uint64) error {
	shardID := routing.ShardForPlayer(playerID)
	r.shardLocks[shardID].RLock()
	defer r.shardLocks[shardID].RUnlock()
	return r.flushPlayerLocked(ctx, playerID)
}

func (r *Runtime) flushPlayerLocked(ctx context.Context, playerID uint64) error {
	r.mu.Lock()
	a := r.actors[playerID]
	targetRevision, dirty := r.dirtyRevision[playerID]
	r.mu.Unlock()
	if a == nil || !dirty {
		return nil
	}
	if a.lifecycle.Load() != actorLifecycleReady || a.state == nil {
		return nil
	}
	var checkpoint *datav1.PlayerCheckpointV1
	var expectedRevision uint64
	var expectedToken StoreToken
	var checkpointErr error
	if err := a.mailbox.Do(ctx, func() {
		if a.lifecycle.Load() != actorLifecycleReady || a.state == nil {
			return
		}
		if a.state.CheckpointRevision < targetRevision {
			return
		}
		checkpoint, checkpointErr = a.state.Checkpoint()
		expectedRevision = a.persistedRevision
		expectedToken = cloneStoreToken(a.persistedToken)
	}); err != nil {
		return fmt.Errorf("snapshot dirty player %d: %w", playerID, err)
	}
	if checkpointErr != nil {
		return fmt.Errorf("build dirty checkpoint for player %d: %w", playerID, checkpointErr)
	}
	if checkpoint == nil {
		return fmt.Errorf("snapshot dirty player %d failed", playerID)
	}
	result, err := r.store.SaveCAS(ctx, CheckpointWrite{
		Checkpoint:       checkpoint,
		ExpectedRevision: expectedRevision,
		ExpectedToken:    expectedToken,
	})
	if writeErr := checkpointWriteError(result, err); writeErr != nil {
		return fmt.Errorf("flush dirty player %d: %w", playerID, writeErr)
	}
	if err := a.mailbox.Do(ctx, func() {
		if a.persistedRevision == expectedRevision &&
			bytes.Equal(a.persistedToken, expectedToken) {
			a.persistedRevision = checkpoint.CheckpointRevision
			a.persistedToken = cloneStoreToken(result.NewToken)
		}
	}); err != nil {
		return fmt.Errorf("acknowledge dirty player %d: %w", playerID, err)
	}
	r.mu.Lock()
	if r.dirtyRevision[playerID] <= checkpoint.CheckpointRevision {
		delete(r.dirtyRevision, playerID)
	}
	r.mu.Unlock()
	return nil
}

func (a *runtimeActor) setActivationErr(err error) {
	if err != nil {
		a.activationErr.Store(err)
	}
}

func (a *runtimeActor) getActivationErr() error {
	if v := a.activationErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}

func (r *Runtime) actorFor(
	ctx context.Context,
	playerID uint64,
	ownerEpoch uint64,
) (*runtimeActor, error) {
	for {
		if err := r.errIfClosed(); err != nil {
			return nil, err
		}
		a, err := r.getOrCreateLoadingActor(playerID, ownerEpoch)
		if err != nil {
			return nil, err
		}
		if err := r.waitForActorReady(ctx, a); err != nil {
			if errors.Is(err, errActorGone) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(2 * time.Millisecond):
				}
				continue
			}
			return nil, err
		}
		if a.lifecycle.Load() != actorLifecycleReady || a.state == nil {
			err := a.getActivationErr()
			if err == nil {
				err = errors.New("player actor activation failed")
			}
			return nil, err
		}
		if a.state.OwnerEpoch != ownerEpoch {
			return nil, ErrNotOwner
		}
		a.touchAccess(r.currentTime())
		return a, nil
	}
}

func (r *Runtime) errIfClosed() error {
	select {
	case <-r.backgroundCtx.Done():
		return ErrRuntimeClosed
	default:
		return nil
	}
}

func (r *Runtime) getOrCreateLoadingActor(playerID, ownerEpoch uint64) (*runtimeActor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing := r.actors[playerID]; existing != nil {
		return existing, nil
	}
	if err := r.errIfClosed(); err != nil {
		return nil, err
	}
	loadCtx, loadCancel := context.WithCancel(r.backgroundCtx)
	created := &runtimeActor{
		mailbox:    actor.NewMailbox(64),
		loadCtx:    loadCtx,
		loadCancel: loadCancel,
	}
	created.lifecycle.Store(actorLifecycleLoading)
	// Enqueue Load/init as the first mailbox job BEFORE publishing the Actor so
	// concurrent waiters cannot slip an empty barrier ahead of activation.
	if err := created.mailbox.Submit(func() {
		r.activateActor(created, playerID, ownerEpoch)
	}); err != nil {
		loadCancel()
		created.mailbox.Close()
		return nil, fmt.Errorf("enqueue player activation: %w", err)
	}
	r.actors[playerID] = created
	return created, nil
}

func (r *Runtime) waitForActorReady(ctx context.Context, a *runtimeActor) error {
	for {
		switch a.lifecycle.Load() {
		case actorLifecycleReady:
			return nil
		case actorLifecycleFailed:
			if err := a.getActivationErr(); err != nil {
				return err
			}
			return errors.New("player actor activation failed")
		case actorLifecycleClosing:
			return ErrRuntimeClosed
		case actorLifecycleEvicting:
			return errActorGone
		}
		// Loading: queue behind the activation job on the same mailbox, then
		// re-check lifecycle (activation may have failed after the barrier).
		if err := a.mailbox.Do(ctx, func() {}); err != nil {
			switch a.lifecycle.Load() {
			case actorLifecycleReady:
				return nil
			case actorLifecycleFailed:
				if actErr := a.getActivationErr(); actErr != nil {
					return actErr
				}
				return errors.New("player actor activation failed")
			case actorLifecycleClosing:
				return ErrRuntimeClosed
			case actorLifecycleEvicting:
				return errActorGone
			}
			return err
		}
	}
}

func (r *Runtime) activateActor(a *runtimeActor, playerID, ownerEpoch uint64) {
	if a.lifecycle.Load() != actorLifecycleLoading {
		return
	}
	fail := func(err error) {
		a.setActivationErr(err)
		a.lifecycle.Store(actorLifecycleFailed)
		r.removeActorIfSame(playerID, a)
		// Close asynchronously: we are inside the mailbox worker and Close waits
		// for the worker loop to exit.
		go a.mailbox.Close()
	}

	state := NewDevelopmentState(playerID)
	persistedRevision := state.CheckpointRevision
	var persistedToken StoreToken
	if r.store != nil {
		loaded, err := r.store.Load(a.loadCtx, playerID)
		switch {
		case err == nil:
			if loaded.State == nil {
				fail(errors.New("loaded player checkpoint has no state"))
				return
			}
			state = loaded.State
			persistedRevision = loaded.PersistedRevision
			persistedToken = cloneStoreToken(loaded.Token)
			// A store may repair the loaded state — the Tcaplus durable store
			// drops Outbox entries the Relay already delivered — and reports
			// the repair as a revision ahead of the persisted row, which the
			// dirty flush at the end of activation writes back. Only a state
			// behind the row is incoherent and must not be served.
			if state.CheckpointRevision < persistedRevision {
				fail(errors.New("loaded checkpoint state is behind the persisted revision"))
				return
			}
		case errors.Is(err, ErrCheckpointNotFound):
			initialized, initRevision, initToken, initErr := r.createInitialPlayerCheckpoint(
				a.loadCtx, playerID, ownerEpoch,
			)
			if initErr != nil {
				fail(initErr)
				return
			}
			state = initialized
			persistedRevision = initRevision
			persistedToken = initToken
		default:
			// 临时错误 / 损坏等绝不能当成新玩家。
			fail(fmt.Errorf("load player checkpoint: %w", err))
			return
		}
	}
	if state.OwnerEpoch > ownerEpoch {
		fail(ErrNotOwner)
		return
	}
	if state.OwnerEpoch != ownerEpoch {
		if state.CheckpointRevision == math.MaxUint64 {
			fail(errors.New("checkpoint revision exhausted during epoch adoption"))
			return
		}
		state.OwnerEpoch = ownerEpoch
		state.CheckpointRevision++
		state.UpdatedAtMS = r.currentTime().UTC().UnixMilli()
	}
	if state.ensureInitialPlots() {
		if state.CheckpointRevision == math.MaxUint64 {
			fail(errors.New("checkpoint revision exhausted during plot backfill"))
			return
		}
		state.CheckpointRevision++
		state.UpdatedAtMS = r.currentTime().UTC().UnixMilli()
	}
	if _, err := state.materializeDueMaturities(r.currentTime()); err != nil {
		fail(fmt.Errorf("activate player maturity: %w", err))
		return
	}
	farmViewEpoch, err := newFarmViewEpoch()
	if err != nil {
		fail(fmt.Errorf("mint farm view epoch: %w", err))
		return
	}
	a.state = state
	a.persistedRevision = persistedRevision
	a.persistedToken = persistedToken
	a.farmViewEpoch = farmViewEpoch
	a.touchAccess(r.currentTime())
	a.lifecycle.Store(actorLifecycleReady)
	if state.CheckpointRevision > persistedRevision {
		r.markDirty(playerID, state.CheckpointRevision)
	}
	r.refreshActorDeadlineOwned(playerID, a)
}

// createInitialPlayerCheckpoint 仅在 Load 明确 NotFound 时调用。
// 必须先 CreateInitial 持久化成功，调用方才能把 Actor 标为 Ready。
func (r *Runtime) createInitialPlayerCheckpoint(
	ctx context.Context,
	playerID, ownerEpoch uint64,
) (*State, uint64, StoreToken, error) {
	creator, ok := r.store.(InitialCheckpointStore)
	if !ok || creator == nil {
		return nil, 0, nil, ErrInitialCheckpointUnsupported
	}
	checkpoint := NewInitialCheckpoint(playerID, r.currentTime().UTC())
	checkpoint.OwnerEpoch = ownerEpoch
	result, err := creator.CreateInitial(ctx, checkpoint)
	// 创建响应丢失等不确定结果：先对账 Load，内容一致则按已应用恢复。
	if result.Status == CheckpointWriteRetryableFailure ||
		errors.Is(err, ErrCheckpointRetryable) {
		if reconciled, rev, token, reconcileErr := r.reconcileInitialCheckpoint(
			ctx, checkpoint,
		); reconcileErr == nil {
			return reconciled, rev, token, nil
		}
	}
	if writeErr := checkpointWriteError(result, err); writeErr != nil {
		if errors.Is(writeErr, ErrCheckpointFenced) {
			return nil, 0, nil, ErrNotOwner
		}
		return nil, 0, nil, fmt.Errorf("create initial player checkpoint: %w", writeErr)
	}
	state, err := StateFromCheckpoint(checkpoint)
	if err != nil {
		return nil, 0, nil, err
	}
	return state, checkpoint.CheckpointRevision, cloneStoreToken(result.NewToken), nil
}

func (r *Runtime) reconcileInitialCheckpoint(
	ctx context.Context,
	expected *datav1.PlayerCheckpointV1,
) (*State, uint64, StoreToken, error) {
	loaded, err := r.store.Load(ctx, expected.PlayerId)
	if err != nil {
		return nil, 0, nil, err
	}
	same, compareErr := loadedMatchesCheckpoint(loaded, expected)
	if compareErr != nil {
		return nil, 0, nil, compareErr
	}
	if !same {
		return nil, 0, nil, ErrCheckpointCorruptConflict
	}
	return loaded.State, loaded.PersistedRevision, cloneStoreToken(loaded.Token), nil
}

func (r *Runtime) removeActorIfSame(playerID uint64, a *runtimeActor) {
	r.mu.Lock()
	if r.actors[playerID] == a {
		delete(r.actors, playerID)
		delete(r.dirtyRevision, playerID)
		r.cancelActorDeadline(playerID)
	}
	r.mu.Unlock()
	if a.loadCancel != nil {
		a.loadCancel()
	}
}

// Handle validates the authenticated internal command boundary and executes the
// snapshot projection on the target player's mailbox.
func (r *Runtime) Handle(ctx context.Context, callerPlayerID, ownerEpoch uint64, request *wsv1.WsEnvelope) (*wsv1.WsEnvelope, error) {
	if ownerEpoch == 0 {
		return nil, ErrNotOwner
	}
	if request == nil ||
		request.ProtocolVersion != ProtocolVersion ||
		request.MessageKind != wsv1.MessageKind_REQUEST ||
		request.RequestId == "" {
		return nil, ErrInvalidEnvelope
	}
	if request.TargetPlayerId == 0 || request.TargetPlayerId != callerPlayerID {
		return nil, ErrForbiddenTarget
	}
	shardID := routing.ShardForPlayer(request.TargetPlayerId)
	r.shardLocks[shardID].RLock()
	defer r.shardLocks[shardID].RUnlock()
	serverNow := r.currentTime()
	config := r.config.Load()
	if config == nil {
		return nil, errors.New("Zone configuration is unavailable")
	}
	isSnapshot := request.Action == wsv1.Action_GET_PLAYER_SNAPSHOT &&
		request.GetGetPlayerSnapshotRequest() != nil
	isGetShop := request.Action == wsv1.Action_GET_SHOP &&
		request.GetGetShopRequest() != nil
	isBuySeeds := request.Action == wsv1.Action_BUY_SEEDS &&
		request.GetBuySeedsRequest() != nil
	isBuyFertilizer := request.Action == wsv1.Action_BUY_FERTILIZER &&
		request.GetBuyFertilizerRequest() != nil
	isPlant := request.Action == wsv1.Action_PLANT &&
		request.GetPlantRequest() != nil
	isApplyFertilizer := request.Action == wsv1.Action_APPLY_FERTILIZER &&
		request.GetApplyFertilizerRequest() != nil
	isHarvest := request.Action == wsv1.Action_HARVEST &&
		request.GetHarvestRequest() != nil
	isCleanPlot := request.Action == wsv1.Action_CLEAN_PLOT &&
		request.GetCleanPlotRequest() != nil
	isCatchPest := request.Action == wsv1.Action_CATCH_PEST &&
		request.GetCatchPestRequest() != nil
	isSellCrop := request.Action == wsv1.Action_SELL_CROP &&
		request.GetSellCropRequest() != nil
	isClaimReward := request.Action == wsv1.Action_CLAIM_CHAPTER_REWARD &&
		request.GetClaimChapterRewardRequest() != nil
	isGetPetPanel := request.Action == wsv1.Action_GET_PET_PANEL &&
		request.GetGetPetPanelRequest() != nil
	isBuyPet := request.Action == wsv1.Action_BUY_PET &&
		request.GetBuyPetRequest() != nil
	isDeployPet := request.Action == wsv1.Action_DEPLOY_PET &&
		request.GetDeployPetRequest() != nil
	isBuyPetFood := request.Action == wsv1.Action_BUY_PET_FOOD &&
		request.GetBuyPetFoodRequest() != nil
	isFeedPet := request.Action == wsv1.Action_FEED_PET &&
		request.GetFeedPetRequest() != nil
	isSendFriendGift := request.Action == wsv1.Action_SEND_FRIEND_GIFT &&
		request.GetSendFriendGiftRequest() != nil
	if !isSnapshot && !isGetShop && !isBuySeeds && !isBuyFertilizer && !isPlant &&
		!isApplyFertilizer && !isHarvest && !isCleanPlot && !isCatchPest &&
		!isSellCrop && !isClaimReward && !isGetPetPanel && !isBuyPet &&
		!isDeployPet && !isBuyPetFood && !isFeedPet && !isSendFriendGift {
		return nil, ErrUnsupportedAction
	}
	if isGetShop {
		return &wsv1.WsEnvelope{
			ProtocolVersion: ProtocolVersion,
			MessageKind:     wsv1.MessageKind_RESPONSE,
			Action:          wsv1.Action_GET_SHOP,
			RequestId:       request.RequestId,
			TargetPlayerId:  callerPlayerID,
			ServerTimeMs:    serverNow.UnixMilli(),
			Payload: &wsv1.WsEnvelope_GetShopResponse{
				GetShopResponse: &wsv1.GetShopResponse{
					ServerConfigVersion: config.Version(),
					Entries:             config.ActiveShopEntries(),
					Crops:               config.ActiveCropCatalog(),
				},
			},
		}, nil
	}

	a, err := r.actorFor(ctx, callerPlayerID, ownerEpoch)
	if err != nil {
		return nil, err
	}
	giftSenderName := defaultGiftSenderName
	if isSendFriendGift {
		r.mu.Lock()
		namer := r.accountNamer
		r.mu.Unlock()
		if namer != nil {
			if name, ok, nameErr := namer.AccountName(ctx, callerPlayerID); nameErr == nil && ok && name != "" {
				giftSenderName = name
			}
		}
	}
	if isSendFriendGift {
		return r.handleSendFriendGiftAwait(
			ctx, a, callerPlayerID, request, config, giftSenderName, serverNow,
		)
	}
	var response *wsv1.WsEnvelope
	var dirty bool
	var dirtyRevision uint64
	var executionErr error
	var maturityEvents []MaturityEvent
	var domainChanges DomainChanges
	err = a.mailbox.Do(ctx, func() {
		var maturityErr error
		maturityEvents, maturityErr = a.state.materializeDueMaturities(serverNow)
		if maturityErr != nil {
			executionErr = maturityErr
			return
		}
		if len(maturityEvents) > 0 {
			dirty = true
			dirtyRevision = a.state.CheckpointRevision
			domainChanges = domainChanges.Merge(DomainChangesFromPlotIDs(maturedPlotIDs(maturityEvents)))
		}
		if isBuySeeds {
			var commandDirty bool
			response, commandDirty = r.buySeeds(a, callerPlayerID, request, config, serverNow)
			dirty = dirty || commandDirty
			dirtyRevision = a.state.CheckpointRevision
			return
		}
		if isBuyFertilizer {
			var commandDirty bool
			response, commandDirty = r.buyFertilizer(a, callerPlayerID, request, config, serverNow)
			dirty = dirty || commandDirty
			dirtyRevision = a.state.CheckpointRevision
			return
		}
		if isPlant {
			var commandDirty bool
			var changes DomainChanges
			response, commandDirty, changes = r.plant(a, callerPlayerID, request, config, serverNow)
			domainChanges = domainChanges.Merge(changes)
			dirty = dirty || commandDirty
			dirtyRevision = a.state.CheckpointRevision
			return
		}
		if isApplyFertilizer {
			var commandDirty bool
			var changes DomainChanges
			response, commandDirty, changes = r.applyFertilizer(a, callerPlayerID, request, config, serverNow)
			domainChanges = domainChanges.Merge(changes)
			dirty = dirty || commandDirty
			dirtyRevision = a.state.CheckpointRevision
			return
		}
		if isHarvest {
			var commandDirty bool
			var changes DomainChanges
			response, commandDirty, changes = r.harvest(a, callerPlayerID, request, config, serverNow)
			domainChanges = domainChanges.Merge(changes)
			dirty = dirty || commandDirty
			dirtyRevision = a.state.CheckpointRevision
			return
		}
		if isCleanPlot {
			var commandDirty bool
			var changes DomainChanges
			response, commandDirty, changes = r.cleanPlot(
				a, callerPlayerID, request, config, serverNow,
			)
			domainChanges = domainChanges.Merge(changes)
			dirty = dirty || commandDirty
			dirtyRevision = a.state.CheckpointRevision
			return
		}
		if isCatchPest {
			var commandDirty bool
			var changes DomainChanges
			response, commandDirty, changes = r.catchPest(
				a, callerPlayerID, request, config, serverNow,
			)
			domainChanges = domainChanges.Merge(changes)
			dirty = dirty || commandDirty
			dirtyRevision = a.state.CheckpointRevision
			return
		}
		if isSellCrop {
			var commandDirty bool
			response, commandDirty = r.sellCrop(a, callerPlayerID, request, config, serverNow)
			dirty = dirty || commandDirty
			dirtyRevision = a.state.CheckpointRevision
			return
		}
		if isClaimReward {
			var commandDirty bool
			response, commandDirty = r.claimChapterReward(
				a, callerPlayerID, request, config, serverNow,
			)
			dirty = dirty || commandDirty
			dirtyRevision = a.state.CheckpointRevision
			return
		}
		if isGetPetPanel {
			response = r.getPetPanel(a, request, config, serverNow)
			return
		}
		if isBuyPet {
			var commandDirty bool
			response, commandDirty = r.buyPet(a, callerPlayerID, request, config, serverNow)
			dirty = dirty || commandDirty
			dirtyRevision = a.state.CheckpointRevision
			return
		}
		if isDeployPet {
			var commandDirty bool
			response, commandDirty = r.deployPet(a, callerPlayerID, request, config, serverNow)
			dirty = dirty || commandDirty
			dirtyRevision = a.state.CheckpointRevision
			return
		}
		if isBuyPetFood {
			var commandDirty bool
			response, commandDirty = r.buyPetFood(a, callerPlayerID, request, config, serverNow)
			dirty = dirty || commandDirty
			dirtyRevision = a.state.CheckpointRevision
			return
		}
		if isFeedPet {
			var commandDirty bool
			response, commandDirty = r.feedPet(a, callerPlayerID, request, config, serverNow)
			dirty = dirty || commandDirty
			dirtyRevision = a.state.CheckpointRevision
			return
		}
		if isSendFriendGift {
			var commandDirty bool
			response, commandDirty = r.sendFriendGift(
				a, callerPlayerID, request, config, giftSenderName, serverNow,
			)
			dirty = dirty || commandDirty
			dirtyRevision = a.state.CheckpointRevision
			return
		}
		snapshot := a.state.Snapshot()
		snapshot.ServerConfigVersion = config.Version()
		response = &wsv1.WsEnvelope{
			ProtocolVersion: ProtocolVersion,
			MessageKind:     wsv1.MessageKind_RESPONSE,
			Action:          wsv1.Action_GET_PLAYER_SNAPSHOT,
			RequestId:       request.RequestId,
			TargetPlayerId:  callerPlayerID,
			StateVersion: &wsv1.StateVersion{
				OwnerEpoch: a.state.OwnerEpoch,
				PlayerSeq:  a.state.PlayerSeq,
			},
			ServerTimeMs: serverNow.UnixMilli(),
			Payload: &wsv1.WsEnvelope_GetPlayerSnapshotResponse{
				GetPlayerSnapshotResponse: &wsv1.GetPlayerSnapshotResponse{
					Snapshot: snapshot,
				},
			},
		}
	})
	if err != nil {
		return nil, fmt.Errorf("execute player mailbox: %w", err)
	}
	if executionErr != nil {
		return nil, fmt.Errorf("materialize player maturity: %w", executionErr)
	}
	if dirty {
		r.markDirty(callerPlayerID, dirtyRevision)
	}
	if !isSnapshot && len(maturityEvents) > 0 {
		_ = r.forwardMaturityEvents(ctx, maturityEvents)
	}
	// DomainChanges 已在 mailbox 内收集：成熟地块 + 成功业务报告的地块。
	// 失败/重放命令返回空变化，不会产生错误的公开事件。
	r.publishFarmViewChanges(ctx, a, callerPlayerID, domainChanges)
	r.refreshActorDeadline(callerPlayerID, a)
	return response, nil
}

func (r *Runtime) handleSendFriendGiftAwait(
	ctx context.Context,
	a *runtimeActor,
	callerPlayerID uint64,
	request *wsv1.WsEnvelope,
	config *ConfigSnapshot,
	giftSenderName string,
	serverNow time.Time,
) (*wsv1.WsEnvelope, error) {
	var response *wsv1.WsEnvelope
	var dirty bool
	var dirtyRevision uint64
	var executionErr error
	var maturityEvents []MaturityEvent
	var domainChanges DomainChanges
	var draft *preparedFriendGift

	handle, err := a.mailbox.BeginAwait(ctx, func(h *actor.AwaitHandle) error {
		var maturityErr error
		maturityEvents, maturityErr = a.state.materializeDueMaturities(serverNow)
		if maturityErr != nil {
			executionErr = maturityErr
			return maturityErr
		}
		if len(maturityEvents) > 0 {
			dirty = true
			dirtyRevision = a.state.CheckpointRevision
			domainChanges = domainChanges.Merge(DomainChangesFromPlotIDs(maturedPlotIDs(maturityEvents)))
		}

		requestID, parseErr := parseRequestID(request.RequestId)
		if parseErr != nil {
			response = errorEnvelope(request, a.state, serverNow,
				&wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT})
			return nil
		}
		gift := request.GetSendFriendGiftRequest()
		fingerprint := sendFriendGiftFingerprint(callerPlayerID, request, gift)
		for _, stored := range a.state.RecentResults {
			if stored.CallerPlayerId != callerPlayerID || !bytes.Equal(stored.RequestId, requestID) {
				continue
			}
			if stored.FingerprintSchemaVersion != idempotencyFingerprintSchemaVersion ||
				stored.ProtocolVersion != request.ProtocolVersion ||
				stored.Action != uint32(request.Action) ||
				stored.TargetPlayerId != request.TargetPlayerId ||
				!bytes.Equal(stored.PayloadFingerprintSha256, fingerprint[:]) {
				response = errorEnvelope(request, a.state, serverNow,
					&wsv1.Error{Code: wsv1.ErrorCode_REQUEST_ID_CONFLICT})
				return nil
			}
			response = replaySendFriendGift(request, stored, serverNow)
			return nil
		}
		if gift == nil || gift.RecipientPlayerId == 0 || gift.CropItemId == 0 ||
			gift.Quantity < minFriendGiftQuantity || gift.Quantity > maxFriendGiftQuantity {
			response = r.storeSendFriendGiftFailure(a, request, requestID, fingerprint, config.Version(), serverNow,
				&wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT})
			dirty = true
			dirtyRevision = a.state.CheckpointRevision
			return nil
		}
		if gift.RecipientPlayerId == callerPlayerID {
			response = r.storeSendFriendGiftFailure(a, request, requestID, fingerprint, config.Version(), serverNow,
				&wsv1.Error{Code: wsv1.ErrorCode_CANNOT_FRIEND_SELF})
			dirty = true
			dirtyRevision = a.state.CheckpointRevision
			return nil
		}
		if !config.IsCropItem(gift.CropItemId) {
			response = r.storeSendFriendGiftFailure(a, request, requestID, fingerprint, config.Version(), serverNow,
				&wsv1.Error{Code: wsv1.ErrorCode_ITEM_NOT_SELLABLE})
			dirty = true
			dirtyRevision = a.state.CheckpointRevision
			return nil
		}
		currentQuantity := a.state.Inventory[gift.CropItemId]
		if gift.Quantity > currentQuantity {
			response = r.storeSendFriendGiftFailure(a, request, requestID, fingerprint, config.Version(), serverNow,
				&wsv1.Error{Code: wsv1.ErrorCode_INSUFFICIENT_ITEM_QUANTITY})
			dirty = true
			dirtyRevision = a.state.CheckpointRevision
			return nil
		}

		displayName := strings.TrimSpace(giftSenderName)
		if displayName == "" {
			displayName = defaultGiftSenderName
		}
		if utf8.RuneCountInString(displayName) > 32 {
			displayName = string([]rune(displayName)[:32])
		}
		draft, response, dirty = r.prepareSendFriendGift(
			a, callerPlayerID, request, config, serverNow, displayName, gift, requestID, fingerprint, currentQuantity,
		)
		if response != nil || draft == nil {
			if dirty {
				dirtyRevision = a.state.CheckpointRevision
			}
			return nil
		}
		if r.giftMailer == nil {
			response = errorEnvelope(request, a.state, serverNow,
				&wsv1.Error{Code: wsv1.ErrorCode_CONFIG_UNAVAILABLE, Retryable: true})
			return nil
		}
		h.Suspend()
		go func() {
			mailCtx, cancel := context.WithTimeout(r.backgroundCtx, 3*time.Second)
			mailResponse, mailErr := r.giftMailer.CreateGiftMail(mailCtx, draft.mailRequest)
			cancel()
			if mailErr != nil || (mailResponse != nil && mailResponse.GetError() != nil) {
				response = errorEnvelope(request, a.state, serverNow,
					&wsv1.Error{Code: wsv1.ErrorCode_CONFIG_UNAVAILABLE, Retryable: true})
				h.Complete(nil)
				return
			}
			if err := h.Resume(r.backgroundCtx, func() error {
				response, dirty = r.commitSendFriendGift(a, callerPlayerID, request, config, serverNow, draft)
				dirtyRevision = a.state.CheckpointRevision
				return nil
			}); err != nil {
				response = errorEnvelope(request, a.state, serverNow,
					&wsv1.Error{Code: wsv1.ErrorCode_CONFIG_UNAVAILABLE, Retryable: true})
				h.Complete(nil)
			}
		}()
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("execute player mailbox: %w", err)
	}
	if executionErr != nil {
		return nil, fmt.Errorf("materialize player maturity: %w", executionErr)
	}
	if err := handle.Wait(ctx); err != nil {
		return nil, err
	}
	if response == nil {
		return nil, fmt.Errorf("gift await completed without response")
	}
	if dirty {
		r.markDirty(callerPlayerID, dirtyRevision)
	}
	if !response.GetReplayed() && len(maturityEvents) > 0 {
		_ = r.forwardMaturityEvents(ctx, maturityEvents)
	}
	r.publishFarmViewChanges(ctx, a, callerPlayerID, domainChanges)
	r.refreshActorDeadline(callerPlayerID, a)
	return response, nil
}

func maturedPlotIDs(events []MaturityEvent) []uint32 {
	ids := make([]uint32, 0, len(events))
	for _, event := range events {
		if plotID := event.Plot.GetPlotId(); plotID != 0 {
			ids = append(ids, plotID)
		}
	}
	return ids
}

// HasActiveActorsForShard reports whether this process has materialized any
// Player Actor in the shard. The memory-only migration prototype refuses to
// hand off such shards because it has no durable checkpoint transfer yet.
func (r *Runtime) HasActiveActorsForShard(shardID uint32) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for playerID := range r.actors {
		if routing.ShardForPlayer(playerID) == shardID {
			return true
		}
	}
	return false
}

func (r *Runtime) SupportsActiveMigration() bool {
	return r.store != nil
}

// DrainShardForMigration settles, durably flushes and evicts every active
// Actor in one shard while excluding commands, maturity work and background
// flushes for that shard.
func (r *Runtime) DrainShardForMigration(
	ctx context.Context,
	shardID uint32,
	ownerEpoch uint64,
) ([]DrainedPlayer, error) {
	if shardID >= routing.ShardCount || ownerEpoch == 0 {
		return nil, ErrNotOwner
	}
	r.shardLocks[shardID].Lock()
	defer r.shardLocks[shardID].Unlock()

	r.mu.Lock()
	playerIDs := make([]uint64, 0)
	for playerID := range r.actors {
		if routing.ShardForPlayer(playerID) == shardID {
			playerIDs = append(playerIDs, playerID)
		}
	}
	r.mu.Unlock()
	sort.Slice(playerIDs, func(i, j int) bool { return playerIDs[i] < playerIDs[j] })
	if len(playerIDs) > 0 && r.store == nil {
		return nil, errors.New("active Actor migration requires a checkpoint store")
	}

	actors := make([]*runtimeActor, 0, len(playerIDs))
	manifest := make([]DrainedPlayer, 0, len(playerIDs))
	for _, playerID := range playerIDs {
		r.mu.Lock()
		a := r.actors[playerID]
		r.mu.Unlock()
		if a == nil {
			continue
		}
		if a.loadCancel != nil {
			a.loadCancel()
		}
		var ready bool
		var checkpoint *datav1.PlayerCheckpointV1
		var expectedRevision uint64
		var expectedToken StoreToken
		var checkpointErr error
		if err := a.mailbox.Do(ctx, func() {
			if a.lifecycle.Load() != actorLifecycleReady || a.state == nil {
				return
			}
			ready = true
			if a.state.OwnerEpoch != ownerEpoch {
				checkpointErr = ErrNotOwner
				return
			}
			_, checkpointErr = a.state.materializeDueMaturities(r.currentTime())
			if checkpointErr != nil {
				return
			}
			checkpoint, checkpointErr = a.state.Checkpoint()
			expectedRevision = a.persistedRevision
			expectedToken = cloneStoreToken(a.persistedToken)
		}); err != nil {
			if errors.Is(err, actor.ErrClosed) {
				actors = append(actors, a)
				continue
			}
			return nil, fmt.Errorf("drain player %d mailbox: %w", playerID, err)
		}
		if !ready {
			actors = append(actors, a)
			continue
		}
		if checkpointErr != nil {
			return nil, fmt.Errorf("drain player %d checkpoint: %w", playerID, checkpointErr)
		}
		if checkpoint.CheckpointRevision > expectedRevision {
			r.markDirty(playerID, checkpoint.CheckpointRevision)
			result, saveErr := r.store.SaveCAS(ctx, CheckpointWrite{
				Checkpoint:       checkpoint,
				ExpectedRevision: expectedRevision,
				ExpectedToken:    expectedToken,
			})
			if writeErr := checkpointWriteError(result, saveErr); writeErr != nil {
				loaded, committed := r.checkpointWasCommitted(ctx, playerID, checkpoint)
				if !committed {
					return nil, fmt.Errorf("final flush player %d: %w", playerID, writeErr)
				}
				result.NewToken = cloneStoreToken(loaded.Token)
			}
			if err := a.mailbox.Do(ctx, func() {
				a.persistedRevision = checkpoint.CheckpointRevision
				a.persistedToken = cloneStoreToken(result.NewToken)
			}); err != nil {
				return nil, fmt.Errorf("acknowledge final flush player %d: %w", playerID, err)
			}
			r.mu.Lock()
			if r.dirtyRevision[playerID] <= checkpoint.CheckpointRevision {
				delete(r.dirtyRevision, playerID)
			}
			r.mu.Unlock()
		}
		actors = append(actors, a)
		manifest = append(manifest, DrainedPlayer{
			PlayerID: playerID, OwnerEpoch: ownerEpoch,
			CheckpointRevision: checkpoint.CheckpointRevision,
		})
	}

	r.mu.Lock()
	for _, a := range actors {
		a.lifecycle.Store(actorLifecycleClosing)
	}
	for _, playerID := range playerIDs {
		if current := r.actors[playerID]; current != nil {
			delete(r.actors, playerID)
			delete(r.dirtyRevision, playerID)
			r.cancelActorDeadline(playerID)
		}
	}
	r.mu.Unlock()
	for _, a := range actors {
		if a.loadCancel != nil {
			a.loadCancel()
		}
		a.mailbox.Close()
	}
	return manifest, nil
}

func (r *Runtime) checkpointWasCommitted(
	_ context.Context,
	playerID uint64,
	expected *datav1.PlayerCheckpointV1,
) (LoadedCheckpoint, bool) {
	if r.store == nil || expected == nil {
		return LoadedCheckpoint{}, false
	}
	reconcileCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	loaded, err := r.store.Load(reconcileCtx, playerID)
	if err != nil || loaded.State == nil {
		return LoadedCheckpoint{}, false
	}
	actual, err := loaded.State.Checkpoint()
	if err != nil {
		return LoadedCheckpoint{}, false
	}
	_, actualDigest, err := MarshalCheckpoint(actual)
	if err != nil {
		return LoadedCheckpoint{}, false
	}
	_, expectedDigest, err := MarshalCheckpoint(expected)
	return loaded, err == nil && bytes.Equal(actualDigest[:], expectedDigest[:])
}

// PrepareShardForMigration validates the old Owner's durable manifest and
// rewrites those checkpoints to the newly fenced epoch without activating
// target Actors before the route becomes ACTIVE.
func (r *Runtime) PrepareShardForMigration(
	ctx context.Context,
	shardID uint32,
	ownerEpoch uint64,
	manifest []DrainedPlayer,
) error {
	if shardID >= routing.ShardCount || ownerEpoch < 2 ||
		r.store == nil {
		return errors.New("checkpoint-backed target preparation is required")
	}
	r.shardLocks[shardID].Lock()
	defer r.shardLocks[shardID].Unlock()
	if r.HasActiveActorsForShard(shardID) {
		return errors.New("target shard already has active Actors")
	}
	players := append([]DrainedPlayer(nil), manifest...)
	sort.Slice(players, func(i, j int) bool { return players[i].PlayerID < players[j].PlayerID })
	for index, item := range players {
		if item.PlayerID == 0 ||
			routing.ShardForPlayer(item.PlayerID) != shardID ||
			item.OwnerEpoch == 0 ||
			item.OwnerEpoch+1 != ownerEpoch ||
			(index > 0 && players[index-1].PlayerID == item.PlayerID) {
			return errors.New("drained player manifest is invalid")
		}
		loaded, err := r.store.Load(ctx, item.PlayerID)
		if err != nil {
			return fmt.Errorf("load migrated player %d: %w", item.PlayerID, err)
		}
		if loaded.State == nil {
			return fmt.Errorf("load migrated player %d: checkpoint has no state", item.PlayerID)
		}
		state := loaded.State
		if state.OwnerEpoch == ownerEpoch &&
			item.CheckpointRevision < math.MaxUint64 &&
			state.CheckpointRevision == item.CheckpointRevision+1 {
			continue
		}
		if state.OwnerEpoch != item.OwnerEpoch ||
			state.CheckpointRevision != item.CheckpointRevision ||
			state.CheckpointRevision == math.MaxUint64 {
			return fmt.Errorf("migrated player %d checkpoint does not match manifest", item.PlayerID)
		}
		expectedRevision := state.CheckpointRevision
		state.OwnerEpoch = ownerEpoch
		state.CheckpointRevision++
		state.UpdatedAtMS = r.currentTime().UTC().UnixMilli()
		checkpoint, err := state.Checkpoint()
		if err != nil {
			return fmt.Errorf("build migrated player %d checkpoint: %w", item.PlayerID, err)
		}
		result, saveErr := r.store.SaveCAS(ctx, CheckpointWrite{
			Checkpoint:       checkpoint,
			ExpectedRevision: expectedRevision,
			ExpectedToken:    cloneStoreToken(loaded.Token),
		})
		if writeErr := checkpointWriteError(result, saveErr); writeErr != nil {
			return fmt.Errorf("prepare migrated player %d: %w", item.PlayerID, writeErr)
		}
	}
	return nil
}

func (r *Runtime) forwardMaturityEvents(ctx context.Context, events []MaturityEvent) error {
	r.mu.Lock()
	forwarder := r.pushForwarder
	stealable := r.stealable
	r.mu.Unlock()
	if forwarder != nil {
		for _, event := range events {
			if err := forwarder.Forward(ctx, event.Envelope()); err != nil {
				return fmt.Errorf("forward maturity push for player %d: %w", event.PlayerID, err)
			}
		}
	}
	if stealable != nil {
		for _, event := range events {
			if !event.Stealable || event.Plot == nil || event.Plot.PlotId == 0 {
				continue
			}
			notificationID := fmt.Sprintf("stealable:%d:%d:%d", event.PlayerID, event.Plot.PlotId, event.PlayerSeq)
			_ = stealable.NotifyOwnerPlotStealable(ctx, event.PlayerID, event.Plot.PlotId, notificationID)
		}
	}
	return nil
}

func (r *Runtime) Close() {
	r.cancel()
	r.wg.Wait()
	if r.store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = r.flushDirty(ctx)
		cancel()
	}
	r.mu.Lock()
	dispatcher := r.farmView
	r.farmView = nil
	actors := make([]*runtimeActor, 0, len(r.actors))
	for _, a := range r.actors {
		a.lifecycle.Store(actorLifecycleClosing)
		actors = append(actors, a)
	}
	r.actors = make(map[uint64]*runtimeActor)
	r.dirtyRevision = make(map[uint64]uint64)
	r.mu.Unlock()
	if dispatcher != nil {
		dispatcher.Close()
	}
	for _, a := range actors {
		if a.loadCancel != nil {
			a.loadCancel()
		}
		a.mailbox.Close()
	}
}
