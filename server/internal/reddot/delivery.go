package reddot

import (
	"context"
	"errors"
	"log/slog"
	"sort"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/gateway"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

const MaxRecipients = 4096

type ZoneDispatcher interface {
	DispatchRedDot(context.Context, gateway.Route, []uint64, *wsv1.RedDotChangedPush) error
}

type Delivery struct {
	routes gateway.RouteResolver
	zones  ZoneDispatcher
	logger *slog.Logger
}

func NewDelivery(routes gateway.RouteResolver, zones ZoneDispatcher, logger *slog.Logger) (*Delivery, error) {
	if routes == nil || zones == nil {
		return nil, errors.New("red-dot route resolver and zone dispatcher are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Delivery{routes: routes, zones: zones, logger: logger}, nil
}

func (d *Delivery) Deliver(ctx context.Context, recipients []uint64, redDot *wsv1.RedDotChangedPush) {
	if d == nil || redDot == nil || redDot.GetNotificationId() == "" {
		return
	}
	recipients = normalize(recipients)
	if len(recipients) > MaxRecipients {
		recipients = recipients[:MaxRecipients]
	}
	d.deliver(ctx, recipients, redDot, false)
}

type routeKey struct {
	shardID       uint32
	ownerZoneID   string
	ownerEndpoint string
	ownerEpoch    uint64
	routeVersion  uint64
}

type routeGroup struct {
	route gateway.Route
	ids   []uint64
}

func (d *Delivery) deliver(ctx context.Context, recipients []uint64, redDot *wsv1.RedDotChangedPush, retry bool) {
	groups := make(map[routeKey]*routeGroup)
	for _, playerID := range recipients {
		shardID := routing.ShardForPlayer(playerID)
		route, err := d.routes.Resolve(ctx, shardID)
		if err != nil {
			d.logger.Warn("red dot route resolve failed", "player_id", playerID, "notification_id", redDot.GetNotificationId(), "error", err)
			continue
		}
		key := routeKey{route.ShardID, route.OwnerZoneID, route.OwnerEndpoint, route.OwnerEpoch, route.RouteVersion}
		group := groups[key]
		if group == nil {
			group = &routeGroup{route: route}
			groups[key] = group
		}
		group.ids = append(group.ids, playerID)
	}
	for _, group := range groups {
		err := d.zones.DispatchRedDot(ctx, group.route, group.ids, redDot)
		if err == nil {
			continue
		}
		if !retry && errors.Is(err, gateway.ErrNotOwner) {
			if invalidator, ok := d.routes.(gateway.RouteInvalidator); ok {
				invalidator.InvalidateIfVersion(group.route.ShardID, group.route.RouteVersion)
			}
			d.deliver(ctx, group.ids, redDot, true)
			continue
		}
		d.logger.Warn("red dot zone dispatch failed", "owner_zone_id", group.route.OwnerZoneID, "shard_id", group.route.ShardID, "notification_id", redDot.GetNotificationId(), "recipients", len(group.ids), "error", err)
	}
}

func normalize(ids []uint64) []uint64 {
	seen := make(map[uint64]struct{}, len(ids))
	out := make([]uint64, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
