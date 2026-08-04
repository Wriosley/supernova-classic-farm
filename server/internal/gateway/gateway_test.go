package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
	reasonv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/reason"
	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"
)

func TestClassifyTransportError(t *testing.T) {
	if got := classifyTransportError(context.DeadlineExceeded); got != "timeout" {
		t.Fatalf("deadline classification = %q", got)
	}
	if got := classifyTransportError(fmt.Errorf("wrapped: %w", context.DeadlineExceeded)); got != "timeout" {
		t.Fatalf("wrapped deadline classification = %q", got)
	}
	if got := classifyTransportError(errors.New("other")); got != "other" {
		t.Fatalf("other classification = %q", got)
	}
}

type ticketFunc func(context.Context, string) (uint64, error)

func (f ticketFunc) Consume(ctx context.Context, ticket string) (uint64, error) {
	return f(ctx, ticket)
}

type routeFunc func(context.Context, uint32) (Route, error)

func (f routeFunc) Resolve(ctx context.Context, shard uint32) (Route, error) {
	return f(ctx, shard)
}

type zoneFunc func(context.Context, Route, uint64, []byte) ([]byte, error)

func (f zoneFunc) Command(ctx context.Context, route Route, caller uint64, body []byte) ([]byte, error) {
	return f(ctx, route, caller, body)
}

func TestHTTPTicketConsumerSingleUseAndGatewayIdentity(t *testing.T) {
	var consumed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Ticket    string `json:"ticket"`
			GatewayID string `json:"gateway_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.Ticket != "single-use" || request.GatewayID != DefaultGatewayID {
			t.Errorf("consume request = %+v", request)
		}
		if consumed.Swap(true) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"player_id":"18446744073709551615"}`))
	}))
	defer server.Close()

	consumer := &HTTPTicketConsumer{Client: server.Client(), Endpoint: server.URL}
	playerID, err := consumer.Consume(context.Background(), "single-use")
	if err != nil || playerID != ^uint64(0) {
		t.Fatalf("first Consume() = (%d, %v)", playerID, err)
	}
	if _, err := consumer.Consume(context.Background(), "single-use"); err == nil {
		t.Fatal("second Consume() succeeded; ticket was not single-use")
	}
}

func TestConnectionCloseCodes(t *testing.T) {
	tests := []struct {
		name     string
		timeout  time.Duration
		send     func(*testing.T, *websocket.Conn)
		wantCode websocket.StatusCode
	}{
		{
			name: "auth timeout", timeout: 20 * time.Millisecond,
			send:     func(*testing.T, *websocket.Conn) {},
			wantCode: websocket.StatusCode(4401),
		},
		{
			name: "unsupported protocol",
			send: func(t *testing.T, conn *websocket.Conn) {
				writeEnvelope(t, conn, &wsv1.WsEnvelope{
					ProtocolVersion: 2, MessageKind: wsv1.MessageKind_REQUEST,
					Action: wsv1.Action_AUTH, RequestId: "auth-v2",
					Payload: &wsv1.WsEnvelope_AuthRequest{AuthRequest: &wsv1.AuthRequest{WsTicket: "ticket"}},
				})
			},
			wantCode: websocket.StatusCode(4406),
		},
		{
			name: "invalid tuple",
			send: func(t *testing.T, conn *websocket.Conn) {
				writeEnvelope(t, conn, &wsv1.WsEnvelope{
					ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_REQUEST,
					Action: wsv1.Action_AUTH, RequestId: "bad-tuple",
					Payload: &wsv1.WsEnvelope_PingRequest{PingRequest: &wsv1.PingRequest{}},
				})
			},
			wantCode: websocket.StatusProtocolError,
		},
		{
			name: "oversized message",
			send: func(t *testing.T, conn *websocket.Conn) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := conn.Write(ctx, websocket.MessageBinary, make([]byte, MaxMessageBytes+1)); err != nil {
					t.Fatalf("write oversized message: %v", err)
				}
			},
			wantCode: websocket.StatusMessageTooBig,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			timeout := test.timeout
			if timeout == 0 {
				timeout = time.Second
			}
			conn, closeServer := unauthenticatedConnection(t, timeout)
			defer closeServer()
			defer conn.CloseNow()
			test.send(t, conn)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, _, err := conn.Read(ctx)
			if code := websocket.CloseStatus(err); code != test.wantCode {
				t.Fatalf("close status = %d (%v), want %d", code, err, test.wantCode)
			}
		})
	}
}

