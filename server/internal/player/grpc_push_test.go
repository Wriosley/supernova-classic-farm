package player

import (
	"context"
	"net"
	"testing"
	"time"

	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

var grpcPushTestKey = []byte("player-grpc-push-test-key-32-bytes-long")

type recordingGatePushServer struct {
	rpcv1.UnimplementedGatePushServiceServer
	request chan *rpcv1.PublishPlayerStateChangedRequest
}

func (s *recordingGatePushServer) PublishPlayerStateChanged(
	_ context.Context,
	request *rpcv1.PublishPlayerStateChangedRequest,
) (*rpcv1.PublishPlayerStateChangedResponse, error) {
	s.request <- proto.Clone(request).(*rpcv1.PublishPlayerStateChangedRequest)
	return &rpcv1.PublishPlayerStateChangedResponse{}, nil
}

func TestGRPCPushForwarderSignsAndPublishesEnvelope(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	interceptor, err := rpcauth.NewServerUnaryInterceptor(rpcauth.ServerConfig{
		Key: grpcPushTestKey,
		AllowedCallers: map[string][]string{
			rpcv1.GatePushService_PublishPlayerStateChanged_FullMethodName: {"zone-a"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := &recordingGatePushServer{
		request: make(chan *rpcv1.PublishPlayerStateChangedRequest, 1),
	}
	server := grpc.NewServer(grpc.UnaryInterceptor(interceptor))
	rpcv1.RegisterGatePushServiceServer(server, recorder)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)

	forwarder, err := NewGRPCPushForwarder(
		grpcPushTestKey, "zone-a", "http://"+listener.Addr().String(), "local-gateway",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = forwarder.Close() })
	envelope := MaturityEvent{
		PlayerID: 42, OwnerEpoch: 3, PlayerSeq: 7,
		ServerTimeMS: time.Now().UnixMilli(),
	}.Envelope()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := forwarder.Forward(ctx, envelope); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-recorder.request:
		if request.GetGateId() != "local-gateway" ||
			request.GetRecipientPlayerId() != 42 ||
			!proto.Equal(request.GetEnvelope(), envelope) {
			t.Fatalf("push request = %+v", request)
		}
	case <-ctx.Done():
		t.Fatal("Gate did not receive gRPC push")
	}
}
