package visit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
)

type fakeSnapshotBuilder struct {
	mu       sync.Mutex
	snapshot *wsv1.FarmVisitSnapshot
	err      error
	calls    int
}

func (f *fakeSnapshotBuilder) BuildPublicFarmSnapshot(
	_ context.Context, ownerPlayerID, _ uint64,
) (*wsv1.FarmVisitSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if f.snapshot != nil {
		return f.snapshot, nil
	}
	return &wsv1.FarmVisitSnapshot{OwnerPlayerId: ownerPlayerID}, nil
}

type publishedPresence struct {
	ownerPlayerID uint64
	kind          wsv1.FarmPresenceKind
	presence      *wsv1.FarmPresencePush
}

type fakePresencePublisher struct {
	mu        sync.Mutex
	published []publishedPresence
}

func (f *fakePresencePublisher) PublishFarmPresence(
	_ context.Context, ownerPlayerID uint64, presence *wsv1.FarmPresencePush,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, publishedPresence{
		ownerPlayerID: ownerPlayerID,
		kind:          presence.GetKind(),
		presence:      presence,
	})
	return nil
}

func (f *fakePresencePublisher) snapshot() []publishedPresence {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]publishedPresence(nil), f.published...)
}

func newTestOwnerService(t *testing.T, snapshots *fakeSnapshotBuilder, presence *fakePresencePublisher, now func() time.Time) *OwnerService {
	t.Helper()
	svc, err := NewOwnerService(snapshots, presence, now)
	if err != nil {
		t.Fatalf("NewOwnerService: %v", err)
	}
	return svc
}

func TestOwnerServiceListVisitorsWrapsRegistry(t *testing.T) {
	now := time.Unix(1000, 0)
	snapshots := &fakeSnapshotBuilder{}
	presence := &fakePresencePublisher{}
	svc := newTestOwnerService(t, snapshots, presence, func() time.Time { return now })

	if got := svc.ListVisitors(1); len(got) != 0 {
		t.Fatalf("ListVisitors before any visit = %+v, want empty", got)
	}
	if _, _, _, wsErr, err := svc.EnterVisitor(
		context.Background(), 1, 42, 2, "gate-a", "req-1",
	); err != nil || wsErr != nil {
		t.Fatalf("EnterVisitor: wsErr=%+v err=%v", wsErr, err)
	}
	got := svc.ListVisitors(1)
	if len(got) != 1 || got[0].VisitorPlayerID != 2 || got[0].GateID != "gate-a" {
		t.Fatalf("ListVisitors after EnterVisitor = %+v", got)
	}
	if len(svc.ListVisitors(999)) != 0 {
		t.Fatalf("ListVisitors for a different owner should stay empty")
	}
}

func TestOwnerServiceEnterVisitorPublishesEnteredOnce(t *testing.T) {
	now := time.Unix(1000, 0)
	snapshots := &fakeSnapshotBuilder{}
	presence := &fakePresencePublisher{}
	svc := newTestOwnerService(t, snapshots, presence, func() time.Time { return now })

	visitID, expiresAtMs, snapshot, wsErr, err := svc.EnterVisitor(
		context.Background(), 1, 42, 2, "gate-a", "req-1",
	)
	if err != nil || wsErr != nil {
		t.Fatalf("EnterVisitor: wsErr=%+v err=%v", wsErr, err)
	}
	if len(visitID) != 16 || snapshot.GetOwnerPlayerId() != 1 {
		t.Fatalf("unexpected result: visitID=%x snapshot=%+v", visitID, snapshot)
	}
	if expiresAtMs != now.Add(VisitTTL).UnixMilli() {
		t.Fatalf("expires_at_ms = %d, want %d", expiresAtMs, now.Add(VisitTTL).UnixMilli())
	}
	if got := presence.snapshot(); len(got) != 1 || got[0].kind != wsv1.FarmPresenceKind_FARM_VISITOR_ENTERED {
		t.Fatalf("expected one ENTERED presence push, got %+v", got)
	} else if got[0].presence.GetVisitorPlayerId() != 2 {
		t.Fatalf("ENTERED visitor_player_id = %d, want 2", got[0].presence.GetVisitorPlayerId())
	}

	// A retry with the same request_id must not publish a second ENTERED tip.
	if _, _, _, _, err := svc.EnterVisitor(context.Background(), 1, 42, 2, "gate-a", "req-1"); err != nil {
		t.Fatalf("retry EnterVisitor: %v", err)
	}
	if got := presence.snapshot(); len(got) != 1 {
		t.Fatalf("expected retry to skip a duplicate ENTERED push, got %+v", got)
	}
}

