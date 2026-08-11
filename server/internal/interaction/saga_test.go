package interaction

import (
	"context"
	"errors"
	"testing"
	"time"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	"github.com/Wriosley/supernova-classic-farm/server/internal/testtcaplus"
)

const (
	testOwnerID   = uint64(11)
	testVisitorID = uint64(7)
	testPlotID    = uint32(1)
)

var (
	testFarmViewEpoch []byte
	testFarmViewSeq   uint64
)

func newTestFixture(t *testing.T, failAtUpdateIndices ...int) (
	*StealSaga, *playerMemStore, *playerMemStore, *inProcessOwnerClient, Store,
) {
	t.Helper()
	client := testtcaplus.New()
	inner, err := NewTcaplusStore(client, 1)
	if err != nil {
		t.Fatalf("NewTcaplusStore: %v", err)
	}
	var store Store = inner
	if len(failAtUpdateIndices) > 0 {
		store = newFlakyStore(inner, failAtUpdateIndices...)
	}

	ownerRuntime, ownerStore := newOwnerRuntime(testOwnerID, testPlotID)
	visitorRuntime, visitorStore := newVisitorRuntime(testVisitorID)
	t.Cleanup(func() {
		ownerRuntime.Close()
		visitorRuntime.Close()
	})

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
	return saga, ownerStore, visitorStore, ownerClient, store
}

func testStealRequest(interactionID []byte) StealRequest {
	return StealRequest{
		InteractionID: interactionID, VisitorPlayerID: testVisitorID,
		VisitorOwnerEpoch: player.LocalOwnerEpoch, OwnerPlayerID: testOwnerID,
		OwnerRoute: dummyOwnerRoute(), VisitID: fixedVisitID(0xAA),
		PlotID: testPlotID, CropItemID: 4001, Quantity: 1,
		FarmViewEpoch: append([]byte(nil), testFarmViewEpoch...),
		FarmViewSeq:   testFarmViewSeq,
	}
}

func TestStealSagaHappyPathCompletesExactlyOnce(t *testing.T) {
	saga, ownerStore, visitorStore, ownerClient, _ := newTestFixture(t)
	ctx := context.Background()
	now := time.Now()

	response, err := saga.Execute(ctx, testStealRequest(fixedVisitID(0x01)), now)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if response == nil || len(response.InteractionId) != 16 {
		t.Fatalf("expected a well-formed FriendActionResponse, got %+v", response)
	}
	if response.GetVisitorPatch().GetInventoryUpserts()[0].GetQuantity() != 1 {
		t.Fatalf("expected visitor inventory patch quantity=1, got %+v", response.GetVisitorPatch())
	}

	plot := ownerStore.plotSnapshot(testPlotID)
	if plot.StealCount != 1 || plot.StolenQuantity != 1 {
		t.Fatalf("expected owner plot mutated exactly once, got %+v", plot)
	}
	if ownerClient.calls() != 1 {
		t.Fatalf("expected exactly one owner RPC call, got %d", ownerClient.calls())
	}

	// Retrying the identical request (same interaction ID, same digest)
	// must replay the completed result without mutating owner or visitor
	// state again.
	response2, err := saga.Execute(ctx, testStealRequest(fixedVisitID(0x01)), now)
	if err != nil {
		t.Fatalf("Execute retry: %v", err)
	}
	if string(response2.InteractionId) != string(response.InteractionId) {
		t.Fatalf("expected identical interaction_id on replay")
	}
	plotAfterRetry := ownerStore.plotSnapshot(testPlotID)
	if plotAfterRetry.StealCount != 1 || plotAfterRetry.StolenQuantity != 1 {
		t.Fatalf("expected retry not to double-mutate owner plot, got %+v", plotAfterRetry)
	}
	if ownerClient.calls() != 1 {
		t.Fatalf("expected retry to skip the owner RPC entirely once COMPLETED, got %d calls", ownerClient.calls())
	}
	if visitorStore.inventoryQuantity(4001) != 1 {
		t.Fatalf("expected exactly 1 crop credited to visitor, got %d", visitorStore.inventoryQuantity(4001))
	}
}

func TestStealSagaRejectsDigestConflict(t *testing.T) {
	saga, _, _, _, _ := newTestFixture(t)
	ctx := context.Background()
	now := time.Now()

	id := fixedVisitID(0x02)
	if _, err := saga.Execute(ctx, testStealRequest(id), now); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	conflicting := testStealRequest(id)
	conflicting.PlotID = testPlotID + 1 // changes the request digest
	if _, err := saga.Execute(ctx, conflicting, now); !errors.Is(err, ErrDigestConflict) {
		t.Fatalf("expected ErrDigestConflict, got %v", err)
	}
}

