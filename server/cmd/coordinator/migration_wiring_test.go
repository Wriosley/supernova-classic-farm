package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	coordinatormigration "github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/migration"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func TestMigrationWorkerConfigDefaultsDisabled(t *testing.T) {
	t.Setenv("COORDINATOR_MIGRATION_WORKER_ENABLED", "")
	config, err := migrationWorkerConfigFromEnvironment()
	if err != nil || config.Enabled || config.Limits != (coordinatormigration.Limits{Global: 8, PerSource: 2, PerTarget: 2}) {
		t.Fatalf("default config = %+v, err=%v", config, err)
	}
	t.Setenv("COORDINATOR_MIGRATION_WORKER_ENABLED", "1")
	config, err = migrationWorkerConfigFromEnvironment()
	if err != nil || !config.Enabled {
		t.Fatalf("enabled config = %+v, err=%v", config, err)
	}
	t.Setenv("COORDINATOR_MIGRATION_WORKER_ENABLED", "true")
	if _, err := migrationWorkerConfigFromEnvironment(); err == nil {
		t.Fatal("non-0/1 worker switch accepted")
	}
}

func TestHTTPZoneLifecycleBindsDrainAndRestoreToTransition(t *testing.T) {
	requests := make(chan map[string]string, 3)
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode %s: %v", r.URL.Path, err)
		}
		requests <- body
		response := &http.Response{StatusCode: http.StatusNoContent, Status: "204 No Content", Header: make(http.Header), Body: http.NoBody, Request: r}
		if strings.HasSuffix(r.URL.Path, "/drain-complete") {
			response.StatusCode, response.Status, response.Body = http.StatusOK, "200 OK", io.NopCloser(strings.NewReader(`{"shard_id":7,"owner_epoch":"3","players":[]}`))
		}
		return response, nil
	})}
	client := newHTTPZoneLifecycle(httpClient)
	source := routing.RouteEntry{ShardID: 7, OwnerEndpoint: "http://zone-a:8082", OwnerEpoch: 3}
	if _, err := client.Drain(t.Context(), source, "transition-7"); err != nil {
		t.Fatal(err)
	}
	if err := client.Restore(t.Context(), source, "transition-7"); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		select {
		case body := <-requests:
			if body["owner_epoch"] != "3" || body["transition_id"] != "transition-7" {
				t.Fatalf("request %d body=%v", index, body)
			}
		case <-time.After(time.Second):
			t.Fatal("missing lifecycle request")
		}
	}
}
