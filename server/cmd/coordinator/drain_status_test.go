package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coordinatormigration "github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/migration"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func TestDrainStatusReportsRemovalGate(t *testing.T) {
	routes, err := routing.NewStaticMap(time.Now(), time.Minute, []routing.ZoneCandidate{{ZoneID: "zone-a", Endpoint: "http://zone-a:8082"}, {ZoneID: "zone-pool", Endpoint: "http://zone-pool:8082"}})
	if err != nil {
		t.Fatal(err)
	}
	store, _ := coordinatormigration.NewMemoryTaskStore()
	recorder := httptest.NewRecorder()
	drainStatusHandler(routes, store, &drainProgressBackend{}, map[string]struct{}{"zone-a": {}}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/internal/v1/zones/drain", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"owner_shards":`) || strings.Contains(recorder.Body.String(), `"removable":true`) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestDrainStatusBlocksRemovalWhileSourceProgressIsOpen(t *testing.T) {
	routes, err := routing.NewStaticMap(time.Now(), time.Minute, []routing.ZoneCandidate{{ZoneID: "zone-pool", Endpoint: "http://zone-pool:8082"}})
	if err != nil {
		t.Fatal(err)
	}
	store, _ := coordinatormigration.NewMemoryTaskStore()
	progress := &drainProgressBackend{rows: []routing.MigrationProgressRow{{ShardID: 7, SourceZoneID: "zone-a"}}}
	recorder := httptest.NewRecorder()
	drainStatusHandler(routes, store, progress, map[string]struct{}{"zone-a": {}}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/internal/v1/zones/drain", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"open_progress":1`) || strings.Contains(recorder.Body.String(), `"removable":true`) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
}

type drainProgressBackend struct {
	rows []routing.MigrationProgressRow
}

func (backend *drainProgressBackend) UpsertProgress(context.Context, routing.MigrationProgressRow) error {
	return nil
}
func (backend *drainProgressBackend) LoadProgress(context.Context, uint32) (routing.MigrationProgressRow, bool, error) {
	return routing.MigrationProgressRow{}, false, nil
}
func (backend *drainProgressBackend) LoadOpenProgress(context.Context) ([]routing.MigrationProgressRow, error) {
	return backend.rows, nil
}
func (backend *drainProgressBackend) MarkAbandoned(context.Context, uint32, string, time.Time) error {
	return nil
}
func (backend *drainProgressBackend) DeleteOpenProgress(context.Context, uint32, string) error {
	return nil
}
