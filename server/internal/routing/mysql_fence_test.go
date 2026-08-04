package routing

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestAdvanceMySQLFenceAppliesAndReplaysPreparedTransition(t *testing.T) {
	prepared := RouteEntry{
		ShardID: 17, OwnerZoneID: "zone-b", OwnerEpoch: 2,
		RouteVersion: 8, State: RouteStatePreparing,
		PreviousOwnerZoneID: "zone-a",
		TransitionID:        "00112233-4455-6677-8899-aabbccddeeff",
	}
	transition, err := parseUUIDBytes(prepared.TransitionID)
	if err != nil {
		t.Fatal(err)
	}
	oldTransition := make([]byte, 16)

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT owner_zone_id.*FROM shard_fences.*FOR UPDATE`).
		WithArgs(uint32(17)).
		WillReturnRows(sqlmock.NewRows([]string{
			"owner_zone_id", "owner_epoch", "route_version", "transition_id",
		}).AddRow("zone-a", uint64(1), uint64(6), oldTransition))
	mock.ExpectExec(`(?s)UPDATE shard_fences.*fenced_at_ms`).
		WithArgs(
			"zone-b", uint64(2), uint64(8), transition, uint32(17),
			"zone-a", uint64(1), uint64(6), oldTransition,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := AdvanceMySQLFence(context.Background(), db, prepared); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, mock, err = sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT owner_zone_id.*FROM shard_fences.*FOR UPDATE`).
		WithArgs(uint32(17)).
		WillReturnRows(sqlmock.NewRows([]string{
			"owner_zone_id", "owner_epoch", "route_version", "transition_id",
		}).AddRow("zone-b", uint64(2), uint64(8), transition))
	mock.ExpectCommit()
	if err := AdvanceMySQLFence(context.Background(), db, prepared); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAdvanceMySQLFenceRejectsConflictingEpoch(t *testing.T) {
	prepared := RouteEntry{
		ShardID: 17, OwnerZoneID: "zone-b", OwnerEpoch: 2,
		RouteVersion: 8, State: RouteStatePreparing,
		PreviousOwnerZoneID: "zone-a",
		TransitionID:        "00112233-4455-6677-8899-aabbccddeeff",
	}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT owner_zone_id.*FROM shard_fences.*FOR UPDATE`).
		WillReturnRows(sqlmock.NewRows([]string{
			"owner_zone_id", "owner_epoch", "route_version", "transition_id",
		}).AddRow("zone-a", uint64(2), uint64(7), make([]byte, 16)))
	mock.ExpectRollback()
	if err := AdvanceMySQLFence(
		context.Background(), db, prepared,
	); !errors.Is(err, ErrFenceConflict) {
		t.Fatalf("AdvanceMySQLFence error = %v, want ErrFenceConflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileStaticMySQLFencesAcceptsAlreadyAlignedMap(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 8, 3, 14, 30, 0, 0, time.UTC)
	routes, err := NewStaticMap(now, time.Minute, []ZoneCandidate{
		{ZoneID: "zone-a", Endpoint: "http://127.0.0.1:8082"},
		{ZoneID: "zone-b", Endpoint: "http://127.0.0.1:8084"},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := routes.Snapshot()
	rows := sqlmock.NewRows([]string{
		"logical_shard_id", "owner_zone_id", "owner_epoch", "route_version",
	})
	for _, entry := range snapshot.Entries {
		rows.AddRow(entry.ShardID, entry.OwnerZoneID, uint64(1), uint64(1))
	}
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT logical_shard_id.*FROM shard_fences.*FOR UPDATE`).
		WillReturnRows(rows)
	mock.ExpectPrepare(regexp.QuoteMeta(`
		UPDATE shard_fences
		SET owner_zone_id = ?, transition_id = ?, fenced_at_ms = ?
		WHERE logical_shard_id = ?
		  AND owner_zone_id = ?
		  AND owner_epoch = 1
		  AND route_version = 1`))
	mock.ExpectCommit()

	updated, err := ReconcileStaticMySQLFences(
		context.Background(), db, snapshot, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated != 0 {
		t.Fatalf("updated = %d, want 0", updated)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileStaticMySQLFencesConvertsLocalBootstrapRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 8, 3, 14, 45, 0, 0, time.UTC)
	routes, err := NewStaticMap(now, time.Minute, []ZoneCandidate{
		{ZoneID: "zone-a", Endpoint: "http://127.0.0.1:8082"},
		{ZoneID: "zone-b", Endpoint: "http://127.0.0.1:8084"},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := routes.Snapshot()
	rows := sqlmock.NewRows([]string{
		"logical_shard_id", "owner_zone_id", "owner_epoch", "route_version",
	})
	for _, entry := range snapshot.Entries {
		rows.AddRow(entry.ShardID, DefaultZoneID, uint64(1), uint64(1))
	}
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT logical_shard_id.*FROM shard_fences.*FOR UPDATE`).
		WillReturnRows(rows)
	prepared := mock.ExpectPrepare(regexp.QuoteMeta(`
		UPDATE shard_fences
		SET owner_zone_id = ?, transition_id = ?, fenced_at_ms = ?
		WHERE logical_shard_id = ?
		  AND owner_zone_id = ?
		  AND owner_epoch = 1
		  AND route_version = 1`))
	for _, entry := range snapshot.Entries {
		prepared.ExpectExec().
			WithArgs(
				entry.OwnerZoneID,
				staticFenceTransitionID(entry.ShardID, entry.OwnerZoneID),
				now.UnixMilli(),
				entry.ShardID,
				DefaultZoneID,
			).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectCommit()

	updated, err := ReconcileStaticMySQLFences(
		context.Background(), db, snapshot, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated != int(ShardCount) {
		t.Fatalf("updated = %d, want %d", updated, ShardCount)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileStaticMySQLFencesRejectsNonBootstrapOwner(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	routes, err := NewStaticMap(now, time.Minute, []ZoneCandidate{
		{ZoneID: "zone-a", Endpoint: "http://127.0.0.1:8082"},
		{ZoneID: "zone-b", Endpoint: "http://127.0.0.1:8084"},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := routes.Snapshot()
	rows := sqlmock.NewRows([]string{
		"logical_shard_id", "owner_zone_id", "owner_epoch", "route_version",
	})
	for _, entry := range snapshot.Entries {
		owner := entry.OwnerZoneID
		if entry.ShardID == 17 {
			owner = "unexpected-zone"
		}
		rows.AddRow(entry.ShardID, owner, uint64(1), uint64(1))
	}
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT logical_shard_id.*FROM shard_fences.*FOR UPDATE`).
		WillReturnRows(rows)
	mock.ExpectRollback()

	if _, err := ReconcileStaticMySQLFences(
		context.Background(), db, snapshot, now,
	); err == nil {
		t.Fatal("non-bootstrap owner was accepted")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStaticFenceTransitionIDIsDeterministicAndDistinct(t *testing.T) {
	first := staticFenceTransitionID(42, "zone-a")
	repeated := staticFenceTransitionID(42, "zone-a")
	other := staticFenceTransitionID(42, "zone-b")
	if string(first) != string(repeated) || string(first) == string(other) ||
		len(first) != 16 {
		t.Fatalf("invalid deterministic IDs: %x %x %x", first, repeated, other)
	}
}
