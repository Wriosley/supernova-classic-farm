package placement

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/membership"
	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/migration"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

var ErrCurrentChanged = errors.New("Current ShardMap changed while planning")

type CurrentSource interface {
	Snapshot() routing.Snapshot
}

// Run reconciles once after membership is ready, then on coalesced membership
// notifications and the periodic convergence interval.
func (planner *Planner) Run(
	ctx context.Context,
	interval time.Duration,
	ready <-chan struct{},
	triggers <-chan struct{},
	report func(Result, error),
) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	select {
	case <-ctx.Done():
		return
	case <-ready:
	}
	run := func() {
		result, err := planner.Reconcile(ctx)
		if report != nil {
			report(result, err)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		case <-triggers:
			for {
				select {
				case <-triggers:
					continue
				default:
				}
				break
			}
			run()
		}
	}
}

type MembershipSource interface {
	Snapshot() membership.Snapshot
}

type Planner struct {
	current    CurrentSource
	membership MembershipSource
	tasks      migration.TaskStore
}

type Result struct {
	Proposed         int
	Deduplicated     int
	Cancelled        int
	Unchanged        int
	SkippedUnhealthy int
}

func NewPlanner(current CurrentSource, members MembershipSource, tasks migration.TaskStore) (*Planner, error) {
	if current == nil || members == nil || tasks == nil {
		return nil, errors.New("planner Current, membership and task store are required")
	}
	return &Planner{current: current, membership: members, tasks: tasks}, nil
}

func (planner *Planner) Reconcile(ctx context.Context) (Result, error) {
	current := planner.current.Snapshot()
	if current.ShardCount != routing.ShardCount ||
		current.AssignmentAlgorithmVersion != routing.AssignmentAlgorithmVersion ||
		len(current.Entries) != int(routing.ShardCount) || current.MapVersion == 0 {
		return Result{}, errors.New("planner Current snapshot is invalid")
	}
	availability := planner.membership.Snapshot()
	candidates := make([]Candidate, 0, len(availability.Members))
	states := make(map[string]membership.State, len(availability.Members))
	for _, member := range availability.Members {
		states[member.LogicalZoneID] = member.State
		if member.State == membership.StateHealthy {
			candidates = append(candidates, Candidate{
				LogicalZoneID: member.LogicalZoneID,
				Endpoint:      member.Endpoint,
			})
		}
	}
	desired, err := Compute(current.ShardCount, current.AssignmentAlgorithmVersion, candidates)
	if err != nil {
		return Result{}, fmt.Errorf("compute Desired placement: %w", err)
	}
	open, err := planner.tasks.LoadOpen(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("load open migration tasks: %w", err)
	}
	openByShard := make(map[uint32]migration.Task, len(open))
	for _, task := range open {
		openByShard[task.ShardID] = task
	}

	var result Result
	for shardID, target := range desired {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		source := current.Entries[shardID]
		if source.ShardID != uint32(shardID) {
			return result, fmt.Errorf("planner Current entry %d has shard ID %d", shardID, source.ShardID)
		}
		if source.OwnerZoneID == target.OwnerZoneID {
			result.Unchanged++
			if task, exists := openByShard[uint32(shardID)]; exists && task.Status == migration.StatusPlanned {
				if planner.current.Snapshot().MapVersion != current.MapVersion {
					return result, ErrCurrentChanged
				}
				if err := planner.tasks.Cancel(ctx, uint32(shardID), task.TaskID, "CURRENT_MATCHES_DESIRED"); err != nil {
					return result, fmt.Errorf("cancel stale migration task %d: %w", shardID, err)
				}
				result.Cancelled++
			}
			continue
		}
		if state := states[source.OwnerZoneID]; state == membership.StateSuspect || state == membership.StateDead {
			result.SkippedUnhealthy++
			continue
		}
		if planner.current.Snapshot().MapVersion != current.MapVersion {
			return result, ErrCurrentChanged
		}
		_, changed, err := planner.tasks.UpsertPlanned(ctx, migration.Task{
			ShardID: uint32(shardID), Reason: migration.ReasonRebalance,
			Priority:     migration.PriorityRebalance,
			SourceZoneID: source.OwnerZoneID, SourceEndpoint: source.OwnerEndpoint,
			SourceOwnerEpoch: source.OwnerEpoch, SourceRouteVersion: source.RouteVersion,
			TargetZoneID: target.OwnerZoneID, TargetEndpoint: target.OwnerEndpoint,
			PlannedFromMapVersion:          current.MapVersion,
			PlannedFromAvailabilityVersion: availability.AvailabilityVersion,
		})
		if err != nil {
			return result, fmt.Errorf("upsert migration task %d: %w", shardID, err)
		}
		if changed {
			result.Proposed++
		} else {
			result.Deduplicated++
		}
	}
	return result, nil
}
