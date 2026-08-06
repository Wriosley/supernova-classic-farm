package player

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
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
)

// FarmViewBroadcaster abstracts farmview.Broadcaster so Runtime never
// imports package farmview (which itself imports player for *Plot). The one
// production implementation is *farmview.Broadcaster, wired by cmd/zone.
type FarmViewBroadcaster interface {
	Broadcast(ctx context.Context, ownerPlayerID uint64, patch *wsv1.FarmViewPatch) error
}

type runtimeActor struct {
	mailbox           *actor.Mailbox
	state             *State
	persistedRevision uint64
	persistedToken    StoreToken

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
	farmView      FarmViewBroadcaster
	config        atomic.Pointer[ConfigSnapshot]
	now           func() time.Time
	backgroundCtx context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	shardLocks    [routing.ShardCount]sync.RWMutex
}

func NewRuntime() *Runtime {
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &Runtime{
		actors:        make(map[uint64]*runtimeActor),
		dirtyRevision: make(map[uint64]uint64),
		now:           time.Now,
		backgroundCtx: ctx,
		cancel:        cancel,
	}
	runtime.config.Store(NewDevelopmentConfigSnapshot())
	runtime.wg.Add(1)
	go runtime.runMaturityScheduler(ctx)
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

func (r *Runtime) runMaturityScheduler(ctx context.Context) {
	defer r.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = r.materializeOnlineMaturities(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (r *Runtime) materializeOnlineMaturities(ctx context.Context) error {
	r.mu.Lock()
	playerIDs := make([]uint64, 0, len(r.actors))
	for playerID := range r.actors {
		playerIDs = append(playerIDs, playerID)
	}
	r.mu.Unlock()
	sort.Slice(playerIDs, func(i, j int) bool { return playerIDs[i] < playerIDs[j] })
	for _, playerID := range playerIDs {
		shardID := routing.ShardForPlayer(playerID)
		r.shardLocks[shardID].RLock()
		r.mu.Lock()
		a := r.actors[playerID]
		r.mu.Unlock()
		if a == nil {
			r.shardLocks[shardID].RUnlock()
			continue
		}
		var events []MaturityEvent
		var revision uint64
		var maturityErr error
		if err := a.mailbox.Do(ctx, func() {
			events, maturityErr = a.state.materializeDueMaturities(r.now())
			revision = a.state.CheckpointRevision
		}); err != nil {
			r.shardLocks[shardID].RUnlock()
			return fmt.Errorf("schedule maturity for player %d: %w", playerID, err)
		}
		if maturityErr != nil {
			r.shardLocks[shardID].RUnlock()
			return fmt.Errorf("materialize maturity for player %d: %w", playerID, maturityErr)
		}
		if len(events) > 0 {
			r.markDirty(playerID, revision)
			if err := r.forwardMaturityEvents(ctx, events); err != nil {
				r.shardLocks[shardID].RUnlock()
				return err
			}
			r.notifyPublicPlots(ctx, a, playerID, maturedPlotIDs(events))
		}
		r.shardLocks[shardID].RUnlock()
	}
	return nil
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

func (r *Runtime) SetPushForwarder(forwarder PushForwarder) error {
	if forwarder == nil {
		return errors.New("push forwarder is required")
	}
	r.mu.Lock()
	r.pushForwarder = forwarder
	r.mu.Unlock()
	return nil
}

// SetFarmViewBroadcaster wires the Phase 4 public FarmViewPatch fan-out.
// Runtime works without one (development/tests): notifyPublicPlots still
// bumps farm_view_seq so ENTER/HEARTBEAT snapshots stay correct, it simply
// has nothing to push out over Gate.
func (r *Runtime) SetFarmViewBroadcaster(broadcaster FarmViewBroadcaster) error {
	if broadcaster == nil {
		return errors.New("farm view broadcaster is required")
	}
	r.mu.Lock()
	r.farmView = broadcaster
	r.mu.Unlock()
	return nil
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
	ticker := time.NewTicker(time.Second)
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
	var checkpoint *datav1.PlayerCheckpointV1
	var expectedRevision uint64
	var expectedToken StoreToken
	var checkpointErr error
	if err := a.mailbox.Do(ctx, func() {
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

func (r *Runtime) actorFor(
	ctx context.Context,
	playerID uint64,
	ownerEpoch uint64,
) (*runtimeActor, error) {
	r.mu.Lock()
	if existing := r.actors[playerID]; existing != nil {
		r.mu.Unlock()
		if existing.state.OwnerEpoch != ownerEpoch {
			return nil, ErrNotOwner
		}
		return existing, nil
	}
	r.mu.Unlock()

	state := NewDevelopmentState(playerID)
	persistedRevision := state.CheckpointRevision
	var persistedToken StoreToken
	if r.store != nil {
		loaded, err := r.store.Load(ctx, playerID)
		if err != nil {
			return nil, fmt.Errorf("load player checkpoint: %w", err)
		}
		if loaded.State == nil {
			return nil, errors.New("loaded player checkpoint has no state")
		}
		state = loaded.State
		persistedRevision = loaded.PersistedRevision
		persistedToken = cloneStoreToken(loaded.Token)
		if persistedRevision != state.CheckpointRevision {
			return nil, errors.New("loaded checkpoint revision does not match state")
		}
	}
	if state.OwnerEpoch > ownerEpoch {
		return nil, ErrNotOwner
	}
	if state.OwnerEpoch != ownerEpoch {
		if state.CheckpointRevision == math.MaxUint64 {
			return nil, errors.New("checkpoint revision exhausted during epoch adoption")
		}
		state.OwnerEpoch = ownerEpoch
		state.CheckpointRevision++
		state.UpdatedAtMS = r.now().UTC().UnixMilli()
	}
	_, err := state.materializeDueMaturities(r.now())
	if err != nil {
		return nil, fmt.Errorf("activate player maturity: %w", err)
	}
	farmViewEpoch, err := newFarmViewEpoch()
	if err != nil {
		return nil, fmt.Errorf("mint farm view epoch: %w", err)
	}
	created := &runtimeActor{
		mailbox:           actor.NewMailbox(64),
		state:             state,
		persistedRevision: persistedRevision,
		persistedToken:    persistedToken,
		farmViewEpoch:     farmViewEpoch,
	}
	r.mu.Lock()
	if existing := r.actors[playerID]; existing != nil {
		r.mu.Unlock()
		created.mailbox.Close()
		if existing.state.OwnerEpoch != ownerEpoch {
			return nil, ErrNotOwner
		}
		return existing, nil
	}
	r.actors[playerID] = created
	r.mu.Unlock()
	if state.CheckpointRevision > persistedRevision {
		r.markDirty(playerID, state.CheckpointRevision)
	}
	return created, nil
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
	serverNow := r.now()
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
	if !isSnapshot && !isGetShop && !isBuySeeds && !isBuyFertilizer && !isPlant &&
		!isApplyFertilizer && !isHarvest && !isCleanPlot && !isCatchPest &&
		!isSellCrop && !isClaimReward {
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
				},
			},
		}, nil
	}

	a, err := r.actorFor(ctx, callerPlayerID, ownerEpoch)
	if err != nil {
		return nil, err
	}
	var response *wsv1.WsEnvelope
	var dirty bool
	var dirtyRevision uint64
	var executionErr error
	var maturityEvents []MaturityEvent
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
			response, commandDirty = r.plant(a, callerPlayerID, request, config, serverNow)
			dirty = dirty || commandDirty
			dirtyRevision = a.state.CheckpointRevision
			return
		}
		if isApplyFertilizer {
			var commandDirty bool
			response, commandDirty = r.applyFertilizer(a, callerPlayerID, request, config, serverNow)
			dirty = dirty || commandDirty
			dirtyRevision = a.state.CheckpointRevision
			return
		}
		if isHarvest {
			var commandDirty bool
			response, commandDirty = r.harvest(a, callerPlayerID, request, config, serverNow)
			dirty = dirty || commandDirty
			dirtyRevision = a.state.CheckpointRevision
			return
		}
		if isCleanPlot {
			var commandDirty bool
			response, commandDirty = r.cleanPlot(
				a, callerPlayerID, request, config, serverNow,
			)
			dirty = dirty || commandDirty
			dirtyRevision = a.state.CheckpointRevision
			return
		}
		if isCatchPest {
			var commandDirty bool
			response, commandDirty = r.catchPest(
				a, callerPlayerID, request, config, serverNow,
			)
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
	r.notifyPublicPlots(ctx, a, callerPlayerID, publicPlotIDsChanged(maturityEvents, request, response))
	return response, nil
}

// publicPlotIDsChanged collects the plot IDs a Handle call just changed in a
// way visitors can see: every plot a background/foreground maturity
// materialized to MATURE (regardless of which action triggered it), plus the
// single plot touched by a successful, non-replayed PLANT/APPLY_FERTILIZER/
// HARVEST/CLEAN_PLOT/CATCH_PEST. BUY_SEEDS, BUY_FERTILIZER, SELL_CROP and
// CLAIM_CHAPTER_REWARD never mutate public plot state, so they contribute
// nothing here.
func publicPlotIDsChanged(
	maturityEvents []MaturityEvent, request, response *wsv1.WsEnvelope,
) []uint32 {
	ids := make([]uint32, 0, len(maturityEvents)+1)
	for _, event := range maturityEvents {
		if plotID := event.Plot.GetPlotId(); plotID != 0 {
			ids = append(ids, plotID)
		}
	}
	if response == nil || response.Error != nil || response.Replayed {
		return ids
	}
	switch request.GetAction() {
	case wsv1.Action_PLANT:
		if plant := request.GetPlantRequest(); plant != nil {
			ids = append(ids, plant.PlotId)
		}
	case wsv1.Action_APPLY_FERTILIZER:
		if apply := request.GetApplyFertilizerRequest(); apply != nil {
			ids = append(ids, apply.PlotId)
		}
	case wsv1.Action_HARVEST:
		if harvest := request.GetHarvestRequest(); harvest != nil {
			ids = append(ids, harvest.PlotId)
		}
	case wsv1.Action_CLEAN_PLOT:
		if clean := request.GetCleanPlotRequest(); clean != nil {
			ids = append(ids, clean.PlotId)
		}
	case wsv1.Action_CATCH_PEST:
		if catch := request.GetCatchPestRequest(); catch != nil {
			ids = append(ids, catch.PlotId)
		}
	}
	return ids
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

// notifyPublicPlots bumps a's farm_view_seq and builds a FarmViewPatch from
// plotIDs' current state synchronously inside the mailbox (so any snapshot
// or re-ENTER built right after this call already observes the new seq),
// then hands the patch to the configured FarmViewBroadcaster on a detached
// goroutine: a slow or failing Gate fan-out must never delay or fail the
// game command that triggered it. It returns the built patch (nil if
// nothing was built) so synchronous Saga steps such as
// Runtime.ApplyStealOnOwner can also return it directly to their own
// caller.
func (r *Runtime) notifyPublicPlots(
	ctx context.Context, a *runtimeActor, ownerPlayerID uint64, plotIDs []uint32,
) *wsv1.FarmViewPatch {
	if a == nil || len(plotIDs) == 0 {
		return nil
	}
	var patch *wsv1.FarmViewPatch
	err := a.mailbox.Do(ctx, func() {
		a.farmViewSeq++
		patch = buildFarmViewPatch(ownerPlayerID, a.farmViewEpoch, a.farmViewSeq, plotIDs, a.state.Plots)
	})
	if err != nil || patch == nil {
		return nil
	}
	r.mu.Lock()
	broadcaster := r.farmView
	r.mu.Unlock()
	if broadcaster == nil {
		return patch
	}
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = broadcaster.Broadcast(bgCtx, ownerPlayerID, patch)
	}()
	return patch
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
			return nil, fmt.Errorf("player %d disappeared during shard drain", playerID)
		}
		var checkpoint *datav1.PlayerCheckpointV1
		var expectedRevision uint64
		var expectedToken StoreToken
		var checkpointErr error
		if err := a.mailbox.Do(ctx, func() {
			if a.state.OwnerEpoch != ownerEpoch {
				checkpointErr = ErrNotOwner
				return
			}
			_, checkpointErr = a.state.materializeDueMaturities(r.now())
			if checkpointErr != nil {
				return
			}
			checkpoint, checkpointErr = a.state.Checkpoint()
			expectedRevision = a.persistedRevision
			expectedToken = cloneStoreToken(a.persistedToken)
		}); err != nil {
			return nil, fmt.Errorf("drain player %d mailbox: %w", playerID, err)
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
	for index, playerID := range playerIDs {
		if r.actors[playerID] != actors[index] {
			r.mu.Unlock()
			return nil, fmt.Errorf("player %d changed during shard drain", playerID)
		}
		delete(r.actors, playerID)
		delete(r.dirtyRevision, playerID)
	}
	r.mu.Unlock()
	for _, a := range actors {
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
		state.UpdatedAtMS = r.now().UTC().UnixMilli()
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
	r.mu.Unlock()
	if forwarder == nil {
		return nil
	}
	for _, event := range events {
		if err := forwarder.Forward(ctx, event.Envelope()); err != nil {
			return fmt.Errorf("forward maturity push for player %d: %w", event.PlayerID, err)
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
	actors := make([]*runtimeActor, 0, len(r.actors))
	for _, a := range r.actors {
		actors = append(actors, a)
	}
	r.actors = make(map[uint64]*runtimeActor)
	r.mu.Unlock()
	for _, a := range actors {
		a.mailbox.Close()
	}
}
