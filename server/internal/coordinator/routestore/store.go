package routestore

import (
	"context"
	"errors"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

var (
	ErrRouteConflict     = errors.New("route store conflict")
	ErrRouteStoreCorrupt = errors.New("route store corrupt")
	ErrRouteStoreEmpty   = errors.New("route store empty")
)

type Metadata struct {
	ShardCount                 uint32
	HashAlgorithmVersion       uint32
	AssignmentAlgorithmVersion uint32
	MapVersion                 uint64
	UpdatedAt                  time.Time
}

type Snapshot struct {
	Metadata Metadata
	Entries  []routing.RouteEntry
}

type Store interface {
	Load(context.Context) (Snapshot, error)
	BootstrapIfEmpty(context.Context, Snapshot) (Snapshot, bool, error)
	CommitPreparing(context.Context, routing.RouteEntry, uint64) (Snapshot, error)
	CommitActive(context.Context, routing.RouteEntry, uint64) (Snapshot, error)
	RestoreSource(context.Context, routing.RouteEntry, uint64) (Snapshot, error)
}

func RoutingSnapshot(snapshot Snapshot) routing.Snapshot {
	return routing.Snapshot{
		ShardCount:                 snapshot.Metadata.ShardCount,
		HashAlgorithmVersion:       snapshot.Metadata.HashAlgorithmVersion,
		AssignmentAlgorithmVersion: snapshot.Metadata.AssignmentAlgorithmVersion,
		MapVersion:                 snapshot.Metadata.MapVersion,
		CommittedTerm:              1, CommittedIndex: snapshot.Metadata.MapVersion,
		Entries: cloneEntries(snapshot.Entries),
	}
}

func FromRoutingSnapshot(snapshot routing.Snapshot, updatedAt time.Time) Snapshot {
	return Snapshot{Metadata: Metadata{
		ShardCount:                 snapshot.ShardCount,
		HashAlgorithmVersion:       snapshot.HashAlgorithmVersion,
		AssignmentAlgorithmVersion: snapshot.AssignmentAlgorithmVersion,
		MapVersion:                 snapshot.MapVersion, UpdatedAt: updatedAt.UTC(),
	}, Entries: cloneEntries(snapshot.Entries)}
}
