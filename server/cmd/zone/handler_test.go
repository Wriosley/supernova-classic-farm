package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/protobuf/proto"
)

func commandRequest(t *testing.T, playerID, epoch uint64) *http.Request {
	t.Helper()
	envelope := &wsv1.WsEnvelope{
		ProtocolVersion: player.ProtocolVersion,
		MessageKind:     wsv1.MessageKind_REQUEST,
		Action:          wsv1.Action_GET_PLAYER_SNAPSHOT,
		RequestId:       "http-request",
		TargetPlayerId:  playerID,
		Payload: &wsv1.WsEnvelope_GetPlayerSnapshotRequest{
			GetPlayerSnapshotRequest: &wsv1.GetPlayerSnapshotRequest{},
		},
	}
	body, err := proto.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/command", bytes.NewReader(body))
	request.RemoteAddr = "127.0.0.1:45678"
	request.Header.Set("X-Caller-Player-ID", "42")
	request.Header.Set("X-Shard-ID", strconv.FormatUint(
		uint64(routing.ShardForPlayer(playerID)), 10))
	request.Header.Set("X-Owner-Zone-ID", routing.DefaultZoneID)
	request.Header.Set("X-Owner-Epoch", "1")
	request.Header.Set("X-Route-Version", "1")
	if epoch != 1 {
		request.Header.Set("X-Owner-Epoch", "2")
	}
	return request
}

func TestCommandHandlerReturnsBinarySnapshot(t *testing.T) {
	runtime := player.NewRuntime()
	defer runtime.Close()
	recorder := httptest.NewRecorder()

	newCommandHandler(runtime).ServeHTTP(recorder, commandRequest(t, 42, 1))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/x-protobuf" {
		t.Fatalf("Content-Type = %q", got)
	}
	response := &wsv1.WsEnvelope{}
	if err := proto.Unmarshal(recorder.Body.Bytes(), response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.GetRequestId() != "http-request" ||
		response.GetGetPlayerSnapshotResponse().GetSnapshot().GetCoinBalance() != player.InitialCoinBalance {
		t.Fatalf("response = %+v", response)
	}
}

func TestCommandHandlerReturnsNotOwnerJSON(t *testing.T) {
	runtime := player.NewRuntime()
	defer runtime.Close()
	recorder := httptest.NewRecorder()

	newCommandHandler(runtime).ServeHTTP(recorder, commandRequest(t, 42, 2))

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	if !strings.Contains(recorder.Body.String(), `"code":"NOT_OWNER"`) {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestCommandHandlerRejectsNonLoopback(t *testing.T) {
	runtime := player.NewRuntime()
	defer runtime.Close()
	request := commandRequest(t, 42, 1)
	request.RemoteAddr = "192.0.2.1:45678"
	recorder := httptest.NewRecorder()

	newCommandHandler(runtime).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d", recorder.Code)
	}
}
