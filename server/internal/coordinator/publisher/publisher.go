package publisher

import (
	"errors"
	"sync"
	"time"

	coordinatorv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/coordinator"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

type SnapshotSource interface{ Snapshot() routing.Snapshot }
type Config struct {
	QueueCapacity int
	PingInterval  time.Duration
	AckTimeout    time.Duration
	Now           func() time.Time
}
type Diagnostics struct {
	ActiveSubscribers       int    `json:"active_subscribers"`
	QueueOverflows          uint64 `json:"queue_overflows"`
	Resyncs                 uint64 `json:"resyncs"`
	LastPublishedMapVersion uint64 `json:"last_published_map_version"`
}

type Publisher struct {
	mu                 sync.Mutex
	source             SnapshotSource
	cfg                Config
	sessions           map[string]*Session
	closed             bool
	overflows, resyncs uint64
	lastMap            uint64
}

func New(source SnapshotSource, cfg Config) (*Publisher, error) {
	if source == nil {
		return nil, errors.New("snapshot source is required")
	}
	if cfg.QueueCapacity == 0 {
		cfg.QueueCapacity = 128
	}
	if cfg.QueueCapacity < 1 {
		return nil, errors.New("queue capacity must be positive")
	}
	if cfg.PingInterval == 0 {
		cfg.PingInterval = 30 * time.Second
	}
	if cfg.AckTimeout == 0 {
		cfg.AckTimeout = 90 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.PingInterval <= 0 || cfg.AckTimeout <= 0 {
		return nil, errors.New("publisher durations must be positive")
	}
	s := source.Snapshot()
	return &Publisher{source: source, cfg: cfg, sessions: make(map[string]*Session), lastMap: s.MapVersion}, nil
}

func (p *Publisher) Register(id string, kind coordinatorv1.SubscriberKind, lastMapVersion, _ uint64) (*Session, error) {
	if id == "" {
		return nil, errors.New("subscriber ID is required")
	}
	if kind != coordinatorv1.SubscriberKind_SUBSCRIBER_KIND_GATE && kind != coordinatorv1.SubscriberKind_SUBSCRIBER_KIND_INFO && kind != coordinatorv1.SubscriberKind_SUBSCRIBER_KIND_ZONE && kind != coordinatorv1.SubscriberKind_SUBSCRIBER_KIND_OTHER {
		return nil, errors.New("subscriber kind is unsupported")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errors.New("publisher is closed")
	}
	if _, exists := p.sessions[id]; exists {
		return nil, errors.New("subscriber ID already connected")
	}
	s := &Session{id: id, kind: kind, messages: make(chan *coordinatorv1.WatchRoutesResponse, p.cfg.QueueCapacity), done: make(chan struct{})}
	current := p.source.Snapshot()
	if lastMapVersion != current.MapVersion {
		encoded, err := SnapshotProto(current)
		if err != nil {
			return nil, err
		}
		s.messages <- &coordinatorv1.WatchRoutesResponse{Payload: &coordinatorv1.WatchRoutesResponse_Snapshot{Snapshot: encoded}}
	}
	p.sessions[id] = s
	return s, nil
}

func (p *Publisher) Unregister(session *Session) {
	if session == nil {
		return
	}
	p.mu.Lock()
	if p.sessions[session.id] == session {
		delete(p.sessions, session.id)
		session.close()
	}
	p.mu.Unlock()
}

func (p *Publisher) PublishRoutes(previous, current routing.Snapshot) error {
	if current.MapVersion <= previous.MapVersion {
		return errors.New("route map version did not advance")
	}
	if len(previous.Entries) != int(routing.ShardCount) || len(current.Entries) != int(routing.ShardCount) {
		return errors.New("route snapshot is incomplete")
	}
	changed := make([]uint32, 0)
	for i := range current.Entries {
		if previous.Entries[i] != current.Entries[i] {
			changed = append(changed, uint32(i))
		}
	}
	if len(changed) == 0 {
		return errors.New("route map version advanced without changed route")
	}
	batch := &coordinatorv1.RouteBatch{PreviousMapVersion: previous.MapVersion, MapVersion: current.MapVersion}
	for _, id := range changed {
		encoded, err := RouteProto(current.Entries[id])
		if err != nil {
			return err
		}
		batch.Routes = append(batch.Routes, encoded)
	}
	response := &coordinatorv1.WatchRoutesResponse{Payload: &coordinatorv1.WatchRoutesResponse_RouteBatch{RouteBatch: batch}}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("publisher is closed")
	}
	p.lastMap = current.MapVersion
	for id, s := range p.sessions {
		if !s.enqueue(response) {
			delete(p.sessions, id)
			p.overflows++
			p.resyncs++
			s.close()
		}
	}
	return nil
}

func (p *Publisher) PublishAvailability(batch *coordinatorv1.AvailabilityBatch) error {
	if batch == nil {
		return errors.New("availability batch is required")
	}
	response := &coordinatorv1.WatchRoutesResponse{Payload: &coordinatorv1.WatchRoutesResponse_AvailabilityBatch{AvailabilityBatch: batch}}
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, s := range p.sessions {
		if !s.enqueue(response) {
			delete(p.sessions, id)
			p.overflows++
			p.resyncs++
			s.close()
		}
	}
	return nil
}
func (p *Publisher) Diagnostics() Diagnostics {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Diagnostics{ActiveSubscribers: len(p.sessions), QueueOverflows: p.overflows, Resyncs: p.resyncs, LastPublishedMapVersion: p.lastMap}
}
func (p *Publisher) Close() {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		for id, s := range p.sessions {
			delete(p.sessions, id)
			s.close()
		}
	}
	p.mu.Unlock()
}
