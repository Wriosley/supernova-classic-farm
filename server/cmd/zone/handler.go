package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/protobuf/proto"
)

const maxEnvelopeBytes = 64 << 10

type runtimeHandler struct {
	runtime       *player.Runtime
	authorization ownerAuthorization
	gates         *shardExecutionGates
	now           func() time.Time
}

type ownerAuthorization interface {
	Validate(uint64, uint32, string, uint64, time.Time) error
	Entry(uint32) (routing.RouteEntry, bool)
}

func newCommandHandler(runtime *player.Runtime) http.Handler {
	return newOwnedCommandHandler(runtime, localAuthorization{}, time.Now)
}

func newOwnedCommandHandler(
	runtime *player.Runtime,
	authorization ownerAuthorization,
	now func() time.Time,
) http.Handler {
	return newOwnedCommandHandlerWithGates(runtime, authorization, nil, now)
}

func newOwnedCommandHandlerWithGates(
	runtime *player.Runtime,
	authorization ownerAuthorization,
	gates *shardExecutionGates,
	now func() time.Time,
) http.Handler {
	if now == nil {
		now = time.Now
	}
	return &runtimeHandler{
		runtime: runtime, authorization: authorization, gates: gates, now: now,
	}
}

func (h *runtimeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "LOOPBACK_ONLY")
		return
	}

	callerPlayerID, err := parseRequiredUintHeader(r, "X-Caller-Player-ID")
	if err != nil || callerPlayerID == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_CALLER_PLAYER_ID")
		return
	}
	shardValue, err := parseRequiredUintHeader(r, "X-Shard-ID")
	if err != nil || shardValue >= uint64(routing.ShardCount) {
		writeError(w, http.StatusBadRequest, "INVALID_SHARD_ID")
		return
	}
	shardID := uint32(shardValue)
	unlockShard := h.gates.readLock(shardID)
	defer unlockShard()
	ownerZoneID := r.Header.Get("X-Owner-Zone-ID")
	if ownerZoneID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_OWNER_ZONE_ID")
		return
	}
	ownerEpoch, err := parseRequiredUintHeader(r, "X-Owner-Epoch")
	if err != nil || ownerEpoch == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_OWNER_EPOCH")
		return
	}
	routeVersion, err := parseRequiredUintHeader(r, "X-Route-Version")
	if err != nil || routeVersion == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_ROUTE_VERSION")
		return
	}
	// route_version is a cache invalidation hint, not an independent write
	// authorization token; Zone authority is shard + Zone + epoch + Lease.
	_ = routeVersion

	r.Body = http.MaxBytesReader(w, r.Body, maxEnvelopeBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "ENVELOPE_TOO_LARGE")
		return
	}
	request := &wsv1.WsEnvelope{}
	if len(body) == 0 || proto.Unmarshal(body, request) != nil {
		writeError(w, http.StatusBadRequest, "MALFORMED_PROTOBUF")
		return
	}
	if h.authorization == nil {
		writeError(w, http.StatusServiceUnavailable, "OWNERSHIP_UNAVAILABLE")
		return
	}
	if err := h.authorization.Validate(
		request.TargetPlayerId, shardID, ownerZoneID, ownerEpoch, h.now(),
	); err != nil {
		writeNotOwner(w, h.authorization, shardID)
		return
	}

	response, err := h.runtime.Handle(r.Context(), callerPlayerID, ownerEpoch, request)
	switch {
	case errors.Is(err, player.ErrNotOwner):
		writeNotOwner(w, h.authorization, shardID)
		return
	case errors.Is(err, player.ErrForbiddenTarget):
		writeError(w, http.StatusForbidden, "FORBIDDEN_TARGET")
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, "INVALID_COMMAND")
		return
	}

	encoded, err := proto.Marshal(response)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ENCODE_FAILED")
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
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

func parseRequiredUintHeader(r *http.Request, name string) (uint64, error) {
	value := r.Header.Get(name)
	if value == "" {
		return 0, errors.New("missing header")
	}
	return strconv.ParseUint(value, 10, 64)
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code string `json:"code"`
	}{Code: code})
}

func writeNotOwner(
	w http.ResponseWriter,
	authorization ownerAuthorization,
	shardID uint32,
) {
	entry, _ := authorization.Entry(shardID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(struct {
		Code         string `json:"code"`
		ShardID      uint32 `json:"shard_id"`
		OwnerZoneID  string `json:"owner_zone_id,omitempty"`
		OwnerEpoch   string `json:"owner_epoch"`
		RouteVersion string `json:"route_version"`
	}{
		Code: "NOT_OWNER", ShardID: shardID,
		OwnerZoneID:  entry.OwnerZoneID,
		OwnerEpoch:   strconv.FormatUint(entry.OwnerEpoch, 10),
		RouteVersion: strconv.FormatUint(entry.RouteVersion, 10),
	})
}