func TestOwnerServicePublishesFarmInteractionEvent(t *testing.T) {
	presence := &fakePresencePublisher{}
	svc := newTestOwnerService(t, &fakeSnapshotBuilder{}, presence, time.Now)
	visitorID := uint64(9)
	plotID := uint32(3)
	cropItemID := uint32(1002)
	quantity := uint32(1)
	guardTriggered := true

	svc.PublishFarmEvent(context.Background(), 7, &wsv1.FarmPresencePush{
		Kind:            wsv1.FarmPresenceKind_FARM_CROP_STOLEN,
		VisitorPlayerId: &visitorID,
		PlotId:          &plotID,
		CropItemId:      &cropItemID,
		Quantity:        &quantity,
		GuardTriggered:  &guardTriggered,
	})

	got := presence.snapshot()
	if len(got) != 1 || got[0].ownerPlayerID != 7 {
		t.Fatalf("published = %+v", got)
	}
	event := got[0].presence
	if event.GetOwnerPlayerId() != 7 || event.GetVisitorPlayerId() != 9 ||
		event.GetPlotId() != 3 || event.GetCropItemId() != 1002 ||
		event.GetQuantity() != 1 || !event.GetGuardTriggered() {
		t.Fatalf("farm interaction event = %+v", event)
	}
}

func TestOwnerServiceEnterVisitorDoesNotRegisterOnSnapshotFailure(t *testing.T) {
	now := time.Unix(1000, 0)
	snapshots := &fakeSnapshotBuilder{err: errors.New("actor unavailable")}
	presence := &fakePresencePublisher{}
	svc := newTestOwnerService(t, snapshots, presence, func() time.Time { return now })

	if _, _, _, _, err := svc.EnterVisitor(context.Background(), 1, 42, 2, "gate-a", "req-1"); err == nil {
		t.Fatal("expected EnterVisitor to fail when the snapshot builder fails")
	}
	if got := presence.snapshot(); len(got) != 0 {
		t.Fatalf("expected no presence push on a failed enter, got %+v", got)
	}
	if got := svc.registry.ListVisitors(1); len(got) != 0 {
		t.Fatalf("expected no orphaned visit lease on a failed enter, got %+v", got)
	}
}

func TestOwnerServiceHeartbeatMapsRegistryErrorsToWsErrors(t *testing.T) {
	now := time.Unix(1000, 0)
	svc := newTestOwnerService(t, &fakeSnapshotBuilder{}, &fakePresencePublisher{}, func() time.Time { return now })

	_, _, _, _, err := svc.EnterVisitor(context.Background(), 1, 42, 2, "gate-a", "req-1")
	if err != nil {
		t.Fatalf("EnterVisitor: %v", err)
	}
	visitors := svc.registry.ListVisitors(1)
	visitID := visitors[0].VisitID

	if _, wsErr, err := svc.RefreshVisitorHeartbeat(context.Background(), 1, 2, []byte("wrong-visit-id!!"), "gate-a"); err != nil || wsErr.GetCode() != wsv1.ErrorCode_VISIT_NOT_FOUND {
		t.Fatalf("RefreshVisitorHeartbeat(wrong visit) = wsErr=%+v err=%v", wsErr, err)
	}

	expired := now.Add(VisitTTL + time.Second)
	svc2 := newTestOwnerService(t, &fakeSnapshotBuilder{}, &fakePresencePublisher{}, func() time.Time { return expired })
	svc2.registry = svc.registry
	if _, wsErr, err := svc2.RefreshVisitorHeartbeat(context.Background(), 1, 2, visitID, "gate-a"); err != nil || wsErr.GetCode() != wsv1.ErrorCode_VISIT_EXPIRED {
		t.Fatalf("RefreshVisitorHeartbeat(expired) = wsErr=%+v err=%v", wsErr, err)
	}
}

