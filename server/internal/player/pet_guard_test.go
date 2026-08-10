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
	runtime.now = func() time.Time { return now }
	runtime.randBPS = func() uint32 { t.Fatal("randBPS must not be called"); return 0 }
	defer runtime.Close()

	payload, _, _, _, err := runtime.ApplyStealOnOwner(
		context.Background(), ownerID, LocalOwnerEpoch, 99, interactionIDFixture(0xA1), plotID,
	)
	if err != nil {
		t.Fatal(err)
	}
	guard := decodeStealGuard(t, payload)
	if guard.GetGuardTriggered() || guard.GetPetId() != 0 || guard.GetFoodActive() {
		t.Fatalf("unexpected guard = %+v", guard)
	}
}

func TestStealWithoutActiveFoodDoesNotRollGuard(t *testing.T) {
	const ownerID = uint64(11)
	const plotID = uint32(1)
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	state := ownerWithGuardPet(ownerID, plotID, developmentVillageDogPetID, now, false)
	store := &recordingCheckpointStore{state: state}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	called := false
	runtime.randBPS = func() uint32 { called = true; return 0 }
	defer runtime.Close()

	payload, _, _, _, err := runtime.ApplyStealOnOwner(
		context.Background(), ownerID, LocalOwnerEpoch, 99, interactionIDFixture(0xA2), plotID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("expired food must not roll guard")
	}
	guard := decodeStealGuard(t, payload)
	if guard.GetPetId() != developmentVillageDogPetID || guard.GetFoodActive() || guard.GetGuardTriggered() {
		t.Fatalf("guard = %+v", guard)
	}
}

func TestVillageDogGuardDeductsTwoCoins(t *testing.T) {
	const ownerID = uint64(11)
	const visitorID = uint64(22)
	const plotID = uint32(1)
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)

	ownerStore := &recordingCheckpointStore{
		state: ownerWithGuardPet(ownerID, plotID, developmentVillageDogPetID, now, true),
	}
	ownerRuntime := NewRuntime()
	ownerRuntime.store = ownerStore
	ownerRuntime.now = func() time.Time { return now }
	ownerRuntime.randBPS = func() uint32 { return 0 } // 强制触发
	defer ownerRuntime.Close()

	visitorState := visitorStateWithStealTask(visitorID, now)
	visitorState.Coins = 10
	visitorStore := &recordingCheckpointStore{state: visitorState}
	visitorRuntime := NewRuntime()
	visitorRuntime.store = visitorStore
	visitorRuntime.now = func() time.Time { return now }
	defer visitorRuntime.Close()

	interactionID := interactionIDFixture(0xB1)
	if _, err := visitorRuntime.ReserveSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1,
	); err != nil {
		t.Fatal(err)
	}
	ownerPayload, _, _, _, err := ownerRuntime.ApplyStealOnOwner(
		context.Background(), ownerID, LocalOwnerEpoch, visitorID, interactionID, plotID,
	)
	if err != nil {
		t.Fatal(err)
	}
	guard := decodeStealGuard(t, ownerPayload)
	if !guard.GetGuardTriggered() || guard.GetGuardPenaltyConfigured() != 2 {
		t.Fatalf("owner guard = %+v", guard)
	}

	response, _, err := visitorRuntime.CommitSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1, ownerPayload,
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
	if inventoryQuantity(visitorStore.saved[len(visitorStore.saved)-1], 4001) != 1 {
		t.Fatal("steal crop not credited")
	}
	// 主人金币不变
	if ownerStore.saved[len(ownerStore.saved)-1].CoinBalance != InitialCoinBalance {
		t.Fatalf("owner coins changed")
	}
}

