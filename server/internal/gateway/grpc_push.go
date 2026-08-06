package gateway

import (
	"context"
	"errors"
	"strings"

	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
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
	if err := s.hub.Publish(request.Envelope); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid player push")
	}
	return &rpcv1.PublishPlayerStateChangedResponse{}, nil
}
