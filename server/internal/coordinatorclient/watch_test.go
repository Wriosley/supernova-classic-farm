package coordinatorclient

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	coordinatorv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/coordinator"
	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/publisher"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/rpcauth"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

func TestAuthServiceUsesStableZoneRole(t *testing.T) {
	got := authService(Config{
		SubscriberID: "d859cea1-ac5b-5524-bffa-4e542301cd95",
		Kind:         coordinatorv1.SubscriberKind_SUBSCRIBER_KIND_ZONE,
	})
	if got != rpcauth.ZoneService {
		t.Fatalf("authService() = %q, want %q", got, rpcauth.ZoneService)
	}
}

func TestClientWarmsAndAppliesPublishedBatch(t *testing.T) {
	now := time.Date(2026, 8, 13, 15, 0, 0, 0, time.UTC)
	routes, _ := routing.NewLocalMap(now, time.Minute)
	source := &mutableSource{snapshot: routes.Snapshot()}
	p, _ := publisher.New(source, publisher.Config{QueueCapacity: 4, PingInterval: time.Hour, AckTimeout: 2 * time.Hour})
	defer p.Close()
	service, _ := publisher.NewGRPCServer(source, p, publisher.GRPCConfig{PingInterval: time.Hour, AckTimeout: 2 * time.Hour})
	listener := bufconn.Listen(1 << 24)
	server := grpc.NewServer(grpc.MaxSendMsgSize(8 << 20))
	coordinatorv1.RegisterCoordinatorServiceServer(server, service)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	client, err := New(Config{Endpoint: "http://127.0.0.1:18083", SubscriberID: "gate-1", Kind: coordinatorv1.SubscriberKind_SUBSCRIBER_KIND_GATE, HMACKey: []byte("coordinator-client-test-key-32-bytes-minimum"), Now: func() time.Time { return now }, Dialer: func(context.Context, string) (net.Conn, error) { return listener.Dial() }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if client.Snapshot().MapVersion != 1 {
		t.Fatal("client did not warm")
	}
	previous := source.Snapshot()
	current := previous
	current.Entries = append([]routing.RouteEntry(nil), previous.Entries...)
	current.MapVersion = 2
	current.Entries[42].RouteVersion++
	current.Entries[42].UpdatedAt = now.Add(time.Second)
	source.set(current)
	if err := p.PublishRoutes(previous, current); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for client.Snapshot().MapVersion != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	entry, err := client.ResolveShard(42)
	if err != nil || entry.RouteVersion != 2 {
		t.Fatalf("entry=%+v err=%v map=%d", entry, err, client.Snapshot().MapVersion)
	}
}

type mutableSource struct {
	mu       sync.RWMutex
	snapshot routing.Snapshot
}

func (s *mutableSource) Snapshot() routing.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copy := s.snapshot
	copy.Entries = append([]routing.RouteEntry(nil), s.snapshot.Entries...)
	return copy
}

func (s *mutableSource) set(snapshot routing.Snapshot) {
	s.mu.Lock()
	s.snapshot = snapshot
	s.mu.Unlock()
}
