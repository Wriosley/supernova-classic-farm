package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/routestore"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

type memoryRouteStore struct {
	snapshot routestore.Snapshot
}

func (m *memoryRouteStore) Load(context.Context) (routestore.Snapshot, error) {
	return m.snapshot, nil
}
func (m *memoryRouteStore) BootstrapIfEmpty(context.Context, routestore.Snapshot) (routestore.Snapshot, bool, error) {
	return m.snapshot, false, nil
}
func (m *memoryRouteStore) CommitPreparing(context.Context, routing.RouteEntry, uint64) (routestore.Snapshot, error) {
	return m.snapshot, nil
}
func (m *memoryRouteStore) CommitActive(context.Context, routing.RouteEntry, uint64) (routestore.Snapshot, error) {
	return m.snapshot, nil
}
func (m *memoryRouteStore) RestoreSource(context.Context, routing.RouteEntry, uint64) (routestore.Snapshot, error) {
	return m.snapshot, nil
}

func TestSyncFollowerRoutesAppliesNewerMapVersion(t *testing.T) {
	now := time.Now().UTC()
	routes, err := routing.NewStaticMap(now, time.Minute, []routing.ZoneCandidate{{
		ZoneID: "zone-a", Endpoint: "http://zone-a:8082",
	}})
	if err != nil {
		t.Fatal(err)
	}
	previous := routes.Snapshot()
	newer := previous
	newer.MapVersion = previous.MapVersion + 1
	newer.Entries = append([]routing.RouteEntry(nil), previous.Entries...)
	newer.Entries[7].RouteVersion++
	newer.Entries[7].UpdatedAt = now
	store := &memoryRouteStore{snapshot: routestore.FromRoutingSnapshot(newer, now)}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := syncFollowerRoutes(context.Background(), routes, store, nil, logger); err != nil {
		t.Fatal(err)
	}
	got := routes.Snapshot()
	if got.MapVersion != newer.MapVersion {
		t.Fatalf("map_version=%d want %d", got.MapVersion, newer.MapVersion)
	}
	if got.Entries[7].RouteVersion != newer.Entries[7].RouteVersion {
		t.Fatalf("route_version not applied: %+v", got.Entries[7])
	}
}

func TestElectionDisabledByDefault(t *testing.T) {
	t.Setenv("COORDINATOR_ELECTION_ENABLED", "")
	cfg, err := electionConfigFromEnvironment()
	if err != nil || cfg.Enabled {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}
