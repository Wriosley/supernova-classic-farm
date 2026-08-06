package main

import (
	"context"
	"testing"
	"time"

	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"github.com/Wriosley/supernova-classic-farm/server/internal/visit"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type stubFriendChecker struct {
	mutual bool
}

func (s stubFriendChecker) CheckMutualFriend(context.Context, uint64, uint64) (bool, []byte, error) {
	return s.mutual, nil, nil
}

type stubOwnerFarmClient struct{}

func (stubOwnerFarmClient) EnterVisitor(
	context.Context, uint64, uint64, string, []byte, string,
) ([]byte, int64, *wsv1.FarmVisitSnapshot, *wsv1.Error, error) {
	return nil, 0, nil, nil, nil
}

func (stubOwnerFarmClient) RefreshVisitorHeartbeat(
	context.Context, uint64, uint64, []byte, string,
) (int64, *wsv1.Error, error) {
	return 0, nil, nil
}

func (stubOwnerFarmClient) ExitVisitor(
	context.Context, uint64, uint64, []byte,
) (*wsv1.Error, error) {
	return nil, nil
}

func (stubOwnerFarmClient) ApplyVisitorAction(
	context.Context, *rpcv1.ApplyVisitorActionRequest,
) (*rpcv1.ApplyVisitorActionResponse, error) {
	return &rpcv1.ApplyVisitorActionResponse{}, nil
}

func newTestVisitorZoneRPCServer(t *testing.T) *visitorZoneRPCServer {
	t.Helper()
	svc, err := visit.NewService(stubFriendChecker{mutual: true}, stubOwnerFarmClient{}, nil)
	if err != nil {
		t.Fatalf("visit.NewService: %v", err)
	}
	return newVisitorZoneRPCServer(svc, localAuthorization{}, routing.DefaultZoneID)
}

func TestVisitorZoneRPCServerEnterRejectsInvalidArgs(t *testing.T) {
	server := newTestVisitorZoneRPCServer(t)
	tests := []*rpcv1.EnterFriendFarmRequest{
		nil,
		{CallerPlayerId: 0, OwnerPlayerId: 2, GateId: "local-gateway", RequestId: "req-1"},
		{CallerPlayerId: 1, OwnerPlayerId: 0, GateId: "local-gateway", RequestId: "req-1"},
		{CallerPlayerId: 1, OwnerPlayerId: 2, GateId: "", RequestId: "req-1"},
		{CallerPlayerId: 1, OwnerPlayerId: 2, GateId: "local-gateway", RequestId: ""},
	}
	for _, request := range tests {
		if _, err := server.EnterFriendFarm(context.Background(), request); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("EnterFriendFarm(%+v) status = %v, want InvalidArgument", request, status.Code(err))
		}
	}
}