func TestPingStaysAtGate(t *testing.T) {
	var zoneCalls atomic.Int32
	conn, closeServer := authenticatedConnection(t, zoneFunc(func(context.Context, Route, uint64, []byte) ([]byte, error) {
		zoneCalls.Add(1)
		return nil, errors.New("unexpected Zone call")
	}), nil)
	defer closeServer()
	defer conn.CloseNow()

	request := pingRequest("ping-1", 7)
	writeEnvelope(t, conn, request)
	response := readEnvelope(t, conn)
	if response.RequestId != request.RequestId || response.GetPingResponse().GetPingId() != 7 {
		t.Fatalf("PING response = %+v", response)
	}
	if zoneCalls.Load() != 0 {
		t.Fatalf("Zone calls = %d, want 0", zoneCalls.Load())
	}
}

func TestSnapshotResponseIsCorrelated(t *testing.T) {
	zone := zoneFunc(func(_ context.Context, _ Route, caller uint64, body []byte) ([]byte, error) {
		request := decodeEnvelope(t, body)
		return proto.Marshal(snapshotResponse(request, caller))
	})
	conn, closeServer := authenticatedConnection(t, zone, nil)
	defer closeServer()
	defer conn.CloseNow()

	writeEnvelope(t, conn, snapshotRequest("snapshot-42", 42))
	response := readEnvelope(t, conn)
	if response.MessageKind != wsv1.MessageKind_RESPONSE ||
		response.Action != wsv1.Action_GET_PLAYER_SNAPSHOT ||
		response.RequestId != "snapshot-42" ||
		response.GetGetPlayerSnapshotResponse().GetSnapshot().GetPlayerId() != 42 {
		t.Fatalf("snapshot response = %+v", response)
	}
}

func TestSnapshotBuffersPushAndFlushesOnlyNewerVersions(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	zone := zoneFunc(func(_ context.Context, _ Route, caller uint64, body []byte) ([]byte, error) {
		request := decodeEnvelope(t, body)
		close(started)
		<-release
		response := snapshotResponse(request, caller)
		response.StateVersion.PlayerSeq = 1
		return proto.Marshal(response)
	})
	conn, handler, closeServer := authenticatedConnectionWithHandler(t, zone, nil)
	defer closeServer()
	defer conn.CloseNow()

	writeEnvelope(t, conn, snapshotRequest("snapshot-buffer", 42))
	<-started
	if err := handler.pushHub.Publish(maturedPush(42, 1)); err != nil {
		t.Fatal(err)
	}
	if err := handler.pushHub.Publish(maturedPush(42, 2)); err != nil {
		t.Fatal(err)
	}
	close(release)

	snapshot := readEnvelope(t, conn)
	if snapshot.GetGetPlayerSnapshotResponse() == nil ||
		snapshot.GetStateVersion().GetPlayerSeq() != 1 {
		t.Fatalf("first envelope = %+v, want snapshot at seq 1", snapshot)
	}
	push := readEnvelope(t, conn)
	if push.GetMessageKind() != wsv1.MessageKind_PUSH ||
		push.GetStateVersion().GetPlayerSeq() != 2 {
		t.Fatalf("second envelope = %+v, want push at seq 2", push)
	}
}

