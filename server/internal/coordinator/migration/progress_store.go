package migration

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

type ProgressBackend interface {
	UpsertProgress(context.Context, routing.MigrationProgressRow) error
	LoadProgress(context.Context, uint32) (routing.MigrationProgressRow, bool, error)
	LoadOpenProgress(context.Context) ([]routing.MigrationProgressRow, error)
	MarkAbandoned(context.Context, uint32, string, time.Time) error
	DeleteOpenProgress(context.Context, uint32, string) error
}

type ProgressStore interface {
	LoadOpen(context.Context) ([]Progress, error)
	Get(context.Context, uint32) (Progress, bool, error)
	Create(context.Context, Progress) error
	Advance(context.Context, Progress, Step) error
	Abandon(context.Context, Progress) error
	Complete(context.Context, Progress) error
}

type DurableProgressStore struct {
	mu      sync.Mutex
	backend ProgressBackend
}

func NewProgressStore(backend ProgressBackend) (*DurableProgressStore, error) {
	if backend == nil {
		return nil, errors.New("migration progress backend is required")
	}
	return &DurableProgressStore{backend: backend}, nil
}

func (store *DurableProgressStore) LoadOpen(ctx context.Context) ([]Progress, error) {
	rows, err := store.backend.LoadOpenProgress(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]Progress, 0, len(rows))
	for _, row := range rows {
		progress, err := progressFromRecord(row)
		if err != nil {
			return nil, err
		}
		result = append(result, progress)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ShardID < result[j].ShardID })
	return result, nil
}

func (store *DurableProgressStore) Get(ctx context.Context, shardID uint32) (Progress, bool, error) {
	row, found, err := store.backend.LoadProgress(ctx, shardID)
	if err != nil || !found {
		return Progress{}, found, err
	}
	// Backends keep terminal rows behind as history, so only an OPEN row
	// describes an in-flight migration. Reporting an ABANDONED row here would
	// make every freshly planned task for the Shard conflict forever.
	if row.Status != routing.MigrationStatusOpen {
		return Progress{}, false, nil
	}
	progress, err := progressFromRecord(row)
	return progress, err == nil, err
}

func (store *DurableProgressStore) Create(ctx context.Context, progress Progress) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if progress.Step != StepSourceDraining {
		return ErrProgressConflict
	}
	current, found, err := store.Get(ctx, progress.ShardID)
	if err != nil {
		return err
	}
	if found {
		if current.Step == progress.Step && sameFrozenProgress(current, progress) {
			return nil
		}
		return ErrProgressConflict
	}
	row, err := progressRecord(progress)
	if err != nil {
		return err
	}
	return store.backend.UpsertProgress(ctx, row)
}

func (store *DurableProgressStore) Advance(ctx context.Context, progress Progress, next Step) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	current, found, err := store.Get(ctx, progress.ShardID)
	if err != nil {
		return err
	}
	if !found || !sameProgressIdentity(current, progress) ||
		(current.Step != StepSourceDraining && !slices.Equal(current.Manifest, progress.Manifest)) {
		return ErrProgressConflict
	}
	if current.Step == next {
		return nil
	}
	if current.Step != progress.Step || nextStep(current.Step) != next {
		return ErrProgressConflict
	}
	progress.Step = next
	progress.UpdatedAtMS = time.Now().UTC().UnixMilli()
	row, err := progressRecord(progress)
	if err != nil {
		return err
	}
	return store.backend.UpsertProgress(ctx, row)
}

func (store *DurableProgressStore) Abandon(ctx context.Context, progress Progress) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	current, found, err := store.Get(ctx, progress.ShardID)
	if err != nil {
		return err
	}
	if !found || !sameFrozenProgress(current, progress) {
		return ErrProgressConflict
	}
	if stepAtOrAfterFence(current.Step) {
		return ErrFenceAlreadyAdvanced
	}
	return store.backend.MarkAbandoned(ctx, current.ShardID, current.TransitionID, time.Now().UTC())
}

func (store *DurableProgressStore) Complete(ctx context.Context, progress Progress) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	current, found, err := store.Get(ctx, progress.ShardID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if current.Step != StepRouteActive || !sameFrozenProgress(current, progress) {
		return ErrProgressConflict
	}
	return store.backend.DeleteOpenProgress(ctx, current.ShardID, current.TransitionID)
}

func nextStep(step Step) Step {
	return map[Step]Step{
		StepSourceDraining: StepSourceFlushed,
		StepSourceFlushed:  StepRoutePreparing,
		StepRoutePreparing: StepFenceAdvanced,
		StepFenceAdvanced:  StepTargetLoading,
		StepTargetLoading:  StepTargetReady,
		StepTargetReady:    StepRouteActive,
	}[step]
}

func stepAtOrAfterFence(step Step) bool {
	switch step {
	case StepFenceAdvanced, StepTargetLoading, StepTargetReady, StepRouteActive:
		return true
	default:
		return false
	}
}

func sameFrozenProgress(left, right Progress) bool {
	return sameProgressIdentity(left, right) && slices.Equal(left.Manifest, right.Manifest)
}

