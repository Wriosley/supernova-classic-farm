package gateway

import (
	"context"
	"errors"
	"strings"
	"time"

	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type GRPCPushServer struct {
	rpcv1.UnimplementedGatePushServiceServer

	hub       *PushHub
	gatewayID string
}

func NewGRPCPushServer(
	handler *Handler,
	gatewayID string,
) (*GRPCPushServer, error) {
	if handler == nil || handler.pushHub == nil {
		return nil, errors.New("Gateway push hub is required")
	}
	if strings.TrimSpace(gatewayID) == "" {
		gatewayID = DefaultGatewayID
	}
	return &GRPCPushServer{hub: handler.pushHub, gatewayID: gatewayID}, nil
}

func (s *GRPCPushServer) PublishPlayerStateChanged(
	_ context.Context,
	request *rpcv1.PublishPlayerStateChangedRequest,
) (*rpcv1.PublishPlayerStateChangedResponse, error) {
	if request == nil || request.GateId != s.gatewayID ||
		request.RecipientPlayerId == 0 || request.Envelope == nil ||
		request.Envelope.TargetPlayerId != request.RecipientPlayerId {
		return nil, status.Error(codes.InvalidArgument, "invalid player push")
	}
	if !s.hub.HasSubscriber(request.RecipientPlayerId) {
		return nil, status.Error(codes.NotFound, "recipient is not connected to this gate")
	}
	if err := s.hub.Publish(request.Envelope); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid player push")
	}
	return &rpcv1.PublishPlayerStateChangedResponse{}, nil
}

// PublishFarmPresence delivers one FARM_PRESENCE_CHANGED tip to the owner.
// Unlike PublishPlayerStateChanged it carries no StateVersion: presence is
// unversioned, so it never participates in GET_PLAYER_SNAPSHOT catch-up
// ordering (see push_hub.go's finishSnapshot).
// PublishFarmViewPatch fans one FarmViewPatch out to every recipient in the
// request that is connected to this Gate, one PUSH envelope per recipient
// (unlike PublishFarmPresence, which always carries exactly one recipient).
// Recipients without a local subscription are skipped; if none are local the
// RPC returns NOT_FOUND so stale lease routing is observable.
func (s *GRPCPushServer) PublishFarmViewPatch(
	_ context.Context,
	request *rpcv1.PublishFarmViewPatchRequest,
) (*rpcv1.PublishFarmViewPatchResponse, error) {
	if request == nil || request.GateId != s.gatewayID ||
		len(request.RecipientPlayerIds) == 0 || request.Patch == nil ||
		request.Patch.OwnerPlayerId == 0 ||
		len(request.Patch.GetVersion().GetFarmViewEpoch()) == 0 ||
		request.Patch.GetVersion().GetFarmViewSeq() == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid farm view patch push")
	}
	now := time.Now().UnixMilli()
	delivered := 0
	for _, recipientPlayerID := range request.RecipientPlayerIds {
		if recipientPlayerID == 0 {
			return nil, status.Error(codes.InvalidArgument, "invalid farm view patch push")
		}
		if !s.hub.HasSubscriber(recipientPlayerID) {
			continue
		}
		envelope := &wsv1.WsEnvelope{
			ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_PUSH,
			Action: wsv1.Action_FARM_VIEW_CHANGED, TargetPlayerId: recipientPlayerID,
			ServerTimeMs: now,
			Payload:      &wsv1.WsEnvelope_FarmViewChangedPush{FarmViewChangedPush: request.Patch},
		}
		if err := s.hub.Publish(envelope); err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid farm view patch push")
		}
		delivered++
	}
	if delivered == 0 {
		return nil, status.Error(codes.NotFound, "no recipient is connected to this gate")
	}
	return &rpcv1.PublishFarmViewPatchResponse{}, nil
}

func (s *GRPCPushServer) PublishFarmPresence(
	_ context.Context,
	request *rpcv1.PublishFarmPresenceRequest,
) (*rpcv1.PublishFarmPresenceResponse, error) {
	if request == nil || request.GateId != s.gatewayID ||
		request.RecipientPlayerId == 0 || request.Presence == nil ||
		request.Presence.OwnerPlayerId != request.RecipientPlayerId ||
		request.Presence.Kind == wsv1.FarmPresenceKind_FARM_PRESENCE_KIND_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "invalid farm presence push")
	}
	if !s.hub.HasSubscriber(request.RecipientPlayerId) {
		return nil, status.Error(codes.NotFound, "recipient is not connected to this gate")
	}
	envelope := &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_PUSH,
		Action: wsv1.Action_FARM_PRESENCE_CHANGED, TargetPlayerId: request.RecipientPlayerId,
		ServerTimeMs: time.Now().UnixMilli(),
		Payload:      &wsv1.WsEnvelope_FarmPresenceChangedPush{FarmPresenceChangedPush: request.Presence},
	}
	if err := s.hub.Publish(envelope); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid farm presence push")
	}
	return &rpcv1.PublishFarmPresenceResponse{}, nil
}

func (s *GRPCPushServer) PublishRedDotChanged(
	_ context.Context,
	request *rpcv1.PublishRedDotChangedRequest,
) (*rpcv1.PublishRedDotChangedResponse, error) {
	if request == nil || request.GateId != s.gatewayID ||
		len(request.RecipientPlayerIds) == 0 || request.RedDot == nil ||
		request.RedDot.NotificationId == "" ||
		request.RedDot.Category == wsv1.RedDotCategory_RED_DOT_CATEGORY_UNSPECIFIED ||
		request.RedDot.Operation == wsv1.RedDotOperation_RED_DOT_OPERATION_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "invalid red dot push")
	}
	now := time.Now().UnixMilli()
	delivered := 0
	for _, recipientPlayerID := range request.RecipientPlayerIds {
		if recipientPlayerID == 0 {
			return nil, status.Error(codes.InvalidArgument, "invalid red dot push")
		}
		if !s.hub.HasSubscriber(recipientPlayerID) {
			continue
		}
		envelope := &wsv1.WsEnvelope{
			ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_PUSH,
			Action: wsv1.Action_RED_DOT_CHANGED, TargetPlayerId: recipientPlayerID,
			ServerTimeMs: now,
			Payload:      &wsv1.WsEnvelope_RedDotChangedPush{RedDotChangedPush: request.RedDot},
		}
		if err := s.hub.Publish(envelope); err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid red dot push")
		}
		delivered++
	}
	if delivered == 0 {
		return nil, status.Error(codes.NotFound, "no recipient is connected to this gate")
	}
	return &rpcv1.PublishRedDotChangedResponse{}, nil
}
