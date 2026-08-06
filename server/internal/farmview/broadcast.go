package farmview

import (
	"context"
	"errors"
	"fmt"
	"strings"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/visit"
)

// PatchPublisher abstracts Gate's GatePushService.PublishFarmViewPatch: one
// call fans one FarmViewPatch out to every recipient_player_id connected to
// gateID. The one production implementation is *player.GRPCPushForwarder.
type PatchPublisher interface {
	PublishFarmViewPatch(
		ctx context.Context, gateID string, recipientPlayerIDs []uint64, patch *wsv1.FarmViewPatch,
	) error
}

// VisitorLister abstracts visit.OwnerService (backed by visit.Registry) so
// Broadcaster can discover an owner's current visitors without importing the
// Owner Zone's full OwnerService surface.
type VisitorLister interface {
	ListVisitors(ownerPlayerID uint64) []visit.VisitRecord
}

// Broadcaster fans one FarmViewPatch out to the owner (always, on
// ownerGateID) plus every currently registered visitor, grouped by GateID so
// each Gate receives exactly one PublishFarmViewPatch call per Broadcast.
type Broadcaster struct {
	publisher   PatchPublisher
	visitors    VisitorLister
	ownerGateID string
}

func NewBroadcaster(
	publisher PatchPublisher, visitors VisitorLister, ownerGateID string,
) (*Broadcaster, error) {
	if publisher == nil || visitors == nil {
		return nil, errors.New("patch publisher and visitor lister are required")
	}
	if strings.TrimSpace(ownerGateID) == "" {
		return nil, errors.New("owner gate id is required")
	}
	return &Broadcaster{publisher: publisher, visitors: visitors, ownerGateID: ownerGateID}, nil
}

// Broadcast delivers patch to the owner (who is always a recipient, so
// maturity while the owner is merely browsing their own farm still reaches
// their WS connection) and to every visitor currently registered for
// ownerPlayerID, one PublishFarmViewPatch call per distinct GateID.
func (b *Broadcaster) Broadcast(
	ctx context.Context, ownerPlayerID uint64, patch *wsv1.FarmViewPatch,
) error {
	if b == nil || b.publisher == nil {
		return errors.New("broadcaster is not configured")
	}
	if ownerPlayerID == 0 || patch == nil {
		return errors.New("owner player id and patch are required")
	}
	groups := make(map[string][]uint64)
	groups[b.ownerGateID] = append(groups[b.ownerGateID], ownerPlayerID)
	for _, record := range b.visitors.ListVisitors(ownerPlayerID) {
		if record.VisitorPlayerID == 0 || strings.TrimSpace(record.GateID) == "" {
			continue
		}
		groups[record.GateID] = append(groups[record.GateID], record.VisitorPlayerID)
	}
	var errs error
	for gateID, recipientIDs := range groups {
		if len(recipientIDs) == 0 {
			continue
		}
		if err := b.publisher.PublishFarmViewPatch(ctx, gateID, recipientIDs, patch); err != nil {
			errs = errors.Join(errs, fmt.Errorf("publish farm view patch to gate %s: %w", gateID, err))
		}
	}
	return errs
}
