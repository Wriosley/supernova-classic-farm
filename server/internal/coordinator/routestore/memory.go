package routestore

import (
	"context"
	"errors"
	"sync"

	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

type MemoryStore struct {
	mu      sync.Mutex
	current Snapshot
	created bool
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{} }

func (s *MemoryStore) Load(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.created {
		return Snapshot{}, ErrRouteStoreEmpty
	}
	return cloneSnapshot(s.current), nil
}

func (s *MemoryStore) BootstrapIfEmpty(ctx context.Context, candidate Snapshot) (Snapshot, bool, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, false, err
	}
	if err := validateSnapshot(candidate); err != nil {
		return Snapshot{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.created {
		if err := validateSnapshot(s.current); err != nil {
			return Snapshot{}, false, err
		}
		return cloneSnapshot(s.current), false, nil
	}
	s.current = cloneSnapshot(candidate)
	s.created = true
	return cloneSnapshot(s.current), true, nil
}

func (s *MemoryStore) CommitPreparing(ctx context.Context, entry routing.RouteEntry, expected uint64) (Snapshot, error) {
	return s.commit(ctx, entry, expected, validatePreparing)
}

func (s *MemoryStore) CommitActive(ctx context.Context, entry routing.RouteEntry, expected uint64) (Snapshot, error) {
	return s.commit(ctx, entry, expected, validateActive)
}

func (s *MemoryStore) RestoreSource(ctx context.Context, entry routing.RouteEntry, expected uint64) (Snapshot, error) {
	return s.commit(ctx, entry, expected, validateRestoredSource)
}

func (s *MemoryStore) commit(ctx context.Context, entry routing.RouteEntry, expected uint64, validate func(routing.RouteEntry, routing.RouteEntry) error) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.created {
		return Snapshot{}, ErrRouteStoreEmpty
	}
	if s.current.Metadata.MapVersion != expected || entry.ShardID >= routing.ShardCount {
		return Snapshot{}, ErrRouteConflict
	}
	current := s.current.Entries[entry.ShardID]
	if current == entry {
		return cloneSnapshot(s.current), nil
	}
	if err := validate(current, entry); err != nil {
		if errors.Is(err, ErrRouteConflict) {
			return Snapshot{}, err
		}
		return Snapshot{}, errors.Join(ErrRouteStoreCorrupt, err)
	}
	s.current.Entries[entry.ShardID] = entry
	s.current.Metadata.MapVersion = expected + 1
	s.current.Metadata.UpdatedAt = entry.UpdatedAt.UTC()
	return cloneSnapshot(s.current), nil
}
