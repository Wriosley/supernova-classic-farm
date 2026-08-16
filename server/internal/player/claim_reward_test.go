package player

import (
	"context"
	"testing"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	eventv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/event"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	chapterv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/chapter"
	"google.golang.org/protobuf/proto"
)

func claimRewardRequest(playerID uint64, requestID string, chapterID uint32) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_REQUEST,
		Action:    wsv1.Action_CLAIM_CHAPTER_REWARD,
		RequestId: requestID, TargetPlayerId: playerID,
		Payload: &wsv1.WsEnvelope_ClaimChapterRewardRequest{
			ClaimChapterRewardRequest: &wsv1.ClaimChapterRewardRequest{ChapterId: chapterID},
		},
	}
}

func claimableRewardState(playerID uint64, now time.Time) *State {
	state := NewDevelopmentState(playerID)
	state.PlayerSeq = 6
	state.CheckpointRevision = 7
	state.CreatedAtMS = now.Add(-4 * time.Minute).UnixMilli()
	state.UpdatedAtMS = now.UnixMilli()
	state.Coins = 19
	state.Inventory = map[uint32]uint32{developmentSeedItemID: 2}
	for index := range state.Tasks {
		state.Tasks[index].Current = state.Tasks[index].Target
	}
	state.Chapter = chapterv1.ChapterStatus_CLAIMABLE
	return state
}

func TestClaimRewardCreditsInventoryActivatesNextChapterAndReplays(t *testing.T) {
	const playerID = uint64(42)
	now := time.Date(2026, 7, 31, 11, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: claimableRewardState(playerID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.SetNow(func() time.Time { return now })
	defer runtime.Close()

	request := claimRewardRequest(
		playerID, "00112233-4455-6677-8899-aabbccddee61", InitialChapterID,
	)
	first, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, request)
	if err != nil {
		t.Fatal(err)
	}
	claimed := first.GetClaimChapterRewardResponse()
	if first.GetError() != nil ||
		first.GetStateVersion().GetPlayerSeq() != 7 ||
		claimed.GetChapterId() != InitialChapterID ||
		claimed.GetCoinGranted() != developmentChapterRewardCoins ||
		len(claimed.GetItemsAddedToInventory()) != 2 ||
		len(claimed.GetItemsPendingMail()) != 0 ||
		claimed.GetPatch().GetCoinBalance() != 29 ||
		claimed.GetPatch().GetCurrentChapter().GetChapterId() != developmentNextChapterID ||
		claimed.GetPatch().GetCurrentChapter().GetStatus() != chapterv1.ChapterStatus_IN_PROGRESS {
		t.Fatalf("unexpected CLAIM_CHAPTER_REWARD response: %+v", first)
	}
	if claimed.GetItemsAddedToInventory()[0].GetItemId() != BasicFertilizerID ||
		claimed.GetItemsAddedToInventory()[0].GetQuantity() != 1 ||
		claimed.GetItemsAddedToInventory()[1].GetItemId() != developmentPumpkinSeedItemID ||
		claimed.GetItemsAddedToInventory()[1].GetQuantity() != 3 {
		t.Fatalf("unexpected chapter one rewards: %+v", claimed.GetItemsAddedToInventory())
	}
	replay, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, request)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.GetReplayed() || replay.GetStateVersion().GetPlayerSeq() != 7 ||
		!proto.Equal(replay.GetClaimChapterRewardResponse(), claimed) ||
		len(runtime.actors[playerID].state.PendingOutbox) != 0 {
		t.Fatalf("unexpected claim replay: %+v", replay)
	}

	if err := runtime.flushDirty(context.Background()); err != nil {
		t.Fatal(err)
	}
	checkpoint := store.saved[0]
	if store.expectedRevision[0] != 7 ||
		checkpoint.PlayerSeq != 7 || checkpoint.CheckpointRevision != 8 ||
		checkpoint.CoinBalance != 29 ||
		checkpoint.CurrentChapter.ChapterId != developmentNextChapterID ||
		checkpoint.CurrentChapter.Status != datav1.ChapterRecordStatus_IN_PROGRESS ||
		len(checkpoint.PendingOutbox) != 0 {
		t.Fatalf("unexpected claim checkpoint: %+v", checkpoint)
	}
}