func TestStealSagaDeterministicOwnerRejectionReleasesAndAborts(t *testing.T) {
	saga, ownerStore, visitorStore, ownerClient, store := newTestFixture(t)
	ctx := context.Background()
	now := time.Now()

	// Exhaust max_steal_times=2 directly on the owner before the Saga runs,
	// so ApplyStealOnOwner deterministically rejects with STEAL_NOT_AVAILABLE.
	for i := 0; i < 2; i++ {
		if _, _, _, _, err := ownerClient.runtime.ApplyStealOnOwner(
			ctx, testOwnerID, player.LocalOwnerEpoch, uint64(900+i), fixedVisitID(byte(0x40+i)), testPlotID,
			4001, testFarmViewEpoch, testFarmViewSeq,
		); err != nil {
			t.Fatalf("prime steal %d: %v", i, err)
		}
	}

	id := fixedVisitID(0x03)
	_, err := saga.Execute(ctx, testStealRequest(id), now)
	var aborted *AbortedError
	if !errors.As(err, &aborted) {
		t.Fatalf("expected AbortedError, got %v", err)
	}
	if aborted.Code != wsv1.ErrorCode_STEAL_NOT_AVAILABLE {
		t.Fatalf("expected STEAL_NOT_AVAILABLE, got %v", aborted.Code)
	}

	record, _, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get interaction record: %v", err)
	}
	if record.Status != tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_ABORTED {
		t.Fatalf("expected a terminal ABORTED record, got %v", record.Status)
	}
	if visitorStore.inventoryQuantity(4001) != 0 {
		t.Fatalf("expected no crop credited after a deterministic rejection, got %d",
			visitorStore.inventoryQuantity(4001))
	}
	_ = ownerStore

	// Retrying an ABORTED interaction stays ABORTED (terminal, not deleted).
	_, err = saga.Execute(ctx, testStealRequest(id), now)
	if !errors.As(err, &aborted) {
		t.Fatalf("expected AbortedError on retry of a terminal record, got %v", err)
	}
}

// TestStealSagaTransportFailureDoesNotBecomeSuccess covers the
// CAS-conflict/retryable-failure requirement: a transport error talking to
// the owner must surface ErrOutcomeUnknown, never a fabricated success, and
// must not mutate owner state.
func TestStealSagaTransportFailureDoesNotBecomeSuccess(t *testing.T) {
	saga, ownerStore, _, ownerClient, _ := newTestFixture(t)
	ctx := context.Background()
	now := time.Now()

	ownerClient.failNextCall(errors.New("simulated network partition"))
	id := fixedVisitID(0x04)
	_, err := saga.Execute(ctx, testStealRequest(id), now)
	if !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("expected ErrOutcomeUnknown, got %v", err)
	}

	plot := ownerStore.plotSnapshot(testPlotID)
	if plot.StealCount != 0 {
		t.Fatalf("expected no owner mutation after a transport failure, got %+v", plot)
	}

	// Retrying with the same interaction ID recovers and completes exactly
	// once, proving the earlier failure never silently "succeeded".
	response, err := saga.Execute(ctx, testStealRequest(id), now)
	if err != nil {
		t.Fatalf("Execute retry after transport failure: %v", err)
	}
	if response == nil {
		t.Fatalf("expected a completed response on retry")
	}
	plotAfterRetry := ownerStore.plotSnapshot(testPlotID)
	if plotAfterRetry.StealCount != 1 {
		t.Fatalf("expected exactly one owner mutation after recovery, got %+v", plotAfterRetry)
	}
}

// --- Crash-window recovery tests -------------------------------------------

// TestStealSagaRecoversCrashWindowA covers "visitor checkpoint reservation
// saved but the interaction record is still INIT": the very first record
// Update (INIT -> VISITOR_RESERVED) never commits, even though
// ReserveSteal's own checkpoint write already succeeded synchronously.
func TestStealSagaRecoversCrashWindowA(t *testing.T) {
	saga, ownerStore, visitorStore, ownerClient, store := newTestFixture(t, 0)
	ctx := context.Background()
	now := time.Now()

	id := fixedVisitID(0x05)
	if _, err := saga.Execute(ctx, testStealRequest(id), now); err == nil {
		t.Fatalf("expected the primed Update failure to surface an error")
	}
	record, _, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get after simulated crash: %v", err)
	}
	if record.Status != tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_INIT {
		t.Fatalf("expected the record to remain INIT after the crash, got %v", record.Status)
	}

	// Resuming (same store, no longer flaky at this call index) must reach
	// COMPLETED, and ReserveSteal's dedupe must mean no second reservation
	// or duplicate mutation occurred.
	response, err := saga.Resume(ctx, testStealRequest(id), now)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if response == nil {
		t.Fatalf("expected a completed response after resume")
	}
	if ownerClient.calls() != 1 {
		t.Fatalf("expected exactly one owner RPC call across crash+resume, got %d", ownerClient.calls())
	}
	plot := ownerStore.plotSnapshot(testPlotID)
	if plot.StealCount != 1 {
		t.Fatalf("expected exactly one owner mutation across crash+resume, got %+v", plot)
	}
	if visitorStore.inventoryQuantity(4001) != 1 {
		t.Fatalf("expected exactly 1 crop credited across crash+resume, got %d", visitorStore.inventoryQuantity(4001))
	}
}

