package main

import (
	"context"
	"errors"
	"time"

	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type gameCommandRPCServer struct {
	rpcv1.UnimplementedGameCommandServiceServer

	runtime           *player.Runtime
	authorization     ownerAuthorization
	gates             *shardExecutionGates
	now               func() time.Time
	expectedGatewayID string
}

func newGameCommandRPCServer(
	runtime *player.Runtime,
	authorization ownerAuthorization,
	gates *shardExecutionGates,
	now func() time.Time,
	expectedGatewayID string,
) *gameCommandRPCServer {
	if now == nil {
		now = time.Now
	}
	return &gameCommandRPCServer{
		runtime: runtime, authorization: authorization, gates: gates, now: now,
		expectedGatewayID: expectedGatewayID,
	}
}

func (s *gameCommandRPCServer) ExecutePlayerCommand(
	ctx context.Context,
	request *rpcv1.ExecutePlayerCommandRequest,
) (*rpcv1.ExecutePlayerCommandResponse, error) {
	if request == nil || request.CallerPlayerId == 0 ||
		request.GateId == "" || request.GateId != s.expectedGatewayID ||
		request.Route == nil || request.Envelope == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid game command")
	}
	route := request.Route
	if route.LogicalShardId >= routing.ShardCount ||
		route.OwnerZoneId == "" || route.OwnerEpoch == 0 ||
		route.RouteVersion == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid committed route")
	}
	if proto.Size(request.Envelope) > maxEnvelopeBytes {
		return nil, status.Error(codes.ResourceExhausted, "game command envelope is too large")
	}
	unlockShard := s.gates.readLock(route.LogicalShardId)
	defer unlockShard()
	if s.authorization == nil {
		return nil, status.Error(codes.Unavailable, "ownership is unavailable")
	}
	if err := s.authorization.Validate(
		request.Envelope.TargetPlayerId,
		route.LogicalShardId,
		route.OwnerZoneId,
		route.OwnerEpoch,
		s.now(),
	); err != nil {
		return nil, status.Error(codes.FailedPrecondition, "not shard owner")
	}
	response, err := s.runtime.Handle(
		ctx, request.CallerPlayerId, route.OwnerEpoch, request.Envelope,
	)
	switch {
	case errors.Is(err, player.ErrNotOwner):
		return nil, status.Error(codes.FailedPrecondition, "not shard owner")
	case errors.Is(err, player.ErrForbiddenTarget):
		return nil, status.Error(codes.PermissionDenied, "forbidden target")
	case err != nil:
		return nil, status.Error(codes.InvalidArgument, "invalid game command")
	case response == nil:
		return nil, status.Error(codes.Internal, "game command returned no response")
	default:
		return &rpcv1.ExecutePlayerCommandResponse{Envelope: response}, nil
	}
}
