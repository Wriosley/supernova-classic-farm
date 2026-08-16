package player

import (
	"context"
	"errors"
	"testing"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
)

func friendGrowingPlotAt(plotID uint32, now time.Time) *Plot {
	estimate := now.Add(100 * time.Second).UnixMilli()
	return &Plot{
		ID: plotID, State: plotv1.PlotState_GROWING,
		CropID: 2001, CropItemID: 1002, CropConfigVersion: ServerConfigVersion,
		PlantedAtMS:          now.Add(-time.Second).UnixMilli(),
		MaturityValueScaled9: 100_000_000_000, BaseGrowthRateScaled6: 1_000_000,
		BaseYield: 3, LastSettledAtMS: now.Add(-time.Second).UnixMilli(),
		EstimatedMatureAtMS: &estimate,
	}
}

func latestSavedState(t *testing.T, store *recordingCheckpointStore) *State {
	t.Helper()
	if len(store.saved) == 0 {
		t.Fatal("expected a synchronous checkpoint write")
	}
	state, err := StateFromCheckpoint(store.saved[len(store.saved)-1])
	if err != nil {
		t.Fatalf("decode saved checkpoint: %v", err)
	}
	return state
}

func TestReserveActionChanceCapacityConflictAndIdempotency(t *testing.T) {
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	state := developmentStateAt(7, now)
	state.FriendActions = &datav1.FriendActionState{ApplyPestChances: 1}
	firstID := interactionIDFixture(0x31)
	already, _, err := reserveActionChance(
		state, firstID, datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND, now,
	)
	if err != nil || already {
		t.Fatalf("first reserve: already=%v err=%v", already, err)
	}
	already, _, err = reserveActionChance(
		state, firstID, datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND, now,
	)
	if err != nil || !already {
		t.Fatalf("idempotent reserve: already=%v err=%v", already, err)
	}
	if _, _, err := reserveActionChance(
		state, firstID, datav1.FriendInteractionAction_CATCH_PEST_FOR_FRIEND, now,
	); !errors.Is(err, ErrActionReservationConflict) {
		t.Fatalf("conflicting retry error = %v", err)
	}
	if _, _, err := reserveActionChance(
		state, interactionIDFixture(0x32), datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND, now,
	); !errors.Is(err, ErrInsufficientActionChance) {
		t.Fatalf("capacity error = %v", err)
	}
}

func TestApplyPestSuccessReplayAndAlreadyPresent(t *testing.T) {
	const ownerID, visitorID = uint64(11), uint64(7)
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	state := developmentStateAt(ownerID, now)
	state.Plots[1] = friendGrowingPlotAt(1, now)
	store := &recordingCheckpointStore{state: state}
	runtime := NewRuntime()
	runtime.store = store
	runtime.SetNow(func() time.Time { return now })
	defer runtime.Close()

	id := interactionIDFixture(0x41)
	if _, _, _, replay, err := runtime.ApplyApplyPestOnOwner(
		context.Background(), ownerID, LocalOwnerEpoch, visitorID, id, 1, developmentPestID,
	); err != nil || replay {
		t.Fatalf("apply pest: replay=%v err=%v", replay, err)
	}
	if len(store.saved) != 0 {
		t.Fatal("owner action must not synchronously persist")
	}
	if err := runtime.flushDirty(context.Background()); err != nil {
		t.Fatalf("flush dirty pest: %v", err)
	}
	saved := latestSavedState(t, store)
	if effect := saved.Plots[1].PestEffect; effect == nil ||
		effect.GetSourcePlayerId() != visitorID || effect.EffectItemOrPestId != developmentPestID {
		t.Fatalf("unexpected saved pest effect: %+v", effect)
	}
	if _, _, _, replay, err := runtime.ApplyApplyPestOnOwner(
		context.Background(), ownerID, LocalOwnerEpoch, visitorID, id, 1, developmentPestID,
	); err != nil || !replay {
		t.Fatalf("replay apply pest: replay=%v err=%v", replay, err)
	}
	if _, _, _, _, err := runtime.ApplyApplyPestOnOwner(
		context.Background(), ownerID, LocalOwnerEpoch, visitorID,
		interactionIDFixture(0x42), 1, developmentPestID,
	); !errors.Is(err, ErrPestAlreadyPresent) {
		t.Fatalf("second pest error = %v", err)
	}
}

