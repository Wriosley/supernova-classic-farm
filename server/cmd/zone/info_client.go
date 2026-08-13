package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	infov1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/info"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type infoStealableNotifier struct {
	client infov1.InfoServiceClient
	conn   *grpc.ClientConn
	logger *slog.Logger
}

func newInfoStealableNotifier(key []byte, endpoint string, logger *slog.Logger) (*infoStealableNotifier, error) {
	if strings.TrimSpace(endpoint) == "" {
		return nil, fmt.Errorf("INFO_RPC_URL is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	interceptor, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{
		Service: rpcauth.ZoneService,
		Key:     key,
	})
	if err != nil {
		return nil, err
	}
	target, err := rpcnet.TargetFromHTTPURL(endpoint)
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
		return nil, err
	}
	return &infoStealableNotifier{
		client: infov1.NewInfoServiceClient(conn),
		conn:   conn,
		logger: logger,
	}, nil
}

func (n *infoStealableNotifier) NotifyOwnerPlotStealable(
	ctx context.Context, ownerPlayerID uint64, plotID uint32, notificationID string,
) error {
	if n == nil || n.client == nil {
		return nil
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
	}
	_, err := n.client.NotifyOwnerPlotStealable(ctx, &infov1.NotifyOwnerPlotStealableRequest{
		OwnerPlayerId: ownerPlayerID, PlotId: plotID, NotificationId: notificationID,
	})
	if err != nil {
		n.logger.Warn("notify stealable red dot failed",
			"owner_player_id", ownerPlayerID, "plot_id", plotID, "error", err)
	}
	return nil
}

func (n *infoStealableNotifier) Close() error {
	if n == nil || n.conn == nil {
		return nil
	}
	return n.conn.Close()
}
