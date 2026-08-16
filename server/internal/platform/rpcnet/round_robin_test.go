package rpcnet

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/resolver/manual"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type countingHealthServer struct {
	healthv1.UnimplementedHealthServer
	calls       atomic.Int64
	unavailable atomic.Bool
}

func (s *countingHealthServer) Check(context.Context, *healthv1.HealthCheckRequest) (*healthv1.HealthCheckResponse, error) {
	s.calls.Add(1)
	if s.unavailable.Load() {
		return nil, status.Error(codes.Unavailable, "injected backend failure")
	}
	return &healthv1.HealthCheckResponse{Status: healthv1.HealthCheckResponse_SERVING}, nil
}

func TestRoundRobinDistributesAndRemovesBackend(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	listeners := map[string]*bufconn.Listener{}
	servers := map[string]*grpc.Server{}
	counters := map[string]*countingHealthServer{}
	for _, address := range []string{"backend-one", "backend-two"} {
		listener := bufconn.Listen(1 << 20)
		serverAuth, err := rpcauth.NewServerUnaryInterceptor(rpcauth.ServerConfig{
			Key: key,
			AllowedCallers: map[string][]string{
				healthv1.Health_Check_FullMethodName: {"test-client"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		server := grpc.NewServer(grpc.UnaryInterceptor(serverAuth))
		counter := &countingHealthServer{}
		healthv1.RegisterHealthServer(server, counter)
		listeners[address], servers[address], counters[address] = listener, server, counter
		go func() { _ = server.Serve(listener) }()
		defer server.Stop()
	}

	builder := manual.NewBuilderWithScheme("classic-farm-test")
	builder.InitialState(resolver.State{Addresses: []resolver.Address{{Addr: "backend-one"}, {Addr: "backend-two"}}})
	balancing, err := RoundRobinDialOption(healthv1.Health_Check_FullMethodName)
	if err != nil {
		t.Fatal(err)
	}
	clientAuth, err := rpcauth.NewClientUnaryInterceptor(rpcauth.ClientConfig{Service: "test-client", Key: key})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := grpc.NewClient(
		"classic-farm-test:///health",
		grpc.WithResolvers(builder),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(clientAuth),
		grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
			return listeners[address].DialContext(ctx)
		}),
		balancing,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := healthv1.NewHealthClient(conn)
	for range 12 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err = client.Check(ctx, &healthv1.HealthCheckRequest{}, grpc.WaitForReady(true))
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
	if counters["backend-one"].calls.Load() == 0 || counters["backend-two"].calls.Load() == 0 {
		t.Fatalf("round_robin calls: one=%d two=%d", counters["backend-one"].calls.Load(), counters["backend-two"].calls.Load())
	}

	// A method explicitly declared safe retries one UNAVAILABLE. Keeping the
	// failing backend in the resolver proves the retry can select the other
	// READY SubConn instead of requiring the application to redial.
	counters["backend-two"].unavailable.Store(true)
	for range 6 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err = client.Check(ctx, &healthv1.HealthCheckRequest{}, grpc.WaitForReady(true))
		cancel()
		if err != nil {
			t.Fatalf("safe retry did not fail over: %v", err)
		}
	}

	before := counters["backend-two"].calls.Load()
	builder.UpdateState(resolver.State{Addresses: []resolver.Address{{Addr: "backend-one"}}})
	for range 4 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err = client.Check(ctx, &healthv1.HealthCheckRequest{}, grpc.WaitForReady(true))
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
	if counters["backend-two"].calls.Load() != before {
		t.Fatalf("removed backend received calls: before=%d after=%d", before, counters["backend-two"].calls.Load())
	}
}
