package gateway

import (
	"context"
	"encoding/json"
	"errors"
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

type fakeVisitorClient struct {
	enter, heartbeat, exit, steal, applyPest, catchPest, helpClean func(context.Context, Route, uint64, *wsv1.WsEnvelope) ([]byte, error)
}

func (f *fakeVisitorClient) Enter(ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope) ([]byte, error) {
	return f.enter(ctx, route, caller, request)
}

func (f *fakeVisitorClient) Heartbeat(ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope) ([]byte, error) {
	return f.heartbeat(ctx, route, caller, request)
}

func (f *fakeVisitorClient) Exit(ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope) ([]byte, error) {
	return f.exit(ctx, route, caller, request)
}

func (f *fakeVisitorClient) Steal(ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope) ([]byte, error) {
	if f.steal == nil {
		return nil, errors.New("fakeVisitorClient.steal not configured")
	}
	return f.steal(ctx, route, caller, request)
}

func (f *fakeVisitorClient) ApplyPest(ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope) ([]byte, error) {
	if f.applyPest == nil {
		return nil, errors.New("fakeVisitorClient.applyPest not configured")
	}
	return f.applyPest(ctx, route, caller, request)
}

func (f *fakeVisitorClient) CatchPest(ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope) ([]byte, error) {
	if f.catchPest == nil {
		return nil, errors.New("fakeVisitorClient.catchPest not configured")
	}
	return f.catchPest(ctx, route, caller, request)
}

func (f *fakeVisitorClient) HelpClean(ctx context.Context, route Route, caller uint64, request *wsv1.WsEnvelope) ([]byte, error) {
	if f.helpClean == nil {
		return nil, errors.New("fakeVisitorClient.helpClean not configured")
	}
	return f.helpClean(ctx, route, caller, request)
}

type fakeFriendClient struct {
	createCode, redeemCode, list func(context.Context, uint64, *wsv1.WsEnvelope) ([]byte, error)
}

func (f *fakeFriendClient) CreateCode(ctx context.Context, caller uint64, request *wsv1.WsEnvelope) ([]byte, error) {
	return f.createCode(ctx, caller, request)
}

func (f *fakeFriendClient) RedeemCode(ctx context.Context, caller uint64, request *wsv1.WsEnvelope) ([]byte, error) {
	return f.redeemCode(ctx, caller, request)
}

func (f *fakeFriendClient) List(ctx context.Context, caller uint64, request *wsv1.WsEnvelope) ([]byte, error) {
	return f.list(ctx, caller, request)
}
func (f *fakeFriendClient) GetOfflineVisitors(context.Context, uint64, *wsv1.WsEnvelope) ([]byte, error) {
	return nil, nil
}
func (f *fakeFriendClient) AckOfflineVisitors(context.Context, uint64, *wsv1.WsEnvelope) ([]byte, error) {
	return nil, nil
}

func (f *fakeFriendClient) CheckMutualFriend(context.Context, uint64, uint64) (bool, error) {
	return true, nil
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

func TestCustomWebSocketOriginPattern(t *testing.T) {
	handler, err := NewHandler(Config{
		Tickets: ticketFunc(func(context.Context, string) (uint64, error) { return 42, nil }),
		Routes: routeFunc(func(context.Context, uint32) (Route, error) {
			return Route{}, errors.New("unused")
		}),
		Zone: zoneFunc(func(context.Context, Route, uint64, []byte) ([]byte, error) {
			return nil, errors.New("unused")
		}),
		OriginPatterns: []string{"192.168.255.10:1616"},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"http://192.168.255.10:1616"}},
	})
	if err != nil {
		t.Fatalf("dial Gate with configured origin: %v", err)
	}
	conn.CloseNow()
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
	return authenticatedConnectionWithConfig(t, Config{Zone: zone, Routes: routes})
}

