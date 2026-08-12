package routing

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrFenceAlreadyAdvanced = errors.New("FENCE_ALREADY_ADVANCED")

const (
	MigrationStatusOpen      = "OPEN"
	MigrationStatusAbandoned = "ABANDONED"

	MigrationStepPreparingCommitted = "PREPARING_COMMITTED"
	MigrationStepDrained            = "DRAINED"
	MigrationStepFenceAdvanced      = "FENCE_ADVANCED"
	MigrationStepTargetPrepared     = "TARGET_PREPARED"
)

// MigrationPlayer is one drained Actor identity carried through recovery.
type MigrationPlayer struct {
	PlayerID           string `json:"player_id"`
	OwnerEpoch         string `json:"owner_epoch"`
	CheckpointRevision string `json:"checkpoint_revision"`
}

// MigrationProgressRow is the durable Coordinator migration progress record.
type MigrationProgressRow struct {
	ShardID              uint32
	TransitionID         string
	Status               string
	Step                 string
	SourceZoneID         string
	SourceEndpoint       string
	SourceOwnerEpoch     uint64
	SourceRouteVersion   uint64
	SourceLeaseID        string
	TargetZoneID         string
	TargetEndpoint       string
	PreparedOwnerEpoch   uint64
	PreparedRouteVersion uint64
	PreparedLeaseID      string
	PreparedLeaseTerm    uint64
	Players              []MigrationPlayer
	UpdatedAtMS          int64
}

