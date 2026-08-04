package auth

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestMySQLRegistrationCommitsAccountCheckpointAndSessionTogether(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	store, err := NewMySQLStore(db)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now }

	mock.ExpectBegin()
	mock.ExpectExec(`(?s)INSERT INTO accounts`).
		WithArgs(
			"durable_farmer", sqlmock.AnyArg(), sqlmock.AnyArg(),
			argonMemoryKiB, argonTime, argonThreads, uint32(0x13),
			now.UnixMilli(), now.UnixMilli(),
		).
		WillReturnResult(sqlmock.NewResult(42, 1))
	mock.ExpectQuery(`(?s)SELECT owner_zone_id, owner_epoch.*FROM shard_fences`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(
			sqlmock.NewRows([]string{"owner_zone_id", "owner_epoch"}).
				AddRow("zone-b", uint64(7)),
		)
	mock.ExpectExec(`(?s)INSERT INTO player_checkpoints`).
		WithArgs(
			uint64(42), sqlmock.AnyArg(), uint64(7), uint64(0),
			uint64(1), uint32(1), sqlmock.AnyArg(), sqlmock.AnyArg(),
			uint64(1), now.UnixMilli(), now.UnixMilli(),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)INSERT INTO auth_sessions`).
		WithArgs(
			sqlmock.AnyArg(), uint64(42), uint64(1), now.UnixMilli(),
			now.Add(12*time.Hour).UnixMilli(), now.Add(7*24*time.Hour).UnixMilli(),
			now.UnixMilli(),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	raw, session, err := store.Register("durable_farmer", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if raw == "" || session.PlayerID != 42 || session.Generation != 1 {
		t.Fatalf("unexpected registration result: raw=%t session=%+v", raw != "", session)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMySQLRegistrationRollsBackWhenCheckpointInsertFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewMySQLStore(db)
	if err != nil {
		t.Fatal(err)
	}

	mock.ExpectBegin()
	mock.ExpectExec(`(?s)INSERT INTO accounts`).
		WithArgs(
			"rollback_farmer", sqlmock.AnyArg(), sqlmock.AnyArg(),
			argonMemoryKiB, argonTime, argonThreads, uint32(0x13),
			sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(42, 1))
	mock.ExpectQuery(`(?s)SELECT owner_zone_id, owner_epoch.*FROM shard_fences`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(
			sqlmock.NewRows([]string{"owner_zone_id", "owner_epoch"}).
				AddRow("zone-a", uint64(1)),
		)
	mock.ExpectExec(`(?s)INSERT INTO player_checkpoints`).
		WithArgs(
			uint64(42), sqlmock.AnyArg(), uint64(1), uint64(0),
			uint64(1), uint32(1), sqlmock.AnyArg(), sqlmock.AnyArg(),
			uint64(1), sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnError(errors.New("checkpoint write failed"))
	mock.ExpectRollback()

	if _, _, err := store.Register("rollback_farmer", "correct horse battery staple"); err == nil {
		t.Fatal("registration succeeded despite checkpoint failure")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMySQLRegistrationRollsBackWhenShardFenceIsMissing(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewMySQLStore(db)
	if err != nil {
		t.Fatal(err)
	}

	mock.ExpectBegin()
	mock.ExpectExec(`(?s)INSERT INTO accounts`).
		WithArgs(
			"missing_fence_farmer", sqlmock.AnyArg(), sqlmock.AnyArg(),
			argonMemoryKiB, argonTime, argonThreads, uint32(0x13),
			sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(42, 1))
	mock.ExpectQuery(`(?s)SELECT owner_zone_id, owner_epoch.*FROM shard_fences`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	if _, _, err := store.Register(
		"missing_fence_farmer", "correct horse battery staple",
	); err == nil {
		t.Fatal("registration succeeded without a shard Fence")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
