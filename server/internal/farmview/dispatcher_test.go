package farmview

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/visit"
)

type blockingPatchPublisher struct {
	mu       sync.Mutex
	calls    int
	block    chan struct{}
	failGate string
	err      error
}

func (p *blockingPatchPublisher) PublishFarmViewPatch(
	ctx context.Context, gateID string, _ []uint64, _ *wsv1.FarmViewPatch,
) error {
	p.mu.Lock()
	p.calls++
	block := p.block
	failGate := p.failGate
	err := p.err
	p.mu.Unlock()
	if failGate != "" && gateID == failGate {
		return err
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (p *blockingPatchPublisher) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func testDispatcher(t *testing.T, publisher PatchPublisher, visitors []visit.VisitRecord) *Dispatcher {
	t.Helper()
	broadcaster, err := NewBroadcaster(publisher, &staticVisitorLister{visitors: visitors}, "owner-gate")
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewDispatcher(broadcaster, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dispatcher.Close)
	return dispatcher
}

func TestDispatcherEnqueueDeliversPatch(t *testing.T) {
	publisher := &blockingPatchPublisher{}
	dispatcher := testDispatcher(t, publisher, nil)
	dispatcher.Enqueue(7, testPatch())

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		published, failed, dropped := dispatcher.Stats()
		if published == 1 && failed == 0 && dropped == 0 && publisher.callCount() == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	published, failed, dropped := dispatcher.Stats()
	t.Fatalf("stats published=%d failed=%d dropped=%d calls=%d",
		published, failed, dropped, publisher.callCount())
}

func TestDispatcherDropsWhenQueueFull(t *testing.T) {
	block := make(chan struct{})
	publisher := &blockingPatchPublisher{block: block}
	broadcaster, err := NewBroadcaster(publisher, &staticVisitorLister{}, "owner-gate")
	if err != nil {
		t.Fatal(err)
	}
	// 用极小队列验证“队满丢最新”：worker 阻塞在 Broadcast 时把队列打满。
	dispatcher, err := NewDispatcherWithConfig(broadcaster, slog.Default(), DispatcherConfig{
		QueueSize: 2, Workers: 1, BroadcastTimeout: time.Second, CloseDrainTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		close(block)
		dispatcher.Close()
	}()

	dispatcher.Enqueue(7, testPatch())
	deadline := time.Now().Add(2 * time.Second)
	for publisher.callCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if publisher.callCount() == 0 {
		t.Fatal("worker never started first broadcast")
	}

	dispatcher.Enqueue(7, testPatch())
	dispatcher.Enqueue(7, testPatch())
	dispatcher.Enqueue(7, testPatch()) // 第 4 次应因队列满被丢弃

	_, _, dropped := dispatcher.Stats()
	if dropped == 0 {
		t.Fatal("expected at least one dropped event when queue is full")
	}
}

func TestDispatcherOneGateFailureDoesNotBlockOtherGates(t *testing.T) {
	publisher := &blockingPatchPublisher{failGate: "gate-a", err: errors.New("gate-a down")}
	dispatcher := testDispatcher(t, publisher, []visit.VisitRecord{
		{VisitorPlayerID: 101, GateID: "gate-a"},
		{VisitorPlayerID: 201, GateID: "gate-b"},
	})
	dispatcher.Enqueue(7, testPatch())

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if publisher.callCount() >= 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if publisher.callCount() < 3 {
		t.Fatalf("expected owner-gate + gate-a + gate-b attempts, got %d", publisher.callCount())
	}
	_, failed, _ := dispatcher.Stats()
	if failed != 1 {
		t.Fatalf("failed = %d, want 1 (joined gate error still counts as one broadcast failure)", failed)
	}
}

func TestDispatcherCloseRejectsNewEventsAndDrains(t *testing.T) {
	publisher := &blockingPatchPublisher{}
	dispatcher := testDispatcher(t, publisher, nil)
	dispatcher.Enqueue(7, testPatch())
	deadline := time.Now().Add(2 * time.Second)
	for publisher.callCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	dispatcher.Close()
	dispatcher.Enqueue(7, testPatch())
	_, _, dropped := dispatcher.Stats()
	if dropped == 0 {
		t.Fatal("expected Close to reject subsequent Enqueue")
	}
}

func TestDispatcherDoesNotSpawnPerEventGoroutine(t *testing.T) {
	var inFlight atomic.Int32
	var peak atomic.Int32
	publisher := &countingConcurrentPublisher{inFlight: &inFlight, peak: &peak}
	dispatcher := testDispatcher(t, publisher, nil)

	for i := 0; i < 64; i++ {
		dispatcher.Enqueue(7, testPatch())
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		published, _, _ := dispatcher.Stats()
		if published == 64 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if peak.Load() > int32(defaultDispatcherWorkers) {
		t.Fatalf("peak concurrent broadcasts = %d, want <= %d fixed workers",
			peak.Load(), defaultDispatcherWorkers)
	}
}

type countingConcurrentPublisher struct {
	inFlight *atomic.Int32
	peak     *atomic.Int32
}

func (p *countingConcurrentPublisher) PublishFarmViewPatch(
	_ context.Context, _ string, _ []uint64, _ *wsv1.FarmViewPatch,
) error {
	cur := p.inFlight.Add(1)
	for {
		old := p.peak.Load()
		if cur <= old || p.peak.CompareAndSwap(old, cur) {
			break
		}
	}
	time.Sleep(2 * time.Millisecond)
	p.inFlight.Add(-1)
	return nil
}

func TestNewDispatcherRequiresBroadcaster(t *testing.T) {
	if _, err := NewDispatcher(nil, slog.Default()); err == nil {
		t.Fatal("expected error for nil broadcaster")
	}
}
