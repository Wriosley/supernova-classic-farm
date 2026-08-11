package interaction

import (
	"context"
	"errors"
	"sync"

	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"

	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
)

// playerMemStore is a minimal player.CheckpointStore for wiring a real
// player.Runtime into interaction Saga tests without touching Tcaplus.
type playerMemStore struct {
	mu    sync.Mutex
	state *player.State
}

func newPlayerMemStore(state *player.State) *playerMemStore {
	return &playerMemStore{state: state}
}

func (s *playerMemStore) Load(_ context.Context, _ uint64) (player.LoadedCheckpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return player.LoadedCheckpoint{State: s.state, PersistedRevision: s.state.CheckpointRevision}, nil
}

func (s *playerMemStore) SaveCAS(
	_ context.Context, write player.CheckpointWrite,
) (player.CheckpointWriteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := player.StateFromCheckpoint(write.Checkpoint)
	if err != nil {
		return player.CheckpointWriteResult{}, err
	}
	s.state = state
	return player.CheckpointWriteResult{Status: player.CheckpointWriteApplied}, nil
}

// plotSnapshot and inventoryQuantity give tests a direct, race-free window
// into what the last synchronous SaveCAS actually committed, without
// depending on any exported Runtime read API.
func (s *playerMemStore) plotSnapshot(plotID uint32) *player.Plot {
	s.mu.Lock()
	defer s.mu.Unlock()
	plot := s.state.Plots[plotID]
	if plot == nil {
		return nil
	}
	clone := *plot
	return &clone
}

func (s *playerMemStore) inventoryQuantity(itemID uint32) uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Inventory[itemID]
}

// maturePlotFixture builds a MATURE plot with steal fields frozen the same
// way player.plant freezes them under NewDevelopmentConfigSnapshot, since
// this package cannot reach player's unexported plant/maturePlot helpers.
func maturePlotFixture(plotID uint32) *player.Plot {
	const maturityValueScaled9 = 1_000_000_000
	return &player.Plot{
		ID: plotID, State: plotv1.PlotState_MATURE,
		CropID: 3001, CropItemID: 4001, CropConfigVersion: player.ServerConfigVersion,
		PlantedAtMS: 1, MaturityValueScaled9: maturityValueScaled9, BaseGrowthRateScaled6: 1_000_000,
		SettledGrowthValueScaled9: maturityValueScaled9, LastSettledAtMS: 1,
		BaseYield: 5, StealQuantity: 1, MaxStealTimes: 2, ProtectedOwnerYield: 1,
	}
}

func newOwnerRuntime(ownerID uint64, plotID uint32) (*player.Runtime, *playerMemStore) {
	state := player.NewDevelopmentState(ownerID)
	state.Plots[plotID] = maturePlotFixture(plotID)
	store := newPlayerMemStore(state)
	runtime, err := player.NewRuntimeWithStore(store)
	if err != nil {
		panic(err)
	}
	return runtime, store
}

func newVisitorRuntime(visitorID uint64) (*player.Runtime, *playerMemStore) {
	state := player.NewDevelopmentState(visitorID)
	state.Tasks = append(state.Tasks, player.Task{ID: 7, Target: 1})
	store := newPlayerMemStore(state)
	runtime, err := player.NewRuntimeWithStore(store)
	if err != nil {
		panic(err)
	}
	return runtime, store
}

// inProcessOwnerClient adapts an owner player.Runtime to OwnerFarmClient
// in-process, standing in for the real gRPC ZoneOwnerFarmClient so Saga
// tests exercise the exact same Runtime.ApplyStealOnOwner contract the
// production wiring uses.
type inProcessOwnerClient struct {
	runtime    *player.Runtime
	ownerEpoch uint64

	mu          sync.Mutex
	failNext    int
	failNextErr error
	callCount   int
}

func newInProcessOwnerClient(runtime *player.Runtime, ownerEpoch uint64) *inProcessOwnerClient {
	return &inProcessOwnerClient{runtime: runtime, ownerEpoch: ownerEpoch}
}

func (c *inProcessOwnerClient) failNextCall(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failNext++
	c.failNextErr = err
}

func (c *inProcessOwnerClient) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.callCount
}

func (c *inProcessOwnerClient) ApplyVisitorAction(
	ctx context.Context, req *rpcv1.ApplyVisitorActionRequest,
) (*rpcv1.ApplyVisitorActionResponse, error) {
	c.mu.Lock()
	c.callCount++
	if c.failNext > 0 {
		c.failNext--
		err := c.failNextErr
		if err == nil {
			err = errors.New("simulated transport failure")
		}
		c.mu.Unlock()
		return nil, err
	}
	c.mu.Unlock()

	payload, digest, farmPatch, _, err := c.runtime.ApplyStealOnOwner(
		ctx, req.GetOwnerPlayerId(), c.ownerEpoch, req.GetVisitorPlayerId(),
		req.GetInteractionId(), req.GetPlotId(),
		req.GetExpectedCropItemId(), req.GetFarmViewEpoch(), req.GetFarmViewSeq(),
	)
	if err != nil {
		if code, ok := ownerDomainErrorCode(err); ok {
			return &rpcv1.ApplyVisitorActionResponse{Error: &wsv1.Error{Code: code}}, nil
		}
		return nil, err
	}
	return &rpcv1.ApplyVisitorActionResponse{
		ResultDigestSha256: digest, ResultPayload: payload, FarmPatch: farmPatch,
	}, nil
}

func ownerDomainErrorCode(err error) (wsv1.ErrorCode, bool) {
	if errors.Is(err, player.ErrStealNotAvailable) {
		return wsv1.ErrorCode_STEAL_NOT_AVAILABLE, true
	}
	return wsv1.ErrorCode_ERROR_UNSPECIFIED, false
}

// flakyStore wraps a Store and, for each Update call index listed in
// failAtIndices, returns a transport-like error instead of forwarding to
// the wrapped Store: the underlying record is left exactly as it was
// before the call, simulating a crash after every prior durable step
// committed but before this particular interaction-record CAS committed.
type flakyStore struct {
	Store
	mu            sync.Mutex
	updateCalls   int
	failAtIndices map[int]bool
}

func newFlakyStore(inner Store, failAtIndices ...int) *flakyStore {
	set := make(map[int]bool, len(failAtIndices))
	for _, index := range failAtIndices {
		set[index] = true
	}
	return &flakyStore{Store: inner, failAtIndices: set}
}

func (f *flakyStore) Update(
	ctx context.Context, record *tcaplusv1.FriendInteraction, expectedVersion int32,
) (int32, error) {
	f.mu.Lock()
	index := f.updateCalls
	f.updateCalls++
	shouldFail := f.failAtIndices[index]
	f.mu.Unlock()
	if shouldFail {
		return 0, errors.New("simulated crash before interaction record commit")
	}
	return f.Store.Update(ctx, record, expectedVersion)
}

func dummyOwnerRoute() *rpcv1.CommittedRoute {
	return &rpcv1.CommittedRoute{LogicalShardId: 1, OwnerZoneId: "zone-local", OwnerEpoch: 1, RouteVersion: 1}
}

func fixedVisitID(fill byte) []byte {
	id := make([]byte, 16)
	for i := range id {
		id[i] = fill
	}
	return id
}
