package main

import (
	"context"
	"sync"
	"testing"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
	"github.com/Wriosley/supernova-classic-farm/server/internal/interaction"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"github.com/Wriosley/supernova-classic-farm/server/internal/visit"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// stealTestCheckpointStore is a minimal player.CheckpointStore for wiring a
// real player.Runtime into these RPC-handler tests, mirroring
// internal/interaction's own test double (duplicated here since that one
// is unexported to its package).
type stealTestCheckpointStore struct {
	mu    sync.Mutex
	state *player.State
}

func (s *stealTestCheckpointStore) Load(context.Context, uint64) (player.LoadedCheckpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return player.LoadedCheckpoint{State: s.state, PersistedRevision: s.state.CheckpointRevision}, nil
}

func (s *stealTestCheckpointStore) SaveCAS(
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

func maturePlotForSteal(plotID uint32) *player.Plot {
	const maturityValueScaled9 = 1_000_000_000
	return &player.Plot{
		ID: plotID, State: plotv1.PlotState_MATURE,
		CropID: 3001, CropItemID: 4001, CropConfigVersion: player.ServerConfigVersion,
		PlantedAtMS: 1, MaturityValueScaled9: maturityValueScaled9, BaseGrowthRateScaled6: 1_000_000,
		SettledGrowthValueScaled9: maturityValueScaled9, LastSettledAtMS: 1,
		BaseYield: 5, StealQuantity: 1, MaxStealTimes: 2, ProtectedOwnerYield: 1,
	}
}

func newStealOwnerRuntime(t *testing.T, ownerID uint64, plotID uint32) *player.Runtime {
	t.Helper()
	state := player.NewDevelopmentState(ownerID)
	state.Plots[plotID] = maturePlotForSteal(plotID)
	runtime, err := player.NewRuntimeWithStore(&stealTestCheckpointStore{state: state})
	if err != nil {
		t.Fatalf("NewRuntimeWithStore: %v", err)
	}
	t.Cleanup(runtime.Close)
	return runtime
}

func newStealVisitorRuntime(t *testing.T, visitorID uint64) *player.Runtime {
	t.Helper()
	state := player.NewDevelopmentState(visitorID)
	runtime, err := player.NewRuntimeWithStore(&stealTestCheckpointStore{state: state})
	if err != nil {
		t.Fatalf("NewRuntimeWithStore: %v", err)
	}
	t.Cleanup(runtime.Close)
	return runtime
}

type noopPresencePublisher struct{}

func (noopPresencePublisher) PublishFarmPresence(context.Context, uint64, *wsv1.FarmPresencePush) error {
	return nil
}

// loopbackOwnerFarmClient plays the same role visit.ZoneOwnerFarmClient
// plays in production: it stamps a resolved CommittedRoute onto the
// request before delegating. It calls the owner Zone's RPC handler
// in-process instead of over gRPC, which is all these tests need since
// rpcauth/gRPC transport is already covered elsewhere.
type loopbackOwnerFarmClient struct {
	server *ownerFarmRPCServer
	route  *rpcv1.CommittedRoute
}

func (c *loopbackOwnerFarmClient) ApplyVisitorAction(
	ctx context.Context, request *rpcv1.ApplyVisitorActionRequest,
) (*rpcv1.ApplyVisitorActionResponse, error) {
	request.OwnerRoute = c.route
	return c.server.ApplyVisitorAction(ctx, request)
}

func localOwnerRoute(ownerID uint64) *rpcv1.CommittedRoute {
	return &rpcv1.CommittedRoute{
		LogicalShardId: routing.ShardForPlayer(ownerID), OwnerZoneId: routing.DefaultZoneID,
		OwnerEpoch: player.LocalOwnerEpoch, RouteVersion: 1,
	}
}

// TestZoneStealFriendCropEndToEnd exercises the full Phase 5 wiring this
// package owns: ExecuteFriendAction (visitor Zone) driving the interaction
// Saga, which calls ApplyVisitorAction (owner Zone) to mutate the owner's
// plot, and back to CommitSteal crediting the visitor's inventory.
func TestZoneStealFriendCropEndToEnd(t *testing.T) {
	const ownerID, visitorID, plotID = uint64(201), uint64(202), uint32(1)
	ownerRuntime := newStealOwnerRuntime(t, ownerID, plotID)
	visitorRuntime := newStealVisitorRuntime(t, visitorID)

	ownerService, err := visit.NewOwnerService(ownerRuntime, noopPresencePublisher{}, time.Now)
	if err != nil {
		t.Fatalf("visit.NewOwnerService: %v", err)
	}
	ctx := context.Background()
	visitID, _, _, wsErr, err := ownerService.EnterVisitor(
		ctx, ownerID, player.LocalOwnerEpoch, visitorID, "local-gateway", "enter-req-1",
	)
	if err != nil || wsErr != nil {
		t.Fatalf("EnterVisitor: wsErr=%+v err=%v", wsErr, err)
	}

	ownerFarmServer := newOwnerFarmRPCServer(ownerService, localAuthorization{}, &shardExecutionGates{}, time.Now)
	ownerFarmServer.withRuntime(ownerRuntime)
	ownerClient := &loopbackOwnerFarmClient{server: ownerFarmServer, route: localOwnerRoute(ownerID)}

	stealSaga, err := interaction.NewStealSaga(interaction.NewMemoryStore(), visitorRuntime, ownerClient)
	if err != nil {
		t.Fatalf("NewStealSaga: %v", err)
	}
	visitorZoneServer := newVisitorZoneRPCServer(nil, localAuthorization{}, routing.DefaultZoneID)
	visitorZoneServer.withStealSaga(visitorRuntime, stealSaga)

	snap, err := ownerRuntime.BuildPublicFarmSnapshot(ctx, ownerID, player.LocalOwnerEpoch)
	if err != nil {
		t.Fatalf("BuildPublicFarmSnapshot: %v", err)
	}
	stealReq := &rpcv1.ExecuteFriendActionRequest{
		CallerPlayerId: visitorID, OwnerPlayerId: ownerID, VisitId: visitID,
		GateId: "local-gateway", RequestId: "00112233-4455-6677-8899-aabbccddeeff",
		Action: datav1.FriendInteractionAction_STEAL_FRIEND_CROP, PlotId: plotID,
		ExpectedCropItemId: 4001,
		FarmViewEpoch:      snap.GetVersion().GetFarmViewEpoch(),
		FarmViewSeq:        snap.GetVersion().GetFarmViewSeq(),
	}
	response, err := visitorZoneServer.ExecuteFriendAction(ctx, stealReq)
	if err != nil {
		t.Fatalf("ExecuteFriendAction: %v", err)
	}
	if response.GetError() != nil {
		t.Fatalf("ExecuteFriendAction returned domain error: %+v", response.GetError())
	}
	if response.GetResult().GetVisitorPatch().GetInventoryUpserts()[0].GetQuantity() != 1 {
		t.Fatalf("expected visitor inventory patch quantity=1, got %+v", response.GetResult().GetVisitorPatch())
	}

	// Retrying the identical request_id must replay without mutating again.
	response2, err := visitorZoneServer.ExecuteFriendAction(ctx, stealReq)
	if err != nil {
		t.Fatalf("ExecuteFriendAction retry: %v", err)
	}
	if response2.GetError() != nil {
		t.Fatalf("retry returned domain error: %+v", response2.GetError())
	}
}

func TestZoneStealFriendCropRejectsUnsupportedAction(t *testing.T) {
	visitorZoneServer := newVisitorZoneRPCServer(nil, localAuthorization{}, routing.DefaultZoneID)
	response, err := visitorZoneServer.ExecuteFriendAction(context.Background(), &rpcv1.ExecuteFriendActionRequest{
		CallerPlayerId: 1, OwnerPlayerId: 2, VisitId: make([]byte, 16),
		GateId: "local-gateway", RequestId: "00112233-4455-6677-8899-aabbccddeeff",
		Action: datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND, PlotId: 1,
	})
	if err != nil {
		t.Fatalf("ExecuteFriendAction: %v", err)
	}
	if response.GetError().GetCode() != wsv1.ErrorCode_UNKNOWN_ACTION {
		t.Fatalf("expected UNKNOWN_ACTION, got %+v", response.GetError())
	}
}

func TestZoneStealFriendCropRejectsInvalidArgs(t *testing.T) {
	visitorZoneServer := newVisitorZoneRPCServer(nil, localAuthorization{}, routing.DefaultZoneID)
	tests := []*rpcv1.ExecuteFriendActionRequest{
		nil,
		{CallerPlayerId: 0, OwnerPlayerId: 2, VisitId: make([]byte, 16), GateId: "g", RequestId: "r", PlotId: 1},
		{CallerPlayerId: 1, OwnerPlayerId: 0, VisitId: make([]byte, 16), GateId: "g", RequestId: "r", PlotId: 1},
		{CallerPlayerId: 1, OwnerPlayerId: 2, VisitId: []byte{1, 2, 3}, GateId: "g", RequestId: "r", PlotId: 1},
		{CallerPlayerId: 1, OwnerPlayerId: 2, VisitId: make([]byte, 16), GateId: "", RequestId: "r", PlotId: 1},
		{CallerPlayerId: 1, OwnerPlayerId: 2, VisitId: make([]byte, 16), GateId: "g", RequestId: "", PlotId: 1},
		{CallerPlayerId: 1, OwnerPlayerId: 2, VisitId: make([]byte, 16), GateId: "g", RequestId: "r", PlotId: 0},
	}
	for _, request := range tests {
		if _, err := visitorZoneServer.ExecuteFriendAction(context.Background(), request); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("ExecuteFriendAction(%+v) status = %v, want InvalidArgument", request, status.Code(err))
		}
	}
}

func TestZoneStealFriendCropUnavailableWithoutSagaWiring(t *testing.T) {
	visitorZoneServer := newVisitorZoneRPCServer(nil, localAuthorization{}, routing.DefaultZoneID)
	_, err := visitorZoneServer.ExecuteFriendAction(context.Background(), &rpcv1.ExecuteFriendActionRequest{
		CallerPlayerId: 1, OwnerPlayerId: 2, VisitId: make([]byte, 16),
		GateId: "local-gateway", RequestId: "00112233-4455-6677-8899-aabbccddeeff",
		Action: datav1.FriendInteractionAction_STEAL_FRIEND_CROP, PlotId: 1,
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("status = %v, want Unavailable", status.Code(err))
	}
}

func TestOwnerFarmRPCServerApplyVisitorActionRejectsUnsupportedAction(t *testing.T) {
	server := newTestOwnerFarmRPCServer(t)
	response, err := server.ApplyVisitorAction(context.Background(), &rpcv1.ApplyVisitorActionRequest{
		OwnerRoute: &rpcv1.CommittedRoute{
			LogicalShardId: routing.ShardForPlayer(2), OwnerZoneId: routing.DefaultZoneID,
			OwnerEpoch: 1, RouteVersion: 1,
		},
		OwnerPlayerId: 2, VisitorPlayerId: 1, VisitId: make([]byte, 16), InteractionId: make([]byte, 16),
		Action: datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND, PlotId: 1,
	})
	if err != nil {
		t.Fatalf("ApplyVisitorAction: %v", err)
	}
	if response.GetError().GetCode() != wsv1.ErrorCode_UNKNOWN_ACTION {
		t.Fatalf("expected UNKNOWN_ACTION, got %+v", response.GetError())
	}
}

func TestOwnerFarmRPCServerApplyVisitorActionRejectsInvalidArgs(t *testing.T) {
	server := newTestOwnerFarmRPCServer(t)
	validRoute := &rpcv1.CommittedRoute{
		LogicalShardId: routing.ShardForPlayer(2), OwnerZoneId: routing.DefaultZoneID,
		OwnerEpoch: 1, RouteVersion: 1,
	}
	tests := []*rpcv1.ApplyVisitorActionRequest{
		nil,
		{OwnerRoute: validRoute, OwnerPlayerId: 0, VisitorPlayerId: 1, VisitId: make([]byte, 16), InteractionId: make([]byte, 16), Action: datav1.FriendInteractionAction_STEAL_FRIEND_CROP, PlotId: 1},
		{OwnerRoute: validRoute, OwnerPlayerId: 2, VisitorPlayerId: 0, VisitId: make([]byte, 16), InteractionId: make([]byte, 16), Action: datav1.FriendInteractionAction_STEAL_FRIEND_CROP, PlotId: 1},
		{OwnerRoute: validRoute, OwnerPlayerId: 2, VisitorPlayerId: 1, VisitId: []byte{1, 2, 3}, InteractionId: make([]byte, 16), Action: datav1.FriendInteractionAction_STEAL_FRIEND_CROP, PlotId: 1},
		{OwnerRoute: validRoute, OwnerPlayerId: 2, VisitorPlayerId: 1, VisitId: make([]byte, 16), InteractionId: []byte{1, 2, 3}, Action: datav1.FriendInteractionAction_STEAL_FRIEND_CROP, PlotId: 1},
		{OwnerRoute: validRoute, OwnerPlayerId: 2, VisitorPlayerId: 1, VisitId: make([]byte, 16), InteractionId: make([]byte, 16), Action: datav1.FriendInteractionAction_STEAL_FRIEND_CROP, PlotId: 0},
	}
	for _, request := range tests {
		if _, err := server.ApplyVisitorAction(context.Background(), request); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("ApplyVisitorAction(%+v) status = %v, want InvalidArgument", request, status.Code(err))
		}
	}
}

func TestOwnerFarmRPCServerApplyVisitorActionRequiresLiveVisit(t *testing.T) {
	const ownerID, visitorID, plotID = uint64(301), uint64(302), uint32(1)
	ownerRuntime := newStealOwnerRuntime(t, ownerID, plotID)
	ownerService, err := visit.NewOwnerService(ownerRuntime, noopPresencePublisher{}, time.Now)
	if err != nil {
		t.Fatalf("visit.NewOwnerService: %v", err)
	}
	server := newOwnerFarmRPCServer(ownerService, localAuthorization{}, &shardExecutionGates{}, time.Now)
	server.withRuntime(ownerRuntime)

	response, err := server.ApplyVisitorAction(context.Background(), &rpcv1.ApplyVisitorActionRequest{
		OwnerRoute: localOwnerRoute(ownerID), OwnerPlayerId: ownerID, VisitorPlayerId: visitorID,
		VisitId: make([]byte, 16), InteractionId: make([]byte, 16),
		Action: datav1.FriendInteractionAction_STEAL_FRIEND_CROP, PlotId: plotID,
	})
	if err != nil {
		t.Fatalf("ApplyVisitorAction: %v", err)
	}
	if response.GetError().GetCode() != wsv1.ErrorCode_VISIT_NOT_FOUND {
		t.Fatalf("expected VISIT_NOT_FOUND without a live visit, got %+v", response.GetError())
	}
}
