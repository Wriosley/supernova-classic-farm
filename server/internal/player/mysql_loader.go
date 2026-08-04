package player

import (
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