func TestNotOwnerRetriesOnceWithSameRequestID(t *testing.T) {
	var routeCalls atomic.Int32
	routes := routeFunc(func(_ context.Context, shard uint32) (Route, error) {
		call := routeCalls.Add(1)
		return Route{ShardID: shard, OwnerEpoch: uint64(call), OwnerEndpoint: "http://127.0.0.1:8082"}, nil
	})
	var mu sync.Mutex
	var requestIDs []string
	var zoneCalls atomic.Int32
	zone := zoneFunc(func(_ context.Context, _ Route, caller uint64, body []byte) ([]byte, error) {
		request := decodeEnvelope(t, body)
		mu.Lock()
		requestIDs = append(requestIDs, request.RequestId)
		mu.Unlock()
		if zoneCalls.Add(1) == 1 {
			return nil, ErrNotOwner
		}
		return proto.Marshal(snapshotResponse(request, caller))
	})
	conn, closeServer := authenticatedConnection(t, zone, routes)
	defer closeServer()
	defer conn.CloseNow()

	writeEnvelope(t, conn, snapshotRequest("same-id", 42))
	if response := readEnvelope(t, conn); response.RequestId != "same-id" {
		t.Fatalf("response request_id = %q", response.RequestId)
	}
	if routeCalls.Load() != 2 || zoneCalls.Load() != 2 {
		t.Fatalf("route/Zone calls = %d/%d, want 2/2", routeCalls.Load(), zoneCalls.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requestIDs) != 2 || requestIDs[0] != "same-id" || requestIDs[1] != "same-id" {
		t.Fatalf("retried request IDs = %v", requestIDs)
	}
}

func TestConcurrentResponsesUseSerializedWebSocketWrites(t *testing.T) {
	zone := zoneFunc(func(_ context.Context, _ Route, caller uint64, body []byte) ([]byte, error) {
		request := decodeEnvelope(t, body)
		time.Sleep(time.Duration(len(request.RequestId)%4) * time.Millisecond)
		return proto.Marshal(snapshotResponse(request, caller))
	})
	conn, closeServer := authenticatedConnection(t, zone, nil)
	defer closeServer()
	defer conn.CloseNow()

	const requests = 32
	for i := 0; i < requests; i++ {
		writeEnvelope(t, conn, snapshotRequest("concurrent-"+string(rune('A'+i)), 42))
	}
	seen := make(map[string]bool, requests)
	for i := 0; i < requests; i++ {
		response := readEnvelope(t, conn)
		if seen[response.RequestId] {
			t.Fatalf("duplicate response %q", response.RequestId)
		}
		seen[response.RequestId] = true
	}
	if len(seen) != requests {
		t.Fatalf("received %d responses, want %d", len(seen), requests)
	}
}

func unauthenticatedConnection(t *testing.T, authTimeout time.Duration) (*websocket.Conn, func()) {
	t.Helper()
	handler, err := NewHandler(Config{
		Tickets: ticketFunc(func(context.Context, string) (uint64, error) { return 42, nil }),
		Routes: routeFunc(func(context.Context, uint32) (Route, error) {
			return Route{}, errors.New("unused")
		}),
		Zone: zoneFunc(func(context.Context, Route, uint64, []byte) ([]byte, error) {
			return nil, errors.New("unused")
		}),
		AuthTimeout: authTimeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"http://127.0.0.1:5173"}},
	})
	if err != nil {
		server.Close()
		t.Fatalf("dial Gate: %v", err)
	}
	return conn, server.Close
}

func authenticatedConnection(t *testing.T, zone ZoneCommander, routes RouteResolver) (*websocket.Conn, func()) {
	t.Helper()
	conn, _, closeServer := authenticatedConnectionWithHandler(t, zone, routes)
	return conn, closeServer
}

