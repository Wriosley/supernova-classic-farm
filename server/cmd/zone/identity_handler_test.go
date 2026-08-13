package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wriosley/supernova-classic-farm/server/internal/zoneidentity"
)

func TestZoneIdentityHandlerReturnsProcessIdentity(t *testing.T) {
	identity := zoneidentity.Identity{LogicalZoneID: "d859cea1-ac5b-5524-bffa-4e542301cd95", IncarnationID: "9e398c48-4c67-41e8-8655-d33167d42fb4", Endpoint: "http://zone-pool-0.zone-headless.classic-farm.svc.cluster.local:8082"}
	recorder := httptest.NewRecorder()
	newZoneIdentityHandler(identity).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/internal/v1/zone-identity", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d", recorder.Code)
	}
	var got zoneidentity.Identity
	if err := json.NewDecoder(recorder.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got != identity {
		t.Fatalf("got=%+v want=%+v", got, identity)
	}
}
