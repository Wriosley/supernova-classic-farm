package main

import (
	"context"
	"strings"
	"time"

	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/push"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type zoneNotificationRPCServer struct {
	rpcv1.UnimplementedZoneNotificationServiceServer

	dispatcher    *push.Dispatcher
	authorization ownerAuthorization
	gates         *shardExecutionGates
	now           func() time.Time
}

func newZoneNotificationRPCServer(
	dispatcher *push.Dispatcher,
	authorization ownerAuthorization,
	gates *shardExecutionGates,
	now func() time.Time,
) *zoneNotificationRPCServer {
	if now == nil {
		now = time.Now
	}
	return &zoneNotificationRPCServer{
		dispatcher: dispatcher, authorization: authorization, gates: gates, now: now,
	}
}

func (s *zoneNotificationRPCServer) DispatchRedDot(
	_ context.Context, request *rpcv1.DispatchRedDotRequest,
) (*rpcv1.DispatchRedDotResponse, error) {
	if s == nil || s.dispatcher == nil || request == nil || request.RecipientRoute == nil ||
		request.RedDot == nil || len(request.RecipientPlayerIds) == 0 ||
		strings.TrimSpace(request.RedDot.NotificationId) == "" ||
		request.RedDot.Category == wsv1.RedDotCategory_RED_DOT_CATEGORY_UNSPECIFIED ||
		request.RedDot.Operation == wsv1.RedDotOperation_RED_DOT_OPERATION_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "invalid red dot dispatch")
	}
	route := request.RecipientRoute
	if route.LogicalShardId >= routing.ShardCount ||
		route.OwnerZoneId == "" || route.OwnerEpoch == 0 || route.RouteVersion == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid committed route")
	}
	unlock := s.gates.readLock(route.LogicalShardId)
	defer unlock()
	if s.authorization == nil {
		return nil, status.Error(codes.Unavailable, "ownership is unavailable")
	}
	now := s.now()
	for _, playerID := range request.RecipientPlayerIds {
		if playerID == 0 || routing.ShardForPlayer(playerID) != route.LogicalShardId {
			return nil, status.Error(codes.InvalidArgument, "invalid recipient player id")
		}
		if err := s.authorization.Validate(
			playerID, route.LogicalShardId, route.OwnerZoneId, route.OwnerEpoch, now,
		); err != nil {
			return nil, status.Error(codes.FailedPrecondition, "not shard owner")
		}
	}
	payload := &push.RedDotChanged{
		NotificationID: request.RedDot.NotificationId,
		Category:       request.RedDot.Category,
		Operation:      request.RedDot.Operation,
		Count:          request.RedDot.Count,
	}
	if request.RedDot.SourcePlayerId != nil {
		payload.HasSource = true
		payload.SourcePlayerID = request.RedDot.GetSourcePlayerId()
	}
	s.dispatcher.Enqueue(push.Event{
		NotificationID:     request.RedDot.NotificationId,
		RecipientPlayerIDs: append([]uint64(nil), request.RecipientPlayerIds...),
		RedDot:             payload,
	})
	return &rpcv1.DispatchRedDotResponse{}, nil
}
