package routing

import (
	"context"
	"testing"
	"time"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/testtcaplus"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
)

const testUUID = "00112233-4455-6677-8899-aabbccddeeff"

func TestTcaplusStaticFenceBootstrapPreservesAdvancedFence(t *testing.T) {
	now := time.Now()
	routes, err := NewStaticMap(now, time.Minute, []ZoneCandidate{
		{ZoneID: "zone-a", Endpoint: "http://zone-a"},
		{ZoneID: "zone-b", Endpoint: "http://zone-b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	client := testtcaplus.New()
	if err := client.DoInsert(&tcaplusv1.ShardFence{
		LogicalShardId: 17, OwnerZoneId: "zone-b",
		OwnerEpoch: 2, RouteVersion: 8,
		TransitionId: make([]byte, 16),
	}, &option.PBOpt{}, 1); err != nil {
		t.Fatal(err)
	}
	store, _ := NewTcaplusControlStore(client, 1)
	if _, err := store.EnsureStaticFences(
		context.Background(), routes.Snapshot(), now,
	); err != nil {
		t.Fatal(err)
	}
	fence, err := store.LoadFence(context.Background(), 17)
	if err != nil {
		t.Fatal(err)
	}
	if fence.OwnerZoneID != "zone-b" || fence.OwnerEpoch != 2 ||
		fence.RouteVersion != 8 {
		t.Fatalf("advanced fence was changed: %+v", fence)
	}
}

func TestTcaplusFenceAdvanceIsIdempotent(t *testing.T) {
	client := testtcaplus.New()
	if err := client.DoInsert(&tcaplusv1.ShardFence{
		LogicalShardId: 17, OwnerZoneId: "zone-a",
		OwnerEpoch: 1, RouteVersion: 6,
		TransitionId: make([]byte, 16),
	}, &option.PBOpt{}, 1); err != nil {
		t.Fatal(err)
	}
	store, err := NewTcaplusControlStore(client, 1)
	if err != nil {
		t.Fatal(err)
	}
	prepared := RouteEntry{
		ShardID: 17, OwnerZoneID: "zone-b", OwnerEpoch: 2,
		RouteVersion: 8, State: RouteStatePreparing,
		PreviousOwnerZoneID: "zone-a", TransitionID: testUUID,
	}
	if err := store.AdvanceFence(context.Background(), prepared); err != nil {
		t.Fatal(err)
	}
	if err := store.AdvanceFence(context.Background(), prepared); err != nil {
		t.Fatalf("idempotent replay failed: %v", err)
	}
	fence, err := store.LoadFence(context.Background(), 17)
	if err != nil || fence.OwnerZoneID != "zone-b" || fence.OwnerEpoch != 2 {
		t.Fatalf("advanced fence = %+v error=%v", fence, err)
	}
}

func TestTcaplusMigrationProgressLifecycle(t *testing.T) {
	client := testtcaplus.New()
	store, _ := NewTcaplusControlStore(client, 1)
	row := MigrationProgressRow{
		ShardID: 17, TransitionID: testUUID,
		Step:         MigrationStepPreparingCommitted,
		SourceZoneID: "zone-a", SourceEndpoint: "http://127.0.0.1:8082",
		SourceOwnerEpoch: 1, SourceRouteVersion: 6,
		SourceLeaseID: testUUID,
		TargetZoneID:  "zone-b", TargetEndpoint: "http://127.0.0.1:8084",
		PreparedOwnerEpoch: 2, PreparedRouteVersion: 8,
		PreparedLeaseID:   "11112233-4455-6677-8899-aabbccddeeff",
		PreparedLeaseTerm: 1,
		UpdatedAtMS:       time.Now().UnixMilli(),
	}
	if err := store.UpsertProgress(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := store.LoadProgress(context.Background(), 17)
	if err != nil || !found || loaded.Step != row.Step ||
		loaded.TransitionID != row.TransitionID {
		t.Fatalf("loaded progress = %+v found=%v error=%v", loaded, found, err)
	}
	open, err := store.LoadOpenProgress(context.Background())
	if err != nil || len(open) != 1 {
		t.Fatalf("open progress = %+v error=%v", open, err)
	}
	if err := store.MarkAbandoned(
		context.Background(), 17, row.TransitionID, time.Now(),
	); err != nil {
		t.Fatal(err)
	}
	epoch, found, err := store.LoadAbandonedEpoch(context.Background(), 17)
	if err != nil || !found || epoch != 2 {
		t.Fatalf("abandoned epoch=%d found=%v error=%v", epoch, found, err)
	}
}
