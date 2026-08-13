package migration

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/tcaplusdb"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/terror"
	"google.golang.org/protobuf/proto"
)

const maxCASAttempts = 4

type TcaplusClient interface {
	DoGet(proto.Message, *option.PBOpt, ...uint32) error
	DoInsert(proto.Message, *option.PBOpt, ...uint32) error
	DoUpdate(proto.Message, *option.PBOpt, ...uint32) error
	Traverse(proto.Message) ([]proto.Message, error)
}

type TcaplusTaskStore struct {
	client TcaplusClient
	zoneID uint32
}

func NewTcaplusTaskStore(client TcaplusClient, zoneID uint32) (*TcaplusTaskStore, error) {
	if client == nil || zoneID == 0 {
		return nil, errors.New("Tcaplus migration task client and zone are required")
	}
	return &TcaplusTaskStore{client: client, zoneID: zoneID}, nil
}

func (s *TcaplusTaskStore) UpsertPlanned(ctx context.Context, proposal Task) (Task, bool, error) {
	if err := validateProposal(proposal); err != nil {
		return Task{}, false, err
	}
	for attempt := 0; attempt < maxCASAttempts; attempt++ {
		current, version, found, err := s.load(ctx, proposal.ShardID)
		if err != nil {
			return Task{}, false, err
		}
		next, changed, err := resolveUpsert(current, found, proposal, nowUnixMilli())
		if err != nil || !changed {
			return next, changed, err
		}
		record := taskRecord(next)
		if !found {
			err = s.client.DoInsert(record, &option.PBOpt{Ctx: ctx}, s.zoneID)
			if tcaplusdb.IsAlreadyExists(err) {
				continue
			}
		} else {
			err = s.client.DoUpdate(record, &option.PBOpt{Ctx: ctx, Version: version}, s.zoneID)
			if isCASConflict(err) {
				continue
			}
		}
		if err != nil {
			return Task{}, false, fmt.Errorf("persist migration task %d: %w", proposal.ShardID, err)
		}
		return next, true, nil
	}
	return Task{}, false, ErrTaskCASConflict
}

