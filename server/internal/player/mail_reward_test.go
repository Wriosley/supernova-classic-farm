package player

import (
	"context"
	"testing"
	"time"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
)

func TestApplyMailRewardAllOrNothingAndReplay(t *testing.T) {
	const playerID = uint64(42)
	now := time.Date(2026, 8, 12, 15, 0, 0, 0, time.UTC)
	state := NewDevelopmentState(playerID)
	state.CreatedAtMS = now.UnixMilli()
	state.UpdatedAtMS = now.UnixMilli()
	state.Inventory = map[uint32]uint32{1002: 1}
	store := &recordingCheckpointStore{state: state}
	runtime := NewRuntime()
	runtime.store = store
	runtime.SetNow(func() time.Time { return now })
	defer runtime.Close()

	claimID := make([]byte, 16)
	for i := range claimID {
		claimID[i] = byte(i + 1)
	}
	first, err := runtime.ApplyMailReward(context.Background(), playerID, LocalOwnerEpoch, claimID, "m1",
		[]MailRewardAttachment{{ItemID: 1002, Quantity: 2}}, 0,
	)
	if err != nil || !first.NewlyApplied || first.ItemsAdded[0].Quantity != 2 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	if runtime.actors[playerID].state.Inventory[1002] != 3 {
		t.Fatalf("inventory=%v", runtime.actors[playerID].state.Inventory)
	}
	if len(store.saved) == 0 {
		t.Fatal("expected sync SaveCAS")
	}

	second, err := runtime.ApplyMailReward(context.Background(), playerID, LocalOwnerEpoch, claimID, "m1",
		[]MailRewardAttachment{{ItemID: 1002, Quantity: 2}}, 0,
	)
	if err != nil || second.NewlyApplied {
		t.Fatalf("replay=%+v err=%v", second, err)
	}
	if runtime.actors[playerID].state.Inventory[1002] != 3 {
		t.Fatal("replay must not double-apply")
	}

	_, err = runtime.ApplyMailReward(context.Background(), playerID, LocalOwnerEpoch, claimID, "m1",
		[]MailRewardAttachment{{ItemID: 1002, Quantity: 1}}, 0,
	)
	if err != ErrMailClaimConflict {
		t.Fatalf("conflict err=%v", err)
	}
}

func TestApplyMailRewardCapacity(t *testing.T) {
	const playerID = uint64(9)
	now := time.Date(2026, 8, 12, 16, 0, 0, 0, time.UTC)
	state := NewDevelopmentState(playerID)
	state.CreatedAtMS = now.UnixMilli()
	state.UpdatedAtMS = now.UnixMilli()
	state.Inventory = map[uint32]uint32{1002: inventoryStackLimit}
	store := &recordingCheckpointStore{state: state}
	runtime := NewRuntime()
	runtime.store = store
	runtime.SetNow(func() time.Time { return now })
	defer runtime.Close()

	claimID := make([]byte, 16)
	claimID[0] = 9
	_, err := runtime.ApplyMailReward(context.Background(), playerID, LocalOwnerEpoch, claimID, "m2",
		[]MailRewardAttachment{{ItemID: 1002, Quantity: 1}}, 0,
	)
	if err != ErrMailInventoryCapacity {
		t.Fatalf("err=%v", err)
	}
	if runtime.actors[playerID].state.Inventory[1002] != inventoryStackLimit {
		t.Fatal("capacity failure must not mutate inventory")
	}
	_ = wsv1.Action_CLAIM_MAIL
}
