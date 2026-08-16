package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	friendv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/friend"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"github.com/Wriosley/supernova-classic-farm/server/internal/reddot"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const stealableQueueSize = 256

type stealableEvent struct {
	ownerPlayerID  uint64
	plotID         uint32
	notificationID string
}

type zoneStealableNotifier struct {
	client   friendv1.FriendServiceClient
	conn     *grpc.ClientConn
	delivery *reddot.Delivery
	logger   *slog.Logger
	queue    chan stealableEvent
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func newZoneStealableNotifier(parent context.Context, key []byte, endpoint string, delivery *reddot.Delivery, logger *slog.Logger) (*zoneStealableNotifier, error) {
	if strings.TrimSpace(endpoint) == "" || delivery == nil {
		return nil, errors.New("friend endpoint and red-dot delivery are required")
	}
	interceptor, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{Service: rpcauth.ZoneService, Key: key})
	if err != nil {
		return nil, err
	}
	target, err := rpcnet.TargetFromHTTPURL(endpoint)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithUnaryInterceptor(interceptor), grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(128<<10), grpc.MaxCallRecvMsgSize(128<<10)))
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(parent)
	n := &zoneStealableNotifier{client: friendv1.NewFriendServiceClient(conn), conn: conn, delivery: delivery, logger: logger, queue: make(chan stealableEvent, stealableQueueSize), cancel: cancel}
	for range 2 {
		n.wg.Add(1)
		go n.run(ctx)
	}
	return n, nil
}

func (n *zoneStealableNotifier) NotifyOwnerPlotStealable(_ context.Context, ownerPlayerID uint64, plotID uint32, notificationID string) error {
	if n == nil || ownerPlayerID == 0 || plotID == 0 || strings.TrimSpace(notificationID) == "" {
		return nil
	}
	select {
	case n.queue <- stealableEvent{ownerPlayerID: ownerPlayerID, plotID: plotID, notificationID: notificationID}:
	default:
		n.logger.Warn("stealable red-dot queue full", "owner_player_id", ownerPlayerID, "plot_id", plotID)
	}
	return nil
}

func (n *zoneStealableNotifier) run(ctx context.Context) {
	defer n.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-n.queue:
			n.deliver(ctx, event)
		}
	}
}

func (n *zoneStealableNotifier) deliver(ctx context.Context, event stealableEvent) {
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	response, err := n.client.ListFriends(callCtx, &friendv1.ListFriendsRequest{CallerPlayerId: event.ownerPlayerID})
	if err != nil || response.GetError() != nil {
		if err == nil {
			err = fmt.Errorf("list friends: %s", response.GetError().GetCode())
		}
		n.logger.Warn("list friends for stealable red dot failed", "owner_player_id", event.ownerPlayerID, "error", err)
		return
	}
	ids := make([]uint64, 0, len(response.GetFriends()))
	for _, friend := range response.GetFriends() {
		if friend.GetPlayerId() != 0 {
			ids = append(ids, friend.GetPlayerId())
		}
	}
	ownerID := event.ownerPlayerID
	n.delivery.Deliver(ctx, ids, &wsv1.RedDotChangedPush{NotificationId: event.notificationID, Category: wsv1.RedDotCategory_RED_DOT_CATEGORY_FRIEND_FARM, Operation: wsv1.RedDotOperation_RED_DOT_OPERATION_SET, SourcePlayerId: &ownerID})
}

func (n *zoneStealableNotifier) Close() error {
	if n == nil {
		return nil
	}
	n.cancel()
	n.wg.Wait()
	return n.conn.Close()
}