// TestStealSagaRecoversCrashWindowB covers "owner checkpoint applied
// receipt saved but the interaction record is still VISITOR_RESERVED": the
// second record Update (VISITOR_RESERVED -> OWNER_APPLIED) never commits,
// even though the owner's ApplyStealOnOwner checkpoint write already
// committed synchronously.
func TestStealSagaRecoversCrashWindowB(t *testing.T) {
	saga, ownerStore, visitorStore, ownerClient, store := newTestFixture(t, 1)
	ctx := context.Background()
	now := time.Now()

	id := fixedVisitID(0x06)
	if _, err := saga.Execute(ctx, testStealRequest(id), now); err == nil {
		t.Fatalf("expected the primed Update failure to surface an error")
	}
	record, _, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get after simulated crash: %v", err)
	}
	if record.Status != tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_VISITOR_RESERVED {
		t.Fatalf("expected the record to remain VISITOR_RESERVED after the crash, got %v", record.Status)
	}
	plotAfterCrash := ownerStore.plotSnapshot(testPlotID)
	if plotAfterCrash.StealCount != 1 {
		t.Fatalf("expected the owner mutation to have already committed before the crash, got %+v", plotAfterCrash)
	}

	response, err := saga.Resume(ctx, testStealRequest(id), now)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if response == nil {
		t.Fatalf("expected a completed response after resume")
	}
	// The owner RPC is called again on resume (the record didn't yet know
	// OWNER_APPLIED succeeded), but ApplyStealOnOwner itself dedupes.
	if ownerClient.calls() != 2 {
		t.Fatalf("expected the owner RPC to be retried exactly once more, got %d calls", ownerClient.calls())
	}
	plot := ownerStore.plotSnapshot(testPlotID)
	if plot.StealCount != 1 {
		t.Fatalf("expected the owner mutation to remain applied exactly once, got %+v", plot)
	}
	if visitorStore.inventoryQuantity(4001) != 1 {
		t.Fatalf("expected exactly 1 crop credited across crash+resume, got %d", visitorStore.inventoryQuantity(4001))
	}
}

// TestStealSagaRecoversCrashWindowC covers "visitor committed receipt saved
// but the interaction record is still OWNER_APPLIED": the third record
// Update (OWNER_APPLIED -> VISITOR_COMMITTED) never commits, even though
// the visitor's CommitSteal checkpoint write already committed
// synchronously.
func TestStealSagaRecoversCrashWindowC(t *testing.T) {
	saga, _, visitorStore, ownerClient, store := newTestFixture(t, 2)
	ctx := context.Background()
	now := time.Now()

	id := fixedVisitID(0x07)
	if _, err := saga.Execute(ctx, testStealRequest(id), now); err == nil {
		t.Fatalf("expected the primed Update failure to surface an error")
	}
	record, _, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get after simulated crash: %v", err)
	}
	if record.Status != tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_OWNER_APPLIED {
		t.Fatalf("expected the record to remain OWNER_APPLIED after the crash, got %v", record.Status)
	}
	if visitorStore.inventoryQuantity(4001) != 1 {
		t.Fatalf("expected CommitSteal to have already committed before the crash, got %d",
			visitorStore.inventoryQuantity(4001))
	}

	response, err := saga.Resume(ctx, testStealRequest(id), now)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if response == nil {
		t.Fatalf("expected a completed response after resume")
	}
	if ownerClient.calls() != 1 {
		t.Fatalf("expected the owner RPC not to be retried once OWNER_APPLIED was durably recorded, got %d calls",
			ownerClient.calls())
	}
	if visitorStore.inventoryQuantity(4001) != 1 {
		t.Fatalf("expected exactly 1 crop credited across crash+resume, got %d", visitorStore.inventoryQuantity(4001))
	}
}

// newTestFixtureWithRuntime is like newTestFixture but also returns the
// owner *player.Runtime directly, for tests that need to prime owner state
// (e.g. exhausting max_steal_times) before driving the Saga.
func newTestFixtureWithRuntime(t *testing.T) (
	*StealSaga, *playerMemStore, *playerMemStore, *player.Runtime, Store,
) {
	t.Helper()
	client := testtcaplus.New()
	store, err := NewTcaplusStore(client, 1)
	if err != nil {
		t.Fatalf("NewTcaplusStore: %v", err)
	}
	ownerRuntime, ownerStore := newOwnerRuntime(testOwnerID, testPlotID)
	visitorRuntime, visitorStore := newVisitorRuntime(testVisitorID)
	t.Cleanup(func() {
		ownerRuntime.Close()
		visitorRuntime.Close()
	})
	ownerClient := newInProcessOwnerClient(ownerRuntime, player.LocalOwnerEpoch)
	saga, err := NewStealSaga(store, visitorRuntime, ownerClient)
	if err != nil {
		t.Fatalf("NewStealSaga: %v", err)
	}
	return saga, ownerStore, visitorStore, ownerRuntime, store
}
