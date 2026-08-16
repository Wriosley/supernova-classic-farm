package placement

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/membership"
	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/migration"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func TestPlannerRunCoalescesMembershipBurst(t *testing.T) {
	current := staticSnapshot(t, eightCandidates())
	store, _ := migration.NewMemoryTaskStore()
	planner, _ := NewPlanner(&snapshotSource{snapshot: current}, fixedMembership{membershipSnapshot(eightCandidates(), membership.StateHealthy)}, store)
	ready := make(chan struct{})
	close(ready)
	triggers := make(chan struct{}, 8)
	triggers <- struct{}{}
	triggers <- struct{}{}
	triggers <- struct{}{}
	reports := make(chan error, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go planner.Run(ctx, time.Hour, ready, triggers, func(_ Result, err error) { reports <- err })

	for count := 0; count < 2; count++ {
		select {
		case err := <-reports:
			if err != nil {
				t.Fatalf("reconcile report: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for initial and coalesced reconcile")
		}
	}
	select {
	case err := <-reports:
		t.Fatalf("membership burst was not coalesced; extra report %v", err)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestPlannerEightToNineCreatesOnlyDesiredDiffWithoutMutatingCurrent(t *testing.T) {
	current := staticSnapshot(t, eightCandidates())
	before := cloneRoutingSnapshot(current)
	source := &snapshotSource{snapshot: current}
	registry := membershipSnapshot(eightCandidates(), membership.StateHealthy)
	registry.Members = append(registry.Members, member(Candidate{LogicalZoneID: "zone-8", Endpoint: "http://zone-8:8082"}, membership.StateHealthy))
	registry.AvailabilityVersion = 9
	store, err := migration.NewMemoryTaskStore()
	if err != nil {
		t.Fatalf("NewMemoryTaskStore: %v", err)
	}
	planner, err := NewPlanner(source, fixedMembership{registry}, store)
	if err != nil {
		t.Fatalf("NewPlanner: %v", err)
	}

	result, err := planner.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.Proposed != 442 || result.Unchanged != int(routing.ShardCount)-442 {
		t.Fatalf("result = %+v, want proposed=442 unchanged=3654", result)
	}
	open, err := store.LoadOpen(context.Background())
	if err != nil || len(open) != 442 {
		t.Fatalf("LoadOpen = %d tasks, err=%v", len(open), err)
	}
	for _, task := range open {
		if task.Reason != migration.ReasonRebalance || task.TargetZoneID != "zone-8" ||
			task.PlannedFromMapVersion != current.MapVersion ||
			task.PlannedFromAvailabilityVersion != registry.AvailabilityVersion {
			t.Fatalf("invalid proposal: %+v", task)
		}
	}
	if !reflect.DeepEqual(source.snapshot, before) {
		t.Fatal("planner mutated Current snapshot")
	}
}

func TestPlannerPreservesClaimedTaskBeforeFirstProgressBoundary(t *testing.T) {
	current := staticSnapshot(t, eightCandidates())
	candidates := append(eightCandidates(), Candidate{LogicalZoneID: "zone-8", Endpoint: "http://zone-8:8082"})
	desired, err := Compute(current.ShardCount, current.AssignmentAlgorithmVersion, candidates)
	if err != nil {
		t.Fatal(err)
	}
	shardID := uint32(0)
	for index := range desired {
		if desired[index].OwnerZoneID != current.Entries[index].OwnerZoneID {
			shardID = uint32(index)
			break
		}
	}
	source := current.Entries[shardID]
	earlierTarget := eightCandidates()[0]
	if earlierTarget.LogicalZoneID == source.OwnerZoneID {
		earlierTarget = eightCandidates()[1]
	}
	running := migration.Task{
		ShardID: shardID, TaskID: make([]byte, 16), Reason: migration.ReasonRebalance,
		Status: migration.StatusRunning, Priority: migration.PriorityRebalance,
		SourceZoneID: source.OwnerZoneID, SourceEndpoint: source.OwnerEndpoint,
		SourceOwnerEpoch: source.OwnerEpoch, SourceRouteVersion: source.RouteVersion,
		TargetZoneID: earlierTarget.LogicalZoneID, TargetEndpoint: earlierTarget.Endpoint,
		PlannedFromMapVersion: current.MapVersion, PlannedFromAvailabilityVersion: 8,
		CreatedAtMS: 1, UpdatedAtMS: 1,
	}
	store, err := migration.NewMemoryTaskStore(running)
	if err != nil {
		t.Fatal(err)
	}
	planner, err := NewPlanner(&snapshotSource{snapshot: current}, fixedMembership{membershipSnapshot(candidates, membership.StateHealthy)}, store)
	if err != nil {
		t.Fatal(err)
	}

	result, err := planner.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.Deduplicated == 0 {
		t.Fatalf("result = %+v, want claimed task deduplicated", result)
	}
	got, found, err := store.Get(context.Background(), shardID)
	if err != nil || !found || got.Status != migration.StatusRunning || got.TargetZoneID != earlierTarget.LogicalZoneID {
		t.Fatalf("claimed task changed = (%+v, %t, %v)", got, found, err)
	}
}

func TestPlannerWaitsForMinimumHealthyZones(t *testing.T) {
	current := staticSnapshot(t, eightCandidates())
	members := membershipSnapshot(eightCandidates()[:7], membership.StateHealthy)
	store, _ := migration.NewMemoryTaskStore()
	planner, err := NewPlannerWithMinimum(&snapshotSource{snapshot: current}, fixedMembership{members}, store, 8)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = planner.Reconcile(context.Background()); !errors.Is(err, ErrInsufficientHealthyZones) {
		t.Fatalf("Reconcile error = %v", err)
	}
	open, _ := store.LoadOpen(context.Background())
	if len(open) != 0 {
		t.Fatalf("planner wrote %d premature tasks", len(open))
	}
}

func TestPlannerNoOpAndCancelsStalePlannedTask(t *testing.T) {
	current := staticSnapshot(t, eightCandidates())
	stale := migration.Task{
		ShardID: 1, TaskID: make([]byte, 16), Reason: migration.ReasonRebalance,
		Status: migration.StatusPlanned, Priority: migration.PriorityRebalance,
		SourceZoneID: "zone-x", SourceEndpoint: "http://zone-x:8082",
		SourceOwnerEpoch: 1, SourceRouteVersion: 1,
		TargetZoneID:                   current.Entries[1].OwnerZoneID,
		TargetEndpoint:                 current.Entries[1].OwnerEndpoint,
		PlannedFromMapVersion:          current.MapVersion,
		PlannedFromAvailabilityVersion: 7, CreatedAtMS: 1, UpdatedAtMS: 1,
	}
	store, err := migration.NewMemoryTaskStore(stale)
	if err != nil {
		t.Fatalf("NewMemoryTaskStore: %v", err)
	}
	planner, err := NewPlanner(&snapshotSource{snapshot: current}, fixedMembership{membershipSnapshot(eightCandidates(), membership.StateHealthy)}, store)
	if err != nil {
		t.Fatalf("NewPlanner: %v", err)
	}

	result, err := planner.Reconcile(context.Background())
	if err != nil || result.Proposed != 0 || result.Cancelled != 1 || result.Unchanged != int(routing.ShardCount) {
		t.Fatalf("Reconcile = (%+v, %v)", result, err)
	}
	open, err := store.LoadOpen(context.Background())
	if err != nil || len(open) != 0 {
		t.Fatalf("open tasks after cancellation = (%+v, %v)", open, err)
	}
}

func TestPlannerDoesNotCancelPreparingTaskWhenTargetMatchesDesired(t *testing.T) {
	current := staticSnapshot(t, eightCandidates())
	const shardID = uint32(42)
	source := current.Entries[shardID]
	source.State = routing.RouteStatePreparing
	current.Entries[shardID] = source
	task := migration.Task{
		ShardID: shardID, TaskID: make([]byte, 16), Reason: migration.ReasonDrain,
		Status: migration.StatusPlanned, Priority: migration.PriorityDrain,
		SourceZoneID: "zone-old", SourceEndpoint: "http://zone-old:8082",
		SourceOwnerEpoch: 1, SourceRouteVersion: 1,
		TargetZoneID: source.OwnerZoneID, TargetEndpoint: source.OwnerEndpoint,
		PlannedFromMapVersion: current.MapVersion, PlannedFromAvailabilityVersion: 8,
		CreatedAtMS: 1, UpdatedAtMS: 1,
	}
	store, err := migration.NewMemoryTaskStore(task)
	if err != nil {
		t.Fatal(err)
	}
	planner, _ := NewPlanner(&snapshotSource{snapshot: current}, fixedMembership{membershipSnapshot(eightCandidates(), membership.StateHealthy)}, store)
	result, err := planner.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := store.Get(context.Background(), shardID)
	if err != nil || !found || got.Status != migration.StatusPlanned || result.Cancelled != 0 {
		t.Fatalf("PREPARING task was cancelled: result=%+v task=(%+v,%t,%v)", result, got, found, err)
	}
}

func TestPlannerPreservesCommittedActiveTaskForProgressTail(t *testing.T) {
	current := staticSnapshot(t, eightCandidates())
	const shardID = uint32(42)
	active := current.Entries[shardID]
	task := migration.Task{
		ShardID: shardID, TaskID: make([]byte, 16), Reason: migration.ReasonDrain,
		Status: migration.StatusPlanned, Priority: migration.PriorityDrain,
		SourceZoneID: "zone-old", SourceEndpoint: "http://zone-old:8082",
		SourceOwnerEpoch: active.OwnerEpoch, SourceRouteVersion: active.RouteVersion,
		TargetZoneID: active.OwnerZoneID, TargetEndpoint: active.OwnerEndpoint,
		PlannedFromMapVersion: current.MapVersion, PlannedFromAvailabilityVersion: 8,
		CreatedAtMS: 1, UpdatedAtMS: 1,
	}
	active.OwnerEpoch = task.SourceOwnerEpoch + 1
	active.RouteVersion = task.SourceRouteVersion + 2
	active.PreviousOwnerZoneID = task.SourceZoneID
	active.TransitionID = "committed-transition"
	current.Entries[shardID] = active

	store, err := migration.NewMemoryTaskStore(task)
	if err != nil {
		t.Fatal(err)
	}
	planner, _ := NewPlanner(&snapshotSource{snapshot: current}, fixedMembership{membershipSnapshot(eightCandidates(), membership.StateHealthy)}, store)
	result, err := planner.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := store.Get(context.Background(), shardID)
	if err != nil || !found || got.Status != migration.StatusPlanned || result.Cancelled != 0 || result.Deduplicated == 0 {
		t.Fatalf("committed task tail was cancelled: result=%+v task=(%+v,%t,%v)", result, got, found, err)
	}
}

func TestPlannerSkipsShardWhoseCurrentOwnerIsUnhealthy(t *testing.T) {
	current := staticSnapshot(t, eightCandidates())
	members := membershipSnapshot(append(eightCandidates(), Candidate{LogicalZoneID: "zone-8", Endpoint: "http://zone-8:8082"}), membership.StateHealthy)
	for index := range members.Members {
		if members.Members[index].LogicalZoneID == current.Entries[1].OwnerZoneID {
			members.Members[index].State = membership.StateSuspect
		}
	}
	store, _ := migration.NewMemoryTaskStore()
	planner, _ := NewPlanner(&snapshotSource{snapshot: current}, fixedMembership{members}, store)

	result, err := planner.Reconcile(context.Background())
	if err != nil || result.SkippedUnhealthy == 0 {
		t.Fatalf("Reconcile = (%+v, %v), want unhealthy skips", result, err)
	}
	if _, found, err := store.Get(context.Background(), 1); err != nil || found {
		t.Fatalf("suspect-owner shard task found=%t err=%v", found, err)
	}
}

func TestPlannerDrainingZoneCreatesOnlyDrainTasks(t *testing.T) {
	candidates := eightCandidates()
	current := staticSnapshot(t, candidates)
	members := membershipSnapshot(candidates, membership.StateHealthy)
	drainingID := current.Entries[0].OwnerZoneID
	want := 0
	for _, entry := range current.Entries {
		if entry.OwnerZoneID == drainingID {
			want++
		}
	}
	for index := range members.Members {
		if members.Members[index].LogicalZoneID == drainingID {
			members.Members[index].State = membership.StateDraining
		}
	}
	store, _ := migration.NewMemoryTaskStore()
	planner, _ := NewPlanner(&snapshotSource{snapshot: current}, fixedMembership{members}, store)
	result, err := planner.Reconcile(context.Background())
	if err != nil || result.Proposed != want {
		t.Fatalf("Reconcile = (%+v, %v), want %d drain tasks", result, err, want)
	}
	open, err := store.LoadOpen(context.Background())
	if err != nil || len(open) != want {
		t.Fatalf("LoadOpen = %d, %v", len(open), err)
	}
	for _, task := range open {
		if task.SourceZoneID != drainingID || task.Reason != migration.ReasonDrain || task.Priority != migration.PriorityDrain {
			t.Fatalf("non-drain proposal: %+v", task)
		}
	}
}

func TestPlannerAbortsBeforeUpsertWhenCurrentVersionChanges(t *testing.T) {
	current := staticSnapshot(t, eightCandidates())
	source := &snapshotSource{snapshot: current, changeOnCall: 2}
	members := membershipSnapshot(append(eightCandidates(), Candidate{LogicalZoneID: "zone-8", Endpoint: "http://zone-8:8082"}), membership.StateHealthy)
	store, _ := migration.NewMemoryTaskStore()
	planner, _ := NewPlanner(source, fixedMembership{members}, store)

	if _, err := planner.Reconcile(context.Background()); !errors.Is(err, ErrCurrentChanged) {
		t.Fatalf("Reconcile error = %v, want ErrCurrentChanged", err)
	}
	open, err := store.LoadOpen(context.Background())
	if err != nil || len(open) != 0 {
		t.Fatalf("stale plan wrote tasks: (%+v, %v)", open, err)
	}
}

type snapshotSource struct {
	snapshot     routing.Snapshot
	calls        int
	changeOnCall int
}

func (source *snapshotSource) Snapshot() routing.Snapshot {
	source.calls++
	result := cloneRoutingSnapshot(source.snapshot)
	if source.changeOnCall > 0 && source.calls >= source.changeOnCall {
		result.MapVersion++
	}
	return result
}

type fixedMembership struct{ snapshot membership.Snapshot }

func (source fixedMembership) Snapshot() membership.Snapshot { return source.snapshot }

func staticSnapshot(t *testing.T, candidates []Candidate) routing.Snapshot {
	t.Helper()
	zones := make([]routing.ZoneCandidate, len(candidates))
	for index, candidate := range candidates {
		zones[index] = routing.ZoneCandidate{ZoneID: candidate.LogicalZoneID, Endpoint: candidate.Endpoint}
	}
	routes, err := routing.NewStaticMap(time.Unix(1_700_000_000, 0), time.Minute, zones)
	if err != nil {
		t.Fatalf("NewStaticMap: %v", err)
	}
	return routes.Snapshot()
}

func membershipSnapshot(candidates []Candidate, state membership.State) membership.Snapshot {
	members := make([]membership.Member, len(candidates))
	for index, candidate := range candidates {
		members[index] = member(candidate, state)
	}
	return membership.Snapshot{AvailabilityVersion: uint64(len(candidates)), Members: members}
}

func member(candidate Candidate, state membership.State) membership.Member {
	return membership.Member{LogicalZoneID: candidate.LogicalZoneID, Endpoint: candidate.Endpoint, State: state}
}

func cloneRoutingSnapshot(snapshot routing.Snapshot) routing.Snapshot {
	snapshot.Entries = append([]routing.RouteEntry(nil), snapshot.Entries...)
	return snapshot
}