func TestOwnerServiceExitVisitorPublishesLeftAndMapsNotFound(t *testing.T) {
	now := time.Unix(1000, 0)
	presence := &fakePresencePublisher{}
	svc := newTestOwnerService(t, &fakeSnapshotBuilder{}, presence, func() time.Time { return now })
	_, _, _, _, err := svc.EnterVisitor(context.Background(), 1, 42, 2, "gate-a", "req-1")
	if err != nil {
		t.Fatalf("EnterVisitor: %v", err)
	}
	visitID := svc.registry.ListVisitors(1)[0].VisitID

	if wsErr, err := svc.ExitVisitor(context.Background(), 1, 2, visitID); err != nil || wsErr != nil {
		t.Fatalf("ExitVisitor: wsErr=%+v err=%v", wsErr, err)
	}
	published := presence.snapshot()
	if len(published) != 2 || published[1].kind != wsv1.FarmPresenceKind_FARM_VISITOR_LEFT {
		t.Fatalf("expected ENTERED then LEFT presence pushes, got %+v", published)
	}

	if wsErr, err := svc.ExitVisitor(context.Background(), 1, 2, visitID); err != nil || wsErr.GetCode() != wsv1.ErrorCode_VISIT_NOT_FOUND {
		t.Fatalf("ExitVisitor(already gone) = wsErr=%+v err=%v", wsErr, err)
	}
}

func TestOwnerServiceGetPublicFarmSnapshotRequiresValidVisitWithoutExtendingTTL(t *testing.T) {
	now := time.Unix(1000, 0)
	svc := newTestOwnerService(t, &fakeSnapshotBuilder{}, &fakePresencePublisher{}, func() time.Time { return now })
	_, _, _, _, err := svc.EnterVisitor(context.Background(), 1, 42, 2, "gate-a", "req-1")
	if err != nil {
		t.Fatalf("EnterVisitor: %v", err)
	}
	visitID := svc.registry.ListVisitors(1)[0].VisitID

	if _, wsErr, err := svc.GetPublicFarmSnapshot(context.Background(), 1, 42, 2, visitID); err != nil || wsErr != nil {
		t.Fatalf("GetPublicFarmSnapshot: wsErr=%+v err=%v", wsErr, err)
	}
	if _, wsErr, err := svc.GetPublicFarmSnapshot(context.Background(), 1, 42, 99, visitID); err != nil || wsErr.GetCode() != wsv1.ErrorCode_VISIT_NOT_FOUND {
		t.Fatalf("GetPublicFarmSnapshot(wrong visitor) = wsErr=%+v err=%v", wsErr, err)
	}
}

func TestOwnerServiceEvictExpiredPublishesLeftForEachRemovedVisit(t *testing.T) {
	now := time.Unix(1000, 0)
	presence := &fakePresencePublisher{}
	svc := newTestOwnerService(t, &fakeSnapshotBuilder{}, presence, func() time.Time { return now })
	if _, _, _, _, err := svc.EnterVisitor(context.Background(), 1, 42, 2, "gate-a", "req-1"); err != nil {
		t.Fatalf("EnterVisitor: %v", err)
	}

	svc.now = func() time.Time { return now.Add(VisitTTL + time.Second) }
	svc.evictExpired(context.Background())

	published := presence.snapshot()
	if len(published) != 2 || published[1].kind != wsv1.FarmPresenceKind_FARM_VISITOR_LEFT {
		t.Fatalf("expected ENTERED then LEFT after eviction, got %+v", published)
	}
	if got := svc.registry.ListVisitors(1); len(got) != 0 {
		t.Fatalf("expected the expired visit to be gone, got %+v", got)
	}
}

func TestOwnerServiceRunEvictionLoopStopsOnContextCancel(t *testing.T) {
	svc := newTestOwnerService(t, &fakeSnapshotBuilder{}, &fakePresencePublisher{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.RunEvictionLoop(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunEvictionLoop did not stop after context cancellation")
	}
}

func TestOwnerServiceRequiresDependencies(t *testing.T) {
	if _, err := NewOwnerService(nil, &fakePresencePublisher{}, nil); err == nil {
		t.Fatal("expected an error for a nil snapshot builder")
	}
	if _, err := NewOwnerService(&fakeSnapshotBuilder{}, nil, nil); err == nil {
		t.Fatal("expected an error for a nil presence publisher")
	}
}
