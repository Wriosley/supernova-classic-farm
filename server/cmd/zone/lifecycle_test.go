package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func TestLifecycleDrainBlocksInactiveShardAndResumeRestoresIt(t *testing.T) {
	now := time.Date(2026, 8, 3, 14, 0, 0, 0, time.UTC)
	runtime := player.NewRuntime()
	defer runtime.Close()
	table, routes := zoneAuthorizationFixture(t, now, "zone-a")
	playerID, shardID := zonePlayer(t, routes, "zone-a", nil)
	handler := &lifecycleHandler{
		runtime: runtime, authorization: table,
		gates: &shardExecutionGates{}, now: func() time.Time { return now },
	}

	response := httptest.NewRecorder()
	handler.drain(response, lifecycleRequest(
		http.MethodPost, shardID, "drain", `{"owner_epoch":"1","transition_id":"move-1"}`,
	))
	if response.Code != http.StatusOK {
		t.Fatalf("drain status=%d body=%s", response.Code, response.Body.String())
	}
	if err := table.Validate(playerID, shardID, "zone-a", 1, now); !routing.IsNotOwner(err) {
		t.Fatalf("drained shard validation=%v, want NOT_OWNER", err)
	}

	response = httptest.NewRecorder()
	handler.resume(response, lifecycleRequest(
		http.MethodPost, shardID, "resume", `{"owner_epoch":"1","transition_id":"move-1"}`,
	))
	if response.Code != http.StatusNoContent {
		t.Fatalf("resume status=%d body=%s", response.Code, response.Body.String())
	}
	if err := table.Validate(playerID, shardID, "zone-a", 1, now); err != nil {
		t.Fatalf("resumed shard rejected: %v", err)
	}
}

func TestLifecycleRefusesShardWithActiveActorAndRollsBackDrain(t *testing.T) {
	now := time.Date(2026, 8, 3, 14, 0, 0, 0, time.UTC)
	runtime := player.NewRuntime()
	defer runtime.Close()
	table, routes := zoneAuthorizationFixture(t, now, "zone-a")
	playerID, shardID := zonePlayer(t, routes, "zone-a", nil)
	if _, err := runtime.Handle(context.Background(), playerID, 1,
		lifecycleSnapshotRequest(playerID, "active-before-drain")); err != nil {
		t.Fatal(err)
	}
	handler := &lifecycleHandler{
		runtime: runtime, authorization: table,
		gates: &shardExecutionGates{}, now: func() time.Time { return now },
	}
	response := httptest.NewRecorder()
	handler.drain(response, lifecycleRequest(
		http.MethodPost, shardID, "drain", `{"owner_epoch":"1","transition_id":"move-1"}`,
	))
	if response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), "SHARD_HAS_ACTIVE_ACTORS") {
		t.Fatalf("drain status=%d body=%s", response.Code, response.Body.String())
	}
	if err := table.Validate(playerID, shardID, "zone-a", 1, now); err != nil {
		t.Fatalf("failed drain did not resume shard: %v", err)
	}
}

