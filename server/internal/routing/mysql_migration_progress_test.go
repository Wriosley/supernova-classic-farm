package routing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestUpsertAndLoadOpenMigrationProgress(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	row := MigrationProgressRow{
		ShardID: 17, TransitionID: "00112233-4455-6677-8899-aabbccddeeff",
		Step: MigrationStepDrained, SourceZoneID: "zone-a",
		SourceEndpoint: "http://127.0.0.1:8082", SourceOwnerEpoch: 1,
		SourceRouteVersion: 1, SourceLeaseID: "11111111-1111-4111-8111-111111111111",
		TargetZoneID: "zone-b", TargetEndpoint: "http://127.0.0.1:8084",
		PreparedOwnerEpoch: 2, PreparedRouteVersion: 2,
		PreparedLeaseID: "22222222-2222-4222-8222-222222222222",
		PreparedLeaseTerm: 1,
		Players: []MigrationPlayer{{
			PlayerID: "9", OwnerEpoch: "1", CheckpointRevision: "4",
		}},
		UpdatedAtMS: 1,
	}
	transition, err := parseUUIDBytes(row.TransitionID)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec(`(?s)INSERT INTO shard_migration_progress.*ON DUPLICATE KEY UPDATE`).
		WithArgs(
			uint32(17), transition, MigrationStatusOpen, MigrationStepDrained,
			"zone-a", "http://127.0.0.1:8082", uint64(1), uint64(1),
			row.SourceLeaseID, "zone-b", "http://127.0.0.1:8084",
			uint64(2), uint64(2), row.PreparedLeaseID, uint64(1),
			sqlmock.AnyArg(), int64(1),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := UpsertOpenMigrationProgress(context.Background(), db, row); err != nil {
		t.Fatal(err)
	}

	playersJSON := []byte(
		`[{"player_id":"9","owner_epoch":"1","checkpoint_revision":"4"}]`,
	)
	mock.ExpectQuery(`(?s)SELECT logical_shard_id.*WHERE status`).
		WithArgs(MigrationStatusOpen).
		WillReturnRows(sqlmock.NewRows([]string{
			"logical_shard_id", "transition_id", "status", "step",
			"source_zone_id", "source_endpoint", "source_owner_epoch",
			"source_route_version", "source_lease_id",
			"target_zone_id", "target_endpoint",
			"prepared_owner_epoch", "prepared_route_version",
			"prepared_lease_id", "prepared_lease_term",
			"players_json", "updated_at_ms",
		}).AddRow(
			uint32(17), transition, MigrationStatusOpen, MigrationStepDrained,
			"zone-a", "http://127.0.0.1:8082", uint64(1), uint64(1),
			row.SourceLeaseID, "zone-b", "http://127.0.0.1:8084",
			uint64(2), uint64(2), row.PreparedLeaseID, uint64(1),
			playersJSON, int64(1),
		))
	loaded, err := LoadOpenMigrationProgress(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 ||
		loaded[0].TransitionID != row.TransitionID ||
		loaded[0].Step != MigrationStepDrained ||
		len(loaded[0].Players) != 1 ||
		loaded[0].Players[0].PlayerID != "9" {
		t.Fatalf("unexpected loaded progress: %+v", loaded)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMarkMigrationAbandonedRefusesAfterFence(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	transition, err := parseUUIDBytes("00112233-4455-6677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT status, step, transition_id.*FOR UPDATE`).
		WithArgs(uint32(17)).
		WillReturnRows(sqlmock.NewRows([]string{
			"status", "step", "transition_id",
		}).AddRow(MigrationStatusOpen, MigrationStepFenceAdvanced, transition))
	mock.ExpectRollback()
	err = MarkMigrationAbandoned(
		context.Background(), db, 17,
		"00112233-4455-6677-8899-aabbccddeeff",
		time.UnixMilli(1).UTC(),
	)
	if !errors.Is(err, ErrFenceAlreadyAdvanced) {
		t.Fatalf("error = %v, want ErrFenceAlreadyAdvanced", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteOpenMigrationProgress(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	transition, err := parseUUIDBytes("00112233-4455-6677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec(`(?s)DELETE FROM shard_migration_progress`).
		WithArgs(uint32(17), MigrationStatusOpen, transition).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := DeleteOpenMigrationProgress(
		context.Background(), db, 17,
		"00112233-4455-6677-8899-aabbccddeeff",
	); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
