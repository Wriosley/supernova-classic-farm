package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

type migrationHandler struct {
	routes        *routing.Map
	zones         map[string]routing.ZoneCandidate
	client        *http.Client
	now           func() time.Time
	leaseDuration time.Duration
	locks         [routing.ShardCount]sync.Mutex
	advanceFence  func(context.Context, routing.RouteEntry) error
	db            *sql.DB
	progress      [routing.ShardCount]*migrationProgress
}

type migrationProgress struct {
	Prepared routing.RouteEntry
	Source   routing.RouteEntry
	Players  []migrationPlayer
	Step     string
}

type migrationPlayer struct {
	PlayerID           string `json:"player_id"`
	OwnerEpoch         string `json:"owner_epoch"`
	CheckpointRevision string `json:"checkpoint_revision"`
}

func newMigrationHandler(
	routes *routing.Map,
	zones []routing.ZoneCandidate,
	client *http.Client,
	now func() time.Time,
	leaseDuration time.Duration,
) *migrationHandler {
	byID := make(map[string]routing.ZoneCandidate, len(zones))
	for _, zone := range zones {
		byID[zone.ZoneID] = zone
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	if now == nil {
		now = time.Now
	}
	return &migrationHandler{
		routes: routes, zones: byID, client: client,
		now: now, leaseDuration: leaseDuration,
	}
}

func (h *migrationHandler) loadOpenProgress(
	ctx context.Context,
	now time.Time,
) (int, error) {
	if h.db == nil {
		return 0, nil
	}
	rows, err := routing.LoadOpenMigrationProgress(ctx, h.db)
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		progress := progressFromRow(row)
		if err := h.routes.RestorePreparing(progress.Prepared); err != nil {
			return 0, fmt.Errorf(
				"restore PREPARING shard %d: %w", row.ShardID, err,
			)
		}
		h.routes.NoteConsumedEpoch(row.ShardID, row.PreparedOwnerEpoch)
		h.progress[row.ShardID] = progress
	}
	_ = now
	return len(rows), nil
}

