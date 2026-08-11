package player

import (
	"context"
	"errors"
	"testing"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

// flakyCheckpointStore rejects a window of SaveCAS attempts with a
// normalized non-success status (retryable, stale or fenced) and records the
// accepted ones like recordingCheckpointStore. attempts counts every attempt,
// so a test can prove a same-ID retry really re-issued the write instead of
// trusting the mutation still sitting in Actor memory.
type flakyCheckpointStore struct {
	recordingCheckpointStore
	attempts   int
	failFrom   int
	failures   int
	failStatus CheckpointWriteStatus
}

func (s *flakyCheckpointStore) SaveCAS(
	ctx context.Context, write CheckpointWrite,
) (CheckpointWriteResult, error) {
	s.attempts++
	if s.failures > 0 && s.attempts >= s.failFrom {
		s.failures--
		return CheckpointWriteResult{Status: s.failStatus}, nil
	}
	return s.recordingCheckpointStore.SaveCAS(ctx, write)
}

func flakyRuntime(store CheckpointStore, now time.Time) *Runtime {
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	return runtime
}

func farmViewSeqOf(t *testing.T, runtime *Runtime, playerID uint64) uint64 {
	t.Helper()
	runtime.mu.Lock()
	a := runtime.actors[playerID]
	runtime.mu.Unlock()
	if a == nil {
		t.Fatalf("player %d has no active Actor", playerID)
	}
	var seq uint64
	if err := a.mailbox.Do(context.Background(), func() { seq = a.farmViewSeq }); err != nil {
		t.Fatalf("read farm_view_seq: %v", err)
	}
	return seq
}

func playerIsDirty(runtime *Runtime, playerID uint64) bool {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	_, dirty := runtime.dirtyRevision[playerID]
	return dirty
}

func reservationStatus(
	checkpoint *datav1.PlayerCheckpointV1, interactionID []byte,
) datav1.FriendReservationStatus {
	for _, reservation := range checkpoint.FriendReservations {
		if string(reservation.InteractionId) == string(interactionID) {
			return reservation.Status
		}
	}
	return datav1.FriendReservationStatus_FRIEND_RESERVATION_STATUS_UNSPECIFIED
}

// TestReserveStealRetriesSaveCASAfterFailedSyncFlush pins the Phase 5 stop
// condition for the visitor's reserve step: a failed synchronous SaveCAS must
// not let the reservation sitting in Actor memory pass for a durable Saga
// step. The same-ID retry has to re-issue the write and only then report
// success.
func TestReserveStealRetriesSaveCASAfterFailedSyncFlush(t *testing.T) {
	const visitorID = uint64(7)
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	store := &flakyCheckpointStore{
		recordingCheckpointStore: recordingCheckpointStore{state: developmentStateAt(visitorID, now)},
		failures:                 1,
		failStatus:               CheckpointWriteRetryableFailure,
	}
	runtime := flakyRuntime(store, now)
	defer runtime.Close()

	interactionID := interactionIDFixture(0x70)
	if _, err := runtime.ReserveSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1,
	); !errors.Is(err, ErrCheckpointRetryable) {
		t.Fatalf("first ReserveSteal error = %v, want ErrCheckpointRetryable", err)
	}
	if store.attempts != 1 || len(store.saved) != 0 {
		t.Fatalf("expected one rejected write and nothing persisted, got attempts=%d saved=%d",
			store.attempts, len(store.saved))
	}

	alreadyReserved, err := runtime.ReserveSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1,
	)
	if err != nil {
		t.Fatalf("retried ReserveSteal: %v", err)
	}
	if !alreadyReserved {
		t.Fatalf("expected the retry to reuse the reservation already in memory")
	}
	if store.attempts != 2 || len(store.saved) != 1 {
		t.Fatalf("expected the retry to re-attempt SaveCAS once and persist, got attempts=%d saved=%d",
			store.attempts, len(store.saved))
	}
	if status := reservationStatus(store.saved[0], interactionID); status !=
		datav1.FriendReservationStatus_FRIEND_RESERVATION_RESERVED {
		t.Fatalf("persisted reservation status = %v, want RESERVED", status)
	}
	if playerIsDirty(runtime, visitorID) {
		t.Fatalf("expected the settled sync step to leave nothing dirty")
	}
}

