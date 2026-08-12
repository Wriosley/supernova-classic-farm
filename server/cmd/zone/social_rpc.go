package main

import (
	"context"
	"errors"
	"time"

	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// playerSocialRPCServer implements PlayerSocialService for FriendSvr's link
// Saga: it credits one TASK_ADD_FRIEND completion on the Owner Zone for a
// player, gated by the same ownership check as game commands.
type playerSocialRPCServer struct {
	rpcv1.UnimplementedPlayerSocialServiceServer

	runtime       *player.Runtime
	authorization ownerAuthorization
	gates         *shardExecutionGates
	now           func() time.Time
}

func newPlayerSocialRPCServer(
	runtime *player.Runtime,
	authorization ownerAuthorization,
	gates *shardExecutionGates,
	now func() time.Time,
) *playerSocialRPCServer {
	if now == nil {
		now = time.Now
	}
	return &playerSocialRPCServer{
		runtime: runtime, authorization: authorization, gates: gates, now: now,
	}
}

func (s *playerSocialRPCServer) ApplyFriendTaskCredit(
	ctx context.Context,
	request *rpcv1.ApplyFriendTaskCreditRequest,
) (*rpcv1.ApplyFriendTaskCreditResponse, error) {
	if request == nil || request.PlayerId == 0 || len(request.RelationId) != 16 ||
		request.PlayerRoute == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid friend task credit request")
	}
	route := request.PlayerRoute
	if route.LogicalShardId >= routing.ShardCount ||
		route.OwnerZoneId == "" || route.OwnerEpoch == 0 || route.RouteVersion == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid committed route")
	}
	unlockShard := s.gates.readLock(route.LogicalShardId)
	defer unlockShard()
	if s.authorization == nil {
		return nil, status.Error(codes.Unavailable, "ownership is unavailable")
	}
	if err := s.authorization.Validate(
		request.PlayerId, route.LogicalShardId, route.OwnerZoneId, route.OwnerEpoch, s.now(),
	); err != nil {
		return nil, status.Error(codes.FailedPrecondition, "not shard owner")
	}
	newlyApplied, playerSeq, err := s.runtime.ApplyFriendTaskCredit(
		ctx, request.PlayerId, route.OwnerEpoch, request.RelationId,
	)
	switch {
	case errors.Is(err, player.ErrNotOwner):
		return nil, status.Error(codes.FailedPrecondition, "not shard owner")
	case err != nil:
		return nil, status.Error(codes.Internal, "friend task credit failed")
	default:
		return &rpcv1.ApplyFriendTaskCreditResponse{
			NewlyApplied: newlyApplied, PlayerSeq: playerSeq,
		}, nil
	}
}

func (s *playerSocialRPCServer) ApplyMailReward(
	ctx context.Context,
	request *rpcv1.ApplyMailRewardRequest,
) (*rpcv1.ApplyMailRewardResponse, error) {
	if request == nil || request.PlayerId == 0 || len(request.ClaimId) != 16 ||
		request.MailId == "" || request.PlayerRoute == nil || request.GetCoinAmount() < 0 ||
		(len(request.Attachments) == 0 && request.GetCoinAmount() <= 0) {
		return nil, status.Error(codes.InvalidArgument, "invalid mail reward request")
	}
	route := request.PlayerRoute
	if route.LogicalShardId >= routing.ShardCount ||
		route.OwnerZoneId == "" || route.OwnerEpoch == 0 || route.RouteVersion == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid committed route")
	}
	unlockShard := s.gates.readLock(route.LogicalShardId)
	defer unlockShard()
	if s.authorization == nil {
		return nil, status.Error(codes.Unavailable, "ownership is unavailable")
	}
	if err := s.authorization.Validate(
		request.PlayerId, route.LogicalShardId, route.OwnerZoneId, route.OwnerEpoch, s.now(),
	); err != nil {
		return nil, status.Error(codes.FailedPrecondition, "not shard owner")
	}
	attachments := make([]player.MailRewardAttachment, 0, len(request.Attachments))
	for _, attachment := range request.Attachments {
		attachments = append(attachments, player.MailRewardAttachment{
			ItemID: attachment.GetItemId(), Quantity: attachment.GetQuantity(),
		})
	}
	result, err := s.runtime.ApplyMailReward(
		ctx, request.PlayerId, route.OwnerEpoch, request.ClaimId, request.MailId,
		attachments, request.GetCoinAmount(),
	)
	switch {
	case errors.Is(err, player.ErrNotOwner):
		return nil, status.Error(codes.FailedPrecondition, "not shard owner")
	case errors.Is(err, player.ErrMailInventoryCapacity):
		return &rpcv1.ApplyMailRewardResponse{
			Error: &wsv1.Error{Code: wsv1.ErrorCode_INVENTORY_CAPACITY_EXCEEDED},
		}, nil
	case errors.Is(err, player.ErrMailClaimConflict):
		return &rpcv1.ApplyMailRewardResponse{
			Error: &wsv1.Error{Code: wsv1.ErrorCode_REQUEST_ID_CONFLICT},
		}, nil
	case err != nil:
		return nil, status.Error(codes.Internal, "mail reward failed")
	default:
		return &rpcv1.ApplyMailRewardResponse{
			NewlyApplied: result.NewlyApplied,
			PlayerSeq:    result.PlayerSeq,
			ItemsAdded:   result.ItemsAdded,
			CoinsAdded:   result.CoinsAdded,
			Patch:        result.Patch,
			OwnerEpoch:   route.OwnerEpoch,
		}, nil
	}
}
