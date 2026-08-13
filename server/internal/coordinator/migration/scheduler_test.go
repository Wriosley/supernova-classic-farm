package migration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/routestore"
)

func TestSchedulerEnforcesGlobalSourceTargetAndShardLimits(t *testing.T) {
	store := schedulerTaskStore(t,
		rebalanceTask(1, "zone-a", "zone-x", 1),
		rebalanceTask(2, "zone-a", "zone-y", 1),
		rebalanceTask(3, "zone-b", "zone-x", 1),
		rebalanceTask(4, "zone-b", "zone-y", 1),
	)
	executor := newBlockingTaskExecutor(map[uint32]Task{
		1: rebalanceTask(1, "zone-a", "zone-x", 1), 2: rebalanceTask(2, "zone-a", "zone-y", 1),
		3: rebalanceTask(3, "zone-b", "zone-x", 1), 4: rebalanceTask(4, "zone-b", "zone-y", 1),
	})
	scheduler, err := NewScheduler(store, executor, Limits{Global: 3, PerSource: 1, PerTarget: 1})
	if err != nil {
		t.Fatal(err)
	}
	if count, err := scheduler.RunOnce(context.Background()); err != nil || count != 2 {
		t.Fatalf("RunOnce = (%d, %v), want two non-conflicting tasks", count, err)
	}
	started := executor.waitStarted(t, 2)
	if started[0].SourceZoneID == started[1].SourceZoneID || started[0].TargetZoneID == started[1].TargetZoneID {
		t.Fatalf("started conflicting tasks: %+v", started)
	}
	if count, err := scheduler.RunOnce(context.Background()); err != nil || count != 0 {
		t.Fatalf("second RunOnce = (%d, %v), want no duplicate/available permits", count, err)
	}
	executor.releaseAll()
	scheduler.Wait()
	for _, task := range started {
		stored, found, err := store.Get(context.Background(), task.ShardID)
		if err != nil || !found || stored.Status != StatusCompleted {
			t.Fatalf("completed task %d = (%+v, %t, %v)", task.ShardID, stored, found, err)
		}
	}
}

func TestSchedulerHonorsRetryTimeAndPersistsBackoff(t *testing.T) {
	now := time.Date(2026, 8, 13, 15, 0, 0, 0, time.UTC)
	store := schedulerTaskStore(t, rebalanceTask(8, "zone-a", "zone-b", 1))
	executor := &recordingTaskExecutor{err: errors.New("zone temporarily unavailable")}
	scheduler, err := NewScheduler(store, executor, Limits{Global: 1, PerSource: 1, PerTarget: 1})
	if err != nil {
		t.Fatal(err)
	}
	scheduler.now = func() time.Time { return now }
	if count, err := scheduler.RunOnce(context.Background()); err != nil || count != 1 {
		t.Fatalf("RunOnce = (%d, %v)", count, err)
	}
	scheduler.Wait()
	stored, found, err := store.Get(context.Background(), 8)
	if err != nil || !found || stored.Status != StatusPlanned || stored.Attempt != 1 ||
		stored.RetryAtMS <= now.Add(30*time.Second).UnixMilli() || stored.RetryAtMS >= now.Add(31*time.Second).UnixMilli() {
		t.Fatalf("retried task = (%+v, %t, %v)", stored, found, err)
	}
	if count, err := scheduler.RunOnce(context.Background()); err != nil || count != 0 {
		t.Fatalf("RunOnce before retry = (%d, %v)", count, err)
	}
	now = time.UnixMilli(stored.RetryAtMS)
	if count, err := scheduler.RunOnce(context.Background()); err != nil || count != 1 {
		t.Fatalf("RunOnce at retry = (%d, %v)", count, err)
	}
	scheduler.Wait()
}