// TestReserveStealFencedFlushNeverReportsSuccess covers the fenced/stale
// class: those never become success just because memory retained the
// reservation, no matter how many times the Saga retries the same ID.
func TestReserveStealFencedFlushNeverReportsSuccess(t *testing.T) {
	const visitorID = uint64(7)
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	store := &flakyCheckpointStore{
		recordingCheckpointStore: recordingCheckpointStore{state: developmentStateAt(visitorID, now)},
		failures:                 4,
		failStatus:               CheckpointWriteFenced,
	}
	runtime := flakyRuntime(store, now)
	defer runtime.Close()

	interactionID := interactionIDFixture(0x71)
	for attempt := 1; attempt <= 3; attempt++ {
		_, err := runtime.ReserveSteal(
			context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1,
		)
		if !errors.Is(err, ErrCheckpointFenced) {
			t.Fatalf("ReserveSteal attempt %d error = %v, want ErrCheckpointFenced", attempt, err)
		}
	}
	if store.attempts != 3 || len(store.saved) != 0 {
		t.Fatalf("expected three rejected writes and nothing persisted, got attempts=%d saved=%d",
			store.attempts, len(store.saved))
	}
}

// TestApplyStealOnOwnerRetryAfterFailedFlushBroadcastsOnce is the owner-side
// counterpart: the FarmViewPatch must be produced by whichever attempt
// durably commits the owner mutation, exactly once, and a later replay of an
// already-durable apply must not bump farm_view_seq again.
func TestApplyStealOnOwnerRetryAfterFailedFlushBroadcastsOnce(t *testing.T) {
	const ownerID = uint64(11)
	const plotID = uint32(1)
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	store := &flakyCheckpointStore{
		recordingCheckpointStore: recordingCheckpointStore{
			state: ownerStateWithMaturePlot(ownerID, plotID, now),
		},
		failures:   1,
		failStatus: CheckpointWriteStaleCopy,
	}
	broadcaster := newRecordingFarmViewBroadcaster()
	runtime := flakyRuntime(store, now)
	defer runtime.Close()
	if err := runtime.SetFarmViewDispatcher(broadcaster); err != nil {
		t.Fatal(err)
	}

	interactionID := interactionIDFixture(0x80)
	_, _, patch, alreadyApplied, err := applySteal(t, runtime, ownerID, 99, interactionID, plotID, 4001)
	if !errors.Is(err, ErrCheckpointConflict) {
		t.Fatalf("first ApplyStealOnOwner error = %v, want ErrCheckpointConflict", err)
	}
	if alreadyApplied || patch != nil {
		t.Fatalf("a failed apply must not report alreadyApplied=%v patch=%v", alreadyApplied, patch)
	}
	if calls, _, _ := broadcaster.snapshot(); calls != 0 {
		t.Fatalf("Broadcast called %d times before the owner mutation was durable, want 0", calls)
	}
	if seq := farmViewSeqOf(t, runtime, ownerID); seq != 0 {
		t.Fatalf("farm_view_seq after a failed apply = %d, want 0", seq)
	}

	payload, digest, patch, alreadyApplied, err := applySteal(t, runtime, ownerID, 99, interactionID, plotID, 4001)
	if err != nil {
		t.Fatalf("retried ApplyStealOnOwner: %v", err)
	}
	if !alreadyApplied {
		t.Fatalf("expected the retry to reuse the OWNER receipt already in memory")
	}
	if len(payload) == 0 || len(digest) == 0 {
		t.Fatalf("expected the retry to replay the receipt's payload/digest")
	}
	if patch == nil {
		t.Fatalf("expected the retry that persisted the apply to own the FarmViewPatch")
	}
	broadcaster.waitForCall(t)
	calls, last, _ := broadcaster.snapshot()
	if calls != 1 {
		t.Fatalf("Broadcast called %d times after the apply became durable, want 1", calls)
	}
	if last.GetVersion().GetFarmViewSeq() != 1 || patch.GetVersion().GetFarmViewSeq() != 1 {
		t.Fatalf("expected a single farm_view_seq bump, got broadcast=%d returned=%d",
			last.GetVersion().GetFarmViewSeq(), patch.GetVersion().GetFarmViewSeq())
	}
	if store.attempts != 2 || len(store.saved) != 1 {
		t.Fatalf("expected the retry to re-attempt SaveCAS once and persist, got attempts=%d saved=%d",
			store.attempts, len(store.saved))
	}
	for _, record := range store.saved[0].Plots {
		if record.PlotId == plotID &&
			(record.StealCount != 1 || record.StolenQuantity != 1) {
			t.Fatalf("expected the plot mutated exactly once, got %+v", record)
		}
	}

	_, _, patch, alreadyApplied, err = applySteal(t, runtime, ownerID, 99, interactionID, plotID, 4001)
	if err != nil || !alreadyApplied {
		t.Fatalf("durable replay: already=%v err=%v", alreadyApplied, err)
	}
	if patch != nil {
		t.Fatalf("expected nil FarmViewPatch on a durable replay, got %v", patch)
	}
	if calls, _, _ := broadcaster.snapshot(); calls != 1 {
		t.Fatalf("Broadcast called %d times across the whole interaction, want 1", calls)
	}
	if seq := farmViewSeqOf(t, runtime, ownerID); seq != 1 {
		t.Fatalf("farm_view_seq after the durable replay = %d, want 1", seq)
	}
	if store.attempts != 2 || len(store.saved) != 1 {
		t.Fatalf("expected the durable replay to write nothing, got attempts=%d saved=%d",
			store.attempts, len(store.saved))
	}
}

