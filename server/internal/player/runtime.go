package player

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/actor"
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

type runtimeActor struct {
	mailbox           *actor.Mailbox
	state             *State
	persistedRevision uint64
}

type CheckpointLoader interface {
	Load(context.Context, uint64) (*State, error)
}

type CheckpointWriter interface {
	Save(context.Context, *datav1.PlayerCheckpointV1, uint64) error
}

// Runtime lazily activates one in-memory Actor per player. Without a loader it
// retains the explicit development-only default-state behavior.
type Runtime struct {
	mu            sync.Mutex
	actors        map[uint64]*runtimeActor
	dirtyRevision map[uint64]uint64
	loader        CheckpointLoader
	writer        CheckpointWriter
	pushForwarder PushForwarder
	config        atomic.Pointer[ConfigSnapshot]
	now           func() time.Time
	backgroundCtx context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
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

func NewRuntimeWithLoader(loader CheckpointLoader) (*Runtime, error) {
	if loader == nil {
		return nil, errors.New("checkpoint loader is required")
	}
	runtime := NewRuntime()
	runtime.loader = loader
	if writer, ok := loader.(CheckpointWriter); ok {
		runtime.writer = writer
		runtime.wg.Add(1)
		go runtime.runDirtyFlusher(runtime.backgroundCtx)
	}
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
		r.mu.Lock()
		a := r.actors[playerID]
		r.mu.Unlock()
		if a == nil {
			continue
		}
		var events []MaturityEvent
		var revision uint64
		var maturityErr error
		if err := a.mailbox.Do(ctx, func() {
			events, maturityErr = a.state.materializeDueMaturities(r.now())
			revision = a.state.CheckpointRevision
		}); err != nil {
			return fmt.Errorf("schedule maturity for player %d: %w", playerID, err)
		}
		if maturityErr != nil {
			return fmt.Errorf("materialize maturity for player %d: %w", playerID, maturityErr)
		}
		if len(events) > 0 {
			r.markDirty(playerID, revision)
			if err := r.forwardMaturityEvents(ctx, events); err != nil {
				return err
			}
		}
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

func (r *Runtime) SetPushForwarder(forwarder PushForwarder) error {
	if forwarder == nil {
		return errors.New("push forwarder is required")
	}
	r.mu.Lock()
	r.pushForwarder = forwarder
	r.mu.Unlock()
	return nil
}

func (r *Runtime) markDirty(playerID, checkpointRevision uint64) {
	if r.writer == nil {
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
	r.mu.Lock()
	a := r.actors[playerID]
	targetRevision, dirty := r.dirtyRevision[playerID]
	r.mu.Unlock()
	if a == nil || !dirty {
		return nil
	}
	var checkpoint *datav1.PlayerCheckpointV1
	var expectedRevision uint64
	var checkpointErr error
	if err := a.mailbox.Do(ctx, func() {
		if a.state.CheckpointRevision < targetRevision {
			return
		}
		checkpoint, checkpointErr = a.state.Checkpoint()
		expectedRevision = a.persistedRevision
	}); err != nil {
		return fmt.Errorf("snapshot dirty player %d: %w", playerID, err)
	}
	if checkpointErr != nil {
		return fmt.Errorf("build dirty checkpoint for player %d: %w", playerID, checkpointErr)
	}
	if checkpoint == nil {
		return fmt.Errorf("snapshot dirty player %d failed", playerID)
	}
	if err := r.writer.Save(ctx, checkpoint, expectedRevision); err != nil {
		return fmt.Errorf("flush dirty player %d: %w", playerID, err)
	}
	if err := a.mailbox.Do(ctx, func() {
		if a.persistedRevision == expectedRevision {
			a.persistedRevision = checkpoint.CheckpointRevision
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

func (r *Runtime) actorFor(ctx context.Context, playerID uint64) (*runtimeActor, error) {
	r.mu.Lock()
	if existing := r.actors[playerID]; existing != nil {
		r.mu.Unlock()
		return existing, nil
	}
	r.mu.Unlock()

	state := NewDevelopmentState(playerID)
	if r.loader != nil {
		var err error
		state, err = r.loader.Load(ctx, playerID)
		if err != nil {
			return nil, fmt.Errorf("load player checkpoint: %w", err)
		}
	}
	persistedRevision := state.CheckpointRevision
	maturityEvents, err := state.materializeDueMaturities(r.now())
	if err != nil {
		return nil, fmt.Errorf("activate player maturity: %w", err)
	}
	created := &runtimeActor{
		mailbox:           actor.NewMailbox(64),
		state:             state,
		persistedRevision: persistedRevision,
	}
	r.mu.Lock()
	if existing := r.actors[playerID]; existing != nil {
		r.mu.Unlock()
		created.mailbox.Close()
		return existing, nil
	}
	r.actors[playerID] = created
	r.mu.Unlock()
	if len(maturityEvents) > 0 {
		r.markDirty(playerID, state.CheckpointRevision)
	}
	return created, nil
}

// Handle validates the authenticated internal command boundary and executes the
// snapshot projection on the target player's mailbox.
func (r *Runtime) Handle(ctx context.Context, callerPlayerID, ownerEpoch uint64, request *wsv1.WsEnvelope) (*wsv1.WsEnvelope, error) {
	if ownerEpoch != LocalOwnerEpoch {
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
	isPlant := request.Action == wsv1.Action_PLANT &&
		request.GetPlantRequest() != nil
	isApplyFertilizer := request.Action == wsv1.Action_APPLY_FERTILIZER &&
		request.GetApplyFertilizerRequest() != nil
	isHarvest := request.Action == wsv1.Action_HARVEST &&
		request.GetHarvestRequest() != nil
	if !isSnapshot && !isGetShop && !isBuySeeds && !isPlant && !isApplyFertilizer && !isHarvest {
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

	a, err := r.actorFor(ctx, callerPlayerID)
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
		snapshot := a.state.Snapshot()
		snapshot.ServerConfigVersion = config.Version()
		response = &wsv1.WsEnvelope{
			ProtocolVersion: ProtocolVersion,
			MessageKind:     wsv1.MessageKind_RESPONSE,
			Action:          wsv1.Action_GET_PLAYER_SNAPSHOT,
			RequestId:       request.RequestId,
			TargetPlayerId:  callerPlayerID,
			StateVersion: &wsv1.StateVersion{
				OwnerEpoch: LocalOwnerEpoch,
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
	return response, nil
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
	if r.writer != nil {
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
