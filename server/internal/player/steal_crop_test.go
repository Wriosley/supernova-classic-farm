package player

import (
	"context"
	"testing"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
)

func inventoryQuantity(checkpoint *datav1.PlayerCheckpointV1, itemID uint32) uint32 {
	for _, stack := range checkpoint.Inventory {
		if stack.ItemId == itemID {
			return stack.Quantity
		}
	}
	return 0
}

func interactionIDFixture(fill byte) []byte {
	id := make([]byte, 16)
	for i := range id {
		id[i] = fill
	}
	return id
}

// developmentStateAt pins a fresh development state's created_at/updated_at
// to the test's fake clock. NewDevelopmentState stamps them from the real
// wall clock, so a checkpoint built at a pinned earlier "now" would fail
// validateCheckpoint's created_at <= updated_at rule.
func developmentStateAt(playerID uint64, now time.Time) *State {
	state := NewDevelopmentState(playerID)
	state.CreatedAtMS = now.Add(-time.Minute).UnixMilli()
	state.UpdatedAtMS = now.Add(-time.Minute).UnixMilli()
	return state
}

func maturePlot(plotID uint32) *Plot {
	const maturityValueScaled9 = 1_000_000_000
	return &Plot{
		ID: plotID, State: plotv1.PlotState_MATURE,
		CropID: 3001, CropItemID: 4001, CropConfigVersion: ServerConfigVersion,
		PlantedAtMS: 1, MaturityValueScaled9: maturityValueScaled9, BaseGrowthRateScaled6: 1_000_000,
		SettledGrowthValueScaled9: maturityValueScaled9, LastSettledAtMS: 1,
		BaseYield: 5, StealQuantity: 1, MaxStealTimes: 2, ProtectedOwnerYield: 1,
	}
}

func ownerStateWithMaturePlot(playerID uint64, plotID uint32, now time.Time) *State {
	state := NewDevelopmentState(playerID)
	state.CreatedAtMS = now.Add(-time.Minute).UnixMilli()
	state.UpdatedAtMS = now.Add(-time.Minute).UnixMilli()
	state.Plots[plotID] = maturePlot(plotID)
	return state
}

func visitorStateWithStealTask(playerID uint64, now time.Time) *State {
	state := NewDevelopmentState(playerID)
	state.CreatedAtMS = now.Add(-time.Minute).UnixMilli()
	state.UpdatedAtMS = now.Add(-time.Minute).UnixMilli()
	state.Tasks = []Task{{ID: stealTaskID, Target: 1}}
	return state
}

func TestReserveStealAppendsReservationAndIsIdempotent(t *testing.T) {
	const visitorID = uint64(7)
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: developmentStateAt(visitorID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	defer runtime.Close()

	interactionID := interactionIDFixture(0x01)
	first, err := runtime.ReserveSteal(context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1)
	if err != nil || first {
		t.Fatalf("first ReserveSteal: alreadyReserved=%v err=%v", first, err)
	}
	if len(store.saved) != 1 {
		t.Fatalf("expected one synchronous checkpoint write, got %d", len(store.saved))
	}
	saved := store.saved[0]
	if len(saved.FriendReservations) != 1 {
		t.Fatalf("expected one reservation, got %+v", saved.FriendReservations)
	}
	reservation := saved.FriendReservations[0]
	if reservation.GetReservedInventoryItemId() != 4001 || reservation.GetReservedInventoryQuantity() != 1 {
		t.Fatalf("unexpected reservation contents: %+v", reservation)
	}
	if saved.PlayerSeq != 0 {
		t.Fatalf("expected reservation to leave player_seq untouched, got %d", saved.PlayerSeq)
	}

	second, err := runtime.ReserveSteal(context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1)
	if err != nil {
		t.Fatalf("second ReserveSteal: %v", err)
	}
	if !second {
		t.Fatalf("expected alreadyReserved=true on retry")
	}
	if len(store.saved) != 1 {
		t.Fatalf("expected retry to skip the synchronous flush, got %d writes", len(store.saved))
	}
}

func TestReserveStealRejectsConflictingRetry(t *testing.T) {
	const visitorID = uint64(7)
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: developmentStateAt(visitorID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	defer runtime.Close()

	interactionID := interactionIDFixture(0x02)
	if _, err := runtime.ReserveSteal(context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1); err != nil {
		t.Fatalf("first ReserveSteal: %v", err)
	}
	if _, err := runtime.ReserveSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 2,
	); err != ErrStealReservationConflict {
		t.Fatalf("expected ErrStealReservationConflict, got %v", err)
	}
}

func TestReserveStealRejectsOverStackLimit(t *testing.T) {
	const visitorID = uint64(7)
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	state := developmentStateAt(visitorID, now)
	state.Inventory[4001] = inventoryStackLimit
	store := &recordingCheckpointStore{state: state}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	defer runtime.Close()

	if _, err := runtime.ReserveSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionIDFixture(0x03), 4001, 1,
	); err != ErrStealInventoryCapacity {
		t.Fatalf("expected ErrStealInventoryCapacity, got %v", err)
	}
	if len(store.saved) != 0 {
		t.Fatalf("expected no checkpoint write on rejection, got %d", len(store.saved))
	}
}