// UpsertOpenMigrationProgress inserts or replaces the OPEN progress row.
func UpsertOpenMigrationProgress(
	ctx context.Context,
	db *sql.DB,
	row MigrationProgressRow,
) error {
	if db == nil {
		return errors.New("MySQL database is required")
	}
	if err := validateOpenProgressRow(row); err != nil {
		return err
	}
	transitionID, err := parseUUIDBytes(row.TransitionID)
	if err != nil {
		return fmt.Errorf("parse transition ID: %w", err)
	}
	playersJSON, err := json.Marshal(row.Players)
	if err != nil {
		return fmt.Errorf("encode migration players: %w", err)
	}
	if row.UpdatedAtMS <= 0 {
		row.UpdatedAtMS = time.Now().UTC().UnixMilli()
	}
	if row.PreparedLeaseTerm == 0 {
		row.PreparedLeaseTerm = 1
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO shard_migration_progress (
			logical_shard_id, transition_id, status, step,
			source_zone_id, source_endpoint, source_owner_epoch,
			source_route_version, source_lease_id,
			target_zone_id, target_endpoint,
			prepared_owner_epoch, prepared_route_version,
			prepared_lease_id, prepared_lease_term,
			players_json, updated_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			transition_id = VALUES(transition_id),
			status = VALUES(status),
			step = VALUES(step),
			source_zone_id = VALUES(source_zone_id),
			source_endpoint = VALUES(source_endpoint),
			source_owner_epoch = VALUES(source_owner_epoch),
			source_route_version = VALUES(source_route_version),
			source_lease_id = VALUES(source_lease_id),
			target_zone_id = VALUES(target_zone_id),
			target_endpoint = VALUES(target_endpoint),
			prepared_owner_epoch = VALUES(prepared_owner_epoch),
			prepared_route_version = VALUES(prepared_route_version),
			prepared_lease_id = VALUES(prepared_lease_id),
			prepared_lease_term = VALUES(prepared_lease_term),
			players_json = VALUES(players_json),
			updated_at_ms = VALUES(updated_at_ms)`,
		row.ShardID, transitionID, MigrationStatusOpen, row.Step,
		row.SourceZoneID, row.SourceEndpoint, row.SourceOwnerEpoch,
		row.SourceRouteVersion, row.SourceLeaseID,
		row.TargetZoneID, row.TargetEndpoint,
		row.PreparedOwnerEpoch, row.PreparedRouteVersion,
		row.PreparedLeaseID, row.PreparedLeaseTerm,
		playersJSON, row.UpdatedAtMS,
	)
	if err != nil {
		return fmt.Errorf("upsert migration progress: %w", err)
	}
	return nil
}

// MarkMigrationAbandoned marks the current OPEN row abandoned for the
// transition. It refuses when the durable step is at or past Fence advance.
func MarkMigrationAbandoned(
	ctx context.Context,
	db *sql.DB,
	shardID uint32,
	transitionID string,
	now time.Time,
) error {
	if db == nil {
		return errors.New("MySQL database is required")
	}
	if err := validShardID(shardID); err != nil {
		return err
	}
	transition, err := parseUUIDBytes(transitionID)
	if err != nil {
		return fmt.Errorf("parse transition ID: %w", err)
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin abandon migration progress: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var status, step string
	var storedTransition []byte
	err = tx.QueryRowContext(ctx, `
		SELECT status, step, transition_id
		FROM shard_migration_progress
		WHERE logical_shard_id = ?
		FOR UPDATE`,
		shardID,
	).Scan(&status, &step, &storedTransition)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("migration progress not found")
	}
	if err != nil {
		return fmt.Errorf("lock migration progress: %w", err)
	}
	if status != MigrationStatusOpen {
		return errors.New("migration progress is not OPEN")
	}
	if !bytes.Equal(storedTransition, transition) {
		return errors.New("migration transition does not match")
	}
	if step == MigrationStepFenceAdvanced ||
		step == MigrationStepTargetPrepared {
		return ErrFenceAlreadyAdvanced
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE shard_migration_progress
		SET status = ?, updated_at_ms = ?
		WHERE logical_shard_id = ?
		  AND status = ?
		  AND transition_id = ?`,
		MigrationStatusAbandoned, now.UTC().UnixMilli(),
		shardID, MigrationStatusOpen, transition,
	)
	if err != nil {
		return fmt.Errorf("mark migration abandoned: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return errors.New("migration progress changed concurrently")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit abandoned migration progress: %w", err)
	}
	return nil
}

// DeleteOpenMigrationProgress removes a completed OPEN progress row.
func DeleteOpenMigrationProgress(
	ctx context.Context,
	db *sql.DB,
	shardID uint32,
	transitionID string,
) error {
	if db == nil {
		return errors.New("MySQL database is required")
	}
	if err := validShardID(shardID); err != nil {
		return err
	}
	transition, err := parseUUIDBytes(transitionID)
	if err != nil {
		return fmt.Errorf("parse transition ID: %w", err)
	}
	result, err := db.ExecContext(ctx, `
		DELETE FROM shard_migration_progress
		WHERE logical_shard_id = ?
		  AND status = ?
		  AND transition_id = ?`,
		shardID, MigrationStatusOpen, transition,
	)
	if err != nil {
		return fmt.Errorf("delete migration progress: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read delete migration progress result: %w", err)
	}
	if affected == 0 {
		return errors.New("open migration progress not found")
	}
	return nil
}

// LoadOpenMigrationProgress returns every OPEN migration progress row.
func LoadOpenMigrationProgress(
	ctx context.Context,
	db *sql.DB,
) ([]MigrationProgressRow, error) {
	if db == nil {
		return nil, errors.New("MySQL database is required")
	}
	rows, err := db.QueryContext(ctx, `
		SELECT logical_shard_id, transition_id, status, step,
		       source_zone_id, source_endpoint, source_owner_epoch,
		       source_route_version, source_lease_id,
		       target_zone_id, target_endpoint,
		       prepared_owner_epoch, prepared_route_version,
		       prepared_lease_id, prepared_lease_term,
		       players_json, updated_at_ms
		FROM shard_migration_progress
		WHERE status = ?
		ORDER BY logical_shard_id`,
		MigrationStatusOpen,
	)
	if err != nil {
		return nil, fmt.Errorf("query open migration progress: %w", err)
	}
	defer rows.Close()

	result := make([]MigrationProgressRow, 0)
	for rows.Next() {
		row, scanErr := scanMigrationProgressRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate open migration progress: %w", err)
	}
	return result, nil
}

// LoadMigrationProgress loads one shard progress row when present.
func LoadMigrationProgress(
	ctx context.Context,
	db *sql.DB,
	shardID uint32,
) (MigrationProgressRow, bool, error) {
	if db == nil {
		return MigrationProgressRow{}, false, errors.New("MySQL database is required")
	}
	if err := validShardID(shardID); err != nil {
		return MigrationProgressRow{}, false, err
	}
	row := db.QueryRowContext(ctx, `
		SELECT logical_shard_id, transition_id, status, step,
		       source_zone_id, source_endpoint, source_owner_epoch,
		       source_route_version, source_lease_id,
		       target_zone_id, target_endpoint,
		       prepared_owner_epoch, prepared_route_version,
		       prepared_lease_id, prepared_lease_term,
		       players_json, updated_at_ms
		FROM shard_migration_progress
		WHERE logical_shard_id = ?`,
		shardID,
	)
	progress, err := scanMigrationProgressRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return MigrationProgressRow{}, false, nil
	}
	if err != nil {
		return MigrationProgressRow{}, false, err
	}
	return progress, true, nil
}