func TestClaimTerminalChapterRewardsAndKeepsClaimedHistory(t *testing.T) {
	const playerID = uint64(43)
	now := time.Date(2026, 8, 16, 3, 0, 0, 0, time.UTC)
	state := NewDevelopmentState(playerID)
	chapter, exists := NewDevelopmentConfigSnapshot().Chapter(developmentNextChapterID)
	if !exists {
		t.Fatal("chapter two config missing")
	}
	state.ChapterID = chapter.ChapterID
	state.ChapterConfigVersion = chapter.ConfigVersion
	state.Chapter = chapterv1.ChapterStatus_CLAIMABLE
	state.Tasks = append([]Task(nil), chapter.Tasks...)
	for index := range state.Tasks {
		state.Tasks[index].Current = state.Tasks[index].Target
	}
	state.Coins = 7
	state.Inventory = map[uint32]uint32{}
	store := &recordingCheckpointStore{state: state}
	runtime := NewRuntime()
	runtime.store = store
	runtime.SetNow(func() time.Time { return now })
	defer runtime.Close()

	request := claimRewardRequest(
		playerID, "00112233-4455-6677-8899-aabbccddee65", developmentNextChapterID,
	)
	response, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, request)
	if err != nil {
		t.Fatal(err)
	}
	claimed := response.GetClaimChapterRewardResponse()
	current := claimed.GetPatch().GetCurrentChapter()
	if response.GetError() != nil || claimed.GetCoinGranted() != 10 ||
		claimed.GetChapterId() != developmentNextChapterID ||
		claimed.GetPatch().GetCoinBalance() != 17 ||
		current.GetChapterId() != developmentNextChapterID ||
		current.GetStatus() != chapterv1.ChapterStatus_CLAIMED ||
		len(current.GetTasks()) != 3 {
		t.Fatalf("unexpected terminal claim response: %+v", response)
	}
	if len(claimed.GetItemsAddedToInventory()) != 2 ||
		claimed.GetItemsAddedToInventory()[0].GetItemId() != BasicFertilizerID ||
		claimed.GetItemsAddedToInventory()[0].GetQuantity() != 5 ||
		claimed.GetItemsAddedToInventory()[1].GetItemId() != developmentWatermelonSeedItemID ||
		claimed.GetItemsAddedToInventory()[1].GetQuantity() != 10 {
		t.Fatalf("unexpected terminal rewards: %+v", claimed.GetItemsAddedToInventory())
	}

	replay, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, request)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.GetReplayed() || !proto.Equal(replay.GetClaimChapterRewardResponse(), claimed) {
		t.Fatalf("unexpected terminal replay: %+v", replay)
	}
}

