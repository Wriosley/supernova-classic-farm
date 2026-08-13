package migration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/routestore"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

type CurrentRoutes interface {
	Snapshot() routing.Snapshot
	ProposePrepare(uint32, string, string, time.Time, time.Duration) (routing.RouteEntry, error)
	ApplyCommittedSnapshot(routing.Snapshot) error
}

type RoutePublisher interface {
	PublishRoutes(routing.Snapshot, routing.Snapshot) error
}

type ExecutorConfig struct {
	Tasks         TaskStore
	Progress      ProgressStore
	Routes        CurrentRoutes
	RouteStore    routestore.Store
	Zones         ZoneLifecycle
	Fences        FenceStore
	Publisher     RoutePublisher
	Now           func() time.Time
	LeaseDuration time.Duration
}

type Executor struct{ cfg ExecutorConfig }

func NewExecutor(cfg ExecutorConfig) (*Executor, error) {
	if cfg.Tasks == nil || cfg.Progress == nil || cfg.Routes == nil || cfg.RouteStore == nil ||
		cfg.Zones == nil || cfg.Fences == nil || cfg.Publisher == nil {
		return nil, errors.New("migration executor dependencies are required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.LeaseDuration <= 0 {
		return nil, errors.New("migration executor lease duration must be positive")
	}
	return &Executor{cfg: cfg}, nil
}

func (executor *Executor) Execute(ctx context.Context, shardID uint32, taskID []byte) error {
	task, found, err := executor.cfg.Tasks.Get(ctx, shardID)
	if err != nil {
		return err
	}
	if !found || task.Status != StatusRunning || !bytes.Equal(task.TaskID, taskID) {
		return ErrTaskConflict
	}
	progress, progressFound, err := executor.cfg.Progress.Get(ctx, shardID)
	if err != nil {
		return err
	}
	if !progressFound {
		current := executor.cfg.Routes.Snapshot()
		if len(current.Entries) != int(routing.ShardCount) {
			return errors.New("migration Current snapshot is incomplete")
		}
		source := current.Entries[shardID]
		if completedTaskRoute(task, source) {
			return executor.cfg.Tasks.Complete(ctx, shardID, taskID)
		}
		if !taskMatchesSource(task, source) {
			return ErrTaskConflict
		}
		prepared, err := executor.cfg.Routes.ProposePrepare(shardID, task.TargetZoneID, task.TargetEndpoint, executor.cfg.Now().UTC(), executor.cfg.LeaseDuration)
		if err != nil {
			return fmt.Errorf("propose PREPARING route: %w", err)
		}
		progress = Progress{ShardID: shardID, TransitionID: prepared.TransitionID, Step: StepSourceDraining,
			Source: source, Prepared: prepared, UpdatedAtMS: executor.cfg.Now().UTC().UnixMilli()}
		if err := executor.cfg.Progress.Create(ctx, progress); err != nil {
			return fmt.Errorf("create migration progress: %w", err)
		}
	} else if !progressMatchesTask(progress, task) {
		return ErrProgressConflict
	}

	for {
		switch progress.Step {
		case StepSourceDraining:
			manifest, err := executor.cfg.Zones.Drain(ctx, progress.Source, progress.TransitionID)
			if err != nil {
				_ = executor.cfg.Zones.Restore(ctx, progress.Source, progress.TransitionID)
				return fmt.Errorf("drain Source shard: %w", err)
			}
			progress.Manifest = manifest
			if err := executor.cfg.Progress.Advance(ctx, progress, StepSourceFlushed); err != nil {
				return err
			}
			progress.Step = StepSourceFlushed

		case StepSourceFlushed:
			stored, err := executor.ensurePreparing(ctx, progress)
			if err != nil {
				return err
			}
			progress.Prepared = stored.Entries[shardID]
			if err := executor.applyAndPublish(stored); err != nil {
				return err
			}
			if err := executor.cfg.Progress.Advance(ctx, progress, StepRoutePreparing); err != nil {
				return err
			}
			progress.Step = StepRoutePreparing

		case StepRoutePreparing:
			if err := executor.cfg.Fences.AdvanceFence(ctx, progress.Prepared); err != nil {
				return fmt.Errorf("advance Shard Fence: %w", err)
			}
			if err := executor.cfg.Progress.Advance(ctx, progress, StepFenceAdvanced); err != nil {
				return err
			}
			progress.Step = StepFenceAdvanced

		case StepFenceAdvanced:
			if err := executor.cfg.Progress.Advance(ctx, progress, StepTargetLoading); err != nil {
				return err
			}
			progress.Step = StepTargetLoading

		case StepTargetLoading:
			if err := executor.cfg.Zones.Prepare(ctx, progress.Prepared, progress.Manifest); err != nil {
				return fmt.Errorf("prepare Target shard: %w", err)
			}
			if err := executor.cfg.Progress.Advance(ctx, progress, StepTargetReady); err != nil {
				return err
			}
			progress.Step = StepTargetReady

		case StepTargetReady:
			stored, err := executor.ensureActive(ctx, progress)
			if err != nil {
				return err
			}
			if err := executor.applyAndPublish(stored); err != nil {
				return err
			}
			if err := executor.cfg.Progress.Advance(ctx, progress, StepRouteActive); err != nil {
				return err
			}
			progress.Step = StepRouteActive

		case StepRouteActive:
			_ = executor.cfg.Zones.RefreshOwnership(ctx, progress.Prepared)
			if err := executor.cfg.Progress.Complete(ctx, progress); err != nil {
				return err
			}
			return executor.cfg.Tasks.Complete(ctx, shardID, taskID)

		default:
			return fmt.Errorf("%w: unsupported step %q", ErrProgressConflict, progress.Step)
		}
	}
}

func (executor *Executor) ensurePreparing(ctx context.Context, progress Progress) (routestore.Snapshot, error) {
	stored, err := executor.cfg.RouteStore.Load(ctx)
	if err != nil {
		return routestore.Snapshot{}, err
	}
	current := stored.Entries[progress.ShardID]
	if samePreparedRoute(current, progress.Prepared) {
		return stored, nil
	}
	if !sameSourceRoute(current, progress.Source) {
		return routestore.Snapshot{}, routestore.ErrRouteConflict
	}
	return executor.cfg.RouteStore.CommitPreparing(ctx, progress.Prepared, stored.Metadata.MapVersion)
}

func (executor *Executor) ensureActive(ctx context.Context, progress Progress) (routestore.Snapshot, error) {
	stored, err := executor.cfg.RouteStore.Load(ctx)
	if err != nil {
		return routestore.Snapshot{}, err
	}
	current := stored.Entries[progress.ShardID]
	if sameActivatedRoute(current, progress.Prepared) {
		return stored, nil
	}
	if current.State != routing.RouteStatePreparing || current.TransitionID != progress.TransitionID {
		return routestore.Snapshot{}, routestore.ErrRouteConflict
	}
	now := executor.cfg.Now().UTC()
	active := current
	active.State = routing.RouteStateActive
	active.RouteVersion++
	active.LeaseExpiresAt = now.Add(executor.cfg.LeaseDuration)
	active.UpdatedAt = now
	return executor.cfg.RouteStore.CommitActive(ctx, active, stored.Metadata.MapVersion)
}

func (executor *Executor) applyAndPublish(stored routestore.Snapshot) error {
	previous := executor.cfg.Routes.Snapshot()
	current := routestore.RoutingSnapshot(stored)
	if current.MapVersion < previous.MapVersion {
		return routestore.ErrRouteConflict
	}
	if current.MapVersion == previous.MapVersion {
		return nil
	}
	if err := executor.cfg.Routes.ApplyCommittedSnapshot(current); err != nil {
		return err
	}
	return executor.cfg.Publisher.PublishRoutes(previous, current)
}

func taskMatchesSource(task Task, source routing.RouteEntry) bool {
	return source.ShardID == task.ShardID && source.State == routing.RouteStateActive &&
		source.OwnerZoneID == task.SourceZoneID && source.OwnerEndpoint == task.SourceEndpoint &&
		source.OwnerEpoch == task.SourceOwnerEpoch && source.RouteVersion == task.SourceRouteVersion
}

func progressMatchesTask(progress Progress, task Task) bool {
	return progress.ShardID == task.ShardID && progress.Prepared.OwnerZoneID == task.TargetZoneID &&
		progress.Prepared.OwnerEndpoint == task.TargetEndpoint && taskMatchesSource(task, progress.Source)
}

func sameSourceRoute(left, right routing.RouteEntry) bool {
	return left.ShardID == right.ShardID && left.State == routing.RouteStateActive &&
		left.OwnerZoneID == right.OwnerZoneID && left.OwnerEndpoint == right.OwnerEndpoint &&
		left.OwnerEpoch == right.OwnerEpoch && left.RouteVersion == right.RouteVersion && left.LeaseID == right.LeaseID
}

func samePreparedRoute(left, right routing.RouteEntry) bool {
	return left.ShardID == right.ShardID && left.State == routing.RouteStatePreparing &&
		left.OwnerZoneID == right.OwnerZoneID && left.OwnerEndpoint == right.OwnerEndpoint &&
		left.OwnerEpoch == right.OwnerEpoch && left.RouteVersion == right.RouteVersion &&
		left.LeaseID == right.LeaseID && left.LeaseTerm == right.LeaseTerm &&
		left.PreviousOwnerZoneID == right.PreviousOwnerZoneID && left.TransitionID == right.TransitionID
}

func sameActivatedRoute(active, prepared routing.RouteEntry) bool {
	return active.ShardID == prepared.ShardID && active.State == routing.RouteStateActive &&
		active.OwnerZoneID == prepared.OwnerZoneID && active.OwnerEndpoint == prepared.OwnerEndpoint &&
		active.OwnerEpoch == prepared.OwnerEpoch && active.RouteVersion == prepared.RouteVersion+1 &&
		active.LeaseID == prepared.LeaseID && active.LeaseTerm == prepared.LeaseTerm &&
		active.PreviousOwnerZoneID == prepared.PreviousOwnerZoneID && active.TransitionID == prepared.TransitionID
}

func completedTaskRoute(task Task, active routing.RouteEntry) bool {
	return active.ShardID == task.ShardID && active.State == routing.RouteStateActive &&
		active.OwnerZoneID == task.TargetZoneID && active.OwnerEndpoint == task.TargetEndpoint &&
		active.OwnerEpoch > task.SourceOwnerEpoch && active.RouteVersion == task.SourceRouteVersion+2 &&
		active.PreviousOwnerZoneID == task.SourceZoneID && active.TransitionID != ""
}