func (s *TcaplusTaskStore) LoadOpen(ctx context.Context) ([]Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	messages, err := s.client.Traverse(&tcaplusv1.MigrationTask{})
	if tcaplusdb.IsNotFound(err) {
		return []Task{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("traverse migration tasks: %w", err)
	}
	result := make([]Task, 0, len(messages))
	for _, message := range messages {
		record, ok := message.(*tcaplusv1.MigrationTask)
		if !ok {
			return nil, fmt.Errorf("traverse migration tasks returned %T", message)
		}
		task := taskFromRecord(record)
		if err := validateStored(task); err != nil {
			return nil, fmt.Errorf("decode migration task %d: %w", task.ShardID, err)
		}
		if task.Status == StatusPlanned || task.Status == StatusRunning {
			result = append(result, task)
		}
	}
	sortOpen(result)
	return result, nil
}

func (s *TcaplusTaskStore) Get(ctx context.Context, shardID uint32) (Task, bool, error) {
	task, _, found, err := s.load(ctx, shardID)
	return task, found, err
}

func (s *TcaplusTaskStore) Cancel(ctx context.Context, shardID uint32, taskID []byte, reason string) error {
	for attempt := 0; attempt < maxCASAttempts; attempt++ {
		task, version, found, err := s.load(ctx, shardID)
		if err != nil {
			return err
		}
		if !found || task.Status != StatusPlanned || !bytes.Equal(task.TaskID, taskID) {
			return ErrTaskConflict
		}
		task.Status = StatusCancelled
		task.LastErrorCode = reason
		task.UpdatedAtMS = nowUnixMilli()
		err = s.client.DoUpdate(taskRecord(task), &option.PBOpt{Ctx: ctx, Version: version}, s.zoneID)
		if isCASConflict(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("cancel migration task %d: %w", shardID, err)
		}
		return nil
	}
	return ErrTaskCASConflict
}

func (s *TcaplusTaskStore) Claim(ctx context.Context, shardID uint32, taskID []byte) (Task, error) {
	for attempt := 0; attempt < maxCASAttempts; attempt++ {
		current, version, found, err := s.load(ctx, shardID)
		if err != nil {
			return Task{}, err
		}
		next, changed, err := resolveClaim(current, found, taskID, nowUnixMilli())
		if err != nil || !changed {
			return next, err
		}
		err = s.client.DoUpdate(taskRecord(next), &option.PBOpt{Ctx: ctx, Version: version}, s.zoneID)
		if isCASConflict(err) {
			continue
		}
		if err != nil {
			return Task{}, fmt.Errorf("claim migration task %d: %w", shardID, err)
		}
		return next, nil
	}
	return Task{}, ErrTaskCASConflict
}

func (s *TcaplusTaskStore) Retry(ctx context.Context, shardID uint32, taskID []byte, attempt uint32, retryAtMS int64, code string) error {
	for casAttempt := 0; casAttempt < maxCASAttempts; casAttempt++ {
		current, version, found, err := s.load(ctx, shardID)
		if err != nil {
			return err
		}
		next, err := resolveRetry(current, found, taskID, attempt, retryAtMS, code, nowUnixMilli())
		if err != nil {
			return err
		}
		err = s.client.DoUpdate(taskRecord(next), &option.PBOpt{Ctx: ctx, Version: version}, s.zoneID)
		if isCASConflict(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("retry migration task %d: %w", shardID, err)
		}
		return nil
	}
	return ErrTaskCASConflict
}

func (s *TcaplusTaskStore) Complete(ctx context.Context, shardID uint32, taskID []byte) error {
	for attempt := 0; attempt < maxCASAttempts; attempt++ {
		current, version, found, err := s.load(ctx, shardID)
		if err != nil {
			return err
		}
		next, changed, err := resolveComplete(current, found, taskID, nowUnixMilli())
		if err != nil || !changed {
			return err
		}
		err = s.client.DoUpdate(taskRecord(next), &option.PBOpt{Ctx: ctx, Version: version}, s.zoneID)
		if isCASConflict(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("complete migration task %d: %w", shardID, err)
		}
		return nil
	}
	return ErrTaskCASConflict
}

func (s *TcaplusTaskStore) Fail(ctx context.Context, shardID uint32, taskID []byte, code string) error {
	for attempt := 0; attempt < maxCASAttempts; attempt++ {
		current, version, found, err := s.load(ctx, shardID)
		if err != nil {
			return err
		}
		next, changed, err := resolveFail(current, found, taskID, code, nowUnixMilli())
		if err != nil || !changed {
			return err
		}
		err = s.client.DoUpdate(taskRecord(next), &option.PBOpt{Ctx: ctx, Version: version}, s.zoneID)
		if isCASConflict(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("fail migration task %d: %w", shardID, err)
		}
		return nil
	}
	return ErrTaskCASConflict
}

func (s *TcaplusTaskStore) load(ctx context.Context, shardID uint32) (Task, int32, bool, error) {
	record := &tcaplusv1.MigrationTask{LogicalShardId: shardID}
	opt := &option.PBOpt{Ctx: ctx}
	err := s.client.DoGet(record, opt, s.zoneID)
	if tcaplusdb.IsNotFound(err) {
		return Task{}, 0, false, nil
	}
	if err != nil {
		return Task{}, 0, false, fmt.Errorf("load migration task %d: %w", shardID, err)
	}
	task := taskFromRecord(record)
	if err := validateStored(task); err != nil {
		return Task{}, 0, false, fmt.Errorf("decode migration task %d: %w", shardID, err)
	}
	return task, opt.Version, true, nil
}

func taskRecord(task Task) *tcaplusv1.MigrationTask {
	return &tcaplusv1.MigrationTask{
		LogicalShardId: task.ShardID, TaskId: bytes.Clone(task.TaskID),
		Reason: string(task.Reason), Status: string(task.Status), Priority: task.Priority,
		SourceZoneId: task.SourceZoneID, SourceEndpoint: task.SourceEndpoint,
		SourceOwnerEpoch: task.SourceOwnerEpoch, SourceRouteVersion: task.SourceRouteVersion,
		TargetZoneId: task.TargetZoneID, TargetEndpoint: task.TargetEndpoint,
		PlannedFromMapVersion:      task.PlannedFromMapVersion,
		PlannedAvailabilityVersion: task.PlannedFromAvailabilityVersion,
		Attempt:                    task.Attempt, RetryAtMs: task.RetryAtMS, LastErrorCode: task.LastErrorCode,
		CreatedAtMs: task.CreatedAtMS, UpdatedAtMs: task.UpdatedAtMS,
	}
}

func taskFromRecord(record *tcaplusv1.MigrationTask) Task {
	return Task{
		ShardID: record.LogicalShardId, TaskID: bytes.Clone(record.TaskId),
		Reason: Reason(record.Reason), Status: Status(record.Status), Priority: record.Priority,
		SourceZoneID: record.SourceZoneId, SourceEndpoint: record.SourceEndpoint,
		SourceOwnerEpoch: record.SourceOwnerEpoch, SourceRouteVersion: record.SourceRouteVersion,
		TargetZoneID: record.TargetZoneId, TargetEndpoint: record.TargetEndpoint,
		PlannedFromMapVersion:          record.PlannedFromMapVersion,
		PlannedFromAvailabilityVersion: record.PlannedAvailabilityVersion,
		Attempt:                        record.Attempt, RetryAtMS: record.RetryAtMs, LastErrorCode: record.LastErrorCode,
		CreatedAtMS: record.CreatedAtMs, UpdatedAtMS: record.UpdatedAtMs,
	}
}

func isCASConflict(err error) bool {
	return errors.Is(err, ErrTaskCASConflict) ||
		tcaplusdb.ErrorCode(err) == terror.SVR_ERR_FAIL_INVALID_VERSION
}