func TestVisitorZoneRPCServerEnterRejectsNonOwner(t *testing.T) {
	// localAuthorization always maps every Shard to this Zone in "local"
	// mode, so exercise the rejection path with an authorization stub that
	// reports a foreign owner for every Shard instead.
	foreign := newVisitorZoneRPCServer(mustVisitService(t), foreignAuthorization{}, routing.DefaultZoneID)
	_, err := foreign.EnterFriendFarm(context.Background(), &rpcv1.EnterFriendFarmRequest{
		CallerPlayerId: 1, OwnerPlayerId: 2, GateId: "local-gateway", RequestId: "req-1",
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("EnterFriendFarm with foreign owner status = %v, want FailedPrecondition", status.Code(err))
	}
}

func TestVisitorZoneRPCServerHeartbeatAndExitRejectInvalidArgs(t *testing.T) {
	server := newTestVisitorZoneRPCServer(t)
	if _, err := server.HeartbeatFriendFarm(context.Background(), &rpcv1.HeartbeatFriendFarmRequest{
		CallerPlayerId: 1, OwnerPlayerId: 2, VisitId: []byte{1, 2, 3}, GateId: "local-gateway",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("HeartbeatFriendFarm(bad visit_id) status = %v, want InvalidArgument", status.Code(err))
	}
	if _, err := server.ExitFriendFarm(context.Background(), &rpcv1.ExitFriendFarmRequest{
		CallerPlayerId: 1, OwnerPlayerId: 2, VisitId: []byte{1, 2, 3},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ExitFriendFarm(bad visit_id) status = %v, want InvalidArgument", status.Code(err))
	}
}

type stubFarmSnapshotBuilder struct{}

func (stubFarmSnapshotBuilder) BuildPublicFarmSnapshot(
	context.Context, uint64, uint64,
) (*wsv1.FarmVisitSnapshot, error) {
	return &wsv1.FarmVisitSnapshot{}, nil
}

type stubPresencePublisher struct{}

func (stubPresencePublisher) PublishFarmPresence(context.Context, uint64, *wsv1.FarmPresencePush) error {
	return nil
}

func newTestOwnerFarmRPCServer(t *testing.T) *ownerFarmRPCServer {
	t.Helper()
	owner, err := visit.NewOwnerService(stubFarmSnapshotBuilder{}, stubPresencePublisher{}, time.Now)
	if err != nil {
		t.Fatalf("visit.NewOwnerService: %v", err)
	}
	return newOwnerFarmRPCServer(owner, localAuthorization{}, &shardExecutionGates{}, time.Now)
}

func TestOwnerFarmRPCServerEnterVisitorRejectsInvalidArgs(t *testing.T) {
	server := newTestOwnerFarmRPCServer(t)
	validRoute := &rpcv1.CommittedRoute{
		LogicalShardId: routing.ShardForPlayer(2), OwnerZoneId: routing.DefaultZoneID,
		OwnerEpoch: 1, RouteVersion: 1,
	}
	tests := []*rpcv1.EnterVisitorRequest{
		nil,
		{OwnerRoute: validRoute, OwnerPlayerId: 0, VisitorPlayerId: 1, GateId: "local-gateway", RelationId: make([]byte, 16), RequestId: "req-1"},
		{OwnerRoute: validRoute, OwnerPlayerId: 2, VisitorPlayerId: 0, GateId: "local-gateway", RelationId: make([]byte, 16), RequestId: "req-1"},
		{OwnerRoute: validRoute, OwnerPlayerId: 2, VisitorPlayerId: 1, GateId: "", RelationId: make([]byte, 16), RequestId: "req-1"},
		{OwnerRoute: validRoute, OwnerPlayerId: 2, VisitorPlayerId: 1, GateId: "local-gateway", RelationId: []byte{1, 2, 3}, RequestId: "req-1"},
		{OwnerRoute: nil, OwnerPlayerId: 2, VisitorPlayerId: 1, GateId: "local-gateway", RelationId: make([]byte, 16), RequestId: "req-1"},
	}
	for _, request := range tests {
		if _, err := server.EnterVisitor(context.Background(), request); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("EnterVisitor(%+v) status = %v, want InvalidArgument", request, status.Code(err))
		}
	}
}

func TestOwnerFarmRPCServerEnterVisitorRejectsStaleRoute(t *testing.T) {
	server := newTestOwnerFarmRPCServer(t)
	_, err := server.EnterVisitor(context.Background(), &rpcv1.EnterVisitorRequest{
		OwnerRoute: &rpcv1.CommittedRoute{
			LogicalShardId: routing.ShardForPlayer(2), OwnerZoneId: "some-other-zone",
			OwnerEpoch: 1, RouteVersion: 1,
		},
		OwnerPlayerId: 2, VisitorPlayerId: 1, GateId: "local-gateway",
		RelationId: make([]byte, 16), RequestId: "req-1",
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("EnterVisitor(stale route) status = %v, want FailedPrecondition", status.Code(err))
	}
}

func mustVisitService(t *testing.T) *visit.Service {
	t.Helper()
	svc, err := visit.NewService(stubFriendChecker{mutual: true}, stubOwnerFarmClient{}, nil)
	if err != nil {
		t.Fatalf("visit.NewService: %v", err)
	}
	return svc
}

// foreignAuthorization always reports every Shard as owned by a Zone other
// than this process, exercising authorizeCaller's rejection path without
// depending on routing.ShardForPlayer's exact distribution.
type foreignAuthorization struct{}

func (foreignAuthorization) Validate(uint64, uint32, string, uint64, time.Time) error {
	return status.Error(codes.FailedPrecondition, "not shard owner")
}

func (foreignAuthorization) Entry(shardID uint32) (routing.RouteEntry, bool) {
	return routing.RouteEntry{
		ShardID: shardID, OwnerZoneID: "some-other-zone",
		OwnerEpoch: 1, RouteVersion: 1, State: routing.RouteStateActive,
	}, true
}
