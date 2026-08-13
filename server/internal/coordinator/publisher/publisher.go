package publisher

import (
	"errors"
	"sync"
	"time"

	coordinatorv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/coordinator"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/protobuf/proto"
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
	LastAvailabilityVersion uint64 `json:"last_availability_version"`
}

type Publisher struct {
	mu                 sync.Mutex
	source             SnapshotSource
	cfg                Config
	sessions           map[string]*Session
	closed             bool
	overflows, resyncs uint64
	lastMap            uint64
	availability       *coordinatorv1.AvailabilityBatch
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
	if p.availability != nil {
		authoritative := proto.Clone(p.availability).(*coordinatorv1.AvailabilityBatch)
		authoritative.PreviousAvailabilityVersion = 0
		if !s.enqueue(&coordinatorv1.WatchRoutesResponse{Payload: &coordinatorv1.WatchRoutesResponse_AvailabilityBatch{AvailabilityBatch: authoritative}}) {
			return nil, errors.New("subscriber queue cannot hold initial control-plane state")
		}
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
	if batch == nil || batch.AvailabilityVersion <= batch.PreviousAvailabilityVersion {
		return errors.New("availability batch is required")
	}
	seen := make(map[string]struct{}, len(batch.Zones))
	for _, zone := range batch.Zones {
		if zone == nil || zone.LogicalZoneId == "" || zone.Availability == coordinatorv1.ZoneAvailability_ZONE_AVAILABILITY_UNSPECIFIED {
			return errors.New("availability batch contains an invalid Zone")
		}
		if _, exists := seen[zone.LogicalZoneId]; exists {
			return errors.New("availability batch contains a duplicate Zone")
		}
		seen[zone.LogicalZoneId] = struct{}{}
	}
	retained := proto.Clone(batch).(*coordinatorv1.AvailabilityBatch)
	response := &coordinatorv1.WatchRoutesResponse{Payload: &coordinatorv1.WatchRoutesResponse_AvailabilityBatch{AvailabilityBatch: proto.Clone(batch).(*coordinatorv1.AvailabilityBatch)}}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("publisher is closed")
	}
	p.availability = retained
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
	diagnostics := Diagnostics{ActiveSubscribers: len(p.sessions), QueueOverflows: p.overflows, Resyncs: p.resyncs, LastPublishedMapVersion: p.lastMap}
	if p.availability != nil {
		diagnostics.LastAvailabilityVersion = p.availability.AvailabilityVersion
	}
	return diagnostics
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