func TestSchedulerFailsInvariantTaskAndResumesRunningTask(t *testing.T) {
	store := schedulerTaskStore(t, rebalanceTask(9, "zone-a", "zone-b", 1))
	created, found, err := store.Get(context.Background(), 9)
	if err != nil || !found {
		t.Fatal(err)
	}
	if _, err := store.Claim(context.Background(), 9, created.TaskID); err != nil {
		t.Fatal(err)
	}
	executor := &recordingTaskExecutor{err: routestore.ErrRouteStoreCorrupt}
	scheduler, err := NewScheduler(store, executor, Limits{Global: 1, PerSource: 1, PerTarget: 1})
	if err != nil {
		t.Fatal(err)
	}
	if count, err := scheduler.RunOnce(context.Background()); err != nil || count != 1 {
		t.Fatalf("resume RunOnce = (%d, %v)", count, err)
	}
	scheduler.Wait()
	stored, found, err := store.Get(context.Background(), 9)
	if err != nil || !found || stored.Status != StatusCancelled || stored.LastErrorCode != "ROUTE_STORE_CORRUPT" {
		t.Fatalf("terminal task = (%+v, %t, %v)", stored, found, err)
	}
}

func TestSchedulerCancellationStopsWorkerAndPersistsRetry(t *testing.T) {
	store := schedulerTaskStore(t, rebalanceTask(10, "zone-a", "zone-b", 1))
	executor := &cancelAwareTaskExecutor{started: make(chan struct{})}
	scheduler, err := NewScheduler(store, executor, Limits{Global: 1, PerSource: 1, PerTarget: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	select {
	case <-executor.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run after cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop after cancellation")
	}
	stored, found, err := store.Get(context.Background(), 10)
	if err != nil || !found || stored.Status != StatusPlanned || stored.Attempt != 1 || stored.LastErrorCode != "CANCELLED" {
		t.Fatalf("cancelled task = (%+v, %t, %v)", stored, found, err)
	}
}

func TestSchedulerShutdownIsBoundedWhenWorkerIgnoresCancellation(t *testing.T) {
	store := schedulerTaskStore(t, rebalanceTask(11, "zone-a", "zone-b", 1))
	executor := &stuckTaskExecutor{started: make(chan struct{}), release: make(chan struct{})}
	scheduler, err := NewScheduler(store, executor, Limits{Global: 1, PerSource: 1, PerTarget: 1})
	if err != nil {
		t.Fatal(err)
	}
	scheduler.shutdownTimeout = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	<-executor.started
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("shutdown exceeded configured bound")
	}
	close(executor.release)
	scheduler.Wait()
}

func schedulerTaskStore(t *testing.T, proposals ...Task) *MemoryTaskStore {
	t.Helper()
	store, err := NewMemoryTaskStore()
	if err != nil {
		t.Fatal(err)
	}
	for _, proposal := range proposals {
		if _, _, err := store.UpsertPlanned(context.Background(), proposal); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

type recordingTaskExecutor struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (executor *recordingTaskExecutor) Execute(context.Context, uint32, []byte) error {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.calls++
	return executor.err
}

type blockingTaskExecutor struct {
	started chan Task
	release chan struct{}
	tasks   map[uint32]Task
}

func newBlockingTaskExecutor(tasks map[uint32]Task) *blockingTaskExecutor {
	return &blockingTaskExecutor{started: make(chan Task, 8), release: make(chan struct{}), tasks: tasks}
}

func (executor *blockingTaskExecutor) Execute(ctx context.Context, shardID uint32, _ []byte) error {
	executor.started <- executor.tasks[shardID]
	select {
	case <-executor.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (executor *blockingTaskExecutor) waitStarted(t *testing.T, count int) []Task {
	t.Helper()
	result := make([]Task, 0, count)
	for len(result) < count {
		select {
		case task := <-executor.started:
			result = append(result, task)
		case <-time.After(time.Second):
			t.Fatalf("started %d tasks, want %d", len(result), count)
		}
	}
	return result
}

func (executor *blockingTaskExecutor) releaseAll() { close(executor.release) }

type cancelAwareTaskExecutor struct{ started chan struct{} }

func (executor *cancelAwareTaskExecutor) Execute(ctx context.Context, _ uint32, _ []byte) error {
	close(executor.started)
	<-ctx.Done()
	return ctx.Err()
}

type stuckTaskExecutor struct {
	started chan struct{}
	release chan struct{}
}

func (executor *stuckTaskExecutor) Execute(context.Context, uint32, []byte) error {
	close(executor.started)
	<-executor.release
	return context.Canceled
}
