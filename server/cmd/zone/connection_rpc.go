package main

import (
	"context"
	"strings"
	"time"

	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	"github.com/Wriosley/supernova-classic-farm/server/internal/connection"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type playerConnectionRPCServer struct {
	rpcv1.UnimplementedPlayerConnectionServiceServer

	registry          *connection.Registry
	authorization     ownerAuthorization
	gates             *shardExecutionGates
	now               func() time.Time
	expectedGatewayID string
}

func newPlayerConnectionRPCServer(
	registry *connection.Registry,
	authorization ownerAuthorization,
	gates *shardExecutionGates,
	now func() time.Time,
	expectedGatewayID string,
) *playerConnectionRPCServer {
	if now == nil {
		now = time.Now
	}
	return &playerConnectionRPCServer{
		registry: registry, authorization: authorization, gates: gates, now: now,
		expectedGatewayID: expectedGatewayID,
	}
}

func (s *playerConnectionRPCServer) RegisterPlayerConnection(
	_ context.Context, request *rpcv1.RegisterPlayerConnectionRequest,
) (*rpcv1.RegisterPlayerConnectionResponse, error) {
	conn, route, err := s.parse(request.GetPlayerId(), request.GetGateId(), request.GetConnectionId(), request.GetRoute(), request.GetExpiresAtMs())
	if err != nil {
		return nil, err
	}
	unlock := s.gates.readLock(route.LogicalShardId)
	defer unlock()
	if err := s.validateOwner(request.GetPlayerId(), route); err != nil {
		return nil, err
	}
	if err := s.registry.Register(conn); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid player connection")
	}
	return &rpcv1.RegisterPlayerConnectionResponse{}, nil
}

func (s *playerConnectionRPCServer) RefreshPlayerConnection(
	_ context.Context, request *rpcv1.RefreshPlayerConnectionRequest,
) (*rpcv1.RefreshPlayerConnectionResponse, error) {
	conn, route, err := s.parse(request.GetPlayerId(), request.GetGateId(), request.GetConnectionId(), request.GetRoute(), request.GetExpiresAtMs())
	if err != nil {
		return nil, err
	}
	unlock := s.gates.readLock(route.LogicalShardId)
	defer unlock()
	if err := s.validateOwner(request.GetPlayerId(), route); err != nil {
		return nil, err
	}
	if err := s.registry.Refresh(conn.PlayerID, conn.GateID, conn.ConnectionID, conn.ExpiresAt); err != nil {
		if err == connection.ErrConnectionMismatch {
			return nil, status.Error(codes.NotFound, "player connection not registered")
		}
		return nil, status.Error(codes.InvalidArgument, "invalid player connection")
	}
	return &rpcv1.RefreshPlayerConnectionResponse{}, nil
}

func (s *playerConnectionRPCServer) UnregisterPlayerConnection(
	_ context.Context, request *rpcv1.UnregisterPlayerConnectionRequest,
) (*rpcv1.UnregisterPlayerConnectionResponse, error) {
	if request == nil || request.GetPlayerId() == 0 ||
		strings.TrimSpace(request.GetGateId()) == "" ||
		strings.TrimSpace(request.GetConnectionId()) == "" ||
		request.GetRoute() == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid player connection")
	}
	if request.GetGateId() != s.expectedGatewayID {
		return nil, status.Error(codes.InvalidArgument, "invalid player connection")
	}
	route := request.GetRoute()
	if route.LogicalShardId >= routing.ShardCount ||
		route.OwnerZoneId == "" || route.OwnerEpoch == 0 || route.RouteVersion == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid committed route")
	}
	unlock := s.gates.readLock(route.LogicalShardId)
	defer unlock()
	if err := s.validateOwner(request.GetPlayerId(), route); err != nil {
		return nil, err
	}
	s.registry.Unregister(request.GetPlayerId(), request.GetGateId(), request.GetConnectionId())
	return &rpcv1.UnregisterPlayerConnectionResponse{}, nil
}

func (s *playerConnectionRPCServer) parse(
	playerID uint64, gateID, connectionID string, route *rpcv1.CommittedRoute, expiresAtMs int64,
) (connection.PlayerConnection, *rpcv1.CommittedRoute, error) {
	if playerID == 0 || strings.TrimSpace(gateID) == "" || strings.TrimSpace(connectionID) == "" ||
		route == nil || expiresAtMs <= 0 || gateID != s.expectedGatewayID {
		return connection.PlayerConnection{}, nil, status.Error(codes.InvalidArgument, "invalid player connection")
	}
	if route.LogicalShardId >= routing.ShardCount ||
		route.OwnerZoneId == "" || route.OwnerEpoch == 0 || route.RouteVersion == 0 {
		return connection.PlayerConnection{}, nil, status.Error(codes.InvalidArgument, "invalid committed route")
	}
	if routing.ShardForPlayer(playerID) != route.LogicalShardId {
		return connection.PlayerConnection{}, nil, status.Error(codes.InvalidArgument, "invalid player connection")
	}
	return connection.PlayerConnection{
		PlayerID: playerID, GateID: gateID, ConnectionID: connectionID,
		ExpiresAt: time.UnixMilli(expiresAtMs),
	}, route, nil
}

func (s *playerConnectionRPCServer) validateOwner(playerID uint64, route *rpcv1.CommittedRoute) error {
	if s.authorization == nil {
		return status.Error(codes.Unavailable, "ownership is unavailable")
	}
	if err := s.authorization.Validate(
		playerID, route.LogicalShardId, route.OwnerZoneId, route.OwnerEpoch, s.now(),
	); err != nil {
		return status.Error(codes.FailedPrecondition, "not shard owner")
	}
	return nil
}
