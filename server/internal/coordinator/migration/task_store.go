// Package migration persists planned Shard ownership migration work. Tasks are
// scheduling intent only; they do not grant ownership or mutate Current.
package migration

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

type Reason string
type Status string

const (
	ReasonRebalance Reason = "REBALANCE"
	ReasonDrain     Reason = "DRAIN"
	ReasonFailover  Reason = "FAILOVER"

	StatusPlanned   Status = "PLANNED"
	StatusRunning   Status = "RUNNING"
	StatusCompleted Status = "COMPLETED"
	StatusCancelled Status = "CANCELLED"

	PriorityRebalance uint32 = 100
	PriorityDrain     uint32 = 200
	PriorityFailover  uint32 = 300
)

var (
	ErrTaskConflict    = errors.New("migration task conflict")
	ErrTaskCASConflict = errors.New("migration task record version conflict")
)

type Task struct {
	ShardID                        uint32
	TaskID                         []byte
	Reason                         Reason
	Status                         Status
	Priority                       uint32
	SourceZoneID                   string
	SourceEndpoint                 string
	SourceOwnerEpoch               uint64
	SourceRouteVersion             uint64
	TargetZoneID                   string
	TargetEndpoint                 string
	PlannedFromMapVersion          uint64
	PlannedFromAvailabilityVersion uint64
	Attempt                        uint32
	RetryAtMS                      int64
	LastErrorCode                  string
	CreatedAtMS                    int64
	UpdatedAtMS                    int64
}

type TaskStore interface {
	UpsertPlanned(context.Context, Task) (Task, bool, error)
	LoadOpen(context.Context) ([]Task, error)
	Get(context.Context, uint32) (Task, bool, error)
	Cancel(context.Context, uint32, []byte, string) error
	Claim(context.Context, uint32, []byte) (Task, error)
	Retry(context.Context, uint32, []byte, uint32, int64, string) error
	Fail(context.Context, uint32, []byte, string) error
	Complete(context.Context, uint32, []byte) error
}

// RecoverOpenProgressTasks restores the queue entry for every durable OPEN
// Progress row. It never changes Route or Fence evidence; it only recreates a
// terminal/missing MigrationTask so the Executor can resume at the persisted
// Progress step after a Coordinator restart.
func RecoverOpenProgressTasks(ctx context.Context, tasks TaskStore, progress ProgressStore, mapVersion uint64) (int, error) {
	if tasks == nil || progress == nil || mapVersion == 0 {
		return 0, errors.New("migration recovery dependencies and map version are required")
	}
	open, err := progress.LoadOpen(ctx)
	if err != nil {
		return 0, fmt.Errorf("load open migration progress for task recovery: %w", err)
	}
	recovered := 0
	for _, item := range open {
		current, found, err := tasks.Get(ctx, item.ShardID)
		if err != nil {
			return recovered, fmt.Errorf("load migration task %d for recovery: %w", item.ShardID, err)
		}
		if found && (current.Status == StatusPlanned || current.Status == StatusRunning) {
			if taskMatchesProgress(current, item) {
				continue
			}
			return recovered, fmt.Errorf("%w: open task %d conflicts with durable progress", ErrTaskConflict, item.ShardID)
		}
		reason, priority := ReasonDrain, uint32(PriorityDrain)
		if found {
			switch current.Reason {
			case ReasonRebalance, ReasonDrain, ReasonFailover:
				reason, priority = current.Reason, current.Priority
			}
		}
		_, changed, err := tasks.UpsertPlanned(ctx, Task{
			ShardID: item.ShardID, Reason: reason, Priority: priority,
			SourceZoneID: item.Source.OwnerZoneID, SourceEndpoint: item.Source.OwnerEndpoint,
			SourceOwnerEpoch: item.Source.OwnerEpoch, SourceRouteVersion: item.Source.RouteVersion,
			TargetZoneID: item.Prepared.OwnerZoneID, TargetEndpoint: item.Prepared.OwnerEndpoint,
			PlannedFromMapVersion: mapVersion, PlannedFromAvailabilityVersion: 1,
		})
		if err != nil {
			return recovered, fmt.Errorf("recover migration task %d: %w", item.ShardID, err)
		}
		if changed {
			recovered++
		}
	}
	return recovered, nil
}

func taskMatchesProgress(task Task, progress Progress) bool {
	return task.ShardID == progress.ShardID &&
		task.SourceZoneID == progress.Source.OwnerZoneID &&
		task.SourceEndpoint == progress.Source.OwnerEndpoint &&
		task.SourceOwnerEpoch == progress.Source.OwnerEpoch &&
		task.SourceRouteVersion == progress.Source.RouteVersion &&
		task.TargetZoneID == progress.Prepared.OwnerZoneID &&
		task.TargetEndpoint == progress.Prepared.OwnerEndpoint
}

func resolveClaim(task Task, found bool, taskID []byte, nowMS int64) (Task, bool, error) {
	if !found || !bytes.Equal(task.TaskID, taskID) {
		return Task{}, false, ErrTaskConflict
	}
	if task.Status == StatusRunning {
		return cloneTask(task), false, nil
	}
	if task.Status != StatusPlanned {
		return Task{}, false, ErrTaskConflict
	}
	task.Status = StatusRunning
	task.UpdatedAtMS = nowMS
	return cloneTask(task), true, nil
}

