package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/connection"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

type shardExecutionGates struct {
	locks [routing.ShardCount]sync.RWMutex
}

func (g *shardExecutionGates) readLock(shardID uint32) func() {
	if g == nil || shardID >= routing.ShardCount {
		return func() {}
	}
	g.locks[shardID].RLock()
	return g.locks[shardID].RUnlock
}

type lifecycleHandler struct {
	runtime       *player.Runtime
	authorization *routing.AuthorizationTable
	gates         *shardExecutionGates
	connections   interface {
		RemoveShard(shardID uint32) []connection.PlayerConnection
	}
	refresh         func() error
	now             func() time.Time
	drainCompleted  [routing.ShardCount]bool
	drainManifests  [routing.ShardCount][]player.DrainedPlayer
	drainEpoch      [routing.ShardCount]uint64
	drainTransition [routing.ShardCount]string
}

func (h *lifecycleHandler) drain(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "LOOPBACK_ONLY")
		return
	}
	shardID, ok := parseShardPath(w, r)
	if !ok {
		return
	}
	var request struct {
		OwnerEpoch   string `json:"owner_epoch"`
		TransitionID string `json:"transition_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_DRAIN_REQUEST")
		return
	}
	ownerEpoch, err := strconv.ParseUint(request.OwnerEpoch, 10, 64)
	if err != nil || ownerEpoch == 0 || request.TransitionID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_OWNER_EPOCH")
		return
	}

	h.gates.locks[shardID].Lock()
	defer h.gates.locks[shardID].Unlock()
	if existing := h.drainTransition[shardID]; existing != "" &&
		(existing != request.TransitionID || h.drainEpoch[shardID] != ownerEpoch) {
		writeError(w, http.StatusConflict, "DRAIN_TRANSITION_CONFLICT")
		return
	}
	entry, err := h.authorization.BeginDrain(shardID, ownerEpoch, h.now())
	if err != nil {
		writeError(w, http.StatusConflict, "NOT_OWNER")
		return
	}
	if h.drainEpoch[shardID] != ownerEpoch {
		h.drainCompleted[shardID] = false
		h.drainManifests[shardID] = nil
		h.drainEpoch[shardID] = ownerEpoch
		h.drainTransition[shardID] = request.TransitionID
	}
	if h.runtime.HasActiveActorsForShard(shardID) &&
		!h.runtime.SupportsActiveMigration() {
		h.authorization.Resume(shardID)
		h.drainEpoch[shardID] = 0
		h.drainTransition[shardID] = ""
		writeError(w, http.StatusConflict, "SHARD_HAS_ACTIVE_ACTORS")
		return
	}
	if h.connections != nil {
		_ = h.connections.RemoveShard(shardID)
	}
	writeLifecycleRoute(w, entry)
}

type migrationPlayerJSON struct {
	PlayerID           string `json:"player_id"`
	OwnerEpoch         string `json:"owner_epoch"`
	CheckpointRevision string `json:"checkpoint_revision"`
}

func (h *lifecycleHandler) completeDrain(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "LOOPBACK_ONLY")
		return
	}
	shardID, ok := parseShardPath(w, r)
	if !ok {
		return
	}
	var request struct {
		OwnerEpoch   string `json:"owner_epoch"`
		TransitionID string `json:"transition_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_DRAIN_REQUEST")
		return
	}
	ownerEpoch, err := strconv.ParseUint(request.OwnerEpoch, 10, 64)
	if err != nil || request.TransitionID == "" {
		writeError(w, http.StatusConflict, "DRAIN_NOT_STARTED")
		return
	}
	h.gates.locks[shardID].Lock()
	defer h.gates.locks[shardID].Unlock()
	if !h.authorization.IsDraining(shardID, ownerEpoch) || h.drainEpoch[shardID] != ownerEpoch ||
		h.drainTransition[shardID] != request.TransitionID {
		writeError(w, http.StatusConflict, "DRAIN_NOT_STARTED")
		return
	}
	manifest := h.drainManifests[shardID]
	if !h.drainCompleted[shardID] {
		manifest, err = h.runtime.DrainShardForMigration(
			r.Context(), shardID, ownerEpoch,
		)
		if err != nil {
			writeError(w, http.StatusConflict, "FINAL_FLUSH_FAILED")
			return
		}
		h.drainCompleted[shardID] = true
		h.drainManifests[shardID] = append(
			[]player.DrainedPlayer(nil), manifest...,
		)
	}
	players := make([]migrationPlayerJSON, len(manifest))
	for index, item := range manifest {
		players[index] = migrationPlayerJSON{
			PlayerID:           strconv.FormatUint(item.PlayerID, 10),
			OwnerEpoch:         strconv.FormatUint(item.OwnerEpoch, 10),
			CheckpointRevision: strconv.FormatUint(item.CheckpointRevision, 10),
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		ShardID    uint32                `json:"shard_id"`
		OwnerEpoch string                `json:"owner_epoch"`
		Players    []migrationPlayerJSON `json:"players"`
	}{
		ShardID: shardID, OwnerEpoch: request.OwnerEpoch, Players: players,
	})
}

