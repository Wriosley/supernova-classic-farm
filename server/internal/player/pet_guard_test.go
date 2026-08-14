package player

import (
	"bytes"
	"context"
	"testing"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"google.golang.org/protobuf/proto"
)

func ownerWithGuardPet(
	playerID uint64, plotID, petID uint32, now time.Time, foodActive bool,
) *State {
	state := ownerStateWithMaturePlot(playerID, plotID, now)
	until := int64(0)
	if foodActive {
		until = now.Add(24 * time.Hour).UnixMilli()
	} else {
		until = now.Add(-time.Hour).UnixMilli()
	}
	state.PetState = &datav1.PetStateRecord{
		OwnedPetIds: []uint32{petID}, ActivePetId: petID, FoodActiveUntilMs: until,
	}
	return state
}

func decodeStealGuard(t *testing.T, payload []byte) *wsv1.StealGuardOutcome {
	t.Helper()
	response := &wsv1.FriendActionResponse{}
	if err := proto.Unmarshal(payload, response); err != nil {
		t.Fatalf("unmarshal owner payload: %v", err)
	}
	return response.StealGuard
}

func TestStealWithoutActivePetDoesNotRollGuard(t *testing.T) {
	const ownerID = uint64(11)
	const plotID = uint32(1)
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: ownerStateWithMaturePlot(ownerID, plotID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.SetNow(func() time.Time { return now })
	runtime.randIntn = func(int) int { t.Fatal("randIntn must not be called"); return 0 }
	defer runtime.Close()

	payload, _, _, _, err := applySteal(t, runtime, ownerID, 99, interactionIDFixture(0xA1), plotID, 4001)
	if err != nil {
		t.Fatal(err)
	}
	guard := decodeStealGuard(t, payload)
	if guard.GetGuardTriggered() || guard.GetPetId() != 0 || guard.GetFoodActive() {
		t.Fatalf("unexpected guard = %+v", guard)
	}
}

func TestStealWithActivePetAlwaysGuardsEvenWithoutFood(t *testing.T) {
	const ownerID = uint64(11)
	const plotID = uint32(1)
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	state := ownerWithGuardPet(ownerID, plotID, developmentVillageDogPetID, now, false)
	store := &recordingCheckpointStore{state: state}
	runtime := NewRuntime()
	runtime.store = store
	runtime.SetNow(func() time.Time { return now })
	runtime.randIntn = func(n int) int {
		if n != 10 {
			t.Fatalf("randIntn(%d), want 10", n)
		}
		return 4
	}
	defer runtime.Close()

	payload, _, _, _, err := applySteal(t, runtime, ownerID, 99, interactionIDFixture(0xA2), plotID, 4001)
	if err != nil {
		t.Fatal(err)
	}
	guard := decodeStealGuard(t, payload)
	if guard.GetPetId() != developmentVillageDogPetID ||
		guard.GetFoodActive() ||
		!guard.GetGuardTriggered() ||
		guard.GetGuardPenaltyConfigured() != 5 ||
		guard.GetGuardProbabilityBps() != 10000 {
		t.Fatalf("guard = %+v", guard)
	}
}

func TestActivePetGuardDeductsRandomCoinsOnVisitorSideEffect(t *testing.T) {
	const ownerID = uint64(11)
	const visitorID = uint64(22)
	const plotID = uint32(1)
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)

	ownerStore := &recordingCheckpointStore{
		state: ownerWithGuardPet(ownerID, plotID, developmentVillageDogPetID, now, true),
	}
	ownerRuntime := NewRuntime()
	ownerRuntime.store = ownerStore
	ownerRuntime.SetNow(func() time.Time { return now })
	ownerRuntime.randIntn = func(int) int { return 1 } // penalty 2
	defer ownerRuntime.Close()

	visitorState := visitorStateWithStealTask(visitorID, now)
	visitorState.Coins = 10
	visitorStore := &recordingCheckpointStore{state: visitorState}
	visitorRuntime := NewRuntime()
	visitorRuntime.store = visitorStore
	visitorRuntime.SetNow(func() time.Time { return now })
	defer visitorRuntime.Close()

	interactionID := interactionIDFixture(0xB1)
	ownerPayload, _, _, _, err := applySteal(t, ownerRuntime, ownerID, visitorID, interactionID, plotID, 4001)
	if err != nil {
		t.Fatal(err)
	}
	guard := decodeStealGuard(t, ownerPayload)
	if !guard.GetGuardTriggered() || guard.GetGuardPenaltyConfigured() != 2 {
		t.Fatalf("owner guard = %+v", guard)
	}

	response, _, err := visitorRuntime.ApplyVisitorFriendSideEffect(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID,
		datav1.FriendInteractionAction_STEAL_FRIEND_CROP, ownerPayload,
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.GetStealGuard().GetGuardPenaltyApplied() != 2 {
		t.Fatalf("applied = %d", response.GetStealGuard().GetGuardPenaltyApplied())
	}
	if response.GetVisitorPatch().GetCoinBalance() != 8 {
		t.Fatalf("visitor coins = %d", response.GetVisitorPatch().GetCoinBalance())
	}
	if ownerStore.saved[len(ownerStore.saved)-1].CoinBalance != InitialCoinBalance {
		t.Fatalf("owner coins changed")
	}
}

func TestPetGuardNeverMakesVisitorCoinsNegative(t *testing.T) {
	const ownerID = uint64(11)
	const visitorID = uint64(24)
	const plotID = uint32(1)
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)

	ownerRuntime := NewRuntime()
	ownerRuntime.store = &recordingCheckpointStore{
		state: ownerWithGuardPet(ownerID, plotID, developmentShepherdDogPetID, now, true),
	}
	ownerRuntime.SetNow(func() time.Time { return now })
	ownerRuntime.randIntn = func(int) int { return 3 } // configured 4
	defer ownerRuntime.Close()

	visitorState := visitorStateWithStealTask(visitorID, now)
	visitorState.Coins = 3
	visitorRuntime := NewRuntime()
	visitorRuntime.store = &recordingCheckpointStore{state: visitorState}
	visitorRuntime.SetNow(func() time.Time { return now })
	defer visitorRuntime.Close()

	interactionID := interactionIDFixture(0xB3)
	ownerPayload, _, _, _, err := applySteal(t, ownerRuntime, ownerID, visitorID, interactionID, plotID, 4001)
	if err != nil {
		t.Fatal(err)
	}
	response, _, err := visitorRuntime.ApplyVisitorFriendSideEffect(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID,
		datav1.FriendInteractionAction_STEAL_FRIEND_CROP, ownerPayload,
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.GetStealGuard().GetGuardPenaltyApplied() != 3 ||
		response.GetVisitorPatch().GetCoinBalance() != 0 {
		t.Fatalf("response = %+v", response)
	}
}

func TestPetGuardOutcomeIsStableAcrossOwnerRetry(t *testing.T) {
	const ownerID = uint64(11)
	const plotID = uint32(1)
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{
		state: ownerWithGuardPet(ownerID, plotID, developmentVillageDogPetID, now, true),
	}
	runtime := NewRuntime()
	runtime.store = store
	runtime.SetNow(func() time.Time { return now })
	rolls := 0
	runtime.randIntn = func(n int) int {
		rolls++
		return 6
	}
	defer runtime.Close()

	interactionID := interactionIDFixture(0xB4)
	payload1, digest1, _, _, err := applySteal(t, runtime, ownerID, 99, interactionID, plotID, 4001)
	if err != nil {
		t.Fatal(err)
	}
	payload2, digest2, _, already, err := applySteal(t, runtime, ownerID, 99, interactionID, plotID, 4001)
	if err != nil || !already {
		t.Fatalf("retry already=%v err=%v", already, err)
	}
	if rolls != 1 {
		t.Fatalf("guard rolled %d times, want 1", rolls)
	}
	if !bytes.Equal(payload1, payload2) || !bytes.Equal(digest1, digest2) {
		t.Fatal("retry produced different frozen outcome")
	}
	if decodeStealGuard(t, payload1).GetGuardPenaltyConfigured() != 7 {
		t.Fatalf("penalty = %d", decodeStealGuard(t, payload1).GetGuardPenaltyConfigured())
	}
}

func TestCatchAndCleanCreditOneCoin(t *testing.T) {
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	visitorID := uint64(40)
	for i, action := range []datav1.FriendInteractionAction{
		datav1.FriendInteractionAction_CATCH_PEST_FOR_FRIEND,
		datav1.FriendInteractionAction_HELP_CLEAN_FRIEND_PLOT,
	} {
		state := NewDevelopmentState(visitorID)
		state.Coins = 5
		state.CreatedAtMS = now.Add(-time.Minute).UnixMilli()
		state.UpdatedAtMS = now.Add(-time.Minute).UnixMilli()
		runtime := NewRuntime()
		runtime.store = &recordingCheckpointStore{state: state}
		runtime.SetNow(func() time.Time { return now })
		defer runtime.Close()

		response, _, err := runtime.ApplyVisitorFriendSideEffect(
			context.Background(), visitorID, LocalOwnerEpoch, interactionIDFixture(0xC1+byte(i)),
			action, nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		if response.GetVisitorPatch().GetCoinBalance() != 6 {
			t.Fatalf("action %v coins = %d", action, response.GetVisitorPatch().GetCoinBalance())
		}
	}
}

func TestApplyPestCreditsRandomCoinDelta(t *testing.T) {
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	visitorID := uint64(41)
	state := NewDevelopmentState(visitorID)
	state.Coins = 20
	state.CreatedAtMS = now.Add(-time.Minute).UnixMilli()
	state.UpdatedAtMS = now.Add(-time.Minute).UnixMilli()
	runtime := NewRuntime()
	runtime.store = &recordingCheckpointStore{state: state}
	runtime.SetNow(func() time.Time { return now })
	runtime.randIntn = func(n int) int {
		if n != 21 {
			t.Fatalf("randIntn(%d), want 21", n)
		}
		return 5 // delta = 5-10 = -5
	}
	defer runtime.Close()

	response, _, err := runtime.ApplyVisitorFriendSideEffect(
		context.Background(), visitorID, LocalOwnerEpoch, interactionIDFixture(0xD1),
		datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.GetVisitorPatch().GetCoinBalance() != 15 {
		t.Fatalf("coins = %d", response.GetVisitorPatch().GetCoinBalance())
	}
	replay, already, err := runtime.ApplyVisitorFriendSideEffect(
		context.Background(), visitorID, LocalOwnerEpoch, interactionIDFixture(0xD1),
		datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND, nil,
	)
	if err != nil || !already {
		t.Fatalf("replay already=%v err=%v", already, err)
	}
	if replay.GetVisitorPatch().GetCoinBalance() != 15 {
		t.Fatalf("replay coins = %d", replay.GetVisitorPatch().GetCoinBalance())
	}
}

// Keep CommitSteal path covering old reservation-based penalty application
// with the new random configured amount.
func TestCommitStealStillAppliesFrozenGuardPenalty(t *testing.T) {
	const ownerID = uint64(11)
	const visitorID = uint64(25)
	const plotID = uint32(1)
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)

	ownerRuntime := NewRuntime()
	ownerRuntime.store = &recordingCheckpointStore{
		state: ownerWithGuardPet(ownerID, plotID, developmentVillageDogPetID, now, true),
	}
	ownerRuntime.SetNow(func() time.Time { return now })
	ownerRuntime.randIntn = func(int) int { return 1 }
	defer ownerRuntime.Close()

	visitorState := visitorStateWithStealTask(visitorID, now)
	visitorState.Coins = 10
	visitorRuntime := NewRuntime()
	visitorRuntime.store = &recordingCheckpointStore{state: visitorState}
	visitorRuntime.SetNow(func() time.Time { return now })
	defer visitorRuntime.Close()

	interactionID := interactionIDFixture(0xB5)
	if _, err := visitorRuntime.ReserveSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1,
	); err != nil {
		t.Fatal(err)
	}
	ownerPayload, _, _, _, err := applySteal(t, ownerRuntime, ownerID, visitorID, interactionID, plotID, 4001)
	if err != nil {
		t.Fatal(err)
	}
	response, _, err := visitorRuntime.CommitSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1, ownerPayload,
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.GetStealGuard().GetGuardPenaltyApplied() != 2 ||
		response.GetVisitorPatch().GetCoinBalance() != 8 {
		t.Fatalf("response = %+v", response)
	}
}
