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

// zoneStealResolver rebuilds a StealRequest's ephemeral fields
// (VisitorOwnerEpoch, CropItemID, Quantity) from live Runtime/authorization
// state for interaction.Reconciler, exactly the same way ExecuteFriendAction
// resolves them for a fresh request: see
// player.ConfigSnapshot.SoleStealableCrop's doc comment for why crop
// resolution is a documented Phase 5 simplification.
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
	cropItemID, quantity, ok := r.runtime.CurrentConfig().SoleStealableCrop()
	if !ok {
		return interaction.StealRequest{}, errors.New("no stealable crop is configured")
	}
	return interaction.StealRequest{
		InteractionID:     record.InteractionId,
		VisitorPlayerID:   record.VisitorPlayerId,
		VisitorOwnerEpoch: entry.OwnerEpoch,
		OwnerPlayerID:     record.OwnerPlayerId,
		VisitID:           record.VisitId,
		PlotID:            record.PlotId,
		CropItemID:        cropItemID,
		Quantity:          quantity,
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