// TestCommitStealRetriesSaveCASAfterFailedSyncFlush: the visitor's inventory
// credit and VISITOR receipt must not be reported committed while they are
// only in memory.
func TestCommitStealRetriesSaveCASAfterFailedSyncFlush(t *testing.T) {
	const visitorID = uint64(7)
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	store := &flakyCheckpointStore{
		recordingCheckpointStore: recordingCheckpointStore{
			state: visitorStateWithStealTask(visitorID, now),
		},
		failFrom:   2, // let the reservation commit, reject the commit step once
		failures:   1,
		failStatus: CheckpointWriteRetryableFailure,
	}
	runtime := flakyRuntime(store, now)
	defer runtime.Close()

	interactionID := interactionIDFixture(0x90)
	if _, err := runtime.ReserveSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1,
	); err != nil {
		t.Fatalf("ReserveSteal: %v", err)
	}
	writesAfterReserve := len(store.saved)

	if _, _, err := runtime.CommitSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1, nil,
	); !errors.Is(err, ErrCheckpointRetryable) {
		t.Fatalf("first CommitSteal error = %v, want ErrCheckpointRetryable", err)
	}
	if len(store.saved) != writesAfterReserve {
		t.Fatalf("expected the rejected commit to persist nothing new")
	}
	if quantity := inventoryQuantity(store.saved[writesAfterReserve-1], 4001); quantity != 0 {
		t.Fatalf("durable inventory after a failed commit = %d, want 0", quantity)
	}

	response, alreadyCommitted, err := runtime.CommitSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1, nil,
	)
	if err != nil {
		t.Fatalf("retried CommitSteal: %v", err)
	}
	if !alreadyCommitted {
		t.Fatalf("expected the retry to replay the VISITOR receipt already in memory")
	}
	if response.GetVisitorPatch().GetInventoryUpserts()[0].GetQuantity() != 1 {
		t.Fatalf("unexpected replayed patch: %+v", response.GetVisitorPatch())
	}
	if len(store.saved) != writesAfterReserve+1 {
		t.Fatalf("expected the retry to re-attempt SaveCAS, got %d writes", len(store.saved))
	}
	saved := store.saved[len(store.saved)-1]
	if inventoryQuantity(saved, 4001) != 1 {
		t.Fatalf("expected exactly one crop credited durably, got %+v", saved.Inventory)
	}
	if saved.CurrentChapter.Tasks[0].CurrentValue != 1 {
		t.Fatalf("expected TASK_STEAL_CROP credited once, got %+v", saved.CurrentChapter.Tasks[0])
	}
	if reservationStatus(saved, interactionID) !=
		datav1.FriendReservationStatus_FRIEND_RESERVATION_CONSUMED {
		t.Fatalf("expected the reservation consumed durably, got %+v", saved.FriendReservations)
	}
}

