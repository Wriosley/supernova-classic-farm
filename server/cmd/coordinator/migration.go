package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	coordinatormigration "github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/migration"
	"github.com/Wriosley/supernova-classic-farm/server/internal/coordinator/routestore"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

type migrationHandler struct {
	routes                 *routing.Map
	zones                  map[string]routing.ZoneCandidate
	client                 *http.Client
	now                    func() time.Time
	leaseDuration          time.Duration
	locks                  [routing.ShardCount]sync.Mutex
	advanceFence           func(context.Context, routing.RouteEntry) error
	db                     *sql.DB
	tcaplus                *routing.TcaplusControlStore
	routeStore             routestore.Store
	runtimeLeases          *routing.RuntimeLeaseOverlay
	tasks                  coordinatormigration.TaskStore
	availabilityVersion    func() uint64
	routePublisher         RouteChangePublisher
	deleteProgressOverride func(context.Context, *migrationProgress) error
	progress               [routing.ShardCount]*migrationProgress
}

type RouteChangePublisher interface {
	PublishRoutes(previous, current routing.Snapshot) error
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
	if h.db == nil && h.tcaplus == nil {
		return 0, nil
	}
	var rows []routing.MigrationProgressRow
	var err error
	if h.tcaplus != nil {
		rows, err = h.tcaplus.LoadOpenProgress(ctx)
	} else {
		rows, err = routing.LoadOpenMigrationProgress(ctx, h.db)
	}
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		progress := progressFromRow(row)
		if h.routeStore == nil {
			if err := h.routes.RestorePreparing(progress.Prepared); err != nil {
				return 0, fmt.Errorf("restore PREPARING shard %d: %w", row.ShardID, err)
			}
			h.routes.NoteConsumedEpoch(row.ShardID, row.PreparedOwnerEpoch)
		} else {
			current, currentErr := h.routes.Entry(row.ShardID)
			if currentErr != nil {
				return 0, currentErr
			}
			if current.State == routing.RouteStatePreparing &&
				current.TransitionID == progress.Prepared.TransitionID &&
				progress.Step == routing.MigrationStepDrained {
				progress.Step = routing.MigrationStepPreparingCommitted
			}
		}
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
	if h.tasks == nil {
		writeMigrationError(w, http.StatusServiceUnavailable, "MIGRATION_QUEUE_UNAVAILABLE")
		return
	}
	h.enqueueDrain(w, r, shardID, target)
}