func authenticatedConnectionWithHandler(
	t *testing.T,
	zone ZoneCommander,
	routes RouteResolver,
) (*websocket.Conn, *Handler, func()) {
	t.Helper()
	if routes == nil {
		routes = routeFunc(func(_ context.Context, shard uint32) (Route, error) {
			return Route{ShardID: shard, OwnerEpoch: 1, OwnerEndpoint: "http://127.0.0.1:8082"}, nil
		})
	}
	handler, err := NewHandler(Config{
		Tickets: ticketFunc(func(_ context.Context, ticket string) (uint64, error) {
			if ticket != "valid-ticket" {
				return 0, errors.New("invalid ticket")
			}
			return 42, nil
		}),
		Routes: routes, Zone: zone,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"http://localhost:5173"}},
	})
	if err != nil {
		server.Close()
		t.Fatalf("dial Gate: %v", err)
	}
	writeEnvelope(t, conn, &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_AUTH, RequestId: "auth-1",
		Payload: &wsv1.WsEnvelope_AuthRequest{AuthRequest: &wsv1.AuthRequest{WsTicket: "valid-ticket"}},
	})
	response := readEnvelope(t, conn)
	if response.GetAuthResponse().GetPlayerId() != 42 ||
		len(response.GetAuthResponse().GetClientConfigSha256()) != 32 {
		conn.CloseNow()
		server.Close()
		t.Fatalf("AUTH response = %+v", response)
	}
	return conn, handler, server.Close
}

func snapshotRequest(requestID string, playerID uint64) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_GET_PLAYER_SNAPSHOT, RequestId: requestID,
		TargetPlayerId: playerID,
		Payload: &wsv1.WsEnvelope_GetPlayerSnapshotRequest{
			GetPlayerSnapshotRequest: &wsv1.GetPlayerSnapshotRequest{},
		},
	}
}

func snapshotResponse(request *wsv1.WsEnvelope, playerID uint64) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId, TargetPlayerId: playerID,
		StateVersion: &wsv1.StateVersion{OwnerEpoch: 1, PlayerSeq: 0},
		ServerTimeMs: time.Now().UnixMilli(),
		Payload: &wsv1.WsEnvelope_GetPlayerSnapshotResponse{
			GetPlayerSnapshotResponse: &wsv1.GetPlayerSnapshotResponse{
				Snapshot: &wsv1.PlayerSnapshot{PlayerId: playerID},
			},
		},
	}
}

func maturedPush(playerID, playerSeq uint64) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion,
		MessageKind:     wsv1.MessageKind_PUSH,
		Action:          wsv1.Action_PLAYER_STATE_CHANGED,
		TargetPlayerId:  playerID,
		StateVersion:    &wsv1.StateVersion{OwnerEpoch: 1, PlayerSeq: playerSeq},
		ServerTimeMs:    time.Now().UnixMilli(),
		Payload: &wsv1.WsEnvelope_PlayerStateChangedPush{
			PlayerStateChangedPush: &wsv1.PlayerStateChangedPush{
				Reason: reasonv1.StateChangeReason_MATURED,
				Patch: &wsv1.PlayerStatePatch{
					PlotUpserts: []*wsv1.PlotView{
						{PlotId: 1, PlotState: plotv1.PlotState_MATURE},
					},
				},
			},
		},
	}
}

func pingRequest(requestID string, pingID uint64) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_PING, RequestId: requestID,
		Payload: &wsv1.WsEnvelope_PingRequest{
			PingRequest: &wsv1.PingRequest{PingId: pingID, ClientSentAtMs: 123},
		},
	}
}

func writeEnvelope(t *testing.T, conn *websocket.Conn, message *wsv1.WsEnvelope) {
	t.Helper()
	body, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageBinary, body); err != nil {
		t.Fatalf("write envelope: %v", err)
	}
}

func readEnvelope(t *testing.T, conn *websocket.Conn) *wsv1.WsEnvelope {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	messageType, body, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read envelope: %v", err)
	}
	if messageType != websocket.MessageBinary {
		t.Fatalf("message type = %v", messageType)
	}
	return decodeEnvelope(t, body)
}

func decodeEnvelope(t *testing.T, body []byte) *wsv1.WsEnvelope {
	t.Helper()
	message := &wsv1.WsEnvelope{}
	if err := proto.Unmarshal(body, message); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return message
}
