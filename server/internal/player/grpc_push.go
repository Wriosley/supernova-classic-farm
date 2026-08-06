package player

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type GRPCPushForwarder struct {
	conn      *grpc.ClientConn
	client    rpcv1.GatePushServiceClient
	gatewayID string
}

func NewGRPCPushForwarder(
	key []byte,
	serviceName string,
	endpoint string,
	gatewayID string,
) (*GRPCPushForwarder, error) {
	if strings.TrimSpace(gatewayID) == "" {
		return nil, errors.New("Gateway ID is required")
	}
	target, err := rpcnet.TargetFromHTTPURL(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid Gate gRPC endpoint: %w", err)
	}
	interceptor, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{
		Service: serviceName,
		Key:     key,
	})
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(interceptor),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallSendMsgSize(128<<10),
			grpc.MaxCallRecvMsgSize(128<<10),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create Gate gRPC client: %w", err)
	}
	return &GRPCPushForwarder{
		conn: conn, client: rpcv1.NewGatePushServiceClient(conn),
		gatewayID: gatewayID,
	}, nil
}

func (f *GRPCPushForwarder) Forward(
	ctx context.Context,
	envelope *wsv1.WsEnvelope,
) error {
	if f == nil || f.client == nil || envelope == nil {
		return errors.New("gRPC push forwarder is not configured")
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
	}
	_, err := f.client.PublishPlayerStateChanged(
		ctx,
		&rpcv1.PublishPlayerStateChangedRequest{
			GateId: f.gatewayID, RecipientPlayerId: envelope.TargetPlayerId,
			Envelope: envelope,
		},
	)
	if err != nil {
		return fmt.Errorf("publish player state change: %w", err)
	}
	return nil
}

// PublishFarmPresence implements visit.PresencePublisher, letting the Owner
// Zone's visit.OwnerService reuse the same static single-gate forwarder that
// carries PLAYER_STATE_CHANGED pushes.
func (f *GRPCPushForwarder) PublishFarmPresence(
	ctx context.Context,
	ownerPlayerID uint64,
	presence *wsv1.FarmPresencePush,
) error {
	if f == nil || f.client == nil || presence == nil {
		return errors.New("gRPC push forwarder is not configured")
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
	}
	_, err := f.client.PublishFarmPresence(
		ctx,
		&rpcv1.PublishFarmPresenceRequest{
			GateId: f.gatewayID, RecipientPlayerId: ownerPlayerID,
			Presence: presence,
		},
	)
	if err != nil {
		return fmt.Errorf("publish farm presence: %w", err)
	}
	return nil
}

// PublishFarmViewPatch implements farmview.PatchPublisher, letting the Owner
// Zone's farmview.Broadcaster reuse the same static single-gate forwarder
// that carries PLAYER_STATE_CHANGED and FARM_PRESENCE_CHANGED pushes. Unlike
// those two, one call fans out to every recipient_player_id on gateID in a
// single RPC.
func (f *GRPCPushForwarder) PublishFarmViewPatch(
	ctx context.Context,
	gateID string,
	recipientPlayerIDs []uint64,
	patch *wsv1.FarmViewPatch,
) error {
	if f == nil || f.client == nil || patch == nil || len(recipientPlayerIDs) == 0 {
		return errors.New("gRPC push forwarder is not configured")
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
	}
	_, err := f.client.PublishFarmViewPatch(
		ctx,
		&rpcv1.PublishFarmViewPatchRequest{
			GateId: gateID, RecipientPlayerIds: recipientPlayerIDs, Patch: patch,
		},
	)
	if err != nil {
		return fmt.Errorf("publish farm view patch: %w", err)
	}
	return nil
}

func (f *GRPCPushForwarder) Close() error {
	if f == nil || f.conn == nil {
		return nil
	}
	return f.conn.Close()
}
