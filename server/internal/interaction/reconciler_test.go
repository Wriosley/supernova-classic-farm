package interaction

import (
	"context"
	"testing"
	"time"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	"github.com/Wriosley/supernova-classic-farm/server/internal/testtcaplus"
)

// stubResolver rebuilds every incoming record's StealRequest from the fixed
// test topology (single owner/visitor/plot/crop), exactly like
// cmd/zone would resolve OwnerRoute and CropItemID/Quantity from live
// routing/config state in production.
type stubResolver struct {
	visitorOwnerEpoch uint64
	cropItemID        uint32
	quantity          uint32
}

func (r *stubResolver) ResolveSteal(_ context.Context, record *tcaplusv1.FriendInteraction) (StealRequest, error) {
	cropItemID := record.GetCropItemId()
	quantity := record.GetQuantity()
	if cropItemID == 0 {
		cropItemID = r.cropItemID
	}
	if quantity == 0 {
		quantity = r.quantity
	}
	epoch := record.GetFarmViewEpoch()
	if len(epoch) == 0 {
		epoch = append([]byte(nil), testFarmViewEpoch...)
	}
	seq := record.GetFarmViewSeq()
	if seq == 0 {
		seq = testFarmViewSeq
	}
	return StealRequest{
		InteractionID:     record.InteractionId,
		VisitorPlayerID:   record.VisitorPlayerId,
		VisitorOwnerEpoch: r.visitorOwnerEpoch,
		OwnerPlayerID:     record.OwnerPlayerId,
		OwnerRoute:        dummyOwnerRoute(),
		VisitID:           record.VisitId,
		PlotID:            record.PlotId,
		CropItemID:        cropItemID,
		Quantity:          quantity,
		FarmViewEpoch:     append([]byte(nil), epoch...),
		FarmViewSeq:       seq,
	}, nil
}

func TestReconcilerReconcilesCrashedInteractionToCompletion(t *testing.T) {
	client := testtcaplus.New()
	inner, err := NewTcaplusStore(client, 1)
	if err != nil {
		t.Fatalf("NewTcaplusStore: %v", err)
	}
	flaky := newFlakyStore(inner, 1) // crash window B: OWNER_APPLIED never commits

	ownerRuntime, ownerStore := newOwnerRuntime(testOwnerID, testPlotID)
	visitorRuntime, visitorStore := newVisitorRuntime(testVisitorID)
	defer ownerRuntime.Close()
	defer visitorRuntime.Close()

	snap, err := ownerRuntime.BuildPublicFarmSnapshot(context.Background(), testOwnerID, player.LocalOwnerEpoch)
	if err != nil {
		t.Fatalf("BuildPublicFarmSnapshot: %v", err)
	}
	testFarmViewEpoch = append([]byte(nil), snap.GetVersion().GetFarmViewEpoch()...)
	testFarmViewSeq = snap.GetVersion().GetFarmViewSeq()

	ownerClient := newInProcessOwnerClient(ownerRuntime, player.LocalOwnerEpoch)
	saga, err := NewStealSaga(flaky, visitorRuntime, ownerClient)
	if err != nil {
		t.Fatalf("NewStealSaga: %v", err)
	}
	resolver := &stubResolver{visitorOwnerEpoch: player.LocalOwnerEpoch, cropItemID: 4001, quantity: 1}
	reconciler, err := NewReconciler(flaky, saga, resolver, client)
	if err != nil {
		t.Fatalf("NewReconciler: %v", err)
	}

	ctx := context.Background()
	now := time.Now()
	id := fixedVisitID(0x08)
	if _, err := saga.Execute(ctx, testStealRequest(id), now); err == nil {
		t.Fatalf("expected the primed Update failure to surface an error")
	}
	record, _, err := flaky.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get after simulated crash: %v", err)
	}
	if record.Status != tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_VISITOR_RESERVED {
		t.Fatalf("expected the record to remain VISITOR_RESERVED after the crash, got %v", record.Status)
	}

	if err := reconciler.ReconcileDue(ctx, now); err != nil {
		t.Fatalf("ReconcileDue: %v", err)
	}

	final, _, err := flaky.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get after reconcile: %v", err)
	}
	if final.Status != tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_COMPLETED {
		t.Fatalf("expected the Reconciler to drive the interaction to COMPLETED, got %v", final.Status)
	}
	plot := ownerStore.plotSnapshot(testPlotID)
	if plot.StealCount != 1 {
		t.Fatalf("expected exactly one owner mutation, got %+v", plot)
	}
	if visitorStore.inventoryQuantity(4001) != 1 {
		t.Fatalf("expected exactly 1 crop credited, got %d", visitorStore.inventoryQuantity(4001))
	}

	// Reconciling again after COMPLETED must be a no-op.
	if err := reconciler.ReconcileDue(ctx, now); err != nil {
		t.Fatalf("second ReconcileDue: %v", err)
	}
	if plot := ownerStore.plotSnapshot(testPlotID); plot.StealCount != 1 {
		t.Fatalf("expected reconcile-after-COMPLETED to be a no-op, got %+v", plot)
	}
}

