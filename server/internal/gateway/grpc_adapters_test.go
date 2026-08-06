package gateway

import (
	"context"
	"net"
	"testing"
	"time"

	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

var grpcAdapterTestKey = []byte("gateway-grpc-adapter-test-key-32-bytes")

type testGameCommandServer struct {
	rpcv1.UnimplementedGameCommandServiceServer
	request  *rpcv1.ExecutePlayerCommandRequest
	notOwner bool
}

func (s *testGameCommandServer) ExecutePlayerCommand(
	_ context.Context,
	request *rpcv1.ExecutePlayerCommandRequest,
) (*rpcv1.ExecutePlayerCommandResponse, error) {
	s.request = proto.Clone(request).(*rpcv1.ExecutePlayerCommandRequest)
	if s.notOwner {
		return nil, status.Error(codes.FailedPrecondition, "not owner")
	}
	return &rpcv1.ExecutePlayerCommandResponse{
		Envelope: proto.Clone(request.Envelope).(*wsv1.WsEnvelope),
	}, nil
}

func TestGRPCZoneCommanderSignsRouteAndReturnsEnvelope(t *testing.T) {
	service := &testGameCommandServer{}
	commander := newTestGRPCZoneCommander(t, service)
	envelope := &wsv1.WsEnvelope{
		ProtocolVersion: 1,
		MessageKind:     wsv1.MessageKind_REQUEST,
		Action:          wsv1.Action_GET_PLAYER_SNAPSHOT,
		RequestId:       "request-id",
		TargetPlayerId:  42,
	}
	body, err := proto.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := commander.Command(ctx, Route{
		ShardID: 17, OwnerZoneID: "zone-a", OwnerEpoch: 3, RouteVersion: 9,
		OwnerEndpoint: "http://127.0.0.1:8082",
	}, 42, body)
	if err != nil {
		t.Fatal(err)
	}
	decoded := &wsv1.WsEnvelope{}
	if proto.Unmarshal(response, decoded) != nil || !proto.Equal(decoded, envelope) {
		t.Fatalf("response = %+v", decoded)
	}
	if service.request.GetCallerPlayerId() != 42 ||
		service.request.GetGateId() != DefaultGatewayID ||
		service.request.GetRoute().GetLogicalShardId() != 17 ||
		service.request.GetRoute().GetOwnerZoneId() != "zone-a" ||
		service.request.GetRoute().GetOwnerEpoch() != 3 ||
		service.request.GetRoute().GetRouteVersion() != 9 {
		t.Fatalf("gRPC request = %+v", service.request)
	}
}

func TestGRPCZoneCommanderMapsFailedPreconditionToNotOwner(t *testing.T) {
	commander := newTestGRPCZoneCommander(t, &testGameCommandServer{notOwner: true})
	body, err := proto.Marshal(&wsv1.WsEnvelope{TargetPlayerId: 42})
	if err != nil {
		t.Fatal(err)
	}
	_, err = commander.Command(context.Background(), Route{
		ShardID: 17, OwnerZoneID: "zone-a", OwnerEpoch: 3, RouteVersion: 9,
		OwnerEndpoint: "http://127.0.0.1:8082",
	}, 42, body)
	if err != ErrNotOwner {
		t.Fatalf("error = %v, want ErrNotOwner", err)
	}
}

func TestGRPCPushServerValidatesRecipientAndPublishes(t *testing.T) {
	pushServer, err := NewGRPCPushServer(
		&Handler{pushHub: newPushHub()}, DefaultGatewayID,
	)
	if err != nil {
		t.Fatal(err)
	}
	envelope := &wsv1.WsEnvelope{
		ProtocolVersion: 1,
		MessageKind:     wsv1.MessageKind_PUSH,
		Action:          wsv1.Action_PLAYER_STATE_CHANGED,
		TargetPlayerId:  42,
		StateVersion:    &wsv1.StateVersion{OwnerEpoch: 1, PlayerSeq: 2},
		ServerTimeMs:    time.Now().UnixMilli(),
		Payload: &wsv1.WsEnvelope_PlayerStateChangedPush{
			PlayerStateChangedPush: &wsv1.PlayerStateChangedPush{
				Reason: 1,
				Patch:  &wsv1.PlayerStatePatch{},
			},
		},
	}
	_, err = pushServer.PublishPlayerStateChanged(
		context.Background(),
		&rpcv1.PublishPlayerStateChangedRequest{
			GateId: DefaultGatewayID, RecipientPlayerId: 42, Envelope: envelope,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pushServer.PublishPlayerStateChanged(
		context.Background(),
		&rpcv1.PublishPlayerStateChangedRequest{
			GateId: DefaultGatewayID, RecipientPlayerId: 43, Envelope: envelope,
		},
	)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("mismatched recipient status = %v", status.Code(err))
	}
}

func TestGRPCPushServerFansFarmViewPatchOutToEveryRecipient(t *testing.T) {
	hub := newPushHub()
	pushServer, err := NewGRPCPushServer(&Handler{pushHub: hub}, DefaultGatewayID)
	if err != nil {
		t.Fatal(err)
	}
	patch := &wsv1.FarmViewPatch{
		OwnerPlayerId: 7,
		Version: &wsv1.FarmViewVersion{
			FarmViewEpoch: []byte("0123456789abcdef"), FarmViewSeq: 1,
		},
		PlotUpserts: []*wsv1.PublicPlotView{{PlotId: 1, PlotState: 1}},
	}
	_, err = pushServer.PublishFarmViewPatch(
		context.Background(),
		&rpcv1.PublishFarmViewPatchRequest{
			GateId: DefaultGatewayID, RecipientPlayerIds: []uint64{7, 101}, Patch: patch,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestGRPCPushServerRejectsInvalidFarmViewPatchPushes(t *testing.T) {
	pushServer, err := NewGRPCPushServer(&Handler{pushHub: newPushHub()}, DefaultGatewayID)
	if err != nil {
		t.Fatal(err)
	}
	validPatch := &wsv1.FarmViewPatch{
		OwnerPlayerId: 7,
		Version: &wsv1.FarmViewVersion{
			FarmViewEpoch: []byte("0123456789abcdef"), FarmViewSeq: 1,
		},
	}
	cases := []*rpcv1.PublishFarmViewPatchRequest{
		{GateId: "wrong-gate", RecipientPlayerIds: []uint64{7}, Patch: validPatch},
		{GateId: DefaultGatewayID, RecipientPlayerIds: nil, Patch: validPatch},
		{GateId: DefaultGatewayID, RecipientPlayerIds: []uint64{0}, Patch: validPatch},
		{GateId: DefaultGatewayID, RecipientPlayerIds: []uint64{7}, Patch: nil},
		{GateId: DefaultGatewayID, RecipientPlayerIds: []uint64{7}, Patch: &wsv1.FarmViewPatch{
			Version: validPatch.Version,
		}},
		{GateId: DefaultGatewayID, RecipientPlayerIds: []uint64{7}, Patch: &wsv1.FarmViewPatch{
			OwnerPlayerId: 7,
		}},
		{GateId: DefaultGatewayID, RecipientPlayerIds: []uint64{7}, Patch: &wsv1.FarmViewPatch{
			OwnerPlayerId: 7, Version: &wsv1.FarmViewVersion{FarmViewEpoch: []byte("0123456789abcdef")},
		}},
	}
	for index, request := range cases {
		if _, err := pushServer.PublishFarmViewPatch(context.Background(), request); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("case %d: status = %v, want InvalidArgument", index, status.Code(err))
		}
	}
}

func newTestGRPCZoneCommander(
	t *testing.T,
	service *testGameCommandServer,
) *GRPCZoneCommander {
	t.Helper()
	interceptor, err := rpcauth.NewServerUnaryInterceptor(rpcauth.ServerConfig{
		Key: grpcAdapterTestKey,
		AllowedCallers: map[string][]string{
			rpcv1.GameCommandService_ExecutePlayerCommand_FullMethodName: {"gate"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer(grpc.UnaryInterceptor(interceptor))
	rpcv1.RegisterGameCommandServiceServer(server, service)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)
	t.Cleanup(func() { _ = listener.Close() })

	commander, err := NewGRPCZoneCommander(grpcAdapterTestKey, DefaultGatewayID)
	if err != nil {
		t.Fatal(err)
	}
	commander.dialOpts = append(commander.dialOpts, grpc.WithContextDialer(
		func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		},
	))
	t.Cleanup(func() { _ = commander.Close() })
	return commander
}