func resolveRetry(task Task, found bool, taskID []byte, attempt uint32, retryAtMS int64, code string, nowMS int64) (Task, error) {
	if !found || task.Status != StatusRunning || !bytes.Equal(task.TaskID, taskID) ||
		attempt <= task.Attempt || retryAtMS <= 0 || strings.TrimSpace(code) == "" {
		return Task{}, ErrTaskConflict
	}
	task.Status, task.Attempt, task.RetryAtMS = StatusPlanned, attempt, retryAtMS
	task.LastErrorCode, task.UpdatedAtMS = code, nowMS
	return cloneTask(task), nil
}

func resolveComplete(task Task, found bool, taskID []byte, nowMS int64) (Task, bool, error) {
	if !found || !bytes.Equal(task.TaskID, taskID) {
		return Task{}, false, ErrTaskConflict
	}
	if task.Status == StatusCompleted {
		return cloneTask(task), false, nil
	}
	if task.Status != StatusRunning {
		return Task{}, false, ErrTaskConflict
	}
	task.Status, task.RetryAtMS, task.LastErrorCode = StatusCompleted, 0, ""
	task.UpdatedAtMS = nowMS
	return cloneTask(task), true, nil
}

func resolveFail(task Task, found bool, taskID []byte, code string, nowMS int64) (Task, bool, error) {
	if !found || !bytes.Equal(task.TaskID, taskID) || strings.TrimSpace(code) == "" {
		return Task{}, false, ErrTaskConflict
	}
	if task.Status == StatusCancelled && task.LastErrorCode == code {
		return cloneTask(task), false, nil
	}
	if task.Status != StatusRunning {
		return Task{}, false, ErrTaskConflict
	}
	task.Status, task.LastErrorCode, task.RetryAtMS = StatusCancelled, code, 0
	task.UpdatedAtMS = nowMS
	return cloneTask(task), true, nil
}

func validateProposal(task Task) error {
	if task.ShardID >= routing.ShardCount || strings.TrimSpace(task.SourceZoneID) == "" ||
		strings.TrimSpace(task.SourceEndpoint) == "" || task.SourceOwnerEpoch == 0 ||
		task.SourceRouteVersion == 0 || strings.TrimSpace(task.TargetZoneID) == "" ||
		strings.TrimSpace(task.TargetEndpoint) == "" || task.PlannedFromMapVersion == 0 ||
		task.PlannedFromAvailabilityVersion == 0 {
		return errors.New("migration task proposal is incomplete")
	}
	if task.SourceZoneID == task.TargetZoneID {
		return errors.New("migration task source and target must differ")
	}
	wantPriority := map[Reason]uint32{
		ReasonRebalance: PriorityRebalance,
		ReasonDrain:     PriorityDrain,
		ReasonFailover:  PriorityFailover,
	}[task.Reason]
	if wantPriority == 0 || task.Priority != wantPriority {
		return errors.New("migration task reason and priority are invalid")
	}
	if task.Status != "" && task.Status != StatusPlanned {
		return errors.New("new migration task must be PLANNED")
	}
	return nil
}

func validateStored(task Task) error {
	proposal := task
	proposal.Status = StatusPlanned
	if err := validateProposal(proposal); err != nil {
		return err
	}
	if len(task.TaskID) != 16 || task.CreatedAtMS <= 0 || task.UpdatedAtMS <= 0 {
		return errors.New("stored migration task identity or timestamps are invalid")
	}
	switch task.Status {
	case StatusPlanned, StatusRunning, StatusCompleted, StatusCancelled:
		return nil
	default:
		return fmt.Errorf("stored migration task status %q is invalid", task.Status)
	}
}

func resolveUpsert(current Task, found bool, proposal Task, nowMS int64) (Task, bool, error) {
	if err := validateProposal(proposal); err != nil {
		return Task{}, false, err
	}
	if found {
		if err := validateStored(current); err != nil {
			return Task{}, false, err
		}
		if current.Status == StatusPlanned && sameProposal(current, proposal) {
			return cloneTask(current), false, nil
		}
		if current.Status == StatusRunning ||
			(current.Status == StatusPlanned && proposal.Priority <= current.Priority) {
			return Task{}, false, ErrTaskConflict
		}
	}
	taskID, err := newTaskID()
	if err != nil {
		return Task{}, false, err
	}
	proposal.TaskID = taskID
	proposal.Status = StatusPlanned
	proposal.Attempt = 0
	proposal.RetryAtMS = 0
	proposal.LastErrorCode = ""
	proposal.CreatedAtMS = nowMS
	proposal.UpdatedAtMS = nowMS
	return cloneTask(proposal), true, nil
}

func sameProposal(a, b Task) bool {
	return a.ShardID == b.ShardID && a.Reason == b.Reason && a.Priority == b.Priority &&
		a.SourceZoneID == b.SourceZoneID && a.SourceEndpoint == b.SourceEndpoint &&
		a.SourceOwnerEpoch == b.SourceOwnerEpoch && a.SourceRouteVersion == b.SourceRouteVersion &&
		a.TargetZoneID == b.TargetZoneID && a.TargetEndpoint == b.TargetEndpoint
}

func sortOpen(tasks []Task) {
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].Priority != tasks[j].Priority {
			return tasks[i].Priority > tasks[j].Priority
		}
		if tasks[i].CreatedAtMS != tasks[j].CreatedAtMS {
			return tasks[i].CreatedAtMS < tasks[j].CreatedAtMS
		}
		return tasks[i].ShardID < tasks[j].ShardID
	})
}

func cloneTask(task Task) Task {
	task.TaskID = bytes.Clone(task.TaskID)
	return task
}

func newTaskID() ([]byte, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, fmt.Errorf("create migration task ID: %w", err)
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return id, nil
}

func nowUnixMilli() int64 { return time.Now().UTC().UnixMilli() }
