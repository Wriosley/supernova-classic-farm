package main

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func TestContinueResumesPersistedPreparingMigration(t *testing.T) {
	var drainCompleted atomic.Bool
	var selectedShardID uint32
	oldZone := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		if strings.HasSuffix(r.URL.Path, "/drain-complete") {
			drainCompleted.Store(true)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"shard_id":` +
				strconv.FormatUint(uint64(selectedShardID), 10) +
				`,"owner_epoch":"1","players":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer oldZone.Close()
	var preparedTarget atomic.Bool
	newZone := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		switch {
		case r.URL.Path == "/readyz":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/internal/v1/ownership/refresh":
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/prepare"):
			preparedTarget.Store(true)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer newZone.Close()

	now := time.Date(2026, 8, 3, 16, 30, 0, 0, time.UTC)
	zones := []routing.ZoneCandidate{
		{ZoneID: "zone-a", Endpoint: oldZone.URL},
		{ZoneID: "zone-b", Endpoint: newZone.URL},
	}
	routes, err := routing.NewStaticMap(now, time.Minute, zones)
	if err != nil {
		t.Fatal(err)
	}
	shardID := coordinatorShardOwnedBy(t, routes, "zone-a")
	selectedShardID = shardID
	source, err := routes.Entry(shardID)
	if err != nil {
		t.Fatal(err)
	}
	prepared := routing.RouteEntry{
		ShardID: shardID, OwnerZoneID: "zone-b", OwnerEndpoint: newZone.URL,
		OwnerEpoch: 2, RouteVersion: 2, State: routing.RouteStatePreparing,
		LeaseTerm: 1, LeaseID: "22222222-2222-4222-8222-222222222222",
		LeaseExpiresAt: now.Add(time.Minute), PreviousOwnerZoneID: "zone-a",
		TransitionID: "00112233-4455-6677-8899-aabbccddeeff", UpdatedAt: now,
	}
	if err := routes.RestorePreparing(prepared); err != nil {
		t.Fatal(err)
	}

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	transition, err := parseTestUUID(prepared.TransitionID)
	if err != nil {
		t.Fatal(err)
	}
	handler := newMigrationHandler(
		routes, zones, http.DefaultClient, func() time.Time { return now }, time.Minute,
	)
	handler.db = db
	handler.progress[shardID] = &migrationProgress{
		Prepared: prepared, Source: source,
		Step: routing.MigrationStepPreparingCommitted,
	}
	var fenced atomic.Bool
	handler.advanceFence = func(_ context.Context, entry routing.RouteEntry) error {
		fenced.Store(true)
		return nil
	}
	expectProgressUpsert(mock, transition, routing.MigrationStepDrained)
	expectProgressUpsert(mock, transition, routing.MigrationStepFenceAdvanced)
	expectProgressUpsert(mock, transition, routing.MigrationStepTargetPrepared)
	mock.ExpectExec(`(?s)DELETE FROM shard_migration_progress`).
		WithArgs(shardID, routing.MigrationStatusOpen, transition).
		WillReturnResult(sqlmock.NewResult(0, 1))

	request := httptest.NewRequest(
		http.MethodPost,
		"/internal/v1/shards/"+strconv.FormatUint(uint64(shardID), 10)+
			"/migration/continue",
		nil,
	)
	request.SetPathValue("shard_id", strconv.FormatUint(uint64(shardID), 10))
	request.RemoteAddr = "127.0.0.1:45678"
	response := httptest.NewRecorder()
	handler.continueMigration(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("continue status=%d body=%s", response.Code, response.Body.String())
	}
	entry, err := routes.Route(shardID, now)
	if err != nil {
		t.Fatal(err)
	}
	if !drainCompleted.Load() || !fenced.Load() || !preparedTarget.Load() ||
		entry.OwnerZoneID != "zone-b" || entry.OwnerEpoch != 2 {
		t.Fatalf("continue incomplete: drain=%v fence=%v prepare=%v route=%+v",
			drainCompleted.Load(), fenced.Load(), preparedTarget.Load(), entry)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAbandonBeforeFenceRestoresSourceActive(t *testing.T) {
	var resumed atomic.Bool
	oldZone := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		if strings.HasSuffix(r.URL.Path, "/resume") {
			resumed.Store(true)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	defer oldZone.Close()
	newZone := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		http.NotFound(w, r)
	}))
	defer newZone.Close()

	now := time.Date(2026, 8, 3, 16, 45, 0, 0, time.UTC)
	zones := []routing.ZoneCandidate{
		{ZoneID: "zone-a", Endpoint: oldZone.URL},
		{ZoneID: "zone-b", Endpoint: newZone.URL},
	}
	routes, err := routing.NewStaticMap(now, time.Minute, zones)
	if err != nil {
		t.Fatal(err)
	}
	shardID := coordinatorShardOwnedBy(t, routes, "zone-a")
	source, err := routes.Entry(shardID)
	if err != nil {
		t.Fatal(err)
	}
	prepared := routing.RouteEntry{
		ShardID: shardID, OwnerZoneID: "zone-b", OwnerEndpoint: newZone.URL,
		OwnerEpoch: 2, RouteVersion: 2, State: routing.RouteStatePreparing,
		LeaseTerm: 1, LeaseID: "22222222-2222-4222-8222-222222222222",
		LeaseExpiresAt: now.Add(time.Minute), PreviousOwnerZoneID: "zone-a",
		TransitionID: "00112233-4455-6677-8899-aabbccddeeff", UpdatedAt: now,
	}
	if err := routes.RestorePreparing(prepared); err != nil {
		t.Fatal(err)
	}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	transition, err := parseTestUUID(prepared.TransitionID)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT status, step, transition_id.*FOR UPDATE`).
		WithArgs(shardID).
		WillReturnRows(sqlmock.NewRows([]string{
			"status", "step", "transition_id",
		}).AddRow(
			routing.MigrationStatusOpen,
			routing.MigrationStepPreparingCommitted,
			transition,
		))
	mock.ExpectExec(`(?s)UPDATE shard_migration_progress SET status`).
		WithArgs(
			routing.MigrationStatusAbandoned, now.UnixMilli(),
			shardID, routing.MigrationStatusOpen, transition,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	handler := newMigrationHandler(
		routes, zones, http.DefaultClient, func() time.Time { return now }, time.Minute,
	)
	handler.db = db
	handler.advanceFence = func(context.Context, routing.RouteEntry) error {
		return nil
	}
	handler.progress[shardID] = &migrationProgress{
		Prepared: prepared, Source: source,
		Step: routing.MigrationStepPreparingCommitted,
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/internal/v1/shards/"+strconv.FormatUint(uint64(shardID), 10)+
			"/migration/abandon",
		nil,
	)
	request.SetPathValue("shard_id", strconv.FormatUint(uint64(shardID), 10))
	request.RemoteAddr = "127.0.0.1:45678"
	response := httptest.NewRecorder()
	handler.abandonMigration(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("abandon status=%d body=%s", response.Code, response.Body.String())
	}
	entry, err := routes.Route(shardID, now)
	if err != nil {
		t.Fatal(err)
	}
	if !resumed.Load() || entry.OwnerZoneID != "zone-a" || entry.OwnerEpoch != 1 {
		t.Fatalf("abandon restore failed: resumed=%v route=%+v", resumed.Load(), entry)
	}
	next, err := routes.Prepare(
		shardID, "zone-b", newZone.URL, now, time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if next.OwnerEpoch != 3 {
		t.Fatalf("abandoned epoch was reused: %+v", next)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAbandonAfterFenceIsRejected(t *testing.T) {
	now := time.Date(2026, 8, 3, 16, 50, 0, 0, time.UTC)
	zones := []routing.ZoneCandidate{
		{ZoneID: "zone-a", Endpoint: "http://127.0.0.1:8082"},
		{ZoneID: "zone-b", Endpoint: "http://127.0.0.1:8084"},
	}
	routes, err := routing.NewStaticMap(now, time.Minute, zones)
	if err != nil {
		t.Fatal(err)
	}
	shardID := coordinatorShardOwnedBy(t, routes, "zone-a")
	source, err := routes.Entry(shardID)
	if err != nil {
		t.Fatal(err)
	}
	handler := newMigrationHandler(
		routes, zones, http.DefaultClient, func() time.Time { return now }, time.Minute,
	)
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler.db = db
	handler.advanceFence = func(context.Context, routing.RouteEntry) error {
		return nil
	}
	handler.progress[shardID] = &migrationProgress{
		Prepared: routing.RouteEntry{
			ShardID: shardID, OwnerZoneID: "zone-b",
			OwnerEndpoint: "http://127.0.0.1:8084", OwnerEpoch: 2,
			TransitionID: "00112233-4455-6677-8899-aabbccddeeff",
		},
		Source: source,
		Step:   routing.MigrationStepFenceAdvanced,
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/internal/v1/shards/"+strconv.FormatUint(uint64(shardID), 10)+
			"/migration/abandon",
		nil,
	)
	request.SetPathValue("shard_id", strconv.FormatUint(uint64(shardID), 10))
	request.RemoteAddr = "127.0.0.1:45678"
	response := httptest.NewRecorder()
	handler.abandonMigration(response, request)
	if response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), "FENCE_ALREADY_ADVANCED") {
		t.Fatalf("abandon status=%d body=%s", response.Code, response.Body.String())
	}
}

func expectProgressUpsert(
	mock sqlmock.Sqlmock,
	transition []byte,
	step string,
) {
	mock.ExpectExec(`(?s)INSERT INTO shard_migration_progress.*ON DUPLICATE KEY UPDATE`).
		WithArgs(
			sqlmock.AnyArg(), transition, routing.MigrationStatusOpen, step,
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
}

func parseTestUUID(value string) ([]byte, error) {
	return hex.DecodeString(strings.ReplaceAll(value, "-", ""))
}
