package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/routestore"
)

type Limits struct {
	Global    int
	PerSource int
	PerTarget int
}

type TaskExecutor interface {
	Execute(context.Context, uint32, []byte) error
}

type Scheduler struct {
	store           TaskStore
	executor        TaskExecutor
	limits          Limits
	now             func() time.Time
	shutdownTimeout time.Duration

	mu       sync.Mutex
	active   map[uint32]struct{}
	sources  map[string]int
	targets  map[string]int
	global   int
	workerWG sync.WaitGroup
}

func NewScheduler(store TaskStore, executor TaskExecutor, limits Limits) (*Scheduler, error) {
	if store == nil || executor == nil {
		return nil, errors.New("migration scheduler store and executor are required")
	}
	if limits.Global <= 0 || limits.PerSource <= 0 || limits.PerTarget <= 0 {
		return nil, errors.New("migration scheduler limits must be positive")
	}
	return &Scheduler{store: store, executor: executor, limits: limits, now: time.Now, shutdownTimeout: 10 * time.Second,
		active: make(map[uint32]struct{}), sources: make(map[string]int), targets: make(map[string]int)}, nil
}

func (scheduler *Scheduler) Run(ctx context.Context) error {
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if _, err := scheduler.RunOnce(workerCtx); err != nil && ctx.Err() == nil {
			return err
		}
		select {
		case <-ctx.Done():
			cancel()
			scheduler.waitBounded()
			return nil
		case <-ticker.C:
		}
	}
}

func (scheduler *Scheduler) RunOnce(ctx context.Context) (int, error) {
	tasks, err := scheduler.store.LoadOpen(ctx)
	if err != nil {
		return 0, err
	}
	nowMS := scheduler.now().UTC().UnixMilli()
	started := 0
	for _, task := range tasks {
		if task.Status == StatusPlanned && task.RetryAtMS > nowMS {
			continue
		}
		if !scheduler.reserve(task) {
			continue
		}
		claimed, err := scheduler.store.Claim(ctx, task.ShardID, task.TaskID)
		if err != nil {
			scheduler.release(task)
			if errors.Is(err, ErrTaskConflict) {
				continue
			}
			return started, err
		}
		started++
		scheduler.workerWG.Add(1)
		go scheduler.execute(ctx, claimed)
	}
	return started, nil
}

func (scheduler *Scheduler) Wait() { scheduler.workerWG.Wait() }

func (scheduler *Scheduler) waitBounded() {
	done := make(chan struct{})
	go func() {
		scheduler.workerWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(scheduler.shutdownTimeout):
	}
}

func (scheduler *Scheduler) reserve(task Task) bool {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if scheduler.global >= scheduler.limits.Global || scheduler.sources[task.SourceZoneID] >= scheduler.limits.PerSource ||
		scheduler.targets[task.TargetZoneID] >= scheduler.limits.PerTarget {
		return false
	}
	if _, exists := scheduler.active[task.ShardID]; exists {
		return false
	}
	scheduler.active[task.ShardID] = struct{}{}
	scheduler.sources[task.SourceZoneID]++
	scheduler.targets[task.TargetZoneID]++
	scheduler.global++
	return true
}

func (scheduler *Scheduler) release(task Task) {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	delete(scheduler.active, task.ShardID)
	scheduler.sources[task.SourceZoneID]--
	scheduler.targets[task.TargetZoneID]--
	scheduler.global--
}

func (scheduler *Scheduler) execute(ctx context.Context, task Task) {
	defer scheduler.workerWG.Done()
	defer scheduler.release(task)
	err := scheduler.executor.Execute(ctx, task.ShardID, task.TaskID)
	if err == nil {
		_ = scheduler.store.Complete(context.WithoutCancel(ctx), task.ShardID, task.TaskID)
		return
	}
	code, permanent := schedulerErrorCode(err)
	persistCtx := context.WithoutCancel(ctx)
	if permanent {
		_ = scheduler.store.Fail(persistCtx, task.ShardID, task.TaskID, code)
		return
	}
	attempt := task.Attempt + 1
	retryAt := scheduler.now().UTC().Add(retryDelay(attempt, task.ShardID)).UnixMilli()
	_ = scheduler.store.Retry(persistCtx, task.ShardID, task.TaskID, attempt, retryAt, code)
}

func retryDelay(attempt uint32, shardID uint32) time.Duration {
	shift := attempt - 1
	if shift > 5 {
		shift = 5
	}
	delay := 30 * time.Second * time.Duration(uint64(1)<<shift)
	if delay > 10*time.Minute {
		delay = 10 * time.Minute
	}
	return delay + time.Duration((shardID*2654435761)%1000)*time.Millisecond
}

func schedulerErrorCode(err error) (string, bool) {
	switch {
	case errors.Is(err, routestore.ErrRouteStoreCorrupt):
		return "ROUTE_STORE_CORRUPT", true
	case errors.Is(err, routestore.ErrRouteConflict):
		return "ROUTE_CONFLICT", true
	case errors.Is(err, ErrProgressConflict):
		return "PROGRESS_CONFLICT", true
	case errors.Is(err, ErrTaskConflict):
		return "TASK_CONFLICT", true
	case errors.Is(err, context.Canceled):
		return "CANCELLED", false
	default:
		code := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(err.Error()), " ", "_"))
		if len(code) > 64 {
			code = code[:64]
		}
		if code == "" {
			code = fmt.Sprintf("ERROR_%T", err)
		}
		return code, false
	}
}