func (h *migrationHandler) move(w http.ResponseWriter, r *http.Request) {
	if !requestIsLoopback(r.RemoteAddr) {
		writeMigrationError(w, http.StatusForbidden, "LOOPBACK_ONLY")
		return
	}
	shardValue, err := strconv.ParseUint(r.PathValue("shard_id"), 10, 32)
	if err != nil || shardValue >= uint64(routing.ShardCount) {
		writeMigrationError(w, http.StatusBadRequest, "INVALID_SHARD_ID")
		return
	}
	shardID := uint32(shardValue)
	var request struct {
		TargetZoneID string `json:"target_zone_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
		writeMigrationError(w, http.StatusBadRequest, "INVALID_MOVE_REQUEST")
		return
	}
	target, exists := h.zones[request.TargetZoneID]
	if !exists {
		writeMigrationError(w, http.StatusBadRequest, "UNKNOWN_TARGET_ZONE")
		return
	}

	h.locks[shardID].Lock()
	defer h.locks[shardID].Unlock()
	if h.advanceFence != nil {
		h.moveMySQL(w, r, shardID, target)
		return
	}
	h.moveMemory(w, r, shardID, target)
}

func (h *migrationHandler) inspect(w http.ResponseWriter, r *http.Request) {
	if !requestIsLoopback(r.RemoteAddr) {
		writeMigrationError(w, http.StatusForbidden, "LOOPBACK_ONLY")
		return
	}
	shardValue, err := strconv.ParseUint(r.PathValue("shard_id"), 10, 32)
	if err != nil || shardValue >= uint64(routing.ShardCount) {
		writeMigrationError(w, http.StatusBadRequest, "INVALID_SHARD_ID")
		return
	}
	shardID := uint32(shardValue)
	entry, err := h.routes.Entry(shardID)
	if err != nil {
		writeMigrationError(w, http.StatusBadRequest, "INVALID_SHARD_ID")
		return
	}
	progress := h.progress[shardID]
	response := migrationInspectResponse{
		ShardID: shardID,
		Route:   routeJSON(entry),
	}
	if progress != nil {
		response.Status = routing.MigrationStatusOpen
		response.Step = progress.Step
		response.TransitionID = progress.Prepared.TransitionID
		response.SourceZoneID = progress.Source.OwnerZoneID
		response.TargetZoneID = progress.Prepared.OwnerZoneID
		response.PreparedOwnerEpoch = strconv.FormatUint(
			progress.Prepared.OwnerEpoch, 10,
		)
	} else if h.db != nil {
		row, found, loadErr := routing.LoadMigrationProgress(
			r.Context(), h.db, shardID,
		)
		if loadErr != nil {
			writeMigrationError(w, http.StatusInternalServerError, "PROGRESS_LOAD_FAILED")
			return
		}
		if found {
			response.Status = row.Status
			response.Step = row.Step
			response.TransitionID = row.TransitionID
			response.SourceZoneID = row.SourceZoneID
			response.TargetZoneID = row.TargetZoneID
			response.PreparedOwnerEpoch = strconv.FormatUint(
				row.PreparedOwnerEpoch, 10,
			)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func (h *migrationHandler) listOpen(w http.ResponseWriter, r *http.Request) {
	if !requestIsLoopback(r.RemoteAddr) {
		writeMigrationError(w, http.StatusForbidden, "LOOPBACK_ONLY")
		return
	}
	items := make([]migrationInspectResponse, 0)
	for shardID, progress := range h.progress {
		if progress == nil {
			continue
		}
		entry, err := h.routes.Entry(uint32(shardID))
		if err != nil {
			continue
		}
		items = append(items, migrationInspectResponse{
			ShardID:             uint32(shardID),
			Status:              routing.MigrationStatusOpen,
			Step:                progress.Step,
			TransitionID:        progress.Prepared.TransitionID,
			SourceZoneID:        progress.Source.OwnerZoneID,
			TargetZoneID:        progress.Prepared.OwnerZoneID,
			PreparedOwnerEpoch:  strconv.FormatUint(progress.Prepared.OwnerEpoch, 10),
			Route:               routeJSON(entry),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(struct {
		Migrations []migrationInspectResponse `json:"migrations"`
	}{Migrations: items})
}

func (h *migrationHandler) continueMigration(
	w http.ResponseWriter,
	r *http.Request,
) {
	if !requestIsLoopback(r.RemoteAddr) {
		writeMigrationError(w, http.StatusForbidden, "LOOPBACK_ONLY")
		return
	}
	if h.advanceFence == nil {
		writeMigrationError(w, http.StatusConflict, "MYSQL_REQUIRED")
		return
	}
	shardValue, err := strconv.ParseUint(r.PathValue("shard_id"), 10, 32)
	if err != nil || shardValue >= uint64(routing.ShardCount) {
		writeMigrationError(w, http.StatusBadRequest, "INVALID_SHARD_ID")
		return
	}
	shardID := uint32(shardValue)
	h.locks[shardID].Lock()
	defer h.locks[shardID].Unlock()
	progress := h.progress[shardID]
	if progress == nil {
		writeMigrationError(w, http.StatusConflict, "RECOVERY_REQUIRED")
		return
	}
	target, exists := h.zones[progress.Prepared.OwnerZoneID]
	if !exists {
		writeMigrationError(w, http.StatusConflict, "UNKNOWN_TARGET_ZONE")
		return
	}
	h.moveMySQL(w, r, shardID, target)
}

func (h *migrationHandler) abandonMigration(
	w http.ResponseWriter,
	r *http.Request,
) {
	if !requestIsLoopback(r.RemoteAddr) {
		writeMigrationError(w, http.StatusForbidden, "LOOPBACK_ONLY")
		return
	}
	if h.advanceFence == nil || h.db == nil {
		writeMigrationError(w, http.StatusConflict, "MYSQL_REQUIRED")
		return
	}
	shardValue, err := strconv.ParseUint(r.PathValue("shard_id"), 10, 32)
	if err != nil || shardValue >= uint64(routing.ShardCount) {
		writeMigrationError(w, http.StatusBadRequest, "INVALID_SHARD_ID")
		return
	}
	shardID := uint32(shardValue)
	h.locks[shardID].Lock()
	defer h.locks[shardID].Unlock()
	progress := h.progress[shardID]
	if progress == nil {
		writeMigrationError(w, http.StatusConflict, "RECOVERY_REQUIRED")
		return
	}
	if progress.Step == routing.MigrationStepFenceAdvanced ||
		progress.Step == routing.MigrationStepTargetPrepared {
		writeMigrationError(w, http.StatusConflict, "FENCE_ALREADY_ADVANCED")
		return
	}
	now := h.now().UTC()
	if err := routing.MarkMigrationAbandoned(
		r.Context(), h.db, shardID, progress.Prepared.TransitionID, now,
	); err != nil {
		if errors.Is(err, routing.ErrFenceAlreadyAdvanced) {
			writeMigrationError(w, http.StatusConflict, "FENCE_ALREADY_ADVANCED")
			return
		}
		writeMigrationError(w, http.StatusConflict, "ABANDON_FAILED")
		return
	}
	source := progress.Source
	source.State = routing.RouteStateActive
	source.RouteVersion++
	source.LeaseExpiresAt = now.Add(h.leaseDuration)
	source.UpdatedAt = now
	source.PreviousOwnerZoneID = ""
	source.TransitionID = ""
	if err := h.routes.RestoreActive(source); err != nil {
		writeMigrationError(w, http.StatusConflict, "RESTORE_SOURCE_FAILED")
		return
	}
	h.routes.NoteConsumedEpoch(shardID, progress.Prepared.OwnerEpoch)
	h.resumeZone(
		r, progress.Source.OwnerEndpoint, shardID, progress.Source.OwnerEpoch,
	)
	h.progress[shardID] = nil
	writeMigrationRoute(w, source)
}

func (h *migrationHandler) moveMemory(
	w http.ResponseWriter,
	r *http.Request,
	shardID uint32,
	target routing.ZoneCandidate,
) {
	now := h.now().UTC()
	current, err := h.routes.Route(shardID, now)
	if err != nil {
		writeMigrationError(w, http.StatusConflict, "SHARD_NOT_ACTIVE")
		return
	}
	if current.OwnerZoneID == target.ZoneID {
		if current.PreviousOwnerZoneID != "" && current.TransitionID != "" {
			if err := h.refreshTarget(r, target); err != nil {
				writeMigrationError(
					w, http.StatusServiceUnavailable, "TARGET_REFRESH_FAILED",
				)
				return
			}
			writeMigrationRoute(w, current)
			return
		}
		writeMigrationError(w, http.StatusConflict, "ALREADY_OWNER")
		return
	}
	if err := h.checkTargetReady(r, target); err != nil {
		writeMigrationError(w, http.StatusServiceUnavailable, "TARGET_NOT_READY")
		return
	}
	if err := h.callZoneControl(
		r, current.OwnerEndpoint, shardID, "drain", current.OwnerEpoch,
	); err != nil {
		h.resumeZone(r, current.OwnerEndpoint, shardID, current.OwnerEpoch)
		writeMigrationError(w, http.StatusConflict, "DRAIN_REJECTED")
		return
	}

	prepared, err := h.routes.Prepare(
		shardID, target.ZoneID, target.Endpoint, now, h.leaseDuration,
	)
	if err != nil {
		h.resumeZone(r, current.OwnerEndpoint, shardID, current.OwnerEpoch)
		writeMigrationError(w, http.StatusConflict, "PREPARE_FAILED")
		return
	}
	active, err := h.routes.Activate(
		shardID, prepared.TransitionID, h.now().UTC(), h.leaseDuration,
	)
	if err != nil {
		writeMigrationError(w, http.StatusConflict, "ACTIVATE_FAILED")
		return
	}
	if err := h.refreshTarget(r, target); err != nil {
		writeMigrationError(w, http.StatusServiceUnavailable, "TARGET_REFRESH_FAILED")
		return
	}
	writeMigrationRoute(w, active)
}

func (h *migrationHandler) moveMySQL(
	w http.ResponseWriter,
	r *http.Request,
	shardID uint32,
	target routing.ZoneCandidate,
) {
	now := h.now().UTC()
	entry, err := h.routes.Entry(shardID)
	if err != nil {
		writeMigrationError(w, http.StatusConflict, "SHARD_NOT_ACTIVE")
		return
	}
	progress := h.progress[shardID]
	switch entry.State {
	case routing.RouteStateActive:
		current, routeErr := h.routes.Route(shardID, now)
		if routeErr != nil {
			writeMigrationError(w, http.StatusConflict, "SHARD_NOT_ACTIVE")
			return
		}
		if current.OwnerZoneID == target.ZoneID {
			if progress != nil &&
				progress.Prepared.OwnerEpoch == current.OwnerEpoch &&
				progress.Prepared.TransitionID == current.TransitionID {
				if err := h.refreshTarget(r, target); err != nil {
					writeMigrationError(
						w, http.StatusServiceUnavailable, "TARGET_REFRESH_FAILED",
					)
					return
				}
				_ = h.deleteProgress(r.Context(), progress)
				h.progress[shardID] = nil
				writeMigrationRoute(w, current)
				return
			}
			writeMigrationError(w, http.StatusConflict, "ALREADY_OWNER")
			return
		}
		if err := h.checkTargetReady(r, target); err != nil {
			writeMigrationError(w, http.StatusServiceUnavailable, "TARGET_NOT_READY")
			return
		}
		if err := h.callZoneControl(
			r, current.OwnerEndpoint, shardID, "drain", current.OwnerEpoch,
		); err != nil {
			h.resumeZone(r, current.OwnerEndpoint, shardID, current.OwnerEpoch)
			writeMigrationError(w, http.StatusConflict, "DRAIN_REJECTED")
			return
		}
		if h.db != nil {
			if abandoned, found, loadErr := routing.LoadAbandonedPreparedEpoch(
				r.Context(), h.db, shardID,
			); loadErr == nil && found {
				h.routes.NoteConsumedEpoch(shardID, abandoned)
			}
		}
		prepared, prepareErr := h.routes.Prepare(
			shardID, target.ZoneID, target.Endpoint, now, h.leaseDuration,
		)
		if prepareErr != nil {
			h.resumeZone(r, current.OwnerEndpoint, shardID, current.OwnerEpoch)
			writeMigrationError(w, http.StatusConflict, "PREPARE_FAILED")
			return
		}
		progress = &migrationProgress{
			Prepared: prepared,
			Source:   current,
			Step:     routing.MigrationStepPreparingCommitted,
		}
		h.progress[shardID] = progress
		if err := h.persistProgress(r.Context(), progress); err != nil {
			writeMigrationError(w, http.StatusConflict, "PROGRESS_PERSIST_FAILED")
			return
		}
	case routing.RouteStatePreparing:
		if progress == nil ||
			progress.Prepared.TransitionID != entry.TransitionID ||
			entry.OwnerZoneID != target.ZoneID {
			writeMigrationError(w, http.StatusConflict, "RECOVERY_REQUIRED")
			return
		}
	default:
		writeMigrationError(w, http.StatusConflict, "SHARD_NOT_ACTIVE")
		return
	}

	if progress.Step == routing.MigrationStepPreparingCommitted {
		players, drainErr := h.completeZoneDrain(r, progress)
		if drainErr != nil {
			writeMigrationError(w, http.StatusConflict, "FINAL_DRAIN_FAILED")
			return
		}
		progress.Players = players
		progress.Step = routing.MigrationStepDrained
		if err := h.persistProgress(r.Context(), progress); err != nil {
			writeMigrationError(w, http.StatusConflict, "PROGRESS_PERSIST_FAILED")
			return
		}
	}
	if progress.Step == routing.MigrationStepDrained {
		if err := h.refreshTarget(r, target); err != nil {
			writeMigrationError(w, http.StatusServiceUnavailable, "TARGET_REFRESH_FAILED")
			return
		}
		if err := h.advanceFence(r.Context(), progress.Prepared); err != nil {
			writeMigrationError(w, http.StatusConflict, "FENCE_ADVANCE_FAILED")
			return
		}
		progress.Step = routing.MigrationStepFenceAdvanced
		if err := h.persistProgress(r.Context(), progress); err != nil {
			writeMigrationError(w, http.StatusConflict, "PROGRESS_PERSIST_FAILED")
			return
		}
	}
	if progress.Step == routing.MigrationStepFenceAdvanced {
		if err := h.prepareTarget(r, target, progress); err != nil {
			writeMigrationError(w, http.StatusConflict, "TARGET_PREPARE_FAILED")
			return
		}
		progress.Step = routing.MigrationStepTargetPrepared
		if err := h.persistProgress(r.Context(), progress); err != nil {
			writeMigrationError(w, http.StatusConflict, "PROGRESS_PERSIST_FAILED")
			return
		}
	}
	active, err := h.routes.Activate(
		shardID, progress.Prepared.TransitionID,
		h.now().UTC(), h.leaseDuration,
	)
	if err != nil {
		writeMigrationError(w, http.StatusConflict, "ACTIVATE_FAILED")
		return
	}
	if err := h.refreshTarget(r, target); err != nil {
		writeMigrationError(w, http.StatusServiceUnavailable, "TARGET_REFRESH_FAILED")
		return
	}
	_ = h.deleteProgress(r.Context(), progress)
	h.progress[shardID] = nil
	writeMigrationRoute(w, active)
}

func (h *migrationHandler) persistProgress(
	ctx context.Context,
	progress *migrationProgress,
) error {
	if h.db == nil || progress == nil {
		return nil
	}
	players := make([]routing.MigrationPlayer, len(progress.Players))
	for index, player := range progress.Players {
		players[index] = routing.MigrationPlayer{
			PlayerID:           player.PlayerID,
			OwnerEpoch:         player.OwnerEpoch,
			CheckpointRevision: player.CheckpointRevision,
		}
	}
	return routing.UpsertOpenMigrationProgress(ctx, h.db, routing.MigrationProgressRow{
		ShardID:              progress.Prepared.ShardID,
		TransitionID:         progress.Prepared.TransitionID,
		Step:                 progress.Step,
		SourceZoneID:         progress.Source.OwnerZoneID,
		SourceEndpoint:       progress.Source.OwnerEndpoint,
		SourceOwnerEpoch:     progress.Source.OwnerEpoch,
		SourceRouteVersion:   progress.Source.RouteVersion,
		SourceLeaseID:        progress.Source.LeaseID,
		TargetZoneID:         progress.Prepared.OwnerZoneID,
		TargetEndpoint:       progress.Prepared.OwnerEndpoint,
		PreparedOwnerEpoch:   progress.Prepared.OwnerEpoch,
		PreparedRouteVersion: progress.Prepared.RouteVersion,
		PreparedLeaseID:      progress.Prepared.LeaseID,
		PreparedLeaseTerm:    progress.Prepared.LeaseTerm,
		Players:              players,
		UpdatedAtMS:          h.now().UTC().UnixMilli(),
	})
}

func (h *migrationHandler) deleteProgress(
	ctx context.Context,
	progress *migrationProgress,
) error {
	if h.db == nil || progress == nil {
		return nil
	}
	return routing.DeleteOpenMigrationProgress(
		ctx, h.db, progress.Prepared.ShardID, progress.Prepared.TransitionID,
	)
}

func (h *migrationHandler) checkTargetReady(
	request *http.Request,
	target routing.ZoneCandidate,
) error {
	req, err := http.NewRequestWithContext(
		request.Context(), http.MethodGet, target.Endpoint+"/readyz", nil,
	)
	if err != nil {
		return err
	}
	response, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("target readiness returned %s", response.Status)
	}
	return nil
}

func (h *migrationHandler) callZoneControl(
	request *http.Request,
	endpoint string,
	shardID uint32,
	action string,
	ownerEpoch uint64,
) error {
	var body io.Reader
	if action == "drain" {
		encoded, _ := json.Marshal(struct {
			OwnerEpoch string `json:"owner_epoch"`
		}{OwnerEpoch: strconv.FormatUint(ownerEpoch, 10)})
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(
		request.Context(), http.MethodPost,
		endpoint+"/internal/v1/shards/"+
			strconv.FormatUint(uint64(shardID), 10)+"/"+action,
		body,
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusOK &&
		response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("Zone %s returned %s", action, response.Status)
	}
	return nil
}

func (h *migrationHandler) resumeZone(
	request *http.Request,
	endpoint string,
	shardID uint32,
	ownerEpoch uint64,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = h.callZoneControl(
		request.Clone(ctx), endpoint, shardID, "resume", ownerEpoch,
	)
}

func (h *migrationHandler) completeZoneDrain(
	request *http.Request,
	progress *migrationProgress,
) ([]migrationPlayer, error) {
	encoded, _ := json.Marshal(struct {
		OwnerEpoch string `json:"owner_epoch"`
	}{OwnerEpoch: strconv.FormatUint(progress.Source.OwnerEpoch, 10)})
	req, err := http.NewRequestWithContext(
		request.Context(), http.MethodPost,
		progress.Source.OwnerEndpoint+"/internal/v1/shards/"+
			strconv.FormatUint(uint64(progress.Source.ShardID), 10)+
			"/drain-complete",
		bytes.NewReader(encoded),
	)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("Zone drain completion returned %s", response.Status)
	}
	var result struct {
		ShardID    uint32            `json:"shard_id"`
		OwnerEpoch string            `json:"owner_epoch"`
		Players    []migrationPlayer `json:"players"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return nil, err
	}
	if result.ShardID != progress.Source.ShardID ||
		result.OwnerEpoch != strconv.FormatUint(progress.Source.OwnerEpoch, 10) {
		return nil, errors.New("Zone drain manifest metadata mismatch")
	}
	return result.Players, nil
}

