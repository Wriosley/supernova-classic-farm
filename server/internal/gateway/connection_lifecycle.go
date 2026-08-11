package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func newConnectionID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func (h *Handler) syncPlayerConnection(
	parent context.Context,
	playerID uint64,
	connectionID string,
	mode string,
) {
	if h == nil || h.connections == nil || playerID == 0 || connectionID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(parent, connectionRPCTimeout)
	defer cancel()
	shardID := routing.ShardForPlayer(playerID)
	route, err := h.routes.Resolve(ctx, shardID)
	if err != nil {
		slog.Warn("player connection route resolve failed",
			"mode", mode, "player_id", playerID, "error", err)
		return
	}
	expiresAt := h.now().Add(ConnectionLeaseTTL)
	call := func(route Route) error {
		switch mode {
		case "register":
			return h.connections.Register(ctx, route, playerID, connectionID, expiresAt)
		case "refresh":
			err := h.connections.Refresh(ctx, route, playerID, connectionID, expiresAt)
			if errors.Is(err, ErrConnectionNotRegistered) {
				return h.connections.Register(ctx, route, playerID, connectionID, expiresAt)
			}
			return err
		case "unregister":
			return h.connections.Unregister(ctx, route, playerID, connectionID)
		default:
			return nil
		}
	}
	err = call(route)
	if errors.Is(err, ErrNotOwner) {
		if invalidator, ok := h.routes.(RouteInvalidator); ok {
			invalidator.InvalidateIfVersion(shardID, route.RouteVersion)
		}
		route, err = h.routes.Resolve(ctx, shardID)
		if err == nil {
			err = call(route)
		}
	}
	if err != nil && mode != "unregister" {
		slog.Warn("player connection sync failed",
			"mode", mode, "player_id", playerID, "connection_id", connectionID, "error", err)
	}
}

func (h *Handler) startConnectionRefresh(
	parent context.Context, playerID uint64, connectionID string,
) (stop func()) {
	if h == nil || h.connections == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(ConnectionRefreshEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.syncPlayerConnection(ctx, playerID, connectionID, "refresh")
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