func TestClaimRewardFullInventoryCreatesOneDeterministicOutbox(t *testing.T) {
	const playerID = uint64(42)
	now := time.Date(2026, 7, 31, 11, 0, 0, 0, time.UTC)
	state := claimableRewardState(playerID, now)
	state.Inventory = make(map[uint32]uint32, inventoryTypeLimit)
	for itemID := uint32(2_000); itemID < 2_000+inventoryTypeLimit; itemID++ {
		state.Inventory[itemID] = 1
	}
	store := &recordingCheckpointStore{state: state}
	runtime := NewRuntime()
	runtime.store = store
	runtime.SetNow(func() time.Time { return now })
	defer runtime.Close()

	request := claimRewardRequest(
		playerID, "00112233-4455-6677-8899-aabbccddee62", InitialChapterID,
	)
	first, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, request)
	if err != nil {
		t.Fatal(err)
	}
	claimed := first.GetClaimChapterRewardResponse()
	if len(claimed.GetItemsAddedToInventory()) != 0 ||
		len(claimed.GetItemsPendingMail()) != 2 ||
		claimed.GetItemsPendingMail()[0].GetItemId() != BasicFertilizerID ||
		claimed.GetItemsPendingMail()[1].GetItemId() != developmentPumpkinSeedItemID {
		t.Fatalf("unexpected pending mail receipt: %+v", claimed)
	}
	actorState := runtime.actors[playerID].state
	if actorState.Coins != 29 || actorState.PlayerSeq != 7 ||
		len(actorState.PendingOutbox) != 1 ||
		len(actorState.RecentResults[len(actorState.RecentResults)-1].OutboxIds) != 1 {
		t.Fatalf("unexpected reward Outbox state: %+v", actorState)
	}
	pending := actorState.PendingOutbox[0]
	payload := &eventv1.CreateRewardMailV1{}
	if len(pending.EventId) != 16 ||
		proto.Unmarshal(pending.Payload, payload) != nil ||
		len(payload.Attachments) != 2 ||
		payload.Attachments[0].GetItemId() != BasicFertilizerID ||
		payload.Attachments[1].GetItemId() != developmentPumpkinSeedItemID ||
		!proto.Equal(payload.Source, &eventv1.RewardMailSourceV1{
			ChapterId: InitialChapterID, ChapterConfigVersion: ServerConfigVersion,
			RequestId: pending.CausedByRequestId,
		}) {
		t.Fatalf("unexpected reward-mail payload: pending=%+v payload=%+v", pending, payload)
	}

	replay, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, request)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.GetReplayed() || len(actorState.PendingOutbox) != 1 ||
		!proto.Equal(replay.GetClaimChapterRewardResponse(), claimed) {
		t.Fatalf("claim replay duplicated Outbox: %+v", replay)
	}
	if err := runtime.flushDirty(context.Background()); err != nil {
		t.Fatal(err)
	}
	checkpoint := store.saved[0]
	if len(checkpoint.PendingOutbox) != 1 {
		t.Fatalf("checkpoint Outbox count = %d", len(checkpoint.PendingOutbox))
	}
	restored, err := StateFromCheckpoint(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.PendingOutbox) != 1 ||
		!proto.Equal(restored.PendingOutbox[0], pending) {
		t.Fatalf("restored pending Outbox = %+v", restored.PendingOutbox)
	}
}

func TestClaimRewardRejectsWrongOrIncompleteChapter(t *testing.T) {
	tests := []struct {
		name      string
		chapterID uint32
		prepare   func(*State)
		wantError wsv1.ErrorCode
	}{
		{
			name: "wrong chapter", chapterID: 9,
			prepare:   func(*State) {},
			wantError: wsv1.ErrorCode_CHAPTER_NOT_FOUND,
		},
		{
			name: "not claimable", chapterID: InitialChapterID,
			prepare: func(state *State) {
				state.Chapter = chapterv1.ChapterStatus_IN_PROGRESS
			},
			wantError: wsv1.ErrorCode_CHAPTER_NOT_CLAIMABLE,
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 7, 31, 11, 0, 0, 0, time.UTC)
			state := claimableRewardState(42, now)
			test.prepare(state)
			store := &recordingCheckpointStore{state: state}
			runtime := NewRuntime()
			runtime.store = store
			runtime.SetNow(func() time.Time { return now })
			defer runtime.Close()

			requestIDs := []string{
				"00112233-4455-6677-8899-aabbccddee71",
				"00112233-4455-6677-8899-aabbccddee72",
			}
			response, err := runtime.Handle(
				context.Background(), 42, LocalOwnerEpoch,
				claimRewardRequest(42, requestIDs[index], test.chapterID),
			)
			if err != nil {
				t.Fatal(err)
			}
			actorState := runtime.actors[42].state
			if response.GetError().GetCode() != test.wantError ||
				actorState.PlayerSeq != 6 || actorState.Coins != 19 ||
				len(actorState.PendingOutbox) != 0 {
				t.Fatalf("claim failure was not atomic: response=%+v state=%+v",
					response, actorState)
			}
		})
	}
}
