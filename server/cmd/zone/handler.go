package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/internalnet"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

const maxEnvelopeBytes = 64 << 10

type ownerAuthorization interface {
	Validate(uint64, uint32, string, uint64, time.Time) error
	Entry(uint32) (routing.RouteEntry, bool)
}

type localAuthorization struct{}

func (localAuthorization) Validate(
	targetPlayerID uint64,
	shardID uint32,
	ownerZoneID string,
	ownerEpoch uint64,
	_ time.Time,
) error {
	if routing.ShardForPlayer(targetPlayerID) != shardID ||
		ownerZoneID != routing.DefaultZoneID ||
		ownerEpoch != player.LocalOwnerEpoch {
		return player.ErrNotOwner
	}
	return nil
}

func (localAuthorization) Entry(shardID uint32) (routing.RouteEntry, bool) {
	if shardID >= routing.ShardCount {
		return routing.RouteEntry{}, false
	}
	return routing.RouteEntry{
		ShardID: shardID, OwnerZoneID: routing.DefaultZoneID,
		OwnerEndpoint: routing.DefaultZoneEndpoint,
		OwnerEpoch:    player.LocalOwnerEpoch, RouteVersion: 1,
		State: routing.RouteStateActive,
	}, true
}

func isLoopback(remoteAddr string) bool {
	return internalnet.RemoteAllowed(remoteAddr)
}

func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code string `json:"code"`
	}{Code: code})
}
