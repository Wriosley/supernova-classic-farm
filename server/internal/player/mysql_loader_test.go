package player

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
)

func TestMySQLCheckpointLoaderVerifiesEnvelopeAndRestoresState(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	checkpoint := NewInitialCheckpoint(42, time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC))
	blob, digest, err := MarshalCheckpoint(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	rows := sqlmock.NewRows([]string{
		"logical_shard_id", "owner_epoch", "player_seq", "checkpoint_revision",
		"checkpoint_schema_version", "checkpoint_blob", "checkpoint_sha256",
		"last_applied_config_version", "created_at_ms", "updated_at_ms",
	}).AddRow(
		checkpoint.LogicalShardId, checkpoint.OwnerEpoch, checkpoint.PlayerSeq,
		checkpoint.CheckpointRevision, checkpoint.SchemaVersion, blob, digest[:],
		checkpoint.LastAppliedConfigVersion, checkpoint.CreatedAtMs, checkpoint.UpdatedAtMs,
	)
	mock.ExpectQuery(`(?s)SELECT logical_shard_id`).
		WithArgs(uint64(42)).
		WillReturnRows(rows)

	state, err := (&MySQLCheckpointLoader{DB: db}).Load(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if state.PlayerID != 42 || state.Coins != InitialCoinBalance || state.Inventory[BasicFertilizerID] != 1 {
		t.Fatalf("unexpected restored state: %+v", state)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMySQLCheckpointLoaderRejectsEnvelopeBlobMismatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	checkpoint := NewInitialCheckpoint(42, time.Now())
	blob, digest, err := MarshalCheckpoint(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	rows := sqlmock.NewRows([]string{
		"logical_shard_id", "owner_epoch", "player_seq", "checkpoint_revision",
		"checkpoint_schema_version", "checkpoint_blob", "checkpoint_sha256",
		"last_applied_config_version", "created_at_ms", "updated_at_ms",
	}).AddRow(
		checkpoint.LogicalShardId, checkpoint.OwnerEpoch+1, checkpoint.PlayerSeq,
		checkpoint.CheckpointRevision, checkpoint.SchemaVersion, blob, digest[:],
		checkpoint.LastAppliedConfigVersion, checkpoint.CreatedAtMs, checkpoint.UpdatedAtMs,
	)
	mock.ExpectQuery(`(?s)SELECT logical_shard_id`).
		WithArgs(uint64(42)).
		WillReturnRows(rows)

	if _, err := (&MySQLCheckpointLoader{DB: db}).Load(context.Background(), 42); err == nil {
		t.Fatal("envelope/blob mismatch was accepted")
	}
}

func TestMySQLCheckpointWriterChecksFenceAndCheckpointCAS(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	checkpoint := NewInitialCheckpoint(42, time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC))
	checkpoint.PlayerSeq = 1
	checkpoint.CheckpointRevision = 2
	checkpoint.CoinBalance = 4
	checkpoint.UpdatedAtMs++

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT owner_zone_id, owner_epoch.*FROM shard_fences`).
		WithArgs(checkpoint.LogicalShardId).
		WillReturnRows(sqlmock.NewRows([]string{"owner_zone_id", "owner_epoch"}).
			AddRow("zone-b", LocalOwnerEpoch))
	mock.ExpectExec(`(?s)UPDATE player_checkpoints`).
		WithArgs(
			checkpoint.LogicalShardId, checkpoint.OwnerEpoch, checkpoint.PlayerSeq,
			checkpoint.CheckpointRevision, checkpoint.SchemaVersion,
			sqlmock.AnyArg(), sqlmock.AnyArg(), checkpoint.LastAppliedConfigVersion,
			checkpoint.UpdatedAtMs, checkpoint.PlayerId, uint64(1),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := (&MySQLCheckpointLoader{
		DB: db, OwnerZoneID: "zone-b",
	}).Save(context.Background(), checkpoint, 1); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMySQLCheckpointWriterRejectsWrongZoneFence(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	checkpoint := NewInitialCheckpoint(
		42, time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC),
	)
	checkpoint.PlayerSeq = 1
	checkpoint.CheckpointRevision = 2
	checkpoint.UpdatedAtMs++

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT owner_zone_id, owner_epoch.*FROM shard_fences`).
		WithArgs(checkpoint.LogicalShardId).
		WillReturnRows(
			sqlmock.NewRows([]string{"owner_zone_id", "owner_epoch"}).
				AddRow("zone-a", checkpoint.OwnerEpoch),
		)
	mock.ExpectRollback()

	err = (&MySQLCheckpointLoader{
		DB: db, OwnerZoneID: "zone-b",
	}).Save(context.Background(), checkpoint, 1)
	if !errors.Is(err, ErrCheckpointFenced) {
		t.Fatalf("Save error = %v, want ErrCheckpointFenced", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMySQLCheckpointWriterCommitsOutboxWithCheckpoint(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 31, 11, 0, 0, 0, time.UTC)
	checkpoint := NewInitialCheckpoint(42, now)
	checkpoint.PlayerSeq = 1
	checkpoint.CheckpointRevision = 2
	checkpoint.UpdatedAtMs++
	chapter, ok := NewDevelopmentConfigSnapshot().Chapter(InitialChapterID)
	if !ok {
		t.Fatal("development chapter missing")
	}
	requestID := []byte{
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
		0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
	}
	pending, err := buildRewardMailOutbox(
		42, requestID, chapter,
		[]*wsv1.ItemStackView{{ItemId: developmentNextSeedItemID, Quantity: 3}},
		LocalOwnerEpoch, 1, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.PendingOutbox = []*datav1.PendingOutboxRecord{pending}

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT owner_zone_id, owner_epoch.*FROM shard_fences`).
		WithArgs(checkpoint.LogicalShardId).
		WillReturnRows(sqlmock.NewRows([]string{"owner_zone_id", "owner_epoch"}).
			AddRow(DefaultZoneID, LocalOwnerEpoch))
	mock.ExpectQuery(`(?s)SELECT db_shard_id, aggregate_player_id.*FROM player_outbox`).
		WithArgs(pending.EventId).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(`(?s)INSERT INTO player_outbox`).
		WithArgs(
			pending.EventId, checkpoint.PlayerId, checkpoint.LogicalShardId,
			uint32(pending.EventType), pending.EventContractVersion,
			pending.CausedByRequestId, pending.CreatedOwnerEpoch,
			pending.CreatedPlayerSeq, pending.CreatedAtMs,
			pending.Payload, pending.PayloadSha256, pending.CreatedAtMs,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`(?s)UPDATE player_checkpoints`).
		WithArgs(
			checkpoint.LogicalShardId, checkpoint.OwnerEpoch, checkpoint.PlayerSeq,
			checkpoint.CheckpointRevision, checkpoint.SchemaVersion,
			sqlmock.AnyArg(), sqlmock.AnyArg(), checkpoint.LastAppliedConfigVersion,
			checkpoint.UpdatedAtMs, checkpoint.PlayerId, uint64(1),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := (&MySQLCheckpointLoader{DB: db}).Save(
		context.Background(), checkpoint, 1,
	); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