func TestLifecycleDrainIsIdempotentAndAdoptsRetargetedTransition(t *testing.T) {
	now := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)
	runtime := player.NewRuntime()
	defer runtime.Close()
	table, routes := zoneAuthorizationFixture(t, now, "zone-a")
	_, shardID := zonePlayer(t, routes, "zone-a", nil)
	handler := &lifecycleHandler{runtime: runtime, authorization: table, gates: &shardExecutionGates{}, now: func() time.Time { return now }}

	for attempt := 0; attempt < 2; attempt++ {
		response := httptest.NewRecorder()
		handler.drain(response, lifecycleRequest(http.MethodPost, shardID, "drain", `{"owner_epoch":"1","transition_id":"move-1"}`))
		if response.Code != http.StatusOK {
			t.Fatalf("same-transition drain %d status=%d body=%s", attempt, response.Code, response.Body.String())
		}
	}

	// A Coordinator that died between drain and drain-complete retries under a
	// new transition; the stale marker must not wedge the Shard.
	response := httptest.NewRecorder()
	handler.drain(response, lifecycleRequest(http.MethodPost, shardID, "drain", `{"owner_epoch":"1","transition_id":"move-2"}`))
	if response.Code != http.StatusOK {
		t.Fatalf("retargeted drain status=%d body=%s", response.Code, response.Body.String())
	}
	if handler.drainTransition[shardID] != "move-2" {
		t.Fatalf("drain transition = %q, want move-2", handler.drainTransition[shardID])
	}
	response = httptest.NewRecorder()
	handler.resume(response, lifecycleRequest(http.MethodPost, shardID, "resume", `{"owner_epoch":"1","transition_id":"move-1"}`))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "DRAIN_TRANSITION_CONFLICT") {
		t.Fatalf("superseded resume status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestLifecycleDrainRefusesDifferentOwnerGeneration(t *testing.T) {
	now := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)
	runtime := player.NewRuntime()
	defer runtime.Close()
	table, routes := zoneAuthorizationFixture(t, now, "zone-a")
	_, shardID := zonePlayer(t, routes, "zone-a", nil)
	handler := &lifecycleHandler{runtime: runtime, authorization: table, gates: &shardExecutionGates{}, now: func() time.Time { return now }}
	response := httptest.NewRecorder()
	handler.drain(response, lifecycleRequest(http.MethodPost, shardID, "drain", `{"owner_epoch":"1","transition_id":"move-1"}`))
	if response.Code != http.StatusOK {
		t.Fatalf("drain status=%d body=%s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	handler.drain(response, lifecycleRequest(http.MethodPost, shardID, "drain", `{"owner_epoch":"2","transition_id":"move-2"}`))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "DRAIN_TRANSITION_CONFLICT") {
		t.Fatalf("cross-generation drain status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestLifecycleCompleteDrainAndResumeRequireSameTransition(t *testing.T) {
	now := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)
	runtime := player.NewRuntime()
	defer runtime.Close()
	table, routes := zoneAuthorizationFixture(t, now, "zone-a")
	_, shardID := zonePlayer(t, routes, "zone-a", nil)
	handler := &lifecycleHandler{runtime: runtime, authorization: table, gates: &shardExecutionGates{}, now: func() time.Time { return now }}
	body := `{"owner_epoch":"1","transition_id":"move-1"}`
	response := httptest.NewRecorder()
	handler.drain(response, lifecycleRequest(http.MethodPost, shardID, "drain", body))
	if response.Code != http.StatusOK {
		t.Fatalf("drain status=%d body=%s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.completeDrain(response, lifecycleRequest(http.MethodPost, shardID, "drain-complete", `{"owner_epoch":"1","transition_id":"move-2"}`))
	if response.Code != http.StatusConflict {
		t.Fatalf("stale complete status=%d body=%s", response.Code, response.Body.String())
	}
	for attempt := 0; attempt < 2; attempt++ {
		response = httptest.NewRecorder()
		handler.completeDrain(response, lifecycleRequest(http.MethodPost, shardID, "drain-complete", body))
		if response.Code != http.StatusOK {
			t.Fatalf("complete %d status=%d body=%s", attempt, response.Code, response.Body.String())
		}
	}
	response = httptest.NewRecorder()
	handler.resume(response, lifecycleRequest(http.MethodPost, shardID, "resume", `{"owner_epoch":"1","transition_id":"move-2"}`))
	if response.Code != http.StatusConflict {
		t.Fatalf("stale resume status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestLifecycleDrainWaitsForConcurrentShardRequest(t *testing.T) {
	now := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)
	runtime := player.NewRuntime()
	defer runtime.Close()
	table, routes := zoneAuthorizationFixture(t, now, "zone-a")
	_, shardID := zonePlayer(t, routes, "zone-a", nil)
	gates := &shardExecutionGates{}
	handler := &lifecycleHandler{runtime: runtime, authorization: table, gates: gates, now: func() time.Time { return now }}
	unlockRequest := gates.readLock(shardID)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		handler.drain(response, lifecycleRequest(http.MethodPost, shardID, "drain", `{"owner_epoch":"1","transition_id":"move-1"}`))
		done <- response
	}()
	select {
	case <-done:
		t.Fatal("Drain overtook an in-flight Shard request")
	case <-time.After(20 * time.Millisecond):
	}
	unlockRequest()
	select {
	case response := <-done:
		if response.Code != http.StatusOK {
			t.Fatalf("drain status=%d body=%s", response.Code, response.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("Drain did not continue after request released its gate")
	}
}

func lifecycleSnapshotRequest(playerID uint64, requestID string) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: player.ProtocolVersion,
		MessageKind:     wsv1.MessageKind_REQUEST,
		Action:          wsv1.Action_GET_PLAYER_SNAPSHOT,
		RequestId:       requestID,
		TargetPlayerId:  playerID,
		Payload: &wsv1.WsEnvelope_GetPlayerSnapshotRequest{
			GetPlayerSnapshotRequest: &wsv1.GetPlayerSnapshotRequest{},
		},
	}
}

func zoneAuthorizationFixture(
	t *testing.T,
	now time.Time,
	zoneID string,
) (*routing.AuthorizationTable, *routing.Map) {
	t.Helper()
	routes, err := routing.NewStaticMap(now, time.Minute, []routing.ZoneCandidate{
		{ZoneID: "zone-a", Endpoint: "http://127.0.0.1:8082"},
		{ZoneID: "zone-b", Endpoint: "http://127.0.0.1:8084"},
	})
	if err != nil {
		t.Fatal(err)
	}
	table, err := routing.NewAuthorizationTable(zoneID)
	if err != nil {
		t.Fatal(err)
	}
	if err := table.Replace(routes.Snapshot()); err != nil {
		t.Fatal(err)
	}
	return table, routes
}

func zonePlayer(
	t *testing.T,
	routes *routing.Map,
	zoneID string,
	excluded map[uint32]bool,
) (uint64, uint32) {
	t.Helper()
	for playerID := uint64(1); playerID < 100_000; playerID++ {
		shardID := routing.ShardForPlayer(playerID)
		if excluded != nil && excluded[shardID] {
			continue
		}
		entry, err := routes.Entry(shardID)
		if err != nil {
			t.Fatal(err)
		}
		if entry.OwnerZoneID == zoneID {
			return playerID, shardID
		}
	}
	t.Fatalf("no player found for %s", zoneID)
	return 0, 0
}

func lifecycleRequest(
	method string,
	shardID uint32,
	action string,
	body string,
) *http.Request {
	request := httptest.NewRequest(method,
		"/internal/v1/shards/"+strconv.FormatUint(uint64(shardID), 10)+"/"+action,
		bytes.NewBufferString(body))
	request.SetPathValue("shard_id", strconv.FormatUint(uint64(shardID), 10))
	request.RemoteAddr = "127.0.0.1:45678"
	return request
}