func authenticatedConnectionWithConfig(
	t *testing.T,
	cfg Config,
) (*websocket.Conn, *Handler, func()) {
	t.Helper()
	if cfg.Routes == nil {
		cfg.Routes = routeFunc(func(_ context.Context, shard uint32) (Route, error) {
			return Route{ShardID: shard, OwnerEpoch: 1, OwnerEndpoint: "http://127.0.0.1:8082"}, nil
		})
	}
	if cfg.Zone == nil {
		cfg.Zone = zoneFunc(func(context.Context, Route, uint64, []byte) ([]byte, error) {
			return nil, errors.New("unused")
		})
	}
	cfg.Tickets = ticketFunc(func(_ context.Context, ticket string) (uint64, error) {
		if ticket != "valid-ticket" {
			return 0, errors.New("invalid ticket")
		}
		return 42, nil
	})
	handler, err := NewHandler(cfg)
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

func stealFriendCropRequest(requestID string, playerID, ownerID uint64, visitID []byte, plotID uint32) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_STEAL_FRIEND_CROP, RequestId: requestID,
		TargetPlayerId: playerID,
		Payload: &wsv1.WsEnvelope_StealFriendCropRequest{
			StealFriendCropRequest: &wsv1.StealFriendCropRequest{
				OwnerPlayerId: ownerID, VisitId: visitID, PlotId: plotID,
				ExpectedCropItemId: 4001,
				FarmViewEpoch:      make([]byte, 16),
				FarmViewSeq:        1,
			},
		},
	}
}

func createFriendCodeRequest(requestID string, playerID uint64) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_CREATE_FRIEND_CODE, RequestId: requestID,
		TargetPlayerId: playerID,
		Payload: &wsv1.WsEnvelope_CreateFriendCodeRequest{
			CreateFriendCodeRequest: &wsv1.CreateFriendCodeRequest{},
		},
	}
}

func enterFriendFarmRequest(requestID string, playerID, ownerID uint64) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_ENTER_FRIEND_FARM, RequestId: requestID,
		TargetPlayerId: playerID,
		Payload: &wsv1.WsEnvelope_EnterFriendFarmRequest{
			EnterFriendFarmRequest: &wsv1.EnterFriendFarmRequest{OwnerPlayerId: ownerID},
		},
	}
}

// genericDomainResponse builds a minimal, correlated RESPONSE envelope for
// friend/visit actions. validateZoneResponse only checks protocol version,
// message kind, Action, and RequestId, not the payload oneof's shape, so a
// single payload type is enough to exercise every action under test.
func genericDomainResponse(request *wsv1.WsEnvelope) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId,
		TargetPlayerId: request.TargetPlayerId, ServerTimeMs: time.Now().UnixMilli(),
		Payload: &wsv1.WsEnvelope_ListFriendsResponse{
			ListFriendsResponse: &wsv1.ListFriendsResponse{},
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

func TestValidateRequestTupleAcceptsFriendAndVisitActions(t *testing.T) {
	tests := []struct {
		name    string
		request *wsv1.WsEnvelope
	}{
		{"create friend code", createFriendCodeRequest("req-1", 42)},
		{"redeem friend code", &wsv1.WsEnvelope{
			MessageKind: wsv1.MessageKind_REQUEST,
			Action:      wsv1.Action_REDEEM_FRIEND_CODE, RequestId: "req-1", TargetPlayerId: 42,
			Payload: &wsv1.WsEnvelope_RedeemFriendCodeRequest{RedeemFriendCodeRequest: &wsv1.RedeemFriendCodeRequest{Code: "abc"}},
		}},
		{"list friends", &wsv1.WsEnvelope{
			MessageKind: wsv1.MessageKind_REQUEST,
			Action:      wsv1.Action_LIST_FRIENDS, RequestId: "req-1", TargetPlayerId: 42,
			Payload: &wsv1.WsEnvelope_ListFriendsRequest{ListFriendsRequest: &wsv1.ListFriendsRequest{}},
		}},
		{"enter friend farm", enterFriendFarmRequest("req-1", 42, 7)},
		{"farm heartbeat", &wsv1.WsEnvelope{
			MessageKind: wsv1.MessageKind_REQUEST,
			Action:      wsv1.Action_FARM_HEARTBEAT, RequestId: "req-1", TargetPlayerId: 42,
			Payload: &wsv1.WsEnvelope_FarmHeartbeatRequest{FarmHeartbeatRequest: &wsv1.FarmHeartbeatRequest{OwnerPlayerId: 7, VisitId: make([]byte, 16)}},
		}},
		{"exit friend farm", &wsv1.WsEnvelope{
			MessageKind: wsv1.MessageKind_REQUEST,
			Action:      wsv1.Action_EXIT_FRIEND_FARM, RequestId: "req-1", TargetPlayerId: 42,
			Payload: &wsv1.WsEnvelope_ExitFriendFarmRequest{ExitFriendFarmRequest: &wsv1.ExitFriendFarmRequest{OwnerPlayerId: 7, VisitId: make([]byte, 16)}},
		}},
		{"steal friend crop", stealFriendCropRequest("req-1", 42, 7, make([]byte, 16), 3)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateRequestTuple(test.request); err != nil {
				t.Fatalf("validateRequestTuple(%s) = %v, want nil", test.name, err)
			}
			missingTarget := proto.Clone(test.request).(*wsv1.WsEnvelope)
			missingTarget.TargetPlayerId = 0
			if err := validateRequestTuple(missingTarget); err == nil {
				t.Fatalf("validateRequestTuple(%s without target_player_id) = nil, want error", test.name)
			}
			missingPayload := proto.Clone(test.request).(*wsv1.WsEnvelope)
			missingPayload.Payload = nil
			if err := validateRequestTuple(missingPayload); err == nil {
				t.Fatalf("validateRequestTuple(%s without payload) = nil, want error", test.name)
			}
		})
	}
}