func TestReconcilerSkipsRecordsNotYetDue(t *testing.T) {
	client := testtcaplus.New()
	store, err := NewTcaplusStore(client, 1)
	if err != nil {
		t.Fatalf("NewTcaplusStore: %v", err)
	}
	ownerRuntime, _ := newOwnerRuntime(testOwnerID, testPlotID)
	visitorRuntime, _ := newVisitorRuntime(testVisitorID)
	defer ownerRuntime.Close()
	defer visitorRuntime.Close()
	snap, err := ownerRuntime.BuildPublicFarmSnapshot(context.Background(), testOwnerID, player.LocalOwnerEpoch)
	if err != nil {
		t.Fatalf("BuildPublicFarmSnapshot: %v", err)
	}
	testFarmViewEpoch = append([]byte(nil), snap.GetVersion().GetFarmViewEpoch()...)
	testFarmViewSeq = snap.GetVersion().GetFarmViewSeq()
	ownerClient := newInProcessOwnerClient(ownerRuntime, player.LocalOwnerEpoch)
	saga, err := NewStealSaga(store, visitorRuntime, ownerClient)
	if err != nil {
		t.Fatalf("NewStealSaga: %v", err)
	}
	resolver := &stubResolver{visitorOwnerEpoch: player.LocalOwnerEpoch, cropItemID: 4001, quantity: 1}
	reconciler, err := NewReconciler(store, saga, resolver, client)
	if err != nil {
		t.Fatalf("NewReconciler: %v", err)
	}

	ctx := context.Background()
	now := time.Now()
	ownerClient.failNextCall(nil)
	id := fixedVisitID(0x09)
	if _, err := saga.Execute(ctx, testStealRequest(id), now); err == nil {
		t.Fatalf("expected the transport failure to surface an error")
	}
	record, _, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if record.RetryAtMs <= now.UnixMilli() {
		t.Fatalf("expected retry_at_ms to be scheduled in the future, got %d vs now=%d",
			record.RetryAtMs, now.UnixMilli())
	}

	// Reconciling right now (before retry_at_ms) must not touch the record.
	if err := reconciler.ReconcileDue(ctx, now); err != nil {
		t.Fatalf("ReconcileDue: %v", err)
	}
	untouched, _, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get after premature reconcile: %v", err)
	}
	if untouched.Status != tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_VISITOR_RESERVED {
		t.Fatalf("expected the not-yet-due record to stay untouched, got %v", untouched.Status)
	}

	// Reconciling after retry_at_ms elapses must complete it.
	later := time.UnixMilli(record.RetryAtMs).Add(time.Millisecond)
	if err := reconciler.ReconcileDue(ctx, later); err != nil {
		t.Fatalf("ReconcileDue (due): %v", err)
	}
	completed, _, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get after due reconcile: %v", err)
	}
	if completed.Status != tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_COMPLETED {
		t.Fatalf("expected the due record to complete, got %v", completed.Status)
	}
}
