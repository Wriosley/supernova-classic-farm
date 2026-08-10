package farmview

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
)

const (
	defaultDispatcherQueueSize = 256
	defaultDispatcherWorkers   = 2
	defaultBroadcastTimeout    = 2 * time.Second
	defaultCloseDrainTimeout   = 3 * time.Second
)

// DispatcherConfig 控制有界队列与 worker 数量；零值字段使用默认值。
type DispatcherConfig struct {
	QueueSize         int
	Workers           int
	BroadcastTimeout  time.Duration
	CloseDrainTimeout time.Duration
}

// Event 是 Actor mailbox 内构造完成的不可变公开农场事件。
// Dispatcher 不得再读取 Actor State。
type Event struct {
	OwnerPlayerID uint64
	Patch         *wsv1.FarmViewPatch
}

// Dispatcher 用有界队列和固定 worker 投递 FarmViewEvent，不阻塞业务路径。
type Dispatcher struct {
	broadcaster       *Broadcaster
	events            chan Event
	logger            *slog.Logger
	broadcastTimeout  time.Duration
	closeDrainTimeout time.Duration

	published atomic.Uint64
	failed    atomic.Uint64
	dropped   atomic.Uint64

	wg     sync.WaitGroup
	closed atomic.Bool
	once   sync.Once
}

func NewDispatcher(broadcaster *Broadcaster, logger *slog.Logger) (*Dispatcher, error) {
	return NewDispatcherWithConfig(broadcaster, logger, DispatcherConfig{})
}

func NewDispatcherWithConfig(broadcaster *Broadcaster, logger *slog.Logger, cfg DispatcherConfig) (*Dispatcher, error) {
	if broadcaster == nil {
		return nil, errBroadcasterRequired
	}
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultDispatcherQueueSize
	}
	if cfg.Workers <= 0 {
		cfg.Workers = defaultDispatcherWorkers
	}
	if cfg.BroadcastTimeout <= 0 {
		cfg.BroadcastTimeout = defaultBroadcastTimeout
	}
	if cfg.CloseDrainTimeout <= 0 {
		cfg.CloseDrainTimeout = defaultCloseDrainTimeout
	}
	d := &Dispatcher{
		broadcaster:       broadcaster,
		events:            make(chan Event, cfg.QueueSize),
		logger:            logger,
		broadcastTimeout:  cfg.BroadcastTimeout,
		closeDrainTimeout: cfg.CloseDrainTimeout,
	}
	for i := 0; i < cfg.Workers; i++ {
		d.wg.Add(1)
		go d.worker()
	}
	return d, nil
}

var errBroadcasterRequired = errString("farm view broadcaster is required")

type errString string

func (e errString) Error() string { return string(e) }

// Enqueue 尝试入队；队列满或已关闭时丢弃最新事件并计数，永不阻塞调用方。
func (d *Dispatcher) Enqueue(ownerPlayerID uint64, patch *wsv1.FarmViewPatch) {
	if d == nil || patch == nil || ownerPlayerID == 0 {
		return
	}
	if d.closed.Load() {
		d.dropped.Add(1)
		return
	}
	event := Event{OwnerPlayerID: ownerPlayerID, Patch: patch}
	select {
	case d.events <- event:
	default:
		d.dropped.Add(1)
		d.logger.Warn("farm view dispatcher queue full; dropping event",
			"owner_player_id", ownerPlayerID,
			"farm_view_seq", patch.GetVersion().GetFarmViewSeq(),
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
	ctx, cancel := context.WithTimeout(context.Background(), d.broadcastTimeout)
	defer cancel()
	if err := d.broadcaster.Broadcast(ctx, event.OwnerPlayerID, event.Patch); err != nil {
		d.failed.Add(1)
		d.logger.Warn("farm view broadcast failed",
			"owner_player_id", event.OwnerPlayerID,
			"farm_view_seq", event.Patch.GetVersion().GetFarmViewSeq(),
			"error", err,
		)
		return
	}
	d.published.Add(1)
}

// Close 停止接收新事件，并在超时内排空已入队事件。
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
			d.logger.Warn("farm view dispatcher close drain timed out",
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