func TestValidateRequestTupleAcceptsCheckMailboxIndicator(t *testing.T) {
	request := &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_CHECK_MAILBOX_INDICATOR, RequestId: "req-1", TargetPlayerId: 42,
		Payload: &wsv1.WsEnvelope_CheckMailboxIndicatorRequest{
			CheckMailboxIndicatorRequest: &wsv1.CheckMailboxIndicatorRequest{},
		},
	}
	if err := validateRequestTuple(request); err != nil {
		t.Fatalf("validateRequestTuple(check mailbox indicator) = %v, want nil", err)
	}
	missingPayload := proto.Clone(request).(*wsv1.WsEnvelope)
	missingPayload.Payload = nil
	if err := validateRequestTuple(missingPayload); err == nil {
		t.Fatal("validateRequestTuple(check mailbox indicator without payload) = nil, want error")
	}
}

func TestValidateRequestTupleRejectsInvalidStealFriendCrop(t *testing.T) {
	base := stealFriendCropRequest("req-1", 42, 7, make([]byte, 16), 3)
	tests := []struct {
		name   string
		mutate func(*wsv1.WsEnvelope)
	}{
		{"zero owner_player_id", func(e *wsv1.WsEnvelope) { e.GetStealFriendCropRequest().OwnerPlayerId = 0 }},
		{"short visit_id", func(e *wsv1.WsEnvelope) { e.GetStealFriendCropRequest().VisitId = []byte{1, 2, 3} }},
		{"nil visit_id", func(e *wsv1.WsEnvelope) { e.GetStealFriendCropRequest().VisitId = nil }},
		{"zero plot_id", func(e *wsv1.WsEnvelope) { e.GetStealFriendCropRequest().PlotId = 0 }},
		{"zero crop", func(e *wsv1.WsEnvelope) { e.GetStealFriendCropRequest().ExpectedCropItemId = 0 }},
		{"short epoch", func(e *wsv1.WsEnvelope) { e.GetStealFriendCropRequest().FarmViewEpoch = []byte{1} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := proto.Clone(base).(*wsv1.WsEnvelope)
			test.mutate(request)
			if err := validateRequestTuple(request); err == nil {
				t.Fatalf("validateRequestTuple(%s) = nil, want error", test.name)
			}
		})
	}
}

func TestFriendAndVisitActionsRejectedWhenClientsUnconfigured(t *testing.T) {
	conn, _, closeServer := authenticatedConnectionWithConfig(t, Config{})
	defer closeServer()
	defer conn.CloseNow()

	writeEnvelope(t, conn, createFriendCodeRequest("friend-unconfigured", 42))
	if response := readEnvelope(t, conn); response.GetError().GetCode() != wsv1.ErrorCode_SERVICE_UNAVAILABLE {
		t.Fatalf("CREATE_FRIEND_CODE without Friends client response = %+v", response)
	}

	writeEnvelope(t, conn, enterFriendFarmRequest("visit-unconfigured", 42, 7))
	if response := readEnvelope(t, conn); response.GetError().GetCode() != wsv1.ErrorCode_SERVICE_UNAVAILABLE {
		t.Fatalf("ENTER_FRIEND_FARM without Visitor client response = %+v", response)
	}
}

