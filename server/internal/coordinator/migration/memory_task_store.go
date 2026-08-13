package migration

import (
	"bytes"
	"context"
	"sync"
)

type MemoryTaskStore struct {
	mu    sync.Mutex
	tasks map[uint32]Task
}

func NewMemoryTaskStore(initial ...Task) (*MemoryTaskStore, error) {
	store := &MemoryTaskStore{tasks: make(map[uint32]Task, len(initial))}
	for _, task := range initial {
		if err := validateStored(task); err != nil {
			return nil, err
		}
		if _, exists := store.tasks[task.ShardID]; exists {
			return nil, ErrTaskConflict
		}
		store.tasks[task.ShardID] = cloneTask(task)
	}
	return store, nil
}

func (s *MemoryTaskStore) UpsertPlanned(ctx context.Context, proposal Task) (Task, bool, error) {
	if err := ctx.Err(); err != nil {
		return Task{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, found := s.tasks[proposal.ShardID]
	next, changed, err := resolveUpsert(current, found, proposal, nowUnixMilli())
	if err != nil || !changed {
		return next, changed, err
	}
	s.tasks[next.ShardID] = cloneTask(next)
	return next, true, nil
}

func (s *MemoryTaskStore) LoadOpen(ctx context.Context) ([]Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Task, 0, len(s.tasks))
	for _, task := range s.tasks {
		if task.Status == StatusPlanned || task.Status == StatusRunning {
			result = append(result, cloneTask(task))
		}
	}
	sortOpen(result)
	return result, nil
}

func (s *MemoryTaskStore) Get(ctx context.Context, shardID uint32) (Task, bool, error) {
	if err := ctx.Err(); err != nil {
		return Task{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task, found := s.tasks[shardID]
	return cloneTask(task), found, nil
}

func (s *MemoryTaskStore) Cancel(ctx context.Context, shardID uint32, taskID []byte, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task, found := s.tasks[shardID]
	if !found || task.Status != StatusPlanned || !bytes.Equal(task.TaskID, taskID) {
		return ErrTaskConflict
	}
	task.Status = StatusCancelled
	task.LastErrorCode = reason
	task.UpdatedAtMS = nowUnixMilli()
	s.tasks[shardID] = task
	return nil
}
