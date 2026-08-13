package migration

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func TestProgressConversionRoundTripsFrozenEvidence(t *testing.T) {
	want := sampleProgress(StepSourceFlushed)
	want.Manifest = Manifest{{PlayerID: 42, OwnerEpoch: 7, CheckpointRevision: 19}}

	row, err := progressRecord(want)
	if err != nil {
		t.Fatalf("progressRecord: %v", err)
	}
	got, err := progressFromRecord(row)
	if err != nil {
		t.Fatalf("progressFromRecord: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestProgressStoreEnforcesAdjacentTransitionsAndFrozenEvidence(t *testing.T) {
	ctx := context.Background()
	backend := newMemoryProgressBackend()
	store, err := NewProgressStore(backend)
	if err != nil {
		t.Fatal(err)
	}
	progress := sampleProgress(StepSourceDraining)
	if err := store.Create(ctx, progress); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Create(ctx, progress); err != nil {
		t.Fatalf("Create replay: %v", err)
	}
	if err := store.Advance(ctx, progress, StepFenceAdvanced); !errors.Is(err, ErrProgressConflict) {
		t.Fatalf("illegal jump error = %v, want ErrProgressConflict", err)
	}

	progress.Manifest = Manifest{{PlayerID: 9, OwnerEpoch: 5, CheckpointRevision: 11}}
	if err := store.Advance(ctx, progress, StepSourceFlushed); err != nil {
		t.Fatalf("Advance SOURCE_FLUSHED: %v", err)
	}
	loaded, found, err := store.Get(ctx, progress.ShardID)
	if err != nil || !found || loaded.Step != StepSourceFlushed || !reflect.DeepEqual(loaded.Manifest, progress.Manifest) {
		t.Fatalf("Get = (%+v, %t, %v)", loaded, found, err)
	}

	conflict := loaded
	conflict.Prepared.OwnerZoneID = "zone-other"
	if err := store.Advance(ctx, conflict, StepRoutePreparing); !errors.Is(err, ErrProgressConflict) {
		t.Fatalf("frozen target conflict error = %v, want ErrProgressConflict", err)
	}
	if err := store.Advance(ctx, loaded, StepRoutePreparing); err != nil {
		t.Fatalf("Advance ROUTE_PREPARING: %v", err)
	}
	if err := store.Advance(ctx, loaded, StepRoutePreparing); err != nil {
		t.Fatalf("Advance replay: %v", err)
	}
	for _, next := range []Step{StepFenceAdvanced, StepTargetLoading, StepTargetReady, StepRouteActive} {
		current, found, err := store.Get(ctx, progress.ShardID)
		if err != nil || !found {
			t.Fatalf("Get before %s = (%+v, %t, %v)", next, current, found, err)
		}
		if err := store.Advance(ctx, current, next); err != nil {
			t.Fatalf("Advance %s: %v", next, err)
		}
	}
}

func TestProgressStoreRestartLoadCompleteAndAbandon(t *testing.T) {
	ctx := context.Background()
	backend := newMemoryProgressBackend()
	store, _ := NewProgressStore(backend)
	first := sampleProgress(StepSourceDraining)
	second := sampleProgress(StepSourceDraining)
	second.ShardID = 3
	second.Source.ShardID = 3
	second.Prepared.ShardID = 3
	second.TransitionID = "33333333-3333-4333-8333-333333333333"
	second.Prepared.TransitionID = second.TransitionID
	if err := store.Create(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(ctx, second); err != nil {
		t.Fatal(err)
	}

	restarted, _ := NewProgressStore(backend)
	open, err := restarted.LoadOpen(ctx)
	if err != nil || len(open) != 2 || open[0].ShardID != 3 || open[1].ShardID != 17 {
		t.Fatalf("LoadOpen = (%+v, %v)", open, err)
	}
	if err := restarted.Abandon(ctx, first); err != nil {
		t.Fatalf("Abandon before Fence: %v", err)
	}
	second.Step = StepRouteActive
	backend.rows[second.ShardID] = mustProgressRecord(t, second)
	if err := restarted.Complete(ctx, second); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	open, err = restarted.LoadOpen(ctx)
	if err != nil || len(open) != 0 {
		t.Fatalf("LoadOpen after terminal states = (%+v, %v)", open, err)
	}

	fenced := sampleProgress(StepFenceAdvanced)
	backend.rows[fenced.ShardID] = mustProgressRecord(t, fenced)
	if err := restarted.Abandon(ctx, fenced); !errors.Is(err, ErrFenceAlreadyAdvanced) {
		t.Fatalf("post-Fence Abandon error = %v, want ErrFenceAlreadyAdvanced", err)
	}
}

func sampleProgress(step Step) Progress {
	updated := time.UnixMilli(1000).UTC()
	return Progress{
		ShardID: 17, TransitionID: "11111111-2222-4333-8444-555555555555", Step: step,
		Source:      routing.RouteEntry{ShardID: 17, OwnerZoneID: "zone-a", OwnerEndpoint: "http://zone-a:8082", OwnerEpoch: 5, RouteVersion: 8, State: routing.RouteStateActive, LeaseID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", LeaseExpiresAt: updated},
		Prepared:    routing.RouteEntry{ShardID: 17, OwnerZoneID: "zone-b", OwnerEndpoint: "http://zone-b:8082", OwnerEpoch: 6, RouteVersion: 9, State: routing.RouteStatePreparing, LeaseID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", LeaseTerm: 2, LeaseExpiresAt: updated, PreviousOwnerZoneID: "zone-a", TransitionID: "11111111-2222-4333-8444-555555555555", UpdatedAt: updated},
		UpdatedAtMS: 1000,
	}
}

func mustProgressRecord(t *testing.T, progress Progress) routing.MigrationProgressRow {
	t.Helper()
	row, err := progressRecord(progress)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

type memoryProgressBackend struct {
	rows map[uint32]routing.MigrationProgressRow
}

func newMemoryProgressBackend() *memoryProgressBackend {
	return &memoryProgressBackend{rows: make(map[uint32]routing.MigrationProgressRow)}
}

func (backend *memoryProgressBackend) UpsertProgress(_ context.Context, row routing.MigrationProgressRow) error {
	backend.rows[row.ShardID] = row
	return nil
}

func (backend *memoryProgressBackend) LoadProgress(_ context.Context, shardID uint32) (routing.MigrationProgressRow, bool, error) {
	row, found := backend.rows[shardID]
	return row, found, nil
}

func (backend *memoryProgressBackend) LoadOpenProgress(context.Context) ([]routing.MigrationProgressRow, error) {
	rows := make([]routing.MigrationProgressRow, 0, len(backend.rows))
	for _, row := range backend.rows {
		if row.Status == routing.MigrationStatusOpen {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func (backend *memoryProgressBackend) MarkAbandoned(_ context.Context, shardID uint32, transitionID string, _ time.Time) error {
	row, found := backend.rows[shardID]
	if !found || row.TransitionID != transitionID {
		return ErrProgressConflict
	}
	row.Status = routing.MigrationStatusAbandoned
	backend.rows[shardID] = row
	return nil
}

func (backend *memoryProgressBackend) DeleteOpenProgress(_ context.Context, shardID uint32, transitionID string) error {
	row, found := backend.rows[shardID]
	if !found || row.TransitionID != transitionID {
		return ErrProgressConflict
	}
	delete(backend.rows, shardID)
	return nil
}