func TestShepherdGuardDeductsUpToFourCoins(t *testing.T) {
	const ownerID = uint64(11)
	const visitorID = uint64(23)
	const plotID = uint32(1)
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)

	ownerStore := &recordingCheckpointStore{
		state: ownerWithGuardPet(ownerID, plotID, developmentShepherdDogPetID, now, true),
	}
	ownerRuntime := NewRuntime()
	ownerRuntime.store = ownerStore
	ownerRuntime.now = func() time.Time { return now }
	ownerRuntime.randBPS = func() uint32 { return 0 }
	defer ownerRuntime.Close()

	visitorState := visitorStateWithStealTask(visitorID, now)
	visitorState.Coins = 10
	visitorStore := &recordingCheckpointStore{state: visitorState}
	visitorRuntime := NewRuntime()
	visitorRuntime.store = visitorStore
	visitorRuntime.now = func() time.Time { return now }
	defer visitorRuntime.Close()

	interactionID := interactionIDFixture(0xB2)
	if _, err := visitorRuntime.ReserveSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1,
	); err != nil {
		t.Fatal(err)
	}
	ownerPayload, _, _, _, err := ownerRuntime.ApplyStealOnOwner(
		context.Background(), ownerID, LocalOwnerEpoch, visitorID, interactionID, plotID,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, _, err := visitorRuntime.CommitSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1, ownerPayload,
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.GetStealGuard().GetGuardPenaltyApplied() != 4 ||
		response.GetVisitorPatch().GetCoinBalance() != 6 {
		t.Fatalf("response = %+v", response)
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
	ownerRuntime.now = func() time.Time { return now }
	ownerRuntime.randBPS = func() uint32 { return 0 }
	defer ownerRuntime.Close()

	visitorState := visitorStateWithStealTask(visitorID, now)
	visitorState.Coins = 3
	visitorRuntime := NewRuntime()
	visitorRuntime.store = &recordingCheckpointStore{state: visitorState}
	visitorRuntime.now = func() time.Time { return now }
	defer visitorRuntime.Close()

	interactionID := interactionIDFixture(0xB3)
	if _, err := visitorRuntime.ReserveSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1,
	); err != nil {
		t.Fatal(err)
	}
	ownerPayload, _, _, _, err := ownerRuntime.ApplyStealOnOwner(
		context.Background(), ownerID, LocalOwnerEpoch, visitorID, interactionID, plotID,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, _, err := visitorRuntime.CommitSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1, ownerPayload,
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.GetStealGuard().GetGuardPenaltyApplied() != 3 ||
		response.GetVisitorPatch().GetCoinBalance() != 0 {
		t.Fatalf("response = %+v", response)
	}
}

func TestPetGuardOutcomeIsStableAcrossSagaRetry(t *testing.T) {
	const ownerID = uint64(11)
	const plotID = uint32(1)
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{
		state: ownerWithGuardPet(ownerID, plotID, developmentVillageDogPetID, now, true),
	}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	rolls := 0
	runtime.randBPS = func() uint32 {
		rolls++
		return 0
	}
	defer runtime.Close()

	interactionID := interactionIDFixture(0xB4)
	payload1, digest1, _, _, err := runtime.ApplyStealOnOwner(
		context.Background(), ownerID, LocalOwnerEpoch, 99, interactionID, plotID,
	)
	if err != nil {
		t.Fatal(err)
	}
	payload2, digest2, _, already, err := runtime.ApplyStealOnOwner(
		context.Background(), ownerID, LocalOwnerEpoch, 99, interactionID, plotID,
	)
	if err != nil || !already {
		t.Fatalf("retry already=%v err=%v", already, err)
	}
	if rolls != 1 {
		t.Fatalf("guard rolled %d times, want 1", rolls)
	}
	if !bytes.Equal(payload1, payload2) || !bytes.Equal(digest1, digest2) {
		t.Fatal("retry produced different frozen outcome")
	}
}

func TestPetGuardPenaltyIsAppliedOnceAfterCrashRecovery(t *testing.T) {
	const ownerID = uint64(11)
	const visitorID = uint64(25)
	const plotID = uint32(1)
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)

	ownerRuntime := NewRuntime()
	ownerRuntime.store = &recordingCheckpointStore{
		state: ownerWithGuardPet(ownerID, plotID, developmentVillageDogPetID, now, true),
	}
	ownerRuntime.now = func() time.Time { return now }
	ownerRuntime.randBPS = func() uint32 { return 0 }
	defer ownerRuntime.Close()

	visitorState := visitorStateWithStealTask(visitorID, now)
	visitorState.Coins = 10
	visitorStore := &flakyCheckpointStore{
		recordingCheckpointStore: recordingCheckpointStore{state: visitorState},
		failures:                 1,
		failStatus:               CheckpointWriteRetryableFailure,
	}
	visitorRuntime := NewRuntime()
	visitorRuntime.store = visitorStore
	visitorRuntime.now = func() time.Time { return now }
	defer visitorRuntime.Close()

	interactionID := interactionIDFixture(0xB5)
	if _, err := visitorRuntime.ReserveSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1,
	); err != nil {
		// Reserve may also hit flaky store; reset failures for commit path.
	}
	// 重新准备：确保有 RESERVED 预约且金币为 10。
	visitorStore.failures = 0
	visitorStore.attempts = 0
	visitorStore.saved = nil
	visitorStore.state = visitorStateWithStealTask(visitorID, now)
	visitorStore.state.Coins = 10
	if _, err := visitorRuntime.ReserveSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1,
	); err != nil {
		t.Fatal(err)
	}

	ownerPayload, _, _, _, err := ownerRuntime.ApplyStealOnOwner(
		context.Background(), ownerID, LocalOwnerEpoch, visitorID, interactionID, plotID,
	)
	if err != nil {
		t.Fatal(err)
	}

	visitorStore.failures = 1
	visitorStore.failStatus = CheckpointWriteRetryableFailure
	if _, _, err := visitorRuntime.CommitSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1, ownerPayload,
	); err == nil {
		t.Fatal("expected first commit flush failure")
	}

	response, already, err := visitorRuntime.CommitSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1, ownerPayload,
	)
	if err != nil {
		t.Fatal(err)
	}
	if already {
		// 第二次应是真正成功的首次持久化，或 replay；金币都必须只扣一次。
	}
	if response.GetVisitorPatch().GetCoinBalance() != 8 {
		t.Fatalf("coins after recovery = %d, want 8", response.GetVisitorPatch().GetCoinBalance())
	}
	final := visitorStore.saved[len(visitorStore.saved)-1]
	if final.CoinBalance != 8 {
		t.Fatalf("persisted coins = %d", final.CoinBalance)
	}

	replay, alreadyCommitted, err := visitorRuntime.CommitSteal(
		context.Background(), visitorID, LocalOwnerEpoch, interactionID, 4001, 1, ownerPayload,
	)
	if err != nil || !alreadyCommitted {
		t.Fatalf("durable replay: already=%v err=%v", alreadyCommitted, err)
	}
	if replay.GetVisitorPatch().GetCoinBalance() != 8 {
		t.Fatalf("replay coins = %d", replay.GetVisitorPatch().GetCoinBalance())
	}
}
