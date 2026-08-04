package routing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrFenceConflict = errors.New("shard fence conflicts with prepared route")

// AdvanceMySQLFence atomically applies one committed PREPARING ownership grant.
// Replaying the exact prepared transition succeeds idempotently.
func AdvanceMySQLFence(
	ctx context.Context,
	db *sql.DB,
	prepared RouteEntry,
) error {
	if db == nil {
		return errors.New("MySQL database is required")
	}
	if prepared.ShardID >= ShardCount ||
		prepared.State != RouteStatePreparing ||
		prepared.OwnerZoneID == "" ||
		prepared.PreviousOwnerZoneID == "" ||
		prepared.OwnerEpoch < 2 ||
		prepared.RouteVersion < 2 ||
		prepared.TransitionID == "" {
		return errors.New("committed PREPARING route is required")
	}
	transitionID, err := parseUUIDBytes(prepared.TransitionID)
	if err != nil {
		return fmt.Errorf("parse transition ID: %w", err)
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin fence advance: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var storedOwner string
	var storedEpoch uint64
	var storedRouteVersion uint64
	var storedTransition []byte
	err = tx.QueryRowContext(ctx, `
		SELECT owner_zone_id, owner_epoch, route_version, transition_id
		FROM shard_fences
		WHERE logical_shard_id = ?
		FOR UPDATE`,
		prepared.ShardID,
	).Scan(
		&storedOwner, &storedEpoch, &storedRouteVersion, &storedTransition,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrFenceConflict
	}
	if err != nil {
		return fmt.Errorf("lock shard fence: %w", err)
	}
	if storedOwner == prepared.OwnerZoneID &&
		storedEpoch == prepared.OwnerEpoch &&
		storedRouteVersion == prepared.RouteVersion &&
		bytes.Equal(storedTransition, transitionID) {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit idempotent fence advance: %w", err)
		}
		return nil
	}
	// Exact +1 is the normal path. A later Prepare after an abandoned
	// PREPARING transition may skip burned epochs, so any higher epoch with
	// the matching previous owner is accepted.
	if storedOwner != prepared.PreviousOwnerZoneID ||
		storedEpoch >= prepared.OwnerEpoch ||
		storedRouteVersion >= prepared.RouteVersion {
		return ErrFenceConflict
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE shard_fences
		SET owner_zone_id = ?, owner_epoch = ?, route_version = ?,
		    transition_id = ?,
		    fenced_at_ms = UNIX_TIMESTAMP(CURRENT_TIMESTAMP(3)) * 1000
		WHERE logical_shard_id = ?
		  AND owner_zone_id = ?
		  AND owner_epoch = ?
		  AND route_version = ?
		  AND transition_id = ?`,
		prepared.OwnerZoneID, prepared.OwnerEpoch, prepared.RouteVersion,
		transitionID, prepared.ShardID, storedOwner, storedEpoch,
		storedRouteVersion, storedTransition,
	)
	if err != nil {
		return fmt.Errorf("advance shard fence: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read fence advance result: %w", err)
	}
	if affected != 1 {
		return ErrFenceConflict
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit fence advance: %w", err)
	}
	return nil
}

func parseUUIDBytes(value string) ([]byte, error) {
	compact := strings.ReplaceAll(value, "-", "")
	if len(compact) != 32 {
		return nil, errors.New("UUID must contain 16 bytes")
	}
	decoded, err := hex.DecodeString(compact)
	if err != nil {
		return nil, errors.New("UUID contains invalid hexadecimal")
	}
	return decoded, nil
}

// ReconcileStaticMySQLFences atomically converts the original zone-local
// bootstrap fences to the committed static dual-Zone assignment. It accepts
// only epoch-one/version-one rows and never advances an existing migration.
func ReconcileStaticMySQLFences(
	ctx context.Context,
	db *sql.DB,
	snapshot Snapshot,
	now time.Time,
) (int, error) {
	if db == nil {
		return 0, errors.New("MySQL database is required")
	}
	if err := validateStaticFenceSnapshot(snapshot); err != nil {
		return 0, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return 0, fmt.Errorf("begin static fence reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
		SELECT logical_shard_id, owner_zone_id, owner_epoch, route_version
		FROM shard_fences
		ORDER BY logical_shard_id
		FOR UPDATE`)
	if err != nil {
		return 0, fmt.Errorf("lock static shard fences: %w", err)
	}
	type fenceRow struct {
		shardID      uint32
		ownerZoneID  string
		ownerEpoch   uint64
		routeVersion uint64
	}
	fences := make([]fenceRow, 0, ShardCount)
	for rows.Next() {
		var fence fenceRow
		if err := rows.Scan(
			&fence.shardID, &fence.ownerZoneID,
			&fence.ownerEpoch, &fence.routeVersion,
		); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan static shard fence: %w", err)
		}
		fences = append(fences, fence)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close static shard fences: %w", err)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate static shard fences: %w", err)
	}
	if len(fences) != int(ShardCount) {
		return 0, fmt.Errorf(
			"expected %d shard fences, got %d", ShardCount, len(fences),
		)
	}
	for index, fence := range fences {
		target := snapshot.Entries[index]
		if fence.shardID != uint32(index) ||
			fence.ownerEpoch != 1 ||
			fence.routeVersion != 1 ||
			(fence.ownerZoneID != DefaultZoneID &&
				fence.ownerZoneID != target.OwnerZoneID) {
			return 0, fmt.Errorf(
				"shard fence %d is not an epoch-one static bootstrap row",
				fence.shardID,
			)
		}
	}

	statement, err := tx.PrepareContext(ctx, `
		UPDATE shard_fences
		SET owner_zone_id = ?, transition_id = ?, fenced_at_ms = ?
		WHERE logical_shard_id = ?
		  AND owner_zone_id = ?
		  AND owner_epoch = 1
		  AND route_version = 1`)
	if err != nil {
		return 0, fmt.Errorf("prepare static fence update: %w", err)
	}
	defer statement.Close()
	updated := 0
	for index, fence := range fences {
		target := snapshot.Entries[index]
		if fence.ownerZoneID == target.OwnerZoneID {
			continue
		}
		result, err := statement.ExecContext(
			ctx,
			target.OwnerZoneID,
			staticFenceTransitionID(target.ShardID, target.OwnerZoneID),
			now.UTC().UnixMilli(),
			target.ShardID,
			DefaultZoneID,
		)
		if err != nil {
			return 0, fmt.Errorf(
				"update static fence %d: %w", target.ShardID, err,
			)
		}
		affected, err := result.RowsAffected()
		if err != nil || affected != 1 {
			return 0, fmt.Errorf(
				"static fence %d changed concurrently", target.ShardID,
			)
		}
		updated++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit static fence reconciliation: %w", err)
	}
	return updated, nil
}

