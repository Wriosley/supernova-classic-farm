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
	for _, field := range []string{"owner_epoch", "route_version", "lease_term"} {
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
