package push

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/connection"
)

const (
	defaultQueueSize         = 256
	defaultWorkers           = 2
	defaultPublishTimeout    = 2 * time.Second
	defaultCloseDrainTimeout = 3 * time.Second
)

// GateClient publishes one Gate-scoped fan-out. Implementations may dial
// different Gate endpoints; callers must not assume a single Gate.
type GateClient interface {
	PublishFarmViewPatch(
		ctx context.Context, gateID, gateEndpoint string, recipientPlayerIDs []uint64, patch *wsv1.FarmViewPatch,
	) error
	PublishRedDotChanged(
		ctx context.Context, gateID, gateEndpoint string, recipientPlayerIDs []uint64, payload *RedDotChanged,
	) error
}

// GateClientResolver looks up a Gate client by gate_id without hard-coding a
// single Gate into business code.
type GateClientResolver interface {
	Resolve(gateID, gateEndpoint string) (GateClient, error)
}

// StaticGateResolver serves the current single-Gate deployment while keeping
// the multi-Gate Resolve(gateID) surface.
type StaticGateResolver struct {
	GateID       string
	GateEndpoint string
	Client       GateClient
}

func (r StaticGateResolver) Resolve(gateID, gateEndpoint string) (GateClient, error) {
	if r.Client == nil || strings.TrimSpace(r.GateID) == "" {
		return nil, errors.New("gate client resolver is not configured")
	}
	if gateID != r.GateID {
		return nil, fmt.Errorf("unknown gate id %q", gateID)
	}
	if r.GateEndpoint != "" && gateEndpoint != r.GateEndpoint {
		return nil, fmt.Errorf("unknown gate endpoint %q", gateEndpoint)
	}
	return r.Client, nil
}

// RedDotChanged is the Zone-internal red-dot payload aligned with WS RedDotChangedPush.
type RedDotChanged struct {
	NotificationID string
	Category       wsv1.RedDotCategory
	Operation      wsv1.RedDotOperation
	SourcePlayerID uint64
	HasSource      bool
	Count          uint32
}

func (r *RedDotChanged) ToPush() *wsv1.RedDotChangedPush {
	if r == nil {
		return nil
	}
	out := &wsv1.RedDotChangedPush{
		NotificationId: r.NotificationID,
		Category:       r.Category,
		Operation:      r.Operation,
		Count:          r.Count,
	}
	if r.HasSource {
		id := r.SourcePlayerID
		out.SourcePlayerId = &id
	}
	return out
}

// ConnectionLister resolves recipient Gate connections.
type ConnectionLister interface {
	List(playerID uint64) []connection.PlayerConnection
}

// Event is one bounded-queue notification for one or more recipients.
type Event struct {
	NotificationID     string
	RecipientPlayerIDs []uint64
	FarmView           *wsv1.FarmViewPatch
	RedDot             *RedDotChanged
}

// Dispatcher is the per-Zone general push worker: one queue, fixed workers.
type Dispatcher struct {
	resolver          GateClientResolver
	connections       ConnectionLister
	events            chan Event
	logger            *slog.Logger
	publishTimeout    time.Duration
	closeDrainTimeout time.Duration

	published atomic.Uint64
	failed    atomic.Uint64
	dropped   atomic.Uint64

	wg     sync.WaitGroup
	closed atomic.Bool
	once   sync.Once
}

type Config struct {
	QueueSize         int
	Workers           int
	PublishTimeout    time.Duration
	CloseDrainTimeout time.Duration
}