func validateStaticFenceSnapshot(snapshot Snapshot) error {
	if snapshot.ShardCount != ShardCount ||
		snapshot.HashAlgorithmVersion != HashAlgorithmVersion ||
		snapshot.AssignmentAlgorithmVersion != AssignmentAlgorithmVersion ||
		len(snapshot.Entries) != int(ShardCount) {
		return errors.New("static fence snapshot metadata is incompatible")
	}
	for shardID, entry := range snapshot.Entries {
		if entry.ShardID != uint32(shardID) ||
			entry.OwnerZoneID == "" ||
			entry.OwnerEpoch != 1 ||
			entry.RouteVersion != 1 ||
			entry.State != RouteStateActive {
			return fmt.Errorf("route %d is not an initial static assignment", shardID)
		}
	}
	return nil
}

func staticFenceTransitionID(shardID uint32, zoneID string) []byte {
	hash := sha256.New()
	_, _ = hash.Write([]byte("classic-farm-static-fence-v1"))
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], shardID)
	_, _ = hash.Write(encoded[:])
	_, _ = hash.Write([]byte(zoneID))
	value := append([]byte(nil), hash.Sum(nil)[:16]...)
	value[6] = (value[6] & 0x0f) | 0x50
	value[8] = (value[8] & 0x3f) | 0x80
	return value
}
