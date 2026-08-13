package migration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/routestore"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func TestExecutorMovesShardDurableFirstAndCompletesTask(t *testing.T) {
	fixture := newExecutorFixture(t)

	if err := fixture.executor.Execute(context.Background(), fixture.task.ShardID, fixture.task.TaskID); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	entry := fixture.routes.Snapshot().Entries[fixture.task.ShardID]
	if entry.State != routing.RouteStateActive || entry.OwnerZoneID != "zone-b" || entry.OwnerEpoch != 2 {
		t.Fatalf("final Current = %+v", entry)
	}
	stored, found, err := fixture.tasks.Get(context.Background(), fixture.task.ShardID)
	if err != nil || !found || stored.Status != StatusCompleted {
		t.Fatalf("task = (%+v, %t, %v)", stored, found, err)
	}
	if _, found, err := fixture.progress.Get(context.Background(), fixture.task.ShardID); err != nil || found {
		t.Fatalf("progress remains: found=%t err=%v", found, err)
	}
	if fixture.zone.drainCalls != 1 || fixture.zone.prepareCalls != 1 || fixture.zone.refreshCalls != 1 || fixture.fence.calls != 1 {
		t.Fatalf("lifecycle calls = drain %d prepare %d refresh %d fence %d", fixture.zone.drainCalls, fixture.zone.prepareCalls, fixture.zone.refreshCalls, fixture.fence.calls)
	}
	if len(fixture.publisher.states) != 2 || fixture.publisher.states[0] != routing.RouteStatePreparing || fixture.publisher.states[1] != routing.RouteStateActive {
		t.Fatalf("published states = %v", fixture.publisher.states)
	}
}

func TestExecutorFenceFailureNeverPublishesTargetActiveAndResumeContinues(t *testing.T) {
	fixture := newExecutorFixture(t)
	fixture.fence.err = errors.New("fence unavailable")

	if err := fixture.executor.Execute(context.Background(), fixture.task.ShardID, fixture.task.TaskID); err == nil {
		t.Fatal("Execute succeeded, want Fence failure")
	}
	entry := fixture.routes.Snapshot().Entries[fixture.task.ShardID]
	if entry.State != routing.RouteStatePreparing || entry.OwnerZoneID != "zone-b" {
		t.Fatalf("Current after Fence failure = %+v", entry)
	}
	if len(fixture.publisher.states) != 1 || fixture.publisher.states[0] != routing.RouteStatePreparing {
		t.Fatalf("published states after Fence failure = %v", fixture.publisher.states)
	}
	progress, found, err := fixture.progress.Get(context.Background(), fixture.task.ShardID)
	if err != nil || !found || progress.Step != StepRoutePreparing {
		t.Fatalf("progress after Fence failure = (%+v, %t, %v)", progress, found, err)
	}

	fixture.fence.err = nil
	if err := fixture.executor.Execute(context.Background(), fixture.task.ShardID, fixture.task.TaskID); err != nil {
		t.Fatalf("resume Execute: %v", err)
	}
	if fixture.zone.drainCalls != 1 || fixture.publisher.states[len(fixture.publisher.states)-1] != routing.RouteStateActive {
		t.Fatalf("resume repeated drain or missed ACTIVE: drain=%d states=%v", fixture.zone.drainCalls, fixture.publisher.states)
	}
}

func TestExecutorActiveCommitFailureNeverPublishesActiveAndResumeDoesNotRepeatLifecycle(t *testing.T) {
	fixture := newExecutorFixture(t)
	fixture.routeStore.activeErr = errors.New("route store unavailable")
	if err := fixture.executor.Execute(context.Background(), fixture.task.ShardID, fixture.task.TaskID); err == nil {
		t.Fatal("Execute succeeded, want ACTIVE commit failure")
	}
	if len(fixture.publisher.states) != 1 || fixture.publisher.states[0] != routing.RouteStatePreparing {
		t.Fatalf("published states = %v, want PREPARING only", fixture.publisher.states)
	}
	progress, found, err := fixture.progress.Get(context.Background(), fixture.task.ShardID)
	if err != nil || !found || progress.Step != StepTargetReady {
		t.Fatalf("progress = (%+v, %t, %v), want TARGET_READY", progress, found, err)
	}
	fixture.routeStore.activeErr = nil
	if err := fixture.executor.Execute(context.Background(), fixture.task.ShardID, fixture.task.TaskID); err != nil {
		t.Fatalf("resume Execute: %v", err)
	}
	if fixture.zone.drainCalls != 1 || fixture.zone.prepareCalls != 1 || fixture.publisher.states[len(fixture.publisher.states)-1] != routing.RouteStateActive {
		t.Fatalf("resume calls/states = drain %d prepare %d states %v", fixture.zone.drainCalls, fixture.zone.prepareCalls, fixture.publisher.states)
	}
}