func (h *lifecycleHandler) prepareMigration(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "LOOPBACK_ONLY")
		return
	}
	shardID, ok := parseShardPath(w, r)
	if !ok {
		return
	}
	var request struct {
		OwnerEpoch   string                `json:"owner_epoch"`
		TransitionID string                `json:"transition_id"`
		Players      []migrationPlayerJSON `json:"players"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PREPARE_REQUEST")
		return
	}
	ownerEpoch, err := strconv.ParseUint(request.OwnerEpoch, 10, 64)
	if err != nil || ownerEpoch == 0 || request.TransitionID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_PREPARE_REQUEST")
		return
	}
	entry, exists := h.authorization.Entry(shardID)
	if !exists || entry.State != routing.RouteStatePreparing ||
		entry.OwnerZoneID != h.authorization.ZoneID() ||
		entry.OwnerEpoch != ownerEpoch ||
		entry.TransitionID != request.TransitionID {
		writeError(w, http.StatusConflict, "PREPARING_ROUTE_MISMATCH")
		return
	}
	manifest := make([]player.DrainedPlayer, len(request.Players))
	for index, item := range request.Players {
		playerID, playerErr := strconv.ParseUint(item.PlayerID, 10, 64)
		oldEpoch, epochErr := strconv.ParseUint(item.OwnerEpoch, 10, 64)
		revision, revisionErr := strconv.ParseUint(item.CheckpointRevision, 10, 64)
		if playerErr != nil || epochErr != nil || revisionErr != nil {
			writeError(w, http.StatusBadRequest, "INVALID_PLAYER_MANIFEST")
			return
		}
		manifest[index] = player.DrainedPlayer{
			PlayerID: playerID, OwnerEpoch: oldEpoch,
			CheckpointRevision: revision,
		}
	}
	h.gates.locks[shardID].Lock()
	defer h.gates.locks[shardID].Unlock()
	if err := h.runtime.PrepareShardForMigration(
		r.Context(), shardID, ownerEpoch, manifest,
	); err != nil {
		writeError(w, http.StatusConflict, "CHECKPOINT_PREPARE_FAILED")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *lifecycleHandler) resume(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "LOOPBACK_ONLY")
		return
	}
	shardID, ok := parseShardPath(w, r)
	if !ok {
		return
	}
	var request struct {
		OwnerEpoch   string `json:"owner_epoch"`
		TransitionID string `json:"transition_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_RESUME_REQUEST")
		return
	}
	ownerEpoch, err := strconv.ParseUint(request.OwnerEpoch, 10, 64)
	if err != nil || ownerEpoch == 0 || request.TransitionID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_RESUME_REQUEST")
		return
	}
	h.gates.locks[shardID].Lock()
	if h.drainEpoch[shardID] != ownerEpoch || h.drainTransition[shardID] != request.TransitionID {
		h.gates.locks[shardID].Unlock()
		writeError(w, http.StatusConflict, "DRAIN_TRANSITION_CONFLICT")
		return
	}
	h.authorization.Resume(shardID)
	h.drainCompleted[shardID] = false
	h.drainManifests[shardID] = nil
	h.drainEpoch[shardID] = 0
	h.drainTransition[shardID] = ""
	h.gates.locks[shardID].Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (h *lifecycleHandler) refreshOwnership(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "LOOPBACK_ONLY")
		return
	}
	if h.refresh == nil {
		writeError(w, http.StatusServiceUnavailable, "REFRESH_UNAVAILABLE")
		return
	}
	if err := h.refresh(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "REFRESH_FAILED")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseShardPath(w http.ResponseWriter, r *http.Request) (uint32, bool) {
	value, err := strconv.ParseUint(r.PathValue("shard_id"), 10, 32)
	if err != nil || value >= uint64(routing.ShardCount) {
		writeError(w, http.StatusBadRequest, "INVALID_SHARD_ID")
		return 0, false
	}
	return uint32(value), true
}

func writeLifecycleRoute(w http.ResponseWriter, entry routing.RouteEntry) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(struct {
		ShardID      uint32 `json:"shard_id"`
		OwnerZoneID  string `json:"owner_zone_id"`
		OwnerEpoch   string `json:"owner_epoch"`
		RouteVersion string `json:"route_version"`
	}{
		ShardID: entry.ShardID, OwnerZoneID: entry.OwnerZoneID,
		OwnerEpoch:   strconv.FormatUint(entry.OwnerEpoch, 10),
		RouteVersion: strconv.FormatUint(entry.RouteVersion, 10),
	})
}