func sameProgressIdentity(left, right Progress) bool {
	// Compare exactly the frozen identity persisted by MigrationProgress. Source
	// may be an ACTIVE route left by an earlier transition, but the progress
	// schema intentionally does not persist that route's historical
	// previous_owner_zone_id or transition_id. Comparing the whole RouteEntry
	// therefore makes a write/read round trip conflict for otherwise valid
	// routes. Expiry and timestamps are also runtime/durability metadata rather
	// than migration identity.
	return left.ShardID == right.ShardID &&
		left.TransitionID == right.TransitionID &&
		samePersistedSource(left.Source, right.Source) &&
		samePersistedPrepared(left.Prepared, right.Prepared)
}

func samePersistedSource(left, right routing.RouteEntry) bool {
	return left.ShardID == right.ShardID &&
		left.OwnerZoneID == right.OwnerZoneID &&
		left.OwnerEndpoint == right.OwnerEndpoint &&
		left.OwnerEpoch == right.OwnerEpoch &&
		left.RouteVersion == right.RouteVersion &&
		left.State == right.State &&
		left.LeaseID == right.LeaseID
}

func samePersistedPrepared(left, right routing.RouteEntry) bool {
	return left.ShardID == right.ShardID &&
		left.OwnerZoneID == right.OwnerZoneID &&
		left.OwnerEndpoint == right.OwnerEndpoint &&
		left.OwnerEpoch == right.OwnerEpoch &&
		left.RouteVersion == right.RouteVersion &&
		left.State == right.State &&
		left.LeaseID == right.LeaseID &&
		left.LeaseTerm == right.LeaseTerm &&
		left.PreviousOwnerZoneID == right.PreviousOwnerZoneID &&
		left.TransitionID == right.TransitionID
}

func progressRecord(progress Progress) (routing.MigrationProgressRow, error) {
	players := make([]routing.MigrationPlayer, len(progress.Manifest))
	for index, item := range progress.Manifest {
		if item.PlayerID == 0 || item.OwnerEpoch == 0 || item.CheckpointRevision == 0 {
			return routing.MigrationProgressRow{}, errors.New("migration manifest entry is invalid")
		}
		players[index] = routing.MigrationPlayer{
			PlayerID: strconv.FormatUint(item.PlayerID, 10), OwnerEpoch: strconv.FormatUint(item.OwnerEpoch, 10),
			CheckpointRevision: strconv.FormatUint(item.CheckpointRevision, 10),
		}
	}
	return routing.MigrationProgressRow{
		ShardID: progress.ShardID, TransitionID: progress.TransitionID,
		Status: routing.MigrationStatusOpen, Step: string(progress.Step),
		SourceZoneID: progress.Source.OwnerZoneID, SourceEndpoint: progress.Source.OwnerEndpoint,
		SourceOwnerEpoch: progress.Source.OwnerEpoch, SourceRouteVersion: progress.Source.RouteVersion,
		SourceLeaseID: progress.Source.LeaseID,
		TargetZoneID:  progress.Prepared.OwnerZoneID, TargetEndpoint: progress.Prepared.OwnerEndpoint,
		PreparedOwnerEpoch: progress.Prepared.OwnerEpoch, PreparedRouteVersion: progress.Prepared.RouteVersion,
		PreparedLeaseID: progress.Prepared.LeaseID, PreparedLeaseTerm: progress.Prepared.LeaseTerm,
		Players: players, UpdatedAtMS: progress.UpdatedAtMS,
	}, nil
}

func progressFromRecord(row routing.MigrationProgressRow) (Progress, error) {
	manifest := make(Manifest, len(row.Players))
	for index, item := range row.Players {
		playerID, playerErr := strconv.ParseUint(item.PlayerID, 10, 64)
		epoch, epochErr := strconv.ParseUint(item.OwnerEpoch, 10, 64)
		revision, revisionErr := strconv.ParseUint(item.CheckpointRevision, 10, 64)
		if playerErr != nil || epochErr != nil || revisionErr != nil || playerID == 0 || epoch == 0 || revision == 0 {
			return Progress{}, errors.New("migration progress manifest is corrupt")
		}
		manifest[index] = ManifestEntry{PlayerID: playerID, OwnerEpoch: epoch, CheckpointRevision: revision}
	}
	step := Step(row.Step)
	if step != StepSourceDraining && nextStep(step) == "" && step != StepRouteActive {
		return Progress{}, fmt.Errorf("unsupported migration progress step %q", row.Step)
	}
	updated := time.UnixMilli(row.UpdatedAtMS).UTC()
	return Progress{
		ShardID: row.ShardID, TransitionID: row.TransitionID, Step: step, Manifest: manifest, UpdatedAtMS: row.UpdatedAtMS,
		Source: routing.RouteEntry{ShardID: row.ShardID, OwnerZoneID: row.SourceZoneID, OwnerEndpoint: row.SourceEndpoint,
			OwnerEpoch: row.SourceOwnerEpoch, RouteVersion: row.SourceRouteVersion, State: routing.RouteStateActive,
			LeaseID: row.SourceLeaseID, LeaseExpiresAt: updated},
		Prepared: routing.RouteEntry{ShardID: row.ShardID, OwnerZoneID: row.TargetZoneID, OwnerEndpoint: row.TargetEndpoint,
			OwnerEpoch: row.PreparedOwnerEpoch, RouteVersion: row.PreparedRouteVersion, State: routing.RouteStatePreparing,
			LeaseTerm: row.PreparedLeaseTerm, LeaseID: row.PreparedLeaseID, LeaseExpiresAt: updated,
			PreviousOwnerZoneID: row.SourceZoneID, TransitionID: row.TransitionID, UpdatedAt: updated},
	}, nil
}
