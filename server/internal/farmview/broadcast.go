package farmview

import (
	"context"
	"errors"
	"fmt"
	"strings"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/connection"
	"github.com/Wriosley/supernova-classic-farm/server/internal/visit"
)

// PatchPublisher abstracts Gate's GatePushService.PublishFarmViewPatch: one
// call fans one FarmViewPatch out to every recipient_player_id connected to
// gateID. The one production implementation is *player.GRPCPushForwarder.
type PatchPublisher interface {
	PublishFarmViewPatch(
		ctx context.Context, gateID, gateEndpoint string, recipientPlayerIDs []uint64, patch *wsv1.FarmViewPatch,
	) error
}

// VisitorLister abstracts visit.OwnerService (backed by visit.Registry) so
// Broadcaster can discover an owner's current visitors without importing the
// Owner Zone's full OwnerService surface. Visitor GateIDs still come from the
// visit lease (cross-Zone visitors are not in this Zone's ConnectionRegistry).
type VisitorLister interface {
	ListVisitors(ownerPlayerID uint64) []visit.VisitRecord
}

// ConnectionLister resolves live owner WebSocket gates from the Zone-local
// ConnectionRegistry. Offline owners simply receive no FarmView push.
type ConnectionLister interface {
	List(playerID uint64) []connection.PlayerConnection
}

// Broadcaster fans one FarmViewPatch out to the owner's registered Gate
// connections plus every currently registered visitor, grouped by GateID so
// each Gate receives exactly one PublishFarmViewPatch call per Broadcast.
type Broadcaster struct {
	publisher   PatchPublisher
	visitors    VisitorLister
	connections ConnectionLister
}

func NewBroadcaster(
	publisher PatchPublisher, visitors VisitorLister, connections ConnectionLister,
) (*Broadcaster, error) {
	if publisher == nil || visitors == nil || connections == nil {
		return nil, errors.New("patch publisher, visitor lister, and connection lister are required")
	}
	return &Broadcaster{publisher: publisher, visitors: visitors, connections: connections}, nil
}

// Broadcast delivers patch to the owner (when online on this Zone) and to
// every visitor currently registered for ownerPlayerID, one PublishFarmViewPatch
// call per distinct GateID.
func (b *Broadcaster) Broadcast(
	ctx context.Context, ownerPlayerID uint64, patch *wsv1.FarmViewPatch,
) error {
	if b == nil || b.publisher == nil {
		return errors.New("broadcaster is not configured")
	}
	if ownerPlayerID == 0 || patch == nil {
		return errors.New("owner player id and patch are required")
	}
	type target struct{ id, endpoint string }
	groups := make(map[target]map[uint64]struct{})
	add := func(gateID, gateEndpoint string, playerID uint64) {
		if playerID == 0 || strings.TrimSpace(gateID) == "" {
			return
		}
		if strings.TrimSpace(gateEndpoint) == "" {
			gateEndpoint = "http://legacy-gate:8081"
		}
		key := target{gateID, gateEndpoint}
		set := groups[key]
		if set == nil {
			set = make(map[uint64]struct{})
			groups[key] = set
		}
		set[playerID] = struct{}{}
	}
	for _, conn := range b.connections.List(ownerPlayerID) {
		if !conn.ExpiresAt.IsZero() {
			add(conn.GateID, conn.GateEndpoint, ownerPlayerID)
		}
	}
	for _, record := range b.visitors.ListVisitors(ownerPlayerID) {
		add(record.GateID, record.GateEndpoint, record.VisitorPlayerID)
	}
	var errs error
	for target, recipientSet := range groups {
		recipientIDs := make([]uint64, 0, len(recipientSet))
		for playerID := range recipientSet {
			recipientIDs = append(recipientIDs, playerID)
		}
		if len(recipientIDs) == 0 {
			continue
		}
		if err := b.publisher.PublishFarmViewPatch(ctx, target.id, target.endpoint, recipientIDs, patch); err != nil {
			errs = errors.Join(errs, fmt.Errorf("publish farm view patch to gate %s: %w", target.id, err))
		}
	}
	return errs
}