// TestReleaseStealRetriesSaveCASAfterFailedSyncFlush: releasing is the abort
// path, so a release that only happened in memory must not report success
// either, or the visitor keeps a reserved-but-unusable capacity slot.
func TestReleaseStealRetriesSaveCASAfterFailedSyncFlush(t *testing.T) {
	const visitorID = uint64(7)
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	store := &flakyCheckpointStore{
		recordingCheckpointStore: recordingCheckpointStore{state: developmentStateAt(visitorID, now)},
		failFrom:                 2,
		failures:                 1,
		failStatus:               CheckpointWriteRetryableFailure,
	}
	runtime := flakyRuntime(store, now)
	defer runtime.Close()

	interactionID := interactionIDFixture(0xA0)
	if _, err := runtime.ReserveSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1,
	); err != nil {
		t.Fatalf("ReserveSteal: %v", err)
	}
	writesAfterReserve := len(store.saved)

	if err := runtime.ReleaseSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID,
	); !errors.Is(err, ErrCheckpointRetryable) {
		t.Fatalf("first ReleaseSteal error = %v, want ErrCheckpointRetryable", err)
	}
	if len(store.saved) != writesAfterReserve {
		t.Fatalf("expected the rejected release to persist nothing new")
	}

	if err := runtime.ReleaseSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID,
	); err != nil {
		t.Fatalf("retried ReleaseSteal: %v", err)
	}
	if len(store.saved) != writesAfterReserve+1 {
		t.Fatalf("expected the retry to re-attempt SaveCAS, got %d writes", len(store.saved))
	}
	if status := reservationStatus(store.saved[len(store.saved)-1], interactionID); status !=
		datav1.FriendReservationStatus_FRIEND_RESERVATION_RELEASED {
		t.Fatalf("persisted reservation status = %v, want RELEASED", status)
	}
}

// TestApplyFriendTaskCreditRetriesSaveCASAfterFailedSyncFlush covers Friend
// Phase 2's identical synchronous pattern: FriendSvr's Saga marks its step
// APPLIED on a successful return, so a credit receipt that only exists in
// Actor memory must not produce one.
func TestApplyFriendTaskCreditRetriesSaveCASAfterFailedSyncFlush(t *testing.T) {
	const playerID = uint64(42)
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	store := &flakyCheckpointStore{
		recordingCheckpointStore: recordingCheckpointStore{state: friendTaskState(playerID, now)},
		failures:                 1,
		failStatus:               CheckpointWriteRetryableFailure,
	}
	runtime := flakyRuntime(store, now)
	defer runtime.Close()

	relationID := relationIDFixture(0xBC)
	if _, _, err := runtime.ApplyFriendTaskCredit(
		context.Background(), playerID, LocalOwnerEpoch, relationID,
	); !errors.Is(err, ErrCheckpointRetryable) {
		t.Fatalf("first ApplyFriendTaskCredit error = %v, want ErrCheckpointRetryable", err)
	}
	if store.attempts != 1 || len(store.saved) != 0 {
		t.Fatalf("expected one rejected write and nothing persisted, got attempts=%d saved=%d",
			store.attempts, len(store.saved))
	}

	_, playerSeq, err := runtime.ApplyFriendTaskCredit(
		context.Background(), playerID, LocalOwnerEpoch, relationID,
	)
	if err != nil {
		t.Fatalf("retried ApplyFriendTaskCredit: %v", err)
	}
	if playerSeq != 1 {
		t.Fatalf("player_seq on retry = %d, want 1", playerSeq)
	}
	if store.attempts != 2 || len(store.saved) != 1 {
		t.Fatalf("expected the retry to re-attempt SaveCAS once and persist, got attempts=%d saved=%d",
			store.attempts, len(store.saved))
	}
	saved := store.saved[0]
	if len(saved.FriendTaskCreditReceipts) != 1 ||
		string(saved.FriendTaskCreditReceipts[0].RelationId) != string(relationID) {
		t.Fatalf("expected exactly one durable credit receipt, got %+v", saved.FriendTaskCreditReceipts)
	}
	if saved.CurrentChapter.Tasks[0].CurrentValue != 1 {
		t.Fatalf("expected TASK_ADD_FRIEND credited once, got %+v", saved.CurrentChapter.Tasks[0])
	}
}