func TestReserveStealRejectsOverTypeLimitConsideringLiveReservations(t *testing.T) {
	const visitorID = uint64(7)
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	state := developmentStateAt(visitorID, now)
	state.Inventory = make(map[uint32]uint32, inventoryTypeLimit)
	for itemID := uint32(1); itemID < inventoryTypeLimit; itemID++ {
		state.Inventory[itemID] = 1
	}
	store := &recordingCheckpointStore{state: state}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	defer runtime.Close()

	// One free type slot remains; a live reservation for a brand-new item
	// consumes it, so a second brand-new item must be rejected.
	if _, err := runtime.ReserveSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionIDFixture(0x04), 9001, 1,
	); err != nil {
		t.Fatalf("first ReserveSteal (fills last type slot): %v", err)
	}
	if _, err := runtime.ReserveSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionIDFixture(0x05), 9002, 1,
	); err != ErrStealInventoryCapacity {
		t.Fatalf("expected ErrStealInventoryCapacity for a second new type, got %v", err)
	}
}

func TestApplyStealOnOwnerMutatesOnceAndDedupesRetry(t *testing.T) {
	const ownerID = uint64(11)
	const plotID = uint32(1)
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: ownerStateWithMaturePlot(ownerID, plotID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	defer runtime.Close()

	interactionID := interactionIDFixture(0x10)
	payload1, digest1, patch1, already1, err := runtime.ApplyStealOnOwner(
		context.Background(), ownerID, LocalOwnerEpoch, 99, interactionID, plotID,
	)
	if err != nil {
		t.Fatalf("first ApplyStealOnOwner: %v", err)
	}
	if already1 {
		t.Fatalf("expected alreadyApplied=false on first apply")
	}
	if len(payload1) == 0 || len(digest1) == 0 {
		t.Fatalf("expected a non-empty result payload/digest")
	}
	if patch1 == nil {
		t.Fatalf("expected a FarmViewPatch on first apply")
	}
	if len(store.saved) != 1 {
		t.Fatalf("expected exactly one synchronous checkpoint write, got %d", len(store.saved))
	}
	saved := store.saved[0]
	if len(saved.Plots) == 0 {
		t.Fatalf("expected plots in checkpoint")
	}
	found := false
	for _, record := range saved.Plots {
		if record.PlotId != plotID {
			continue
		}
		found = true
		if record.StealCount != 1 {
			t.Fatalf("expected steal_count=1, got %d", record.StealCount)
		}
		if record.StolenQuantity != 1 {
			t.Fatalf("expected stolen_quantity incremented by frozen steal_quantity=1, got %d", record.StolenQuantity)
		}
	}
	if !found {
		t.Fatalf("expected plot %d in checkpoint", plotID)
	}
	if saved.PlayerSeq != 1 {
		t.Fatalf("expected owner player_seq=1, got %d", saved.PlayerSeq)
	}

	payload2, digest2, patch2, already2, err := runtime.ApplyStealOnOwner(
		context.Background(), ownerID, LocalOwnerEpoch, 99, interactionID, plotID,
	)
	if err != nil {
		t.Fatalf("second ApplyStealOnOwner: %v", err)
	}
	if !already2 {
		t.Fatalf("expected alreadyApplied=true on retry")
	}
	if string(payload2) != string(payload1) || string(digest2) != string(digest1) {
		t.Fatalf("expected identical payload/digest on retry")
	}
	if patch2 != nil {
		t.Fatalf("expected nil FarmViewPatch on replay (already broadcast once)")
	}
	if len(store.saved) != 1 {
		t.Fatalf("expected retry to skip the synchronous flush, got %d writes", len(store.saved))
	}
}

func TestApplyStealOnOwnerRejectsWhenNotStealableWithoutMutating(t *testing.T) {
	const ownerID = uint64(11)
	const plotID = uint32(1)
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	state := ownerStateWithMaturePlot(ownerID, plotID, now)
	state.Plots[plotID].StealCount = state.Plots[plotID].MaxStealTimes // already exhausted
	store := &recordingCheckpointStore{state: state}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	defer runtime.Close()

	_, _, _, _, err := runtime.ApplyStealOnOwner(
		context.Background(), ownerID, LocalOwnerEpoch, 99, interactionIDFixture(0x11), plotID,
	)
	if err != ErrStealNotAvailable {
		t.Fatalf("expected ErrStealNotAvailable, got %v", err)
	}
	if len(store.saved) != 0 {
		t.Fatalf("expected no checkpoint write for a deterministic rejection, got %d", len(store.saved))
	}
}

func TestApplyStealOnOwnerConcurrentVisitorsRespectMaxStealTimes(t *testing.T) {
	const ownerID = uint64(11)
	const plotID = uint32(1)
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	state := ownerStateWithMaturePlot(ownerID, plotID, now)
	state.Plots[plotID].BaseYield = 10 // enough yield headroom for both steals
	store := &recordingCheckpointStore{state: state}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	defer runtime.Close()

	for i, fill := range []byte{0x20, 0x21} {
		_, _, _, already, err := runtime.ApplyStealOnOwner(
			context.Background(), ownerID, LocalOwnerEpoch, uint64(100+i), interactionIDFixture(fill), plotID,
		)
		if err != nil || already {
			t.Fatalf("visitor %d ApplyStealOnOwner: already=%v err=%v", i, already, err)
		}
	}
	// max_steal_times=2 is now exhausted; a third distinct visitor/interaction must fail deterministically.
	_, _, _, _, err := runtime.ApplyStealOnOwner(
		context.Background(), ownerID, LocalOwnerEpoch, 102, interactionIDFixture(0x22), plotID,
	)
	if err != ErrStealNotAvailable {
		t.Fatalf("expected third steal to be rejected once max_steal_times is reached, got %v", err)
	}
	saved := store.saved[len(store.saved)-1]
	for _, record := range saved.Plots {
		if record.PlotId != plotID {
			continue
		}
		if record.StealCount != 2 {
			t.Fatalf("expected steal_count capped at max_steal_times=2, got %d", record.StealCount)
		}
	}
}

func TestCommitStealCreditsInventoryAndTaskExactlyOnce(t *testing.T) {
	const visitorID = uint64(7)
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	state := visitorStateWithStealTask(visitorID, now)
	store := &recordingCheckpointStore{state: state}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	defer runtime.Close()

	interactionID := interactionIDFixture(0x30)
	if _, err := runtime.ReserveSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1,
	); err != nil {
		t.Fatalf("ReserveSteal: %v", err)
	}

	response1, already1, err := runtime.CommitSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1, nil,
	)
	if err != nil {
		t.Fatalf("first CommitSteal: %v", err)
	}
	if already1 {
		t.Fatalf("expected alreadyCommitted=false on first commit")
	}
	if response1.GetVisitorPatch().GetInventoryUpserts()[0].GetQuantity() != 1 {
		t.Fatalf("expected inventory patch quantity=1, got %+v", response1.GetVisitorPatch())
	}
	lastSaved := store.saved[len(store.saved)-1]
	if inventoryQuantity(lastSaved, 4001) != 1 {
		t.Fatalf("expected exactly 1 crop credited, got %+v", lastSaved.Inventory)
	}
	if lastSaved.CurrentChapter.Tasks[0].CurrentValue != 1 {
		t.Fatalf("expected TASK_STEAL_CROP incremented, got %+v", lastSaved.CurrentChapter.Tasks[0])
	}
	writesAfterFirstCommit := len(store.saved)

	response2, already2, err := runtime.CommitSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1, nil,
	)
	if err != nil {
		t.Fatalf("second CommitSteal: %v", err)
	}
	if !already2 {
		t.Fatalf("expected alreadyCommitted=true on retry")
	}
	if response2.GetVisitorPatch().GetInventoryUpserts()[0].GetQuantity() != 1 {
		t.Fatalf("expected identical patch on retry, got %+v", response2.GetVisitorPatch())
	}
	if len(store.saved) != writesAfterFirstCommit {
		t.Fatalf("expected retry to skip the synchronous flush, got %d additional writes",
			len(store.saved)-writesAfterFirstCommit)
	}
	finalInventory := inventoryQuantity(store.saved[len(store.saved)-1], 4001)
	if finalInventory != 1 {
		t.Fatalf("expected inventory credited exactly once across retries, got %d", finalInventory)
	}
}

