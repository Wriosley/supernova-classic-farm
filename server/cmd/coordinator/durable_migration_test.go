package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/routestore"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func TestDurableFenceFailureLeavesCommittedPreparing(t *testing.T) {
	handler, routes, shardID, request := durableMigrationFixture(t)
	handler.advanceFence = func(context.Context, routing.RouteEntry) error { return errors.New("injected fence failure") }
	response := httptest.NewRecorder()
	handler.move(response, request)
	entry, _ := routes.Entry(shardID)
	if response.Code == http.StatusOK || entry.State != routing.RouteStatePreparing || routes.Snapshot().MapVersion != 2 {
		t.Fatalf("status=%d map=%d route=%+v", response.Code, routes.Snapshot().MapVersion, entry)
	}
}

func TestDurableTargetPrepareFailureRemainsResumable(t *testing.T) {
	handler, routes, shardID, request := durableMigrationFixture(t)
	handler.client = clientRejectingPath("/prepare", 1)
	response := httptest.NewRecorder()
	handler.move(response, request)
	entry, _ := routes.Entry(shardID)
	if response.Code == http.StatusOK || entry.State != routing.RouteStatePreparing ||
		handler.progress[shardID] == nil || handler.progress[shardID].Step != routing.MigrationStepFenceAdvanced {
		t.Fatalf("status=%d route=%+v progress=%+v", response.Code, entry, handler.progress[shardID])
	}
}

func TestDurableActiveRefreshAndCleanupFailuresNeverRollBack(t *testing.T) {
	for name, configure := range map[string]func(*migrationHandler){
		"refresh": func(handler *migrationHandler) { handler.client = clientRejectingPath("/ownership/refresh", 1) },
		"cleanup": func(handler *migrationHandler) {
			handler.deleteProgressOverride = func(context.Context, *migrationProgress) error { return errors.New("injected cleanup failure") }
		},
	} {
		t.Run(name, func(t *testing.T) {
			handler, routes, shardID, request := durableMigrationFixture(t)
			configure(handler)
			response := httptest.NewRecorder()
			handler.move(response, request)
			entry, _ := routes.Entry(shardID)
			if response.Code == http.StatusOK || entry.State != routing.RouteStateActive ||
				entry.OwnerZoneID != "zone-b" || routes.Snapshot().MapVersion != 3 {
				t.Fatalf("status=%d map=%d route=%+v", response.Code, routes.Snapshot().MapVersion, entry)
			}
		})
	}
}

func TestDurablePreparingFailureDoesNotChangeInMemoryCurrent(t *testing.T) {
	handler, routes, shardID, request := durableMigrationFixture(t)
	handler.routeStore = &failingRouteStore{Store: handler.routeStore, failPreparing: true}
	response := httptest.NewRecorder()
	handler.move(response, request)
	entry, _ := routes.Entry(shardID)
	if response.Code == http.StatusOK || entry.State != routing.RouteStateActive || entry.OwnerZoneID != "zone-a" {
		t.Fatalf("status=%d route=%+v", response.Code, entry)
	}
}

func TestDurableActiveFailureLeavesPreparingInMemory(t *testing.T) {
	handler, routes, shardID, request := durableMigrationFixture(t)
	handler.routeStore = &failingRouteStore{Store: handler.routeStore, failActive: true}
	response := httptest.NewRecorder()
	handler.move(response, request)
	entry, _ := routes.Entry(shardID)
	if response.Code == http.StatusOK || entry.State != routing.RouteStatePreparing || entry.OwnerZoneID != "zone-b" {
		t.Fatalf("status=%d route=%+v", response.Code, entry)
	}
}

func TestDurableMigrationExposesOnlyCommittedActive(t *testing.T) {
	handler, routes, shardID, request := durableMigrationFixture(t)
	recorder := &recordingRoutePublisher{}
	handler.routePublisher = recorder
	response := httptest.NewRecorder()
	handler.move(response, request)
	entry, err := routes.Entry(shardID)
	if err != nil || response.Code != http.StatusOK || entry.State != routing.RouteStateActive ||
		entry.OwnerZoneID != "zone-b" || routes.Snapshot().MapVersion != 3 {
		t.Fatalf("status=%d map=%d route=%+v err=%v", response.Code, routes.Snapshot().MapVersion, entry, err)
	}
	if len(recorder.changes) != 2 || recorder.changes[0][0].Entries[shardID].State != routing.RouteStateActive || recorder.changes[0][1].Entries[shardID].State != routing.RouteStatePreparing || recorder.changes[1][1].Entries[shardID].State != routing.RouteStateActive {
		t.Fatalf("published changes=%v", recorder.changes)
	}
}

