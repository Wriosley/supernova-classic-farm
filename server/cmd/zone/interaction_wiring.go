package main

import (
	"context"
	"errors"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/interaction"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

// zoneStealResolver rebuilds StealRequest ephemeral VisitorOwnerEpoch from
// live authorization. Crop/quantity/farm-view identity are taken from the
// durable FriendInteraction row so Resume never re-derives crop from config.
type zoneStealResolver struct {
	runtime       *player.Runtime
	authorization ownerAuthorization
}

func newZoneStealResolver(runtime *player.Runtime, authorization ownerAuthorization) *zoneStealResolver {
	return &zoneStealResolver{runtime: runtime, authorization: authorization}
}

func (r *zoneStealResolver) ResolveSteal(
	_ context.Context, record *tcaplusv1.FriendInteraction,
) (interaction.StealRequest, error) {
	entry, ok := r.authorization.Entry(routing.ShardForPlayer(record.VisitorPlayerId))
	if !ok {
		return interaction.StealRequest{}, errors.New("visitor shard ownership is unavailable")
	}
	if record.GetCropItemId() == 0 || record.GetQuantity() == 0 || len(record.GetFarmViewEpoch()) != 16 {
		return interaction.StealRequest{}, errors.New("friend interaction is missing frozen steal crop fields")
	}
	return interaction.StealRequest{
		InteractionID:     record.InteractionId,
		VisitorPlayerID:   record.VisitorPlayerId,
		VisitorOwnerEpoch: entry.OwnerEpoch,
		OwnerPlayerID:     record.OwnerPlayerId,
		VisitID:           record.VisitId,
		PlotID:            record.PlotId,
		CropItemID:        record.GetCropItemId(),
		Quantity:          record.GetQuantity(),
		FarmViewEpoch:     append([]byte(nil), record.GetFarmViewEpoch()...),
		FarmViewSeq:       record.GetFarmViewSeq(),
	}, nil
}

func (r *zoneStealResolver) ResolveAction(
	_ context.Context, record *tcaplusv1.FriendInteraction,
) (interaction.ActionRequest, error) {
	entry, ok := r.authorization.Entry(routing.ShardForPlayer(record.VisitorPlayerId))
	if !ok {
		return interaction.ActionRequest{}, errors.New("visitor shard ownership is unavailable")
	}
	return interaction.ActionRequest{
		InteractionID: record.InteractionId, VisitorPlayerID: record.VisitorPlayerId,
		VisitorOwnerEpoch: entry.OwnerEpoch, OwnerPlayerID: record.OwnerPlayerId,
		VisitID: record.VisitId,
		Action:  datav1.FriendInteractionAction(record.Action),
		PlotID:  record.PlotId, PestID: record.PestId,
	}, nil
}