func TestCommitStealRejectsWithoutMatchingReservation(t *testing.T) {
	const visitorID = uint64(7)
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: visitorStateWithStealTask(visitorID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	defer runtime.Close()

	if _, _, err := runtime.CommitSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionIDFixture(0x40), 4001, 1, nil,
	); err != ErrStealReservationMissing {
		t.Fatalf("expected ErrStealReservationMissing, got %v", err)
	}
	if len(store.saved) != 0 {
		t.Fatalf("expected no checkpoint write without a reservation, got %d", len(store.saved))
	}
}

func TestReleaseStealReleasesLiveReservationIdempotently(t *testing.T) {
	const visitorID = uint64(7)
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: developmentStateAt(visitorID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	defer runtime.Close()

	interactionID := interactionIDFixture(0x50)
	if _, err := runtime.ReserveSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1,
	); err != nil {
		t.Fatalf("ReserveSteal: %v", err)
	}
	writesAfterReserve := len(store.saved)

	if err := runtime.ReleaseSteal(context.Background(), visitorID, LocalOwnerEpoch, interactionID); err != nil {
		t.Fatalf("first ReleaseSteal: %v", err)
	}
	if len(store.saved) != writesAfterReserve+1 {
		t.Fatalf("expected release to flush once, got %d total writes", len(store.saved))
	}
	saved := store.saved[len(store.saved)-1]
	if saved.PlayerSeq != 0 {
		t.Fatalf("expected release to leave player_seq untouched, got %d", saved.PlayerSeq)
	}

	// Idempotent: releasing again (or a reservation that never existed) is a no-op.
	if err := runtime.ReleaseSteal(context.Background(), visitorID, LocalOwnerEpoch, interactionID); err != nil {
		t.Fatalf("second ReleaseSteal: %v", err)
	}
	if len(store.saved) != writesAfterReserve+1 {
		t.Fatalf("expected retry to skip the synchronous flush, got %d total writes", len(store.saved))
	}
	if err := runtime.ReleaseSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionIDFixture(0x51),
	); err != nil {
		t.Fatalf("ReleaseSteal for a never-created interaction: %v", err)
	}
}

