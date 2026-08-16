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
var ErrInsufficientHealthyZones = errors.New("insufficient healthy Zones for placement")

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
	current        CurrentSource
	membership     MembershipSource
	tasks          migration.TaskStore
	minimumHealthy int
}

type Result struct {
	Proposed         int
	Deduplicated     int
	Cancelled        int
	Unchanged        int
	SkippedUnhealthy int
}

func NewPlanner(current CurrentSource, members MembershipSource, tasks migration.TaskStore) (*Planner, error) {
	return NewPlannerWithMinimum(current, members, tasks, 1)
}

func NewPlannerWithMinimum(current CurrentSource, members MembershipSource, tasks migration.TaskStore, minimumHealthy int) (*Planner, error) {
	if current == nil || members == nil || tasks == nil {
		return nil, errors.New("planner Current, membership and task store are required")
	}
	if minimumHealthy <= 0 {
		return nil, errors.New("minimum healthy Zone count must be positive")
	}
	return &Planner{current: current, membership: members, tasks: tasks, minimumHealthy: minimumHealthy}, nil
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
	if len(candidates) < planner.minimumHealthy {
		return Result{}, fmt.Errorf("%w: have %d, require %d", ErrInsufficientHealthyZones, len(candidates), planner.minimumHealthy)
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
			if task, exists := openByShard[uint32(shardID)]; exists &&
				task.Status == migration.StatusPlanned && source.State == routing.RouteStateActive {
				// A recovered task may have an OPEN Progress row even though its
				// target ACTIVE route was already committed before the crash. Keep
				// that task so the Worker can advance/complete Progress. Merely
				// comparing Current owner with Desired would strand the durable
				// migration tail by cancelling it as stale.
				if migration.CompletedTaskRoute(task, source) {
					result.Deduplicated++
					continue
				}
				if planner.current.Snapshot().MapVersion != current.MapVersion {
					return result, ErrCurrentChanged
				}
				if err := planner.tasks.Cancel(ctx, uint32(shardID), task.TaskID, "CURRENT_MATCHES_DESIRED"); err != nil {
					return result, fmt.Errorf("cancel stale migration task %d: %w", shardID, err)
				}
				result.Cancelled++
			}
			// PREPARING ownership is not settled merely because its target equals
			// Desired. The migration Worker must finish the persisted Progress;
			// cancelling here strands a fail-closed route forever.
			if source.State == routing.RouteStatePreparing {
				if _, exists := openByShard[uint32(shardID)]; exists {
					result.Deduplicated++
				}
			}
			continue
		}
		// An open task whose frozen Source still matches Current already owns
		// this planning slot. In particular, a RUNNING task may have been
		// durably claimed immediately before a Coordinator restart but not yet
		// have written MigrationProgress. Replacing or conflicting with that
		// task would prevent the Worker from resuming the defined crash
		// boundary. A healthy earlier Target is still valid for draining the
		// Source; a later reconcile can rebalance from that committed owner.
		if task, exists := openByShard[uint32(shardID)]; exists &&
			taskMatchesCurrentSource(task, source) &&
			states[task.TargetZoneID] == membership.StateHealthy {
			result.Deduplicated++
			continue
		}
		if state := states[source.OwnerZoneID]; state == membership.StateSuspect || state == membership.StateDead {
			result.SkippedUnhealthy++
			continue
		}
		if planner.current.Snapshot().MapVersion != current.MapVersion {
			return result, ErrCurrentChanged
		}
		reason, priority := migration.ReasonRebalance, uint32(migration.PriorityRebalance)
		if states[source.OwnerZoneID] == membership.StateDraining {
			reason, priority = migration.ReasonDrain, migration.PriorityDrain
		}
		_, changed, err := planner.tasks.UpsertPlanned(ctx, migration.Task{
			ShardID: uint32(shardID), Reason: reason,
			Priority:     priority,
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

func taskMatchesCurrentSource(task migration.Task, source routing.RouteEntry) bool {
	return task.ShardID == source.ShardID &&
		task.SourceZoneID == source.OwnerZoneID &&
		task.SourceEndpoint == source.OwnerEndpoint &&
		task.SourceOwnerEpoch == source.OwnerEpoch &&
		task.SourceRouteVersion == source.RouteVersion
}
