package main

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	infov1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/info"
	"github.com/Wriosley/supernova-classic-farm/server/internal/connection"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcnet"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type zoneQuickInfoClient struct {
	client        infov1.InfoServiceClient
	conn          *grpc.ClientConn
	logicalZoneID string
	incarnationID string
	seq           atomic.Uint64
	queue         chan *infov1.PresenceLeaseUpdate
	farmQueue     chan *infov1.FarmQuickInfoUpdate
	mu            sync.Mutex
	epochs        map[uint64]uint64
	logger        *slog.Logger
	cancel        context.CancelFunc
	wg            sync.WaitGroup
}

func newZoneQuickInfoClient(parent context.Context, key []byte, endpoint, logicalZoneID, incarnationID string, logger *slog.Logger) (*zoneQuickInfoClient, error) {
	interceptor, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{Service: rpcauth.ZoneService, Key: key})
	if err != nil {
		return nil, err
	}
	target, err := rpcnet.TargetFromHTTPURL(endpoint)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithUnaryInterceptor(interceptor), grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(256<<10), grpc.MaxCallRecvMsgSize(256<<10)))
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(parent)
	c := &zoneQuickInfoClient{client: infov1.NewInfoServiceClient(conn), conn: conn, logicalZoneID: logicalZoneID, incarnationID: incarnationID, queue: make(chan *infov1.PresenceLeaseUpdate, 1024), farmQueue: make(chan *infov1.FarmQuickInfoUpdate, 1024), epochs: make(map[uint64]uint64), logger: logger, cancel: cancel}
	c.wg.Add(2)
	go c.run(ctx)
	go c.runFarm(ctx)
	return c, nil
}

func (c *zoneQuickInfoClient) NotifyFarmQuickInfo(_ context.Context, summary player.FarmQuickSummary) {
	if c == nil || summary.PlayerID == 0 {
		return
	}
	update := &infov1.FarmQuickInfoUpdate{PlayerId: summary.PlayerID, OwnerEpoch: summary.OwnerEpoch, CheckpointRevision: summary.CheckpointRevision, HasGrowingCrop: summary.HasGrowingCrop, EarliestMatureAtMs: summary.EarliestMatureAtMS, HasMatureCropCandidate: summary.HasMatureCropCandidate, UpdatedAtMs: summary.UpdatedAtMS}
	select {
	case c.farmQueue <- update:
	default:
		c.logger.Warn("farm quick-info queue full", "player_id", summary.PlayerID)
	}
}

func (c *zoneQuickInfoClient) RecordOfflineFarmVisit(ctx context.Context, visitor, owner uint64) error {
	if c == nil || visitor == 0 || owner == 0 {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := c.client.RecordOfflineFarmVisit(callCtx, &infov1.RecordOfflineFarmVisitRequest{VisitorPlayerId: visitor, OwnerPlayerId: owner, VisitedAtMs: time.Now().UTC().UnixMilli()})
	return err
}

func (c *zoneQuickInfoClient) runFarm(ctx context.Context) {
	defer c.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case update := <-c.farmQueue:
			callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, err := c.client.UpdateFarmQuickInfo(callCtx, &infov1.UpdateFarmQuickInfoRequest{Update: update})
			cancel()
			if err != nil {
				c.logger.Warn("update farm quick info failed", "player_id", update.GetPlayerId(), "error", err)
			}
		}
	}
}

func (c *zoneQuickInfoClient) Presence(playerID, ownerEpoch uint64, online bool, onlineUntil time.Time) {
	if c == nil || playerID == 0 || ownerEpoch == 0 {
		return
	}
	c.mu.Lock()
	c.epochs[playerID] = ownerEpoch
	c.mu.Unlock()
	nowMS := time.Now().UTC().UnixMilli()
	update := &infov1.PresenceLeaseUpdate{PlayerId: playerID, Online: online, OnlineUntilMs: onlineUntil.UTC().UnixMilli(), LastSeenAtMs: nowMS, LogicalZoneId: c.logicalZoneID, IncarnationId: c.incarnationID, OwnerEpoch: ownerEpoch, SourceSeq: c.seq.Add(1)}
	select {
	case c.queue <- update:
	default:
		c.logger.Warn("presence quick-info queue full", "player_id", playerID)
	}
}

func (c *zoneQuickInfoClient) PresenceExpired(playerID uint64) {
	c.mu.Lock()
	epoch := c.epochs[playerID]
	c.mu.Unlock()
	if epoch != 0 {
		c.Presence(playerID, epoch, false, time.Now())
	}
}

func (c *zoneQuickInfoClient) ReconcilePresence(conns []connection.PlayerConnection) {
	maxExpiry := make(map[uint64]time.Time)
	for _, conn := range conns {
		if conn.ExpiresAt.After(maxExpiry[conn.PlayerID]) {
			maxExpiry[conn.PlayerID] = conn.ExpiresAt
		}
	}
	for playerID, expiry := range maxExpiry {
		c.mu.Lock()
		epoch := c.epochs[playerID]
		c.mu.Unlock()
		if epoch != 0 {
			c.Presence(playerID, epoch, true, expiry)
		}
	}
}

func (c *zoneQuickInfoClient) run(ctx context.Context) {
	defer c.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case first := <-c.queue:
			batch := []*infov1.PresenceLeaseUpdate{first}
			for len(batch) < 256 {
				select {
				case next := <-c.queue:
					batch = append(batch, next)
				default:
					goto send
				}
			}
		send:
			callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, err := c.client.BatchRenewPresenceLeases(callCtx, &infov1.BatchRenewPresenceLeasesRequest{Updates: batch})
			cancel()
			if err != nil {
				c.logger.Warn("update presence quick info failed", "count", len(batch), "error", err)
			}
		}
	}
}

func (c *zoneQuickInfoClient) Close() error {
	if c == nil {
		return nil
	}
	c.cancel()
	c.wg.Wait()
	return c.conn.Close()
}