func TestHandleGameRoutesFriendActionToFriendsClient(t *testing.T) {
	var gotCaller uint64
	friends := &fakeFriendClient{
		createCode: func(_ context.Context, caller uint64, request *wsv1.WsEnvelope) ([]byte, error) {
			gotCaller = caller
			return proto.Marshal(genericDomainResponse(request))
		},
	}
	conn, _, closeServer := authenticatedConnectionWithConfig(t, Config{Friends: friends})
	defer closeServer()
	defer conn.CloseNow()

	writeEnvelope(t, conn, createFriendCodeRequest("create-code-1", 42))
	response := readEnvelope(t, conn)
	if response.Action != wsv1.Action_CREATE_FRIEND_CODE || response.RequestId != "create-code-1" {
		t.Fatalf("CREATE_FRIEND_CODE response = %+v", response)
	}
	if gotCaller != 42 {
		t.Fatalf("caller passed to Friends.CreateCode = %d, want 42", gotCaller)
	}
}

func TestHandleGameRoutesVisitActionToVisitorClientWithNotOwnerRetry(t *testing.T) {
	var routeCalls atomic.Int32
	routes := routeFunc(func(_ context.Context, shard uint32) (Route, error) {
		call := routeCalls.Add(1)
		return Route{ShardID: shard, OwnerEpoch: uint64(call), OwnerEndpoint: "http://127.0.0.1:8082"}, nil
	})
	var enterCalls atomic.Int32
	visitor := &fakeVisitorClient{
		enter: func(_ context.Context, _ Route, _ uint64, request *wsv1.WsEnvelope) ([]byte, error) {
			if enterCalls.Add(1) == 1 {
				return nil, ErrNotOwner
			}
			return proto.Marshal(genericDomainResponse(request))
		},
	}
	conn, _, closeServer := authenticatedConnectionWithConfig(t, Config{Visitor: visitor, Routes: routes})
	defer closeServer()
	defer conn.CloseNow()

	writeEnvelope(t, conn, enterFriendFarmRequest("enter-1", 42, 7))
	response := readEnvelope(t, conn)
	if response.Action != wsv1.Action_ENTER_FRIEND_FARM || response.RequestId != "enter-1" || response.Error != nil {
		t.Fatalf("ENTER_FRIEND_FARM response = %+v", response)
	}
	if routeCalls.Load() != 2 || enterCalls.Load() != 2 {
		t.Fatalf("route/visitor calls = %d/%d, want 2/2", routeCalls.Load(), enterCalls.Load())
	}
}

func TestHandleGameRoutesStealFriendCropToVisitorClient(t *testing.T) {
	routes := routeFunc(func(_ context.Context, shard uint32) (Route, error) {
		return Route{ShardID: shard, OwnerEpoch: 1, OwnerEndpoint: "http://127.0.0.1:8082"}, nil
	})
	var gotCaller uint64
	var stealCalls atomic.Int32
	visitor := &fakeVisitorClient{
		steal: func(_ context.Context, _ Route, caller uint64, request *wsv1.WsEnvelope) ([]byte, error) {
			stealCalls.Add(1)
			gotCaller = caller
			return proto.Marshal(genericDomainResponse(request))
		},
	}
	conn, _, closeServer := authenticatedConnectionWithConfig(t, Config{Visitor: visitor, Routes: routes})
	defer closeServer()
	defer conn.CloseNow()

	writeEnvelope(t, conn, stealFriendCropRequest("steal-1", 42, 7, make([]byte, 16), 3))
	response := readEnvelope(t, conn)
	if response.Action != wsv1.Action_STEAL_FRIEND_CROP || response.RequestId != "steal-1" || response.Error != nil {
		t.Fatalf("STEAL_FRIEND_CROP response = %+v", response)
	}
	if stealCalls.Load() != 1 || gotCaller != 42 {
		t.Fatalf("visitor.Steal calls=%d caller=%d, want 1/42", stealCalls.Load(), gotCaller)
	}
}
