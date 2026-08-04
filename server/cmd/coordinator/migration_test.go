package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func TestMySQLMigrationDrainsAdvancesFencePreparesAndActivates(t *testing.T) {
	var drainStarted atomic.Bool
	var drainCompleted atomic.Bool
	var selectedShardID uint32
	oldZone := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/drain-complete"):
			drainCompleted.Store(true)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"shard_id":` +
				strconv.FormatUint(uint64(selectedShardID), 10) +
				`,"owner_epoch":"1","players":[]}`))
		case strings.HasSuffix(r.URL.Path, "/drain"):
			drainStarted.Store(true)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer oldZone.Close()
	var preparedTarget atomic.Bool
	newZone := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		switch {
		case r.URL.Path == "/readyz":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/internal/v1/ownership/refresh":
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/prepare"):
			preparedTarget.Store(true)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer newZone.Close()

	now := time.Date(2026, 8, 3, 15, 0, 0, 0, time.UTC)
	zones := []routing.ZoneCandidate{
		{ZoneID: "zone-a", Endpoint: oldZone.URL},
		{ZoneID: "zone-b", Endpoint: newZone.URL},
	}
	routes, err := routing.NewStaticMap(now, time.Minute, zones)
	if err != nil {
		t.Fatal(err)
	}
	shardID := coordinatorShardOwnedBy(t, routes, "zone-a")
	selectedShardID = shardID
	handler := newMigrationHandler(
		routes, zones, http.DefaultClient, func() time.Time { return now }, time.Minute,
	)
	var fenced atomic.Bool
	handler.advanceFence = func(_ context.Context, entry routing.RouteEntry) error {
		if entry.State != routing.RouteStatePreparing || entry.OwnerEpoch != 2 {
			t.Fatalf("unexpected prepared Fence route: %+v", entry)
		}
		fenced.Store(true)
		return nil
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/internal/v1/shards/"+strconv.FormatUint(uint64(shardID), 10)+"/move",
		strings.NewReader(`{"target_zone_id":"zone-b"}`),
	)
	request.SetPathValue("shard_id", strconv.FormatUint(uint64(shardID), 10))
	request.RemoteAddr = "127.0.0.1:45678"
	response := httptest.NewRecorder()
	handler.move(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("move status=%d body=%s", response.Code, response.Body.String())
	}
	entry, err := routes.Route(shardID, now)
	if err != nil {
		t.Fatal(err)
	}
	if !drainStarted.Load() || !drainCompleted.Load() ||
		!fenced.Load() || !preparedTarget.Load() ||
		entry.OwnerZoneID != "zone-b" || entry.OwnerEpoch != 2 {
		t.Fatalf("migration steps/route: drain=%v complete=%v fence=%v prepare=%v route=%+v",
			drainStarted.Load(), drainCompleted.Load(), fenced.Load(),
			preparedTarget.Load(), entry)
	}
}

func TestManualMigrationDrainsOldOwnerAndRefreshesNewOwner(t *testing.T) {
	var drained atomic.Bool
	oldZone := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		if strings.HasSuffix(r.URL.Path, "/drain") {
			drained.Store(true)
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer oldZone.Close()
	var refreshed atomic.Bool
	newZone := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		switch r.URL.Path {
		case "/readyz":
			w.WriteHeader(http.StatusOK)
		case "/internal/v1/ownership/refresh":
			refreshed.Store(true)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer newZone.Close()

	now := time.Date(2026, 8, 3, 14, 0, 0, 0, time.UTC)
	zones := []routing.ZoneCandidate{
		{ZoneID: "zone-a", Endpoint: oldZone.URL},
		{ZoneID: "zone-b", Endpoint: newZone.URL},
	}
	routes, err := routing.NewStaticMap(now, time.Minute, zones)
	if err != nil {
		t.Fatal(err)
	}
	shardID := coordinatorShardOwnedBy(t, routes, "zone-a")
	handler := newMigrationHandler(
		routes, zones, http.DefaultClient, func() time.Time { return now }, time.Minute,
	)
	request := httptest.NewRequest(http.MethodPost,
		"/internal/v1/shards/"+strconv.FormatUint(uint64(shardID), 10)+"/move",
		strings.NewReader(`{"target_zone_id":"zone-b"}`))
	request.SetPathValue("shard_id", strconv.FormatUint(uint64(shardID), 10))
	request.RemoteAddr = "127.0.0.1:45678"
	response := httptest.NewRecorder()

	handler.move(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("move status=%d body=%s", response.Code, response.Body.String())
	}
	entry, err := routes.Route(shardID, now)
	if err != nil {
		t.Fatal(err)
	}
	if !drained.Load() || !refreshed.Load() ||
		entry.OwnerZoneID != "zone-b" ||
		entry.OwnerEpoch != 2 ||
		entry.State != routing.RouteStateActive {
		t.Fatalf("drained=%v refreshed=%v route=%+v",
			drained.Load(), refreshed.Load(), entry)
	}
}

func TestManualMigrationLeavesRouteUnchangedWhenDrainRejects(t *testing.T) {
	oldZone := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		writeMigrationError(w, http.StatusConflict, "SHARD_HAS_ACTIVE_ACTORS")
	}))
	defer oldZone.Close()
	newZone := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.WriteHeader(http.StatusOK)
	}))
	defer newZone.Close()
	now := time.Now().UTC()
	zones := []routing.ZoneCandidate{
		{ZoneID: "zone-a", Endpoint: oldZone.URL},
		{ZoneID: "zone-b", Endpoint: newZone.URL},
	}
	routes, err := routing.NewStaticMap(now, time.Minute, zones)
	if err != nil {
		t.Fatal(err)
	}
	shardID := coordinatorShardOwnedBy(t, routes, "zone-a")
	handler := newMigrationHandler(
		routes, zones, http.DefaultClient, func() time.Time { return now }, time.Minute,
	)
	request := httptest.NewRequest(http.MethodPost,
		"/internal/v1/shards/"+strconv.FormatUint(uint64(shardID), 10)+"/move",
		strings.NewReader(`{"target_zone_id":"zone-b"}`))
	request.SetPathValue("shard_id", strconv.FormatUint(uint64(shardID), 10))
	request.RemoteAddr = "127.0.0.1:45678"
	response := httptest.NewRecorder()

	handler.move(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("move status=%d body=%s", response.Code, response.Body.String())
	}
	entry, err := routes.Route(shardID, now)
	if err != nil {
		t.Fatal(err)
	}
	if entry.OwnerZoneID != "zone-a" || entry.OwnerEpoch != 1 ||
		entry.RouteVersion != 1 {
		t.Fatalf("rejected drain changed route: %+v", entry)
	}
}

func coordinatorShardOwnedBy(
	t *testing.T,
	routes *routing.Map,
	zoneID string,
) uint32 {
	t.Helper()
	for shardID := uint32(0); shardID < routing.ShardCount; shardID++ {
		entry, err := routes.Entry(shardID)
		if err != nil {
			t.Fatal(err)
		}
		if entry.OwnerZoneID == zoneID {
			return shardID
		}
	}
	t.Fatalf("no shard owned by %s", zoneID)
	return 0
}