// TestOrdinaryCommandStaysAsyncWhileSyncStepIsPending guards the other half
// of the invariant: forcing a synchronous step's write through must not turn
// ordinary single-player commands synchronous, even while a durable-pending
// marker is outstanding for the same Actor.
func TestOrdinaryCommandStaysAsyncWhileSyncStepIsPending(t *testing.T) {
	const playerID = uint64(7)
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	store := &flakyCheckpointStore{
		recordingCheckpointStore: recordingCheckpointStore{state: developmentStateAt(playerID, now)},
		failures:                 1,
		failStatus:               CheckpointWriteRetryableFailure,
	}
	runtime := flakyRuntime(store, now)
	defer runtime.Close()

	interactionID := interactionIDFixture(0xB0)
	if _, err := runtime.ReserveSteal(
		context.Background(), playerID, LocalOwnerEpoch, interactionID, 4001, 1,
	); !errors.Is(err, ErrCheckpointRetryable) {
		t.Fatalf("first ReserveSteal error = %v, want ErrCheckpointRetryable", err)
	}

	if _, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		buySeedsRequest(playerID, "00112233-4455-6677-8899-aabbccddee70", 1)); err != nil {
		t.Fatalf("Handle(BUY_SEEDS): %v", err)
	}
	if store.attempts != 1 {
		t.Fatalf("expected BUY_SEEDS to stay async (no SaveCAS of its own), got attempts=%d",
			store.attempts)
	}
	if !playerIsDirty(runtime, playerID) {
		t.Fatalf("expected BUY_SEEDS to leave the player dirty for the periodic flusher")
	}

	if _, err := runtime.ReserveSteal(
		context.Background(), playerID, LocalOwnerEpoch, interactionID, 4001, 1,
	); err != nil {
		t.Fatalf("retried ReserveSteal: %v", err)
	}
	if store.attempts != 2 || len(store.saved) != 1 {
		t.Fatalf("expected exactly one accepted write from the retry, got attempts=%d saved=%d",
			store.attempts, len(store.saved))
	}
	saved := store.saved[0]
	if reservationStatus(saved, interactionID) !=
		datav1.FriendReservationStatus_FRIEND_RESERVATION_RESERVED {
		t.Fatalf("expected the reservation persisted by the retry, got %+v", saved.FriendReservations)
	}
	// BUY_SEEDS never issued a write of its own, but whole-state checkpoints
	// mean the retry's flush legitimately carries its coin spend along.
	if saved.CoinBalance != InitialCoinBalance-2 {
		t.Fatalf("expected the whole-state flush to carry the pending BUY_SEEDS spend, got %d",
			saved.CoinBalance)
	}
	if playerIsDirty(runtime, playerID) {
		t.Fatalf("expected the settled flush to clear the dirty marker")
	}
}

// TestSettleSyncStepRejectsAFlushThatDidNotReachTheMarkedRevision covers
// settleSyncStepLocked's last line of defence directly: an accepted SaveCAS
// is not by itself proof, only persistedRevision covering the marked
// revision is. This branch is otherwise only reachable through races (a
// concurrent flush retiring the dirty entry, or an acknowledgement skipped
// because the Actor's persisted revision/token moved underneath), so the
// marked revision is pushed out of reach here to pin the behaviour: the step
// fails with ErrCheckpointNotDurable and keeps its marker, leaving the
// same-ID retry path intact.
func TestSettleSyncStepRejectsAFlushThatDidNotReachTheMarkedRevision(t *testing.T) {
	const playerID = uint64(7)
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: developmentStateAt(playerID, now)}
	runtime := flakyRuntime(store, now)
	defer runtime.Close()

	ctx := context.Background()
	a, err := runtime.actorFor(ctx, playerID, LocalOwnerEpoch)
	if err != nil {
		t.Fatalf("actorFor: %v", err)
	}
	const stepKey = "unreachable-revision"
	if err := a.mailbox.Do(ctx, func() {
		a.markSyncPending(stepKey, pendingSyncStep{revision: a.state.CheckpointRevision + 1})
	}); err != nil {
		t.Fatalf("mark pending step: %v", err)
	}

	shardID := routing.ShardForPlayer(playerID)
	runtime.shardLocks[shardID].RLock()
	owedChanges, err := runtime.settleSyncStepLocked(ctx, playerID, a, stepKey)
	runtime.shardLocks[shardID].RUnlock()
	if !errors.Is(err, ErrCheckpointNotDurable) {
		t.Fatalf("settleSyncStepLocked error = %v, want ErrCheckpointNotDurable", err)
	}
	if !owedChanges.Empty() {
		t.Fatalf("a step that is not durable must not hand over its broadcast, got %v", owedChanges.PlotIDs())
	}
	if len(store.saved) != 1 {
		t.Fatalf("expected the settle attempt to have issued one write, got %d", len(store.saved))
	}

	var stillPending bool
	if err := a.mailbox.Do(ctx, func() {
		_, stillPending = a.syncPending[stepKey]
	}); err != nil {
		t.Fatalf("inspect pending step: %v", err)
	}
	if !stillPending {
		t.Fatalf("expected the durable-pending marker to survive so a retry can still settle it")
	}
}
