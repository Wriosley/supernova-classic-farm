package player

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
)

var (
	ErrCheckpointNotFound = errors.New("player checkpoint not found")
	ErrCheckpointConflict = errors.New("player checkpoint compare-and-set conflict")
	ErrCheckpointFenced   = errors.New("player checkpoint owner epoch is fenced")
)

type MySQLCheckpointLoader struct {
	DB *sql.DB
}

func (l *MySQLCheckpointLoader) Load(ctx context.Context, playerID uint64) (*State, error) {
	if l == nil || l.DB == nil {
		return nil, errors.New("MySQL checkpoint loader is not configured")
	}
	var envelope datav1.PlayerCheckpointV1
	var blob []byte
	var digest []byte
	err := l.DB.QueryRowContext(ctx, `
		SELECT logical_shard_id, owner_epoch, player_seq, checkpoint_revision,
		       checkpoint_schema_version, checkpoint_blob, checkpoint_sha256,
		       last_applied_config_version, created_at_ms, updated_at_ms
		FROM player_checkpoints
		WHERE player_id = ?`,
		playerID,
	).Scan(
		&envelope.LogicalShardId, &envelope.OwnerEpoch, &envelope.PlayerSeq,
		&envelope.CheckpointRevision, &envelope.SchemaVersion, &blob, &digest,
		&envelope.LastAppliedConfigVersion, &envelope.CreatedAtMs, &envelope.UpdatedAtMs,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCheckpointNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query player checkpoint: %w", err)
	}
	checkpoint, err := UnmarshalCheckpoint(blob, digest)
	if err != nil {
		return nil, err
	}
	if checkpoint.PlayerId != playerID ||
		checkpoint.LogicalShardId != envelope.LogicalShardId ||
		checkpoint.OwnerEpoch != envelope.OwnerEpoch ||
		checkpoint.PlayerSeq != envelope.PlayerSeq ||
		checkpoint.CheckpointRevision != envelope.CheckpointRevision ||
		checkpoint.SchemaVersion != envelope.SchemaVersion ||
		checkpoint.LastAppliedConfigVersion != envelope.LastAppliedConfigVersion ||
		checkpoint.CreatedAtMs != envelope.CreatedAtMs ||
		checkpoint.UpdatedAtMs != envelope.UpdatedAtMs {
		return nil, errors.New("checkpoint envelope does not match blob")
	}
	return StateFromCheckpoint(checkpoint)
}

func (l *MySQLCheckpointLoader) Save(ctx context.Context, checkpoint *datav1.PlayerCheckpointV1, expectedRevision uint64) error {
	if l == nil || l.DB == nil {
		return errors.New("MySQL checkpoint writer is not configured")
	}
	if err := ValidateCheckpoint(checkpoint); err != nil {
		return err
	}
	blob, digest, err := MarshalCheckpoint(checkpoint)
	if err != nil {
		return err
	}
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin checkpoint flush: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var ownerZoneID string
	var ownerEpoch uint64
	err = tx.QueryRowContext(ctx, `
		SELECT owner_zone_id, owner_epoch
		FROM shard_fences
		WHERE logical_shard_id = ?
		FOR UPDATE`,
		checkpoint.LogicalShardId,
	).Scan(&ownerZoneID, &ownerEpoch)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCheckpointFenced
	}
	if err != nil {
		return fmt.Errorf("read checkpoint fence: %w", err)
	}
	if ownerZoneID != DefaultZoneID || ownerEpoch != checkpoint.OwnerEpoch {
		return ErrCheckpointFenced
	}
	if checkpoint.CheckpointRevision <= expectedRevision {
		return errors.New("dirty checkpoint revision must advance")
	}
	for _, pending := range checkpoint.PendingOutbox {
		if err := persistPendingOutbox(ctx, tx, checkpoint, pending); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE player_checkpoints
		SET logical_shard_id = ?, owner_epoch = ?, player_seq = ?,
		    checkpoint_revision = ?, checkpoint_schema_version = ?,
		    checkpoint_blob = ?, checkpoint_sha256 = ?,
		    last_applied_config_version = ?, updated_at_ms = ?
		WHERE player_id = ? AND checkpoint_revision = ?`,
		checkpoint.LogicalShardId, checkpoint.OwnerEpoch, checkpoint.PlayerSeq,
		checkpoint.CheckpointRevision, checkpoint.SchemaVersion, blob, digest[:],
		checkpoint.LastAppliedConfigVersion, checkpoint.UpdatedAtMs,
		checkpoint.PlayerId, expectedRevision,
	)
	if err != nil {
		return fmt.Errorf("update player checkpoint: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read checkpoint update result: %w", err)
	}
	if rows != 1 {
		return ErrCheckpointConflict
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit checkpoint flush: %w", err)
	}
	return nil
}

func persistPendingOutbox(
	ctx context.Context,
	tx *sql.Tx,
	checkpoint *datav1.PlayerCheckpointV1,
	pending *datav1.PendingOutboxRecord,
) error {
	var (
		dbShardID            uint32
		aggregatePlayerID    uint64
		logicalShardID       uint32
		eventType            uint32
		eventContractVersion uint32
		causedByRequestID    []byte
		createdOwnerEpoch    uint64
		createdPlayerSeq     uint64
		createdAtMS          int64
		payload              []byte
		payloadSHA256        []byte
	)
	err := tx.QueryRowContext(ctx, `
		SELECT db_shard_id, aggregate_player_id, logical_shard_id, event_type,
		       event_contract_version, caused_by_request_id, created_owner_epoch,
		       created_player_seq, created_at_ms, payload, payload_sha256
		FROM player_outbox
		WHERE event_id = ?
		FOR UPDATE`,
		pending.EventId,
	).Scan(
		&dbShardID, &aggregatePlayerID, &logicalShardID, &eventType,
		&eventContractVersion, &causedByRequestID, &createdOwnerEpoch,
		&createdPlayerSeq, &createdAtMS, &payload, &payloadSHA256,
	)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO player_outbox (
			    event_id, db_shard_id, aggregate_player_id, logical_shard_id,
			    event_type, event_contract_version, caused_by_request_id,
			    created_owner_epoch, created_player_seq, created_at_ms,
			    payload, payload_sha256, relay_status, attempt_count,
			    next_attempt_at_ms
			) VALUES (?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 0, ?)`,
			pending.EventId, checkpoint.PlayerId, checkpoint.LogicalShardId,
			uint32(pending.EventType), pending.EventContractVersion,
			pending.CausedByRequestId, pending.CreatedOwnerEpoch,
			pending.CreatedPlayerSeq, pending.CreatedAtMs,
			pending.Payload, pending.PayloadSha256, pending.CreatedAtMs,
		)
		if err != nil {
			return fmt.Errorf("insert pending Outbox row: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read pending Outbox row: %w", err)
	}
	if dbShardID != 0 ||
		aggregatePlayerID != checkpoint.PlayerId ||
		logicalShardID != checkpoint.LogicalShardId ||
		eventType != uint32(pending.EventType) ||
		eventContractVersion != pending.EventContractVersion ||
		!bytes.Equal(causedByRequestID, pending.CausedByRequestId) ||
		createdOwnerEpoch != pending.CreatedOwnerEpoch ||
		createdPlayerSeq != pending.CreatedPlayerSeq ||
		createdAtMS != pending.CreatedAtMs ||
		!bytes.Equal(payload, pending.Payload) ||
		!bytes.Equal(payloadSHA256, pending.PayloadSha256) {
		return errors.New("pending Outbox immutable row conflict")
	}
	return nil
}
