package info

import (
	"context"
	"errors"
	"testing"

	infov1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/info"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/gateway"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

type fakeRoutes struct {
	routes map[uint32]gateway.Route
	calls  int
}

func (f *fakeRoutes) Resolve(_ context.Context, shardID uint32) (gateway.Route, error) {
	f.calls++
	route, ok := f.routes[shardID]
	if !ok {
		return gateway.Route{}, errors.New("missing route")
	}
	return route, nil
}

func (f *fakeRoutes) InvalidateIfVersion(uint32, uint64) {}

type fakeZones struct {
	calls   []dispatchCall
	failOnce map[string]error
}

type dispatchCall struct {
	endpoint   string
	recipients []uint64
	redDot     *wsv1.RedDotChangedPush
}

func (z *fakeZones) DispatchRedDot(
	_ context.Context, route gateway.Route, recipients []uint64, redDot *wsv1.RedDotChangedPush,
) error {
	z.calls = append(z.calls, dispatchCall{
		endpoint: route.OwnerEndpoint, recipients: append([]uint64(nil), recipients...), redDot: redDot,
	})
	if err, ok := z.failOnce[route.OwnerEndpoint]; ok {
		delete(z.failOnce, route.OwnerEndpoint)
		return err
	}
	return nil
}

type fakeFriends struct {
	ids []uint64
}

func (f *fakeFriends) ListFriendPlayerIDs(context.Context, uint64) ([]uint64, error) {
	return append([]uint64(nil), f.ids...), nil
}

func TestSetMailRedDotRoutesOnce(t *testing.T) {
	playerID := uint64(7)
	shard := routing.ShardForPlayer(playerID)
	routes := &fakeRoutes{routes: map[uint32]gateway.Route{
		shard: {ShardID: shard, OwnerZoneID: "zone-a", OwnerEpoch: 1, RouteVersion: 1, OwnerEndpoint: "http://zone-a"},
	}}
	zones := &fakeZones{}
	svc, err := NewService(routes, zones, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := svc.SetMailRedDot(context.Background(), &infov1.SetMailRedDotRequest{
		PlayerId: playerID, NotificationId: "mail-1",
	})
	if err != nil || resp.GetError() != nil {
		t.Fatalf("resp=%v err=%v", resp, err)
	}
	if routes.calls != 1 {
		t.Fatalf("route calls=%d want 1 (no per-hit coordinator)", routes.calls)
	}
	if len(zones.calls) != 1 || len(zones.calls[0].recipients) != 1 || zones.calls[0].recipients[0] != playerID {
		t.Fatalf("calls=%+v", zones.calls)
	}
	if zones.calls[0].redDot.GetCategory() != wsv1.RedDotCategory_RED_DOT_CATEGORY_MAIL {
		t.Fatalf("category=%v", zones.calls[0].redDot.GetCategory())
	}
}

func TestNotifyOwnerPlotStealableFansOutFriends(t *testing.T) {
	owner := uint64(1)
	friendA := uint64(2)
	friendB := uint64(3)
	routes := &fakeRoutes{routes: map[uint32]gateway.Route{}}
	for _, id := range []uint64{friendA, friendB} {
		shard := routing.ShardForPlayer(id)
		routes.routes[shard] = gateway.Route{
			ShardID: shard, OwnerZoneID: "zone-a", OwnerEpoch: 1, RouteVersion: 1, OwnerEndpoint: "http://zone-a",
		}
	}
	zones := &fakeZones{}
	svc, err := NewService(routes, zones, &fakeFriends{ids: []uint64{friendB, friendA, friendA}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.NotifyOwnerPlotStealable(context.Background(), &infov1.NotifyOwnerPlotStealableRequest{
		OwnerPlayerId: owner, PlotId: 1, NotificationId: "stealable-1-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(zones.calls) != 1 {
		t.Fatalf("calls=%+v", zones.calls)
	}
	got := zones.calls[0].recipients
	if len(got) != 2 || got[0] != friendA || got[1] != friendB {
		t.Fatalf("recipients=%v", got)
	}
	if zones.calls[0].redDot.GetSourcePlayerId() != owner {
		t.Fatalf("source=%v", zones.calls[0].redDot.GetSourcePlayerId())
	}
}

func TestDispatchRetriesOnceOnNotOwner(t *testing.T) {
	playerID := uint64(11)
	shard := routing.ShardForPlayer(playerID)
	routes := &fakeRoutes{routes: map[uint32]gateway.Route{
		shard: {ShardID: shard, OwnerZoneID: "zone-a", OwnerEpoch: 1, RouteVersion: 3, OwnerEndpoint: "http://zone-a"},
	}}
	zones := &fakeZones{failOnce: map[string]error{"http://zone-a": gateway.ErrNotOwner}}
	svc, err := NewService(routes, zones, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = svc.SetMailRedDot(context.Background(), &infov1.SetMailRedDotRequest{
		PlayerId: playerID, NotificationId: "mail-retry",
	})
	if len(zones.calls) != 2 {
		t.Fatalf("calls=%d want 2 (first NOT_OWNER then retry)", len(zones.calls))
	}
	if zones.calls[0].redDot.GetNotificationId() != "mail-retry" ||
		zones.calls[1].redDot.GetNotificationId() != "mail-retry" {
		t.Fatalf("notification ids drifted: %+v", zones.calls)
	}
}