func TestDurablePublisherFailureDoesNotRollBackCurrent(t *testing.T) {
	handler, routes, shardID, request := durableMigrationFixture(t)
	handler.routePublisher = failingRoutePublisher{}
	response := httptest.NewRecorder()
	handler.move(response, request)
	entry, _ := routes.Entry(shardID)
	if entry.State != routing.RouteStateActive || entry.OwnerZoneID != "zone-b" || routes.Snapshot().MapVersion != 3 {
		t.Fatalf("publisher failure rolled back Current: %+v", entry)
	}
}

type recordingRoutePublisher struct{ changes [][2]routing.Snapshot }

func (p *recordingRoutePublisher) PublishRoutes(previous, current routing.Snapshot) error {
	p.changes = append(p.changes, [2]routing.Snapshot{previous, current})
	return nil
}

type failingRoutePublisher struct{}

func (failingRoutePublisher) PublishRoutes(routing.Snapshot, routing.Snapshot) error {
	return errors.New("injected publish failure")
}

type failingRouteStore struct {
	routestore.Store
	failPreparing bool
	failActive    bool
}

func (s *failingRouteStore) CommitPreparing(ctx context.Context, entry routing.RouteEntry, expected uint64) (routestore.Snapshot, error) {
	if s.failPreparing {
		return routestore.Snapshot{}, errors.New("injected preparing failure")
	}
	return s.Store.CommitPreparing(ctx, entry, expected)
}

func (s *failingRouteStore) CommitActive(ctx context.Context, entry routing.RouteEntry, expected uint64) (routestore.Snapshot, error) {
	if s.failActive {
		return routestore.Snapshot{}, errors.New("injected active failure")
	}
	return s.Store.CommitActive(ctx, entry, expected)
}

func durableMigrationFixture(t *testing.T) (*migrationHandler, *routing.Map, uint32, *http.Request) {
	t.Helper()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	oldZone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/drain") {
			w.WriteHeader(http.StatusOK)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/drain-complete") {
			shardID, _ := strconv.ParseUint(r.PathValue("shard_id"), 10, 32)
			_ = shardID
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"shard_id":0,"owner_epoch":"1","players":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(oldZone.Close)
	newZone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/readyz":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/internal/v1/ownership/refresh":
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/prepare"):
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(newZone.Close)
	zones := []routing.ZoneCandidate{{ZoneID: "zone-a", Endpoint: oldZone.URL}, {ZoneID: "zone-b", Endpoint: newZone.URL}}
	routes, err := routing.NewStaticMap(now, time.Minute, zones)
	if err != nil {
		t.Fatal(err)
	}
	shardID := coordinatorShardOwnedBy(t, routes, "zone-a")
	// The test server response needs the selected shard rather than assuming 0.
	oldZone.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/drain") {
			w.WriteHeader(http.StatusOK)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/drain-complete") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"shard_id":` + strconv.FormatUint(uint64(shardID), 10) + `,"owner_epoch":"1","players":[]}`))
			return
		}
		http.NotFound(w, r)
	})
	store := routestore.NewMemoryStore()
	if _, _, err := store.BootstrapIfEmpty(context.Background(), routestore.FromRoutingSnapshot(routes.Snapshot(), now)); err != nil {
		t.Fatal(err)
	}
	handler := newMigrationHandler(routes, zones, http.DefaultClient, func() time.Time { return now }, time.Minute)
	handler.routeStore = store
	handler.advanceFence = func(context.Context, routing.RouteEntry) error { return nil }
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/shards/"+strconv.FormatUint(uint64(shardID), 10)+"/move", strings.NewReader(`{"target_zone_id":"zone-b"}`))
	request.SetPathValue("shard_id", strconv.FormatUint(uint64(shardID), 10))
	request.RemoteAddr = "127.0.0.1:45678"
	return handler, routes, shardID, request
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func clientRejectingPath(suffix string, failures int) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if failures > 0 && strings.HasSuffix(request.URL.Path, suffix) {
			failures--
			return &http.Response{StatusCode: http.StatusServiceUnavailable,
				Status: "503 Service Unavailable", Body: http.NoBody, Header: make(http.Header), Request: request}, nil
		}
		return http.DefaultTransport.RoundTrip(request)
	})}
}
