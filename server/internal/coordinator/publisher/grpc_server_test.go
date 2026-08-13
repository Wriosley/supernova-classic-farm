package publisher

import (
	"context"
	"net"
	"testing"
	"time"

	coordinatorv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/coordinator"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestGRPCServerUnaryAndWatchInitialSnapshot(t *testing.T) {
	source := &snapshotSource{snapshot: testSnapshot(t)}
	p, _ := New(source, Config{QueueCapacity: 4, PingInterval: time.Hour, AckTimeout: 2 * time.Hour})
	defer p.Close()
	service, err := NewGRPCServer(source, p, GRPCConfig{PingInterval: time.Hour, AckTimeout: 2 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	client, cleanup := grpcTestClient(t, service)
	defer cleanup()
	snapshot, err := client.GetRouteSnapshot(context.Background(), &coordinatorv1.GetRouteSnapshotRequest{})
	if err != nil || len(snapshot.GetSnapshot().Entries) != 4096 {
		t.Fatalf("snapshot=%v err=%v", snapshot, err)
	}
	shard, err := client.GetShardRoute(context.Background(), &coordinatorv1.GetShardRouteRequest{ShardId: 42})
	if err != nil || shard.Route.ShardId != 42 || shard.MapVersion != source.snapshot.MapVersion {
		t.Fatalf("shard=%v err=%v", shard, err)
	}
	if _, err := client.GetShardRoute(context.Background(), &coordinatorv1.GetShardRouteRequest{ShardId: 4096}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid shard code=%v", status.Code(err))
	}
	watch, err := client.WatchRoutes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := watch.Send(&coordinatorv1.WatchRoutesRequest{Payload: &coordinatorv1.WatchRoutesRequest_Subscribe{Subscribe: &coordinatorv1.Subscribe{SubscriberId: "gate-1", Kind: coordinatorv1.SubscriberKind_SUBSCRIBER_KIND_GATE}}}); err != nil {
		t.Fatal(err)
	}
	message, err := watch.Recv()
	if err != nil || message.GetSnapshot() == nil {
		t.Fatalf("initial=%v err=%v", message, err)
	}
}

func TestGRPCServerWatchRejectsMessageBeforeSubscribe(t *testing.T) {
	source := &snapshotSource{snapshot: testSnapshot(t)}
	p, _ := New(source, Config{})
	defer p.Close()
	service, _ := NewGRPCServer(source, p, GRPCConfig{})
	client, cleanup := grpcTestClient(t, service)
	defer cleanup()
	watch, _ := client.WatchRoutes(context.Background())
	_ = watch.Send(&coordinatorv1.WatchRoutesRequest{Payload: &coordinatorv1.WatchRoutesRequest_Ack{Ack: &coordinatorv1.RouteAck{MapVersion: 1}}})
	_, err := watch.Recv()
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("code=%v err=%v", status.Code(err), err)
	}
}

func grpcTestClient(t *testing.T, service coordinatorv1.CoordinatorServiceServer) (coordinatorv1.CoordinatorServiceClient, func()) {
	t.Helper()
	listener := bufconn.Listen(1 << 24)
	server := grpc.NewServer(grpc.MaxSendMsgSize(8<<20), grpc.MaxRecvMsgSize(8<<20))
	coordinatorv1.RegisterCoordinatorServiceServer(server, service)
	go func() { _ = server.Serve(listener) }()
	conn, err := grpc.DialContext(context.Background(), "bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithInsecure(), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(8<<20)))
	if err != nil {
		t.Fatal(err)
	}
	return coordinatorv1.NewCoordinatorServiceClient(conn), func() { _ = conn.Close(); server.Stop() }
}