func TestExecutorDrainFailureRestoresSourceBeforeFence(t *testing.T) {
	fixture := newExecutorFixture(t)
	fixture.zone.drainErr = errors.New("flush failed")
	if err := fixture.executor.Execute(context.Background(), fixture.task.ShardID, fixture.task.TaskID); err == nil {
		t.Fatal("Execute succeeded, want Drain failure")
	}
	entry := fixture.routes.Snapshot().Entries[fixture.task.ShardID]
	if entry.State != routing.RouteStateActive || entry.OwnerZoneID != "zone-a" || fixture.zone.restoreCalls != 1 || fixture.fence.calls != 0 || len(fixture.publisher.states) != 0 {
		t.Fatalf("failure state = entry %+v restore %d fence %d published %v", entry, fixture.zone.restoreCalls, fixture.fence.calls, fixture.publisher.states)
	}
}

type executorFixture struct {
	executor   *Executor
	routes     *routing.Map
	tasks      *MemoryTaskStore
	progress   *DurableProgressStore
	zone       *fakeZoneLifecycle
	fence      *fakeFenceStore
	publisher  *recordingRoutePublisher
	routeStore *failingActiveRouteStore
	task       Task
}

func newExecutorFixture(t *testing.T) executorFixture {
	t.Helper()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	routes, err := routing.NewStaticMap(now, time.Minute, []routing.ZoneCandidate{{ZoneID: "zone-a", Endpoint: "http://zone-a:8082"}})
	if err != nil {
		t.Fatal(err)
	}
	durable := routestore.NewMemoryStore()
	if _, _, err := durable.BootstrapIfEmpty(context.Background(), routestore.FromRoutingSnapshot(routes.Snapshot(), now)); err != nil {
		t.Fatal(err)
	}
	tasks, _ := NewMemoryTaskStore()
	source := routes.Snapshot().Entries[17]
	proposal := Task{ShardID: 17, Reason: ReasonRebalance, Priority: PriorityRebalance,
		SourceZoneID: source.OwnerZoneID, SourceEndpoint: source.OwnerEndpoint,
		SourceOwnerEpoch: source.OwnerEpoch, SourceRouteVersion: source.RouteVersion,
		TargetZoneID: "zone-b", TargetEndpoint: "http://zone-b:8082",
		PlannedFromMapVersion: routes.Snapshot().MapVersion, PlannedFromAvailabilityVersion: 2}
	created, _, err := tasks.UpsertPlanned(context.Background(), proposal)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := tasks.Claim(context.Background(), created.ShardID, created.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	progress, _ := NewProgressStore(newMemoryProgressBackend())
	zone := &fakeZoneLifecycle{manifest: Manifest{{PlayerID: 42, OwnerEpoch: 1, CheckpointRevision: 9}}}
	fence := &fakeFenceStore{}
	publisher := &recordingRoutePublisher{shardID: claimed.ShardID}
	routeStore := &failingActiveRouteStore{Store: durable}
	executor, err := NewExecutor(ExecutorConfig{
		Tasks: tasks, Progress: progress, Routes: routes, RouteStore: routeStore,
		Zones: zone, Fences: fence, Publisher: publisher,
		Now: func() time.Time { return now }, LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return executorFixture{executor: executor, routes: routes, tasks: tasks, progress: progress, zone: zone, fence: fence, publisher: publisher, routeStore: routeStore, task: claimed}
}

type fakeZoneLifecycle struct {
	manifest                                             Manifest
	drainCalls, restoreCalls, prepareCalls, refreshCalls int
	drainErr                                             error
}

func (zone *fakeZoneLifecycle) Drain(context.Context, routing.RouteEntry, string) (Manifest, error) {
	zone.drainCalls++
	return append(Manifest(nil), zone.manifest...), zone.drainErr
}
func (zone *fakeZoneLifecycle) Restore(context.Context, routing.RouteEntry, string) error {
	zone.restoreCalls++
	return nil
}
func (zone *fakeZoneLifecycle) Prepare(context.Context, routing.RouteEntry, Manifest) error {
	zone.prepareCalls++
	return nil
}
func (zone *fakeZoneLifecycle) RefreshOwnership(context.Context, routing.RouteEntry) error {
	zone.refreshCalls++
	return nil
}

type fakeFenceStore struct {
	calls int
	err   error
}

func (store *fakeFenceStore) AdvanceFence(context.Context, routing.RouteEntry) error {
	store.calls++
	return store.err
}

type recordingRoutePublisher struct {
	shardID uint32
	states  []routing.RouteState
}

func (publisher *recordingRoutePublisher) PublishRoutes(_ routing.Snapshot, current routing.Snapshot) error {
	publisher.states = append(publisher.states, current.Entries[publisher.shardID].State)
	return nil
}

type failingActiveRouteStore struct {
	routestore.Store
	activeErr error
}

func (store *failingActiveRouteStore) CommitActive(ctx context.Context, entry routing.RouteEntry, expected uint64) (routestore.Snapshot, error) {
	if store.activeErr != nil {
		return routestore.Snapshot{}, store.activeErr
	}
	return store.Store.CommitActive(ctx, entry, expected)
}