func (h *migrationHandler) prepareTarget(
	request *http.Request,
	target routing.ZoneCandidate,
	progress *migrationProgress,
) error {
	encoded, err := json.Marshal(struct {
		OwnerEpoch   string            `json:"owner_epoch"`
		TransitionID string            `json:"transition_id"`
		Players      []migrationPlayer `json:"players"`
	}{
		OwnerEpoch:   strconv.FormatUint(progress.Prepared.OwnerEpoch, 10),
		TransitionID: progress.Prepared.TransitionID,
		Players:      progress.Players,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(
		request.Context(), http.MethodPost,
		target.Endpoint+"/internal/v1/shards/"+
			strconv.FormatUint(uint64(progress.Prepared.ShardID), 10)+
			"/prepare",
		bytes.NewReader(encoded),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("target preparation returned %s", response.Status)
	}
	return nil
}

func (h *migrationHandler) refreshTarget(
	request *http.Request,
	target routing.ZoneCandidate,
) error {
	req, err := http.NewRequestWithContext(
		request.Context(), http.MethodPost,
		target.Endpoint+"/internal/v1/ownership/refresh", nil,
	)
	if err != nil {
		return err
	}
	response, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("target refresh returned %s", response.Status)
	}
	return nil
}

func progressFromRow(row routing.MigrationProgressRow) *migrationProgress {
	players := make([]migrationPlayer, len(row.Players))
	for index, player := range row.Players {
		players[index] = migrationPlayer{
			PlayerID:           player.PlayerID,
			OwnerEpoch:         player.OwnerEpoch,
			CheckpointRevision: player.CheckpointRevision,
		}
	}
	return &migrationProgress{
		Step:    row.Step,
		Players: players,
		Source: routing.RouteEntry{
			ShardID:        row.ShardID,
			OwnerZoneID:    row.SourceZoneID,
			OwnerEndpoint:  row.SourceEndpoint,
			OwnerEpoch:     row.SourceOwnerEpoch,
			RouteVersion:   row.SourceRouteVersion,
			State:          routing.RouteStateActive,
			LeaseID:        row.SourceLeaseID,
			LeaseExpiresAt: time.UnixMilli(row.UpdatedAtMS).UTC(),
		},
		Prepared: routing.RouteEntry{
			ShardID:             row.ShardID,
			OwnerZoneID:         row.TargetZoneID,
			OwnerEndpoint:       row.TargetEndpoint,
			OwnerEpoch:          row.PreparedOwnerEpoch,
			RouteVersion:        row.PreparedRouteVersion,
			State:               routing.RouteStatePreparing,
			LeaseTerm:           row.PreparedLeaseTerm,
			LeaseID:             row.PreparedLeaseID,
			LeaseExpiresAt:      time.UnixMilli(row.UpdatedAtMS).UTC(),
			PreviousOwnerZoneID: row.SourceZoneID,
			TransitionID:        row.TransitionID,
			UpdatedAt:           time.UnixMilli(row.UpdatedAtMS).UTC(),
		},
	}
}

type migrationInspectResponse struct {
	ShardID             uint32    `json:"shard_id"`
	Status              string    `json:"status,omitempty"`
	Step                string    `json:"step,omitempty"`
	TransitionID        string    `json:"transition_id,omitempty"`
	SourceZoneID        string    `json:"source_zone_id,omitempty"`
	TargetZoneID        string    `json:"target_zone_id,omitempty"`
	PreparedOwnerEpoch  string    `json:"prepared_owner_epoch,omitempty"`
	Route               routeView `json:"route"`
}

type routeView struct {
	ShardID      uint32 `json:"shard_id"`
	OwnerZoneID  string `json:"owner_zone_id"`
	OwnerEpoch   string `json:"owner_epoch"`
	RouteVersion string `json:"route_version"`
	State        string `json:"state"`
}

func routeJSON(entry routing.RouteEntry) routeView {
	return routeView{
		ShardID: entry.ShardID, OwnerZoneID: entry.OwnerZoneID,
		OwnerEpoch:   strconv.FormatUint(entry.OwnerEpoch, 10),
		RouteVersion: strconv.FormatUint(entry.RouteVersion, 10),
		State:        string(entry.State),
	}
}

func requestIsLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func writeMigrationError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code string `json:"code"`
	}{Code: code})
}

func writeMigrationRoute(w http.ResponseWriter, entry routing.RouteEntry) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(struct {
		ShardID      uint32 `json:"shard_id"`
		OwnerZoneID  string `json:"owner_zone_id"`
		OwnerEpoch   string `json:"owner_epoch"`
		RouteVersion string `json:"route_version"`
		State        string `json:"state"`
	}{
		ShardID: entry.ShardID, OwnerZoneID: entry.OwnerZoneID,
		OwnerEpoch:   strconv.FormatUint(entry.OwnerEpoch, 10),
		RouteVersion: strconv.FormatUint(entry.RouteVersion, 10),
		State:        string(entry.State),
	})
}