// LoadAbandonedPreparedEpoch returns the highest abandoned prepared epoch for
// a shard, if any. Used so a later Prepare does not reuse that epoch.
func LoadAbandonedPreparedEpoch(
	ctx context.Context,
	db *sql.DB,
	shardID uint32,
) (uint64, bool, error) {
	if db == nil {
		return 0, false, errors.New("MySQL database is required")
	}
	if err := validShardID(shardID); err != nil {
		return 0, false, err
	}
	var epoch uint64
	err := db.QueryRowContext(ctx, `
		SELECT prepared_owner_epoch
		FROM shard_migration_progress
		WHERE logical_shard_id = ?
		  AND status = ?`,
		shardID, MigrationStatusAbandoned,
	).Scan(&epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("load abandoned prepared epoch: %w", err)
	}
	return epoch, true, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanMigrationProgressRow(scanner rowScanner) (MigrationProgressRow, error) {
	var row MigrationProgressRow
	var transition []byte
	var playersJSON []byte
	if err := scanner.Scan(
		&row.ShardID, &transition, &row.Status, &row.Step,
		&row.SourceZoneID, &row.SourceEndpoint, &row.SourceOwnerEpoch,
		&row.SourceRouteVersion, &row.SourceLeaseID,
		&row.TargetZoneID, &row.TargetEndpoint,
		&row.PreparedOwnerEpoch, &row.PreparedRouteVersion,
		&row.PreparedLeaseID, &row.PreparedLeaseTerm,
		&playersJSON, &row.UpdatedAtMS,
	); err != nil {
		return MigrationProgressRow{}, err
	}
	row.TransitionID = formatUUIDBytes(transition)
	if len(playersJSON) > 0 {
		if err := json.Unmarshal(playersJSON, &row.Players); err != nil {
			return MigrationProgressRow{}, fmt.Errorf(
				"decode migration players: %w", err,
			)
		}
	}
	return row, nil
}

func validateOpenProgressRow(row MigrationProgressRow) error {
	if err := validShardID(row.ShardID); err != nil {
		return err
	}
	switch row.Step {
	case MigrationStepPreparingCommitted,
		MigrationStepDrained,
		MigrationStepFenceAdvanced,
		MigrationStepTargetPrepared:
	default:
		return fmt.Errorf("unsupported migration step %q", row.Step)
	}
	if strings.TrimSpace(row.TransitionID) == "" ||
		strings.TrimSpace(row.SourceZoneID) == "" ||
		strings.TrimSpace(row.SourceEndpoint) == "" ||
		strings.TrimSpace(row.TargetZoneID) == "" ||
		strings.TrimSpace(row.TargetEndpoint) == "" ||
		strings.TrimSpace(row.SourceLeaseID) == "" ||
		strings.TrimSpace(row.PreparedLeaseID) == "" ||
		row.SourceOwnerEpoch == 0 ||
		row.PreparedOwnerEpoch == 0 ||
		row.SourceRouteVersion == 0 ||
		row.PreparedRouteVersion == 0 {
		return errors.New("migration progress row is incomplete")
	}
	return nil
}

func formatUUIDBytes(value []byte) string {
	if len(value) != 16 {
		return ""
	}
	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		value[0:4],
		value[4:6],
		value[6:8],
		value[8:10],
		value[10:16],
	)
}

// FormatUUIDBytes converts a 16-byte durable identity to canonical text.
func FormatUUIDBytes(value []byte) string { return formatUUIDBytes(value) }
