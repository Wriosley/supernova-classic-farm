package routing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRouteHTTPReturnsDecimalStrings(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	routes, err := NewLocalMap(now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandler(routes, func() time.Time { return now })
	request := httptest.NewRequest(http.MethodGet, "/internal/v1/routes/42", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"owner_epoch", "route_version", "map_version", "lease_term"} {
		if _, ok := body[field].(string); !ok {
			t.Errorf("%s type = %T, want JSON string", field, body[field])
		}
	}
	if body["owner_zone_id"] != DefaultZoneID ||
		body["owner_endpoint"] != DefaultZoneEndpoint ||
		body["state"] != string(RouteStateActive) ||
		body["routable"] != true {
		t.Fatalf("unexpected response: %#v", body)
	}
}

func TestRouteHTTPSnapshotIsCompleteAndCounted(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	routes, err := NewStaticMap(now, time.Minute, []ZoneCandidate{
		{ZoneID: "zone-a", Endpoint: "http://127.0.0.1:8082"},
		{ZoneID: "zone-b", Endpoint: "http://127.0.0.1:8084"},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandler(routes, func() time.Time { return now })
	response := httptest.NewRecorder()
	handler.ServeHTTP(response,
		httptest.NewRequest(http.MethodGet, "/internal/v1/routes", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		ShardCount                 uint32          `json:"shard_count"`
		HashAlgorithmVersion       uint32          `json:"hash_algorithm_version"`
		AssignmentAlgorithmVersion uint32          `json:"assignment_algorithm_version"`
		MapVersion                 string          `json:"map_version"`
		Entries                    []routeResponse `json:"entries"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ShardCount != ShardCount ||
		body.HashAlgorithmVersion != HashAlgorithmVersion ||
		body.AssignmentAlgorithmVersion != AssignmentAlgorithmVersion ||
		body.MapVersion != "1" ||
		len(body.Entries) != int(ShardCount) {
		t.Fatalf("invalid snapshot metadata: shard_count=%d hash=%d assignment=%d map=%s entries=%d",
			body.ShardCount, body.HashAlgorithmVersion,
			body.AssignmentAlgorithmVersion, body.MapVersion, len(body.Entries))
	}
	for shardID, entry := range body.Entries {
		if entry.ShardID != uint32(shardID) || !entry.Routable {
			t.Fatalf("invalid snapshot entry %d: %+v", shardID, entry)
		}
	}

	statsResponse := httptest.NewRecorder()
	handler.ServeHTTP(statsResponse,
		httptest.NewRequest(http.MethodGet, "/internal/v1/debug/route-lookups", nil))
	var stats routeLookupStats
	if err := json.Unmarshal(statsResponse.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Snapshot != 1 || stats.Shard != 0 {
		t.Fatalf("unexpected lookup stats: %+v", stats)
	}
}

func TestRouteHTTPRejectsExpiredLeaseAsNotOwner(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	routes, err := NewLocalMap(now, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandler(routes, func() time.Time { return now.Add(time.Second) })
	request := httptest.NewRequest(http.MethodGet, "/internal/v1/routes/7", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Routable bool `json:"routable"`
		Error    struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Routable || body.Error.Code != "NOT_OWNER" {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestRouteHTTPUsesRuntimeLeaseBeyondDurableExpiry(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	routes, err := NewLocalMap(now, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := NewRuntimeLeaseOverlay(routes.Snapshot(), now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return now.Add(45 * time.Second) }
	handler := NewHTTPHandlerWithRuntimeLeases(routes, overlay, clock)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/internal/v1/routes/42", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body routeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.LeaseExpiresAtMS != now.Add(time.Minute).UnixMilli() || !body.Routable {
		t.Fatalf("effective route=%+v", body)
	}
}

func TestDurableHTTPLeaseRefreshKeepsZoneAuthorizedPastStoredExpiry(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	routes, _ := NewLocalMap(now, 30*time.Second)
	durable := routes.Snapshot()
	overlay, _ := NewRuntimeLeaseOverlay(durable, now.Add(20*time.Second), 30*time.Second)
	refreshAt := now.Add(45 * time.Second)
	effective := durable
	effective.Entries = append([]RouteEntry(nil), durable.Entries...)
	for index, entry := range effective.Entries {
		resolved, err := overlay.Effective(entry, refreshAt)
		if err != nil {
			t.Fatalf("effective shard %d: %v", index, err)
		}
		effective.Entries[index] = resolved
	}
	table, _ := NewAuthorizationTable(DefaultZoneID)
	if err := table.Replace(durable); err != nil {
		t.Fatal(err)
	}
	if err := table.Replace(effective); err != nil {
		t.Fatalf("same-version HTTP lease refresh rejected: %v", err)
	}
	playerID, shardID := playerOwnedBy(t, routes, DefaultZoneID)
	if err := table.Validate(playerID, shardID, DefaultZoneID, 1, refreshAt); err != nil {
		t.Fatalf("Zone rejected durable route after stored 30s expiry: %v", err)
	}
}

func TestRouteHTTPFailsClosedWhenRuntimeLeaseBindingIsStale(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	routes, _ := NewLocalMap(now, 30*time.Second)
	overlay, _ := NewRuntimeLeaseOverlay(routes.Snapshot(), now, time.Minute)
	before := routes.Snapshot()
	changed := before
	changed.MapVersion++
	changed.CommittedIndex++
	changed.Entries = append([]RouteEntry(nil), before.Entries...)
	changed.Entries[42].RouteVersion++
	if err := routes.ApplyCommittedSnapshot(changed); err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandlerWithRuntimeLeases(routes, overlay, func() time.Time { return now })
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/internal/v1/routes/42", nil))
	if response.Code != http.StatusConflict {
		t.Fatalf("stale overlay status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRouteHTTPRejectsInvalidShardID(t *testing.T) {
	now := time.Now().UTC()
	routes, err := NewLocalMap(now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandler(routes, func() time.Time { return now })
	for _, path := range []string{
		"/internal/v1/routes/not-a-number",
		"/internal/v1/routes/4096",
	} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}
}
