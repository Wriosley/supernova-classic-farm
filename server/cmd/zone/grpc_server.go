package main

import (
	"context"
	"errors"
	"log/slog"
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

	runtime       *player.Runtime
	authorization ownerAuthorization
	gates         *shardExecutionGates
	now           func() time.Time
	logger        *slog.Logger
}

func newGameCommandRPCServer(
	runtime *player.Runtime,
	authorization ownerAuthorization,
	gates *shardExecutionGates,
	now func() time.Time,
	_ string,
	logger *slog.Logger,
) *gameCommandRPCServer {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &gameCommandRPCServer{
		runtime: runtime, authorization: authorization, gates: gates, now: now,
		logger: logger,
	}
}

func (s *gameCommandRPCServer) ExecutePlayerCommand(
	ctx context.Context,
	request *rpcv1.ExecutePlayerCommandRequest,
) (*rpcv1.ExecutePlayerCommandResponse, error) {
	// GateId is the Gate process incarnation (Pod UID under precise-push
	// routing). Do not require a cluster-wide GATEWAY_ID; connection leases
	// already stopped doing that.
	if request == nil || request.CallerPlayerId == 0 ||
		request.GateId == "" ||
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
		// The gRPC status stays generic so Gate never leaks internals to the
		// client, which makes this the only place the real cause survives.
		s.logger.Error("game command rejected",
			"action", request.Envelope.Action.String(),
			"player_id", request.CallerPlayerId,
			"shard_id", route.LogicalShardId,
			"error", err)
		return nil, status.Error(codes.InvalidArgument, "invalid game command")
	case response == nil:
		return nil, status.Error(codes.Internal, "game command returned no response")
	default:
		return &rpcv1.ExecutePlayerCommandResponse{Envelope: response}, nil
	}
}
