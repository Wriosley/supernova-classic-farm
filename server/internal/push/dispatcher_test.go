package push_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/connection"
	"github.com/Wriosley/supernova-classic-farm/server/internal/push"
)

type recordingGateClient struct {
	farmCalls atomic.Int32
	redCalls  atomic.Int32
}

func (c *recordingGateClient) PublishFarmViewPatch(
	_ context.Context, _ string, _ []uint64, _ *wsv1.FarmViewPatch,
) error {
	c.farmCalls.Add(1)
	return nil
}

func (c *recordingGateClient) PublishRedDotChanged(
	_ context.Context, _ string, _ []uint64, _ *push.RedDotChanged,
) error {
	c.redCalls.Add(1)
	return nil
}

func TestDispatcherGroupsByConnectionRegistry(t *testing.T) {
	reg := connection.NewRegistry()
	now := time.Unix(1000, 0)
	_ = reg.Register(connection.PlayerConnection{
		PlayerID: 1, GateID: "gate-a", ConnectionID: "c1", ExpiresAt: now.Add(connection.LeaseTTL),
	})
	_ = reg.Register(connection.PlayerConnection{
		PlayerID: 2, GateID: "gate-a", ConnectionID: "c2", ExpiresAt: now.Add(connection.LeaseTTL),
	})
	client := &recordingGateClient{}
	dispatcher, err := push.NewDispatcher(
		push.StaticGateResolver{GateID: "gate-a", Client: client},
		reg,
		slog.Default(),
		push.Config{QueueSize: 8, Workers: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer dispatcher.Close()
	dispatcher.Enqueue(push.Event{
		NotificationID:     "n1",
		RecipientPlayerIDs: []uint64{1, 2},
		RedDot: &push.RedDotChanged{
			NotificationID: "n1",
			Category:       wsv1.RedDotCategory_RED_DOT_CATEGORY_MAIL,
			Operation:      wsv1.RedDotOperation_RED_DOT_OPERATION_SET,
		},
	})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		published, failed, dropped := dispatcher.Stats()
		if published == 1 && failed == 0 && dropped == 0 && client.redCalls.Load() == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	published, failed, dropped := dispatcher.Stats()
	t.Fatalf("published=%d failed=%d dropped=%d redCalls=%d", published, failed, dropped, client.redCalls.Load())
}

func TestStaticGateResolverRejectsUnknownGate(t *testing.T) {
	resolver := push.StaticGateResolver{GateID: "gate-a", Client: &recordingGateClient{}}
	if _, err := resolver.Resolve("gate-b"); err == nil {
		t.Fatal("expected unknown gate error")
	}
}
