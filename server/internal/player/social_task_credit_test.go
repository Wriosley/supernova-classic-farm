package player

import (
	"context"
	"testing"
	"time"

	chapterv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/chapter"
)

func friendTaskState(playerID uint64, now time.Time) *State {
	state := NewDevelopmentState(playerID)
	state.CreatedAtMS = now.Add(-time.Minute).UnixMilli()
	state.UpdatedAtMS = now.Add(-time.Minute).UnixMilli()
	state.Tasks = []Task{{ID: addFriendTaskID, Target: 1}}
	return state
}

func relationIDFixture(fill byte) []byte {
	id := make([]byte, 16)
	for i := range id {
		id[i] = fill
	}
	return id
}

func TestApplyFriendTaskCreditMigratesCreditsAndFlushesSynchronously(t *testing.T) {
	const playerID = uint64(42)
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: friendTaskState(playerID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.SetNow(func() time.Time { return now })
	defer runtime.Close()

	relationID := relationIDFixture(0xAB)
	newlyApplied, playerSeq, err := runtime.ApplyFriendTaskCredit(
		context.Background(), playerID, LocalOwnerEpoch, relationID,
	)
	if err != nil {
		t.Fatalf("ApplyFriendTaskCredit: %v", err)
	}
	if !newlyApplied {
		t.Fatalf("expected newlyApplied=true on first credit")
	}
	if playerSeq != 1 {
		t.Fatalf("expected player_seq=1, got %d", playerSeq)
	}

	if len(store.saved) != 1 {
		t.Fatalf("expected exactly one synchronous checkpoint write, got %d", len(store.saved))
	}
	saved := store.saved[0]
	if saved.FriendActions == nil ||
		saved.FriendActions.ApplyPestChances != friendActionInitialChances ||
		saved.FriendActions.CatchPestChances != friendActionInitialChances ||
		saved.FriendActions.HelpCleanChances != friendActionInitialChances {
		t.Fatalf("expected migrated friend_actions with initial chances, got %+v", saved.FriendActions)
	}
	if saved.SchemaVersion != CheckpointSchemaVersion {
		t.Fatalf("expected schema_version=%d after migration, got %d", CheckpointSchemaVersion, saved.SchemaVersion)
	}
	if len(saved.FriendTaskCreditReceipts) != 1 ||
		string(saved.FriendTaskCreditReceipts[0].RelationId) != string(relationID) {
		t.Fatalf("expected one task credit receipt for the relation, got %+v", saved.FriendTaskCreditReceipts)
	}
	if saved.CurrentChapter.Tasks[0].CurrentValue != 1 || !saved.CurrentChapter.Tasks[0].Completed {
		t.Fatalf("expected TASK_ADD_FRIEND completed, got %+v", saved.CurrentChapter.Tasks[0])
	}
	if saved.CurrentChapter.Status != chapterStatusToRecord(chapterv1.ChapterStatus_CLAIMABLE) {
		t.Fatalf("expected chapter CLAIMABLE once its only task completes, got %v", saved.CurrentChapter.Status)
	}
}

func TestApplyFriendTaskCreditIsIdempotentOnRetry(t *testing.T) {
	const playerID = uint64(42)
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: friendTaskState(playerID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.SetNow(func() time.Time { return now })
	defer runtime.Close()

	relationID := relationIDFixture(0xCD)
	firstApplied, firstSeq, err := runtime.ApplyFriendTaskCredit(
		context.Background(), playerID, LocalOwnerEpoch, relationID,
	)
	if err != nil || !firstApplied {
		t.Fatalf("first ApplyFriendTaskCredit: applied=%v err=%v", firstApplied, err)
	}
	if len(store.saved) != 1 {
		t.Fatalf("expected one write after first call, got %d", len(store.saved))
	}

	secondApplied, secondSeq, err := runtime.ApplyFriendTaskCredit(
		context.Background(), playerID, LocalOwnerEpoch, relationID,
	)
	if err != nil {
		t.Fatalf("second ApplyFriendTaskCredit: %v", err)
	}
	if secondApplied {
		t.Fatalf("expected newlyApplied=false on retry")
	}
	if secondSeq != firstSeq {
		t.Fatalf("expected player_seq unchanged on retry, got %d vs %d", secondSeq, firstSeq)
	}
	if len(store.saved) != 1 {
		t.Fatalf("expected retry to skip the synchronous flush, got %d writes", len(store.saved))
	}
}

func TestApplyFriendTaskCreditRejectsInvalidRelationID(t *testing.T) {
	const playerID = uint64(42)
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: friendTaskState(playerID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.SetNow(func() time.Time { return now })
	defer runtime.Close()

	if _, _, err := runtime.ApplyFriendTaskCredit(
		context.Background(), playerID, LocalOwnerEpoch, []byte{1, 2, 3},
	); err == nil {
		t.Fatal("expected an error for a malformed relation ID")
	}
}

func TestApplyFriendTaskCreditWithoutTargetTaskStillCreditsReceipt(t *testing.T) {
	const playerID = uint64(42)
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	state := NewDevelopmentState(playerID)
	state.CreatedAtMS = now.Add(-time.Minute).UnixMilli()
	state.UpdatedAtMS = now.Add(-time.Minute).UnixMilli()
	store := &recordingCheckpointStore{state: state}
	runtime := NewRuntime()
	runtime.store = store
	runtime.SetNow(func() time.Time { return now })
	defer runtime.Close()

	relationID := relationIDFixture(0xEF)
	newlyApplied, playerSeq, err := runtime.ApplyFriendTaskCredit(
		context.Background(), playerID, LocalOwnerEpoch, relationID,
	)
	if err != nil || !newlyApplied || playerSeq != 1 {
		t.Fatalf("unexpected result without TASK_ADD_FRIEND: applied=%v seq=%d err=%v",
			newlyApplied, playerSeq, err)
	}
	if len(store.saved) != 1 || len(store.saved[0].FriendTaskCreditReceipts) != 1 {
		t.Fatalf("expected the receipt to still be recorded, got %+v", store.saved)
	}
}