func (h *migrationHandler) enqueueDrain(w http.ResponseWriter, r *http.Request, shardID uint32, target routing.ZoneCandidate) {
	current, err := h.routes.Entry(shardID)
	if err != nil || current.State != routing.RouteStateActive {
		writeMigrationError(w, http.StatusConflict, "SHARD_NOT_ACTIVE")
		return
	}
	if current.OwnerZoneID == target.ZoneID {
		writeMigrationError(w, http.StatusConflict, "ALREADY_OWNER")
		return
	}
	availabilityVersion := uint64(1)
	if h.availabilityVersion != nil {
		if currentVersion := h.availabilityVersion(); currentVersion > 0 {
			availabilityVersion = currentVersion
		}
	}
	task, _, err := h.tasks.UpsertPlanned(r.Context(), coordinatormigration.Task{
		ShardID: shardID, Reason: coordinatormigration.ReasonDrain, Priority: coordinatormigration.PriorityDrain,
		SourceZoneID: current.OwnerZoneID, SourceEndpoint: current.OwnerEndpoint,
		SourceOwnerEpoch: current.OwnerEpoch, SourceRouteVersion: current.RouteVersion,
		TargetZoneID: target.ZoneID, TargetEndpoint: target.Endpoint,
		PlannedFromMapVersion: h.routes.Snapshot().MapVersion, PlannedFromAvailabilityVersion: availabilityVersion,
	})
	if err != nil {
		writeMigrationError(w, http.StatusConflict, "TASK_CREATE_FAILED")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(struct {
		ShardID uint32 `json:"shard_id"`
		TaskID  string `json:"task_id"`
		Status  string `json:"status"`
	}{ShardID: shardID, TaskID: hex.EncodeToString(task.TaskID), Status: string(task.Status)})
}

func (h *migrationHandler) moveDurable(w http.ResponseWriter, r *http.Request, shardID uint32, target routing.ZoneCandidate) {
	now := h.now().UTC()
	entry, err := h.routes.Entry(shardID)
	if err != nil {
		writeMigrationError(w, http.StatusConflict, "SHARD_NOT_ACTIVE")
		return
	}
	progress := h.progress[shardID]
	if entry.State == routing.RouteStateActive && entry.OwnerZoneID == target.ZoneID {
		if progress == nil || progress.Prepared.TransitionID != entry.TransitionID {
			writeMigrationError(w, http.StatusConflict, "ALREADY_OWNER")
			return
		}
		if err := h.refreshTarget(r, target); err != nil {
			writeMigrationError(w, http.StatusServiceUnavailable, "TARGET_REFRESH_FAILED")
			return
		}
		if err := h.deleteProgress(r.Context(), progress); err != nil {
			writeMigrationError(w, http.StatusServiceUnavailable, "PROGRESS_CLEANUP_FAILED")
			return
		}
		h.progress[shardID] = nil
		writeMigrationRoute(w, entry)
		return
	}

	if entry.State == routing.RouteStateActive {
		if progress == nil {
			if err := h.checkTargetReady(r, target); err != nil {
				writeMigrationError(w, http.StatusServiceUnavailable, "TARGET_NOT_READY")
				return
			}
			if err := h.callZoneControl(r, entry.OwnerEndpoint, shardID, "drain", entry.OwnerEpoch); err != nil {
				h.resumeZone(r, entry.OwnerEndpoint, shardID, entry.OwnerEpoch)
				writeMigrationError(w, http.StatusConflict, "DRAIN_REJECTED")
				return
			}
			prepared, proposeErr := h.routes.ProposePrepare(shardID, target.ZoneID, target.Endpoint, now, h.leaseDuration)
			if proposeErr != nil {
				writeMigrationError(w, http.StatusConflict, "PREPARE_FAILED")
				return
			}
			progress = &migrationProgress{Prepared: prepared, Source: entry}
			players, drainErr := h.completeZoneDrain(r, progress)
			if drainErr != nil {
				writeMigrationError(w, http.StatusConflict, "FINAL_DRAIN_FAILED")
				return
			}
			progress.Players = players
			progress.Step = routing.MigrationStepDrained
			h.progress[shardID] = progress
			if err := h.persistProgress(r.Context(), progress); err != nil {
				writeMigrationError(w, http.StatusConflict, "PROGRESS_PERSIST_FAILED")
				return
			}
		} else if progress.Step != routing.MigrationStepDrained || progress.Source != entry {
			writeMigrationError(w, http.StatusConflict, "RECOVERY_REQUIRED")
			return
		}
		committed, commitErr := h.routeStore.CommitPreparing(r.Context(), progress.Prepared, h.routes.Snapshot().MapVersion)
		if commitErr != nil {
			writeMigrationError(w, http.StatusConflict, "PREPARING_STORE_FAILED")
			return
		}
		if err := h.applyDurableSnapshot(committed); err != nil {
			writeMigrationError(w, http.StatusInternalServerError, "CURRENT_APPLY_FAILED")
			return
		}
		progress.Step = routing.MigrationStepPreparingCommitted
		if err := h.persistProgress(r.Context(), progress); err != nil {
			writeMigrationError(w, http.StatusConflict, "PROGRESS_PERSIST_FAILED")
			return
		}
		entry = progress.Prepared
	}
	if entry.State != routing.RouteStatePreparing || progress == nil || entry.TransitionID != progress.Prepared.TransitionID {
		writeMigrationError(w, http.StatusConflict, "RECOVERY_REQUIRED")
		return
	}
	if progress.Step == routing.MigrationStepPreparingCommitted {
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
	active := progress.Prepared
	active.State = routing.RouteStateActive
	active.RouteVersion++
	active.LeaseExpiresAt = h.now().UTC().Add(h.leaseDuration)
	active.UpdatedAt = h.now().UTC()
	committed, err := h.routeStore.CommitActive(r.Context(), active, h.routes.Snapshot().MapVersion)
	if err != nil {
		writeMigrationError(w, http.StatusConflict, "ACTIVE_STORE_FAILED")
		return
	}
	if err := h.applyDurableSnapshot(committed); err != nil {
		writeMigrationError(w, http.StatusInternalServerError, "CURRENT_APPLY_FAILED")
		return
	}
	if err := h.refreshTarget(r, target); err != nil {
		writeMigrationError(w, http.StatusServiceUnavailable, "TARGET_REFRESH_FAILED")
		return
	}
	if err := h.deleteProgress(r.Context(), progress); err != nil {
		writeMigrationError(w, http.StatusServiceUnavailable, "PROGRESS_CLEANUP_FAILED")
		return
	}
	h.progress[shardID] = nil
	writeMigrationRoute(w, active)
}

func (h *migrationHandler) applyDurableSnapshot(snapshot routestore.Snapshot) error {
	previous := h.routes.Snapshot()
	if err := h.routes.ApplyCommittedSnapshot(routestore.RoutingSnapshot(snapshot)); err != nil {
		return err
	}
	current := h.routes.Snapshot()
	if h.runtimeLeases != nil {
		if err := h.runtimeLeases.Renew(current, h.now().UTC(), h.leaseDuration); err != nil {
			return err
		}
	}
	if h.routePublisher != nil {
		if err := h.routePublisher.PublishRoutes(previous, current); err != nil {
			slog.Warn("publish committed route change", "error", err, "previous_map_version", previous.MapVersion, "map_version", current.MapVersion)
		}
	}
	return nil
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
	} else if h.db != nil || h.tcaplus != nil {
		var row routing.MigrationProgressRow
		var found bool
		var loadErr error
		if h.tcaplus != nil {
			row, found, loadErr = h.tcaplus.LoadProgress(r.Context(), shardID)
		} else {
			row, found, loadErr = routing.LoadMigrationProgress(
				r.Context(), h.db, shardID,
			)
		}
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
			ShardID:            uint32(shardID),
			Status:             routing.MigrationStatusOpen,
			Step:               progress.Step,
			TransitionID:       progress.Prepared.TransitionID,
			SourceZoneID:       progress.Source.OwnerZoneID,
			TargetZoneID:       progress.Prepared.OwnerZoneID,
			PreparedOwnerEpoch: strconv.FormatUint(progress.Prepared.OwnerEpoch, 10),
			Route:              routeJSON(entry),
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

func (h *migrationHandler) abandonDurable(w http.ResponseWriter, r *http.Request, progress *migrationProgress) {
	shardID := progress.Prepared.ShardID
	current, err := h.routes.Entry(shardID)
	if err != nil {
		writeMigrationError(w, http.StatusConflict, "RESTORE_SOURCE_FAILED")
		return
	}
	now := h.now().UTC()
	source := progress.Source
	source.State = routing.RouteStateActive
	source.RouteVersion = current.RouteVersion + 1
	source.LeaseExpiresAt = now.Add(h.leaseDuration)
	source.UpdatedAt = now
	source.PreviousOwnerZoneID = ""
	source.TransitionID = ""
	if current.State == routing.RouteStatePreparing {
		committed, commitErr := h.routeStore.RestoreSource(r.Context(), source, h.routes.Snapshot().MapVersion)
		if commitErr != nil || h.applyDurableSnapshot(committed) != nil {
			writeMigrationError(w, http.StatusConflict, "RESTORE_SOURCE_FAILED")
			return
		}
	} else if current.State != routing.RouteStateActive || current.OwnerZoneID != source.OwnerZoneID {
		writeMigrationError(w, http.StatusConflict, "RESTORE_SOURCE_FAILED")
		return
	} else {
		source = current
	}
	var abandonErr error
	if h.tcaplus != nil {
		abandonErr = h.tcaplus.MarkAbandoned(r.Context(), shardID, progress.Prepared.TransitionID, now)
	} else if h.db != nil {
		abandonErr = routing.MarkMigrationAbandoned(r.Context(), h.db, shardID, progress.Prepared.TransitionID, now)
	}
	if abandonErr != nil {
		writeMigrationError(w, http.StatusConflict, "ABANDON_FAILED")
		return
	}
	h.resumeZone(r, progress.Source.OwnerEndpoint, shardID, progress.Source.OwnerEpoch)
	h.progress[shardID] = nil
	writeMigrationRoute(w, source)
}

func (h *migrationHandler) abandonMigration(
	w http.ResponseWriter,
	r *http.Request,
) {
	if !requestIsLoopback(r.RemoteAddr) {
		writeMigrationError(w, http.StatusForbidden, "LOOPBACK_ONLY")
		return
	}
	if h.advanceFence == nil || (h.db == nil && h.tcaplus == nil) {
		writeMigrationError(w, http.StatusConflict, "DURABLE_STORE_REQUIRED")
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
	if h.routeStore != nil {
		h.abandonDurable(w, r, progress)
		return
	}
	now := h.now().UTC()
	var abandonErr error
	if h.tcaplus != nil {
		abandonErr = h.tcaplus.MarkAbandoned(
			r.Context(), shardID, progress.Prepared.TransitionID, now,
		)
	} else {
		abandonErr = routing.MarkMigrationAbandoned(
			r.Context(), h.db, shardID, progress.Prepared.TransitionID, now,
		)
	}
	if abandonErr != nil {
		if errors.Is(abandonErr, routing.ErrFenceAlreadyAdvanced) {
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
		if h.db != nil || h.tcaplus != nil {
			var abandoned uint64
			var found bool
			var loadErr error
			if h.tcaplus != nil {
				abandoned, found, loadErr = h.tcaplus.LoadAbandonedEpoch(
					r.Context(), shardID,
				)
			} else {
				abandoned, found, loadErr = routing.LoadAbandonedPreparedEpoch(
					r.Context(), h.db, shardID,
				)
			}
			if loadErr == nil && found {
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
	if (h.db == nil && h.tcaplus == nil) || progress == nil {
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
	row := routing.MigrationProgressRow{
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
	}
	if h.tcaplus != nil {
		return h.tcaplus.UpsertProgress(ctx, row)
	}
	return routing.UpsertOpenMigrationProgress(ctx, h.db, row)
}

func (h *migrationHandler) deleteProgress(
	ctx context.Context,
	progress *migrationProgress,
) error {
	if h.deleteProgressOverride != nil {
		return h.deleteProgressOverride(ctx, progress)
	}
	if (h.db == nil && h.tcaplus == nil) || progress == nil {
		return nil
	}
	if h.tcaplus != nil {
		return h.tcaplus.DeleteOpenProgress(
			ctx, progress.Prepared.ShardID, progress.Prepared.TransitionID,
		)
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
	ShardID            uint32    `json:"shard_id"`
	Status             string    `json:"status,omitempty"`
	Step               string    `json:"step,omitempty"`
	TransitionID       string    `json:"transition_id,omitempty"`
	SourceZoneID       string    `json:"source_zone_id,omitempty"`
	TargetZoneID       string    `json:"target_zone_id,omitempty"`
	PreparedOwnerEpoch string    `json:"prepared_owner_epoch,omitempty"`
	Route              routeView `json:"route"`
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