func TestCatchOwnPestForbidden(t *testing.T) {
	const ownerID, visitorID = uint64(11), uint64(7)
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	state := developmentStateAt(ownerID, now)
	state.Plots[1] = friendGrowingPlotAt(1, now)
	state.Plots[1].PestEffect = &datav1.TimedEffectRecord{
		EffectKind: datav1.EffectKind_PEST, EffectItemOrPestId: 1,
		ConfigVersion: 1, Modifier: &datav1.RateDecimal6{ScaledValue: -300_000},
		StartAtMs: now.Add(-time.Second).UnixMilli(), EndAtMs: now.Add(time.Minute).UnixMilli(),
		SourcePlayerId: uint64Ptr(visitorID),
	}
	store := &recordingCheckpointStore{state: state}
	runtime := NewRuntime()
	runtime.store = store
	runtime.SetNow(func() time.Time { return now })
	defer runtime.Close()
	if _, _, _, _, err := runtime.ApplyCatchPestOnOwner(
		context.Background(), ownerID, LocalOwnerEpoch, visitorID,
		interactionIDFixture(0x51), 1,
	); !errors.Is(err, ErrPestSourceForbidden) {
		t.Fatalf("catch own pest error = %v", err)
	}
	if len(store.saved) != 0 {
		t.Fatal("deterministic rejection must not persist")
	}
}

func TestHelpCleanSuccessAndApplyPestTaskOnce(t *testing.T) {
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	ownerState := developmentStateAt(11, now)
	ownerState.Plots[1] = &Plot{ID: 1, State: plotv1.PlotState_NEED_CLEANUP}
	ownerStore := &recordingCheckpointStore{state: ownerState}
	ownerRuntime := NewRuntime()
	ownerRuntime.store = ownerStore
	ownerRuntime.SetNow(func() time.Time { return now })
	defer ownerRuntime.Close()
	if _, _, _, _, err := ownerRuntime.ApplyHelpCleanOnOwner(
		context.Background(), 11, LocalOwnerEpoch, 7, interactionIDFixture(0x61), 1,
	); err != nil {
		t.Fatalf("help clean: %v", err)
	}
	if len(ownerStore.saved) != 0 {
		t.Fatal("owner action must not synchronously persist")
	}
	if err := ownerRuntime.flushDirty(context.Background()); err != nil {
		t.Fatalf("flush dirty clean: %v", err)
	}
	if got := latestSavedState(t, ownerStore).Plots[1].State; got != plotv1.PlotState_EMPTY {
		t.Fatalf("cleaned plot state = %v", got)
	}

	visitorState := developmentStateAt(7, now)
	visitorState.Tasks = []Task{{ID: applyPestTaskID, Target: 1}}
	visitorStore := &recordingCheckpointStore{state: visitorState}
	visitorRuntime := NewRuntime()
	visitorRuntime.store = visitorStore
	visitorRuntime.SetNow(func() time.Time { return now })
	defer visitorRuntime.Close()
	id := interactionIDFixture(0x62)
	action := datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND
	if _, err := visitorRuntime.ReserveActionChance(context.Background(), 7, LocalOwnerEpoch, id, action); err != nil {
		t.Fatalf("reserve apply pest: %v", err)
	}
	if _, replay, err := visitorRuntime.CommitActionChance(
		context.Background(), 7, LocalOwnerEpoch, id, action, nil,
	); err != nil || replay {
		t.Fatalf("commit apply pest: replay=%v err=%v", replay, err)
	}
	if _, replay, err := visitorRuntime.CommitActionChance(
		context.Background(), 7, LocalOwnerEpoch, id, action, nil,
	); err != nil || !replay {
		t.Fatalf("replay commit apply pest: replay=%v err=%v", replay, err)
	}
	saved := latestSavedState(t, visitorStore)
	if len(saved.Tasks) != 1 || saved.Tasks[0].Current != 1 {
		t.Fatalf("apply pest task advanced incorrectly: %+v", saved.Tasks)
	}
	if saved.FriendActions.ApplyPestChances != friendActionInitialChances-1 {
		t.Fatalf("apply pest chances = %d", saved.FriendActions.ApplyPestChances)
	}
}
