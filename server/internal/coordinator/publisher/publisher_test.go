package publisher

import (
	"testing"
	"time"

	coordinatorv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/coordinator"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func TestPublisherSendsSnapshotThenOrderedRouteDiff(t *testing.T) {
	source := &snapshotSource{snapshot: testSnapshot(t)}
	p, err := New(source, Config{QueueCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	session, err := p.Register("gate-1", coordinatorv1.SubscriberKind_SUBSCRIBER_KIND_GATE, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	first := <-session.Messages()
	if first.GetSnapshot() == nil || len(first.GetSnapshot().Entries) != int(routing.ShardCount) {
		t.Fatal("missing initial snapshot")
	}
	previous := source.snapshot
	current := cloneSnapshot(previous)
	current.MapVersion++
	current.Entries[42].OwnerEpoch++
	current.Entries[42].RouteVersion++
	current.Entries[42].UpdatedAt = current.Entries[42].UpdatedAt.Add(time.Second)
	current.Entries[100].State = routing.RouteStatePreparing
	current.Entries[100].RouteVersion++
	current.Entries[100].UpdatedAt = current.Entries[100].UpdatedAt.Add(time.Second)
	source.snapshot = current
	if err := p.PublishRoutes(previous, current); err != nil {
		t.Fatal(err)
	}
	batch := (<-session.Messages()).GetRouteBatch()
	if batch == nil || batch.PreviousMapVersion != previous.MapVersion || batch.MapVersion != current.MapVersion || len(batch.Routes) != 2 || batch.Routes[0].ShardId != 42 || batch.Routes[1].ShardId != 100 {
		t.Fatalf("batch=%+v", batch)
	}
}

func TestPublisherCurrentSubscriberWaitsForFutureAndRejectsInvalidPublish(t *testing.T) {
	source := &snapshotSource{snapshot: testSnapshot(t)}
	p, _ := New(source, Config{QueueCapacity: 2})
	defer p.Close()
	session, err := p.Register("zone-a", coordinatorv1.SubscriberKind_SUBSCRIBER_KIND_ZONE, source.snapshot.MapVersion, 0)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-session.Messages():
		t.Fatal("current subscriber received snapshot")
	default:
	}
	if err := p.PublishRoutes(source.snapshot, source.snapshot); err == nil {
		t.Fatal("same version accepted")
	}
	if _, err := p.Register("zone-a", coordinatorv1.SubscriberKind_SUBSCRIBER_KIND_ZONE, 0, 0); err == nil {
		t.Fatal("duplicate subscriber accepted")
	}
}

func TestPublisherOverflowClosesOnlySlowSession(t *testing.T) {
	source := &snapshotSource{snapshot: testSnapshot(t)}
	p, _ := New(source, Config{QueueCapacity: 1})
	defer p.Close()
	slow, _ := p.Register("slow", coordinatorv1.SubscriberKind_SUBSCRIBER_KIND_GATE, source.snapshot.MapVersion, 0)
	fast, _ := p.Register("fast", coordinatorv1.SubscriberKind_SUBSCRIBER_KIND_INFO, source.snapshot.MapVersion, 0)
	previous := source.snapshot
	current := changed(previous, 1)
	if err := p.PublishRoutes(previous, current); err != nil {
		t.Fatal(err)
	}
	if (<-fast.Messages()).GetRouteBatch() == nil {
		t.Fatal("fast missed first")
	}
	next := changed(current, 2)
	if err := p.PublishRoutes(current, next); err != nil {
		t.Fatal(err)
	}
	if (<-fast.Messages()).GetRouteBatch() == nil {
		t.Fatal("fast missed second")
	}
	select {
	case <-slow.Done():
	default:
		t.Fatal("slow session not closed")
	}
	d := p.Diagnostics()
	if d.QueueOverflows != 1 || d.Resyncs != 1 || d.ActiveSubscribers != 1 {
		t.Fatalf("diagnostics=%+v", d)
	}
}

type snapshotSource struct{ snapshot routing.Snapshot }

func (s *snapshotSource) Snapshot() routing.Snapshot { return cloneSnapshot(s.snapshot) }

func testSnapshot(t *testing.T) routing.Snapshot {
	t.Helper()
	m, err := routing.NewLocalMap(time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return m.Snapshot()
}
func cloneSnapshot(s routing.Snapshot) routing.Snapshot {
	s.Entries = append([]routing.RouteEntry(nil), s.Entries...)
	return s
}
func changed(s routing.Snapshot, seconds int) routing.Snapshot {
	n := cloneSnapshot(s)
	n.MapVersion++
	n.Entries[42].RouteVersion++
	n.Entries[42].UpdatedAt = n.Entries[42].UpdatedAt.Add(time.Duration(seconds) * time.Second)
	return n
}