func NewDispatcher(
	resolver GateClientResolver,
	connections ConnectionLister,
	logger *slog.Logger,
	cfg Config,
) (*Dispatcher, error) {
	if resolver == nil || connections == nil {
		return nil, errors.New("gate resolver and connection lister are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultQueueSize
	}
	if cfg.Workers <= 0 {
		cfg.Workers = defaultWorkers
	}
	if cfg.PublishTimeout <= 0 {
		cfg.PublishTimeout = defaultPublishTimeout
	}
	if cfg.CloseDrainTimeout <= 0 {
		cfg.CloseDrainTimeout = defaultCloseDrainTimeout
	}
	d := &Dispatcher{
		resolver: resolver, connections: connections,
		events: make(chan Event, cfg.QueueSize), logger: logger,
		publishTimeout: cfg.PublishTimeout, closeDrainTimeout: cfg.CloseDrainTimeout,
	}
	for i := 0; i < cfg.Workers; i++ {
		d.wg.Add(1)
		go d.worker()
	}
	return d, nil
}

func (d *Dispatcher) Enqueue(event Event) {
	if d == nil || len(event.RecipientPlayerIDs) == 0 || (event.FarmView == nil && event.RedDot == nil) {
		return
	}
	if d.closed.Load() {
		d.dropped.Add(1)
		return
	}
	select {
	case d.events <- event:
	default:
		d.dropped.Add(1)
		d.logger.Warn("push dispatcher queue full; dropping event",
			"notification_id", event.NotificationID,
			"recipients", len(event.RecipientPlayerIDs),
			"queue_capacity", cap(d.events),
		)
	}
}

func (d *Dispatcher) worker() {
	defer d.wg.Done()
	for event := range d.events {
		d.deliver(event)
	}
}

func (d *Dispatcher) deliver(event Event) {
	ctx, cancel := context.WithTimeout(context.Background(), d.publishTimeout)
	defer cancel()
	type target struct{ id, endpoint string }
	groups := make(map[target]map[uint64]struct{})
	for _, playerID := range event.RecipientPlayerIDs {
		for _, conn := range d.connections.List(playerID) {
			if !conn.ExpiresAt.After(time.Now()) {
				continue
			}
			if strings.TrimSpace(conn.GateID) == "" || strings.TrimSpace(conn.GateEndpoint) == "" {
				continue
			}
			key := target{conn.GateID, conn.GateEndpoint}
			set := groups[key]
			if set == nil {
				set = make(map[uint64]struct{})
				groups[key] = set
			}
			set[playerID] = struct{}{}
		}
	}
	if len(groups) == 0 {
		return
	}
	var failed bool
	for target, set := range groups {
		client, err := d.resolver.Resolve(target.id, target.endpoint)
		if err != nil {
			failed = true
			d.logger.Warn("push gate resolve failed", "gate_id", target.id, "gate_endpoint", target.endpoint, "error", err)
			continue
		}
		recipients := make([]uint64, 0, len(set))
		for playerID := range set {
			recipients = append(recipients, playerID)
		}
		switch {
		case event.FarmView != nil:
			err = client.PublishFarmViewPatch(ctx, target.id, target.endpoint, recipients, event.FarmView)
		case event.RedDot != nil:
			err = client.PublishRedDotChanged(ctx, target.id, target.endpoint, recipients, event.RedDot)
		}
		if err != nil {
			failed = true
			d.logger.Warn("push publish failed",
				"gate_id", target.id, "gate_endpoint", target.endpoint, "notification_id", event.NotificationID, "error", err)
		}
	}
	if failed {
		d.failed.Add(1)
		return
	}
	d.published.Add(1)
}

func (d *Dispatcher) Close() {
	if d == nil {
		return
	}
	d.once.Do(func() {
		d.closed.Store(true)
		close(d.events)
		done := make(chan struct{})
		go func() {
			d.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(d.closeDrainTimeout):
			d.logger.Warn("push dispatcher close drain timed out",
				"published", d.published.Load(),
				"failed", d.failed.Load(),
				"dropped", d.dropped.Load(),
			)
		}
	})
}

func (d *Dispatcher) Stats() (published, failed, dropped uint64) {
	if d == nil {
		return 0, 0, 0
	}
	return d.published.Load(), d.failed.Load(), d.dropped.Load()
}