// TestOrdinaryCommandRemainsAsyncDirtyAfterSyncInteraction guards against a
// regression where a synchronous Saga flush (ReserveSteal, ApplyStealOnOwner,
// CommitSteal, ReleaseSteal) accidentally makes ordinary single-player
// commands (e.g. BUY_SEEDS) synchronous too: those must remain queued in
// dirtyRevision for the periodic flusher, per
// docs/contracts/idempotency-and-errors.md.
func TestOrdinaryCommandRemainsAsyncDirtyAfterSyncInteraction(t *testing.T) {
	const playerID = uint64(7)
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: developmentStateAt(playerID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	defer runtime.Close()

	if _, err := runtime.ReserveSteal(
		context.Background(), playerID, LocalOwnerEpoch, interactionIDFixture(0x60), 4001, 1,
	); err != nil {
		t.Fatalf("ReserveSteal: %v", err)
	}
	writesAfterReserve := len(store.saved)
	if writesAfterReserve == 0 {
		t.Fatalf("expected the interaction step to flush synchronously")
	}

	if _, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		buySeedsRequest(playerID, "00112233-4455-6677-8899-aabbccddee60", 1)); err != nil {
		t.Fatalf("Handle(BUY_SEEDS): %v", err)
	}
	if len(store.saved) != writesAfterReserve {
		t.Fatalf("expected BUY_SEEDS to stay async/dirty (no immediate write), got %d new writes",
			len(store.saved)-writesAfterReserve)
	}

	runtime.mu.Lock()
	_, dirty := runtime.dirtyRevision[playerID]
	runtime.mu.Unlock()
	if !dirty {
		t.Fatalf("expected BUY_SEEDS to leave the player marked dirty for the periodic flusher")
	}

	if err := runtime.flushDirty(context.Background()); err != nil {
		t.Fatalf("flushDirty: %v", err)
	}
	if len(store.saved) != writesAfterReserve+1 {
		t.Fatalf("expected exactly one deferred flush for BUY_SEEDS, got %d new writes",
			len(store.saved)-writesAfterReserve)
	}
}
