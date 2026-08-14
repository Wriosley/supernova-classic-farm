package player

import (
	"context"
	"testing"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
)

func petRequest(playerID uint64, action wsv1.Action, requestID string, payload any) *wsv1.WsEnvelope {
	envelope := &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion,
		MessageKind:     wsv1.MessageKind_REQUEST,
		Action:          action,
		RequestId:       requestID,
		TargetPlayerId:  playerID,
	}
	switch v := payload.(type) {
	case *wsv1.GetPetPanelRequest:
		envelope.Payload = &wsv1.WsEnvelope_GetPetPanelRequest{GetPetPanelRequest: v}
	case *wsv1.BuyPetRequest:
		envelope.Payload = &wsv1.WsEnvelope_BuyPetRequest{BuyPetRequest: v}
	case *wsv1.DeployPetRequest:
		envelope.Payload = &wsv1.WsEnvelope_DeployPetRequest{DeployPetRequest: v}
	case *wsv1.BuyPetFoodRequest:
		envelope.Payload = &wsv1.WsEnvelope_BuyPetFoodRequest{BuyPetFoodRequest: v}
	case *wsv1.FeedPetRequest:
		envelope.Payload = &wsv1.WsEnvelope_FeedPetRequest{FeedPetRequest: v}
	}
	return envelope
}

func TestDevelopmentPetConfig(t *testing.T) {
	cfg := NewDevelopmentConfigSnapshot()
	village, ok := cfg.Pet(developmentVillageDogPetID)
	if !ok || village.PriceCoins != 5 || village.GuardProbabilityBPS != 1000 ||
		village.GuardPenaltyCoins != 2 {
		t.Fatalf("village dog config = %+v", village)
	}
	shepherd, ok := cfg.Pet(developmentShepherdDogPetID)
	if !ok || shepherd.PriceCoins != 10 || shepherd.GuardPenaltyCoins != 4 {
		t.Fatalf("shepherd dog config = %+v", shepherd)
	}
	food, entry, ok := cfg.PrimaryPetFood()
	if !ok || food.ItemID != developmentPetFoodItemID || food.DurationSeconds != 86400 ||
		entry.UnitPrice != 5 || entry.PriceVersion != developmentPetFoodPriceVersion {
		t.Fatalf("pet food config food=%+v entry=%+v", food, entry)
	}
}

func TestPetStateCheckpointRoundTrip(t *testing.T) {
	now := time.Date(2026, 8, 11, 8, 0, 0, 0, time.UTC)
	state := NewDevelopmentState(42)
	state.PetState = &datav1.PetStateRecord{
		OwnedPetIds:       []uint32{2, 1},
		ActivePetId:       1,
		FoodActiveUntilMs: now.Add(48 * time.Hour).UnixMilli(),
	}
	checkpoint, err := state.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.PetState == nil ||
		len(checkpoint.PetState.OwnedPetIds) != 2 ||
		checkpoint.PetState.OwnedPetIds[0] != 1 ||
		checkpoint.PetState.OwnedPetIds[1] != 2 {
		t.Fatalf("checkpoint pet_state = %+v", checkpoint.PetState)
	}
	body, digest, err := MarshalCheckpoint(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalCheckpoint(body, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	restored, err := StateFromCheckpoint(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if restored.PetState.ActivePetId != 1 ||
		restored.PetState.FoodActiveUntilMs != state.PetState.FoodActiveUntilMs ||
		len(restored.PetState.OwnedPetIds) != 2 {
		t.Fatalf("restored pet_state = %+v", restored.PetState)
	}
}

func TestInitialCheckpointHasEmptyPetState(t *testing.T) {
	checkpoint := NewInitialCheckpoint(7, time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC))
	if checkpoint.PetState != nil {
		t.Fatalf("new player should have nil pet_state, got %+v", checkpoint.PetState)
	}
	state, err := StateFromCheckpoint(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if state.PetState != nil {
		t.Fatalf("loaded state pet_state = %+v", state.PetState)
	}
}

func TestBuyVillageDogAndIdempotentReplay(t *testing.T) {
	const playerID = uint64(42)
	runtime := NewRuntime()
	defer runtime.Close()

	reqID := "00112233-4455-6677-8899-aabbccddee01"
	response, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, petRequest(
		playerID, wsv1.Action_BUY_PET, reqID,
		&wsv1.BuyPetRequest{PetId: developmentVillageDogPetID, ExpectedConfigVersion: ServerConfigVersion},
	))
	if err != nil || response.GetError() != nil {
		t.Fatalf("BUY_PET failed: %+v err=%v", response, err)
	}
	if response.GetBuyPetResponse().GetPriceCoins() != 5 {
		t.Fatalf("price = %d", response.GetBuyPetResponse().GetPriceCoins())
	}
	runtime.mu.Lock()
	actor := runtime.actors[playerID]
	runtime.mu.Unlock()
	if actor.state.Coins != InitialCoinBalance-5 ||
		!ownsPet(actor.state.PetState, developmentVillageDogPetID) {
		t.Fatalf("state after buy: coins=%d pet=%+v", actor.state.Coins, actor.state.PetState)
	}

	replay, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, petRequest(
		playerID, wsv1.Action_BUY_PET, reqID,
		&wsv1.BuyPetRequest{PetId: developmentVillageDogPetID, ExpectedConfigVersion: ServerConfigVersion},
	))
	if err != nil || replay.GetError() != nil || !replay.Replayed {
		t.Fatalf("replay BUY_PET: %+v err=%v", replay, err)
	}
	if actor.state.Coins != InitialCoinBalance-5 {
		t.Fatalf("idempotent replay deducted again: coins=%d", actor.state.Coins)
	}
}

func TestBuyPetInsufficientCoinsAndAlreadyOwned(t *testing.T) {
	const playerID = uint64(43)
	runtime := NewRuntime()
	defer runtime.Close()
	runtime.mu.Lock()
	// force empty wallet via Handle after activation
	runtime.mu.Unlock()

	_, _ = runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, petRequest(
		playerID, wsv1.Action_GET_PET_PANEL, "00112233-4455-6677-8899-aabbccddee10",
		&wsv1.GetPetPanelRequest{},
	))
	runtime.mu.Lock()
	runtime.actors[playerID].state.Coins = 4
	runtime.mu.Unlock()

	fail, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, petRequest(
		playerID, wsv1.Action_BUY_PET, "00112233-4455-6677-8899-aabbccddee11",
		&wsv1.BuyPetRequest{PetId: developmentVillageDogPetID, ExpectedConfigVersion: ServerConfigVersion},
	))
	if err != nil || fail.GetError().GetCode() != wsv1.ErrorCode_INSUFFICIENT_COINS {
		t.Fatalf("want INSUFFICIENT_COINS, got %+v err=%v", fail, err)
	}

	runtime.mu.Lock()
	runtime.actors[playerID].state.Coins = 20
	runtime.mu.Unlock()
	ok, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, petRequest(
		playerID, wsv1.Action_BUY_PET, "00112233-4455-6677-8899-aabbccddee12",
		&wsv1.BuyPetRequest{PetId: developmentVillageDogPetID, ExpectedConfigVersion: ServerConfigVersion},
	))
	if err != nil || ok.GetError() != nil {
		t.Fatalf("buy: %+v err=%v", ok, err)
	}
	dup, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, petRequest(
		playerID, wsv1.Action_BUY_PET, "00112233-4455-6677-8899-aabbccddee13",
		&wsv1.BuyPetRequest{PetId: developmentVillageDogPetID, ExpectedConfigVersion: ServerConfigVersion},
	))
	if err != nil || dup.GetError().GetCode() != wsv1.ErrorCode_PET_ALREADY_OWNED {
		t.Fatalf("want PET_ALREADY_OWNED, got %+v err=%v", dup, err)
	}
}

func TestBuyShepherdRejectsStaleConfigVersion(t *testing.T) {
	const playerID = uint64(44)
	runtime := NewRuntime()
	defer runtime.Close()
	response, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, petRequest(
		playerID, wsv1.Action_BUY_PET, "00112233-4455-6677-8899-aabbccddee20",
		&wsv1.BuyPetRequest{PetId: developmentShepherdDogPetID, ExpectedConfigVersion: 999},
	))
	if err != nil || response.GetError().GetCode() != wsv1.ErrorCode_PRICE_CHANGED {
		t.Fatalf("want PRICE_CHANGED, got %+v err=%v", response, err)
	}
}

func TestDeployPetSwitchKeepsFoodTimer(t *testing.T) {
	const playerID = uint64(45)
	fixed := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	runtime := NewRuntime()
	runtime.SetNow(func() time.Time { return fixed })
	defer runtime.Close()

	// 激活 Actor 并补充金币，以便买下两只宠物。
	if _, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, petRequest(
		playerID, wsv1.Action_GET_PET_PANEL, "00112233-4455-6677-8899-aabbccddee29",
		&wsv1.GetPetPanelRequest{},
	)); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	runtime.actors[playerID].state.Coins = 50
	runtime.mu.Unlock()

	for _, step := range []struct {
		id  string
		pet uint32
	}{
		{"00112233-4455-6677-8899-aabbccddee30", developmentVillageDogPetID},
		{"00112233-4455-6677-8899-aabbccddee31", developmentShepherdDogPetID},
	} {
		response, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, petRequest(
			playerID, wsv1.Action_BUY_PET, step.id,
			&wsv1.BuyPetRequest{PetId: step.pet, ExpectedConfigVersion: ServerConfigVersion},
		))
		if err != nil || response.GetError() != nil {
			t.Fatalf("buy pet %d: %+v err=%v", step.pet, response, err)
		}
	}
	if _, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, petRequest(
		playerID, wsv1.Action_BUY_PET_FOOD, "00112233-4455-6677-8899-aabbccddee32",
		&wsv1.BuyPetFoodRequest{
			ShopEntryId: developmentPetFoodShopEntryID, Quantity: 1,
			ExpectedPriceVersion: developmentPetFoodPriceVersion,
		},
	)); err != nil {
		t.Fatal(err)
	}
	feed, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, petRequest(
		playerID, wsv1.Action_FEED_PET, "00112233-4455-6677-8899-aabbccddee33",
		&wsv1.FeedPetRequest{},
	))
	if err != nil || feed.GetError() != nil {
		t.Fatalf("feed: %+v err=%v", feed, err)
	}
	until := feed.GetFeedPetResponse().GetFoodActiveUntilMs()

	if _, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, petRequest(
		playerID, wsv1.Action_DEPLOY_PET, "00112233-4455-6677-8899-aabbccddee34",
		&wsv1.DeployPetRequest{PetId: developmentVillageDogPetID},
	)); err != nil {
		t.Fatal(err)
	}
	switchResp, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, petRequest(
		playerID, wsv1.Action_DEPLOY_PET, "00112233-4455-6677-8899-aabbccddee35",
		&wsv1.DeployPetRequest{PetId: developmentShepherdDogPetID},
	))
	if err != nil || switchResp.GetError() != nil {
		t.Fatalf("deploy: %+v err=%v", switchResp, err)
	}
	runtime.mu.Lock()
	actor := runtime.actors[playerID]
	runtime.mu.Unlock()
	if actor.state.PetState.ActivePetId != developmentShepherdDogPetID {
		t.Fatalf("active=%d", actor.state.PetState.ActivePetId)
	}
	if actor.state.PetState.FoodActiveUntilMs != until {
		t.Fatalf("food timer changed on deploy switch: %d -> %d", until, actor.state.PetState.FoodActiveUntilMs)
	}
}

func TestDeployUnownedPetFails(t *testing.T) {
	const playerID = uint64(46)
	runtime := NewRuntime()
	defer runtime.Close()
	response, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, petRequest(
		playerID, wsv1.Action_DEPLOY_PET, "00112233-4455-6677-8899-aabbccddee40",
		&wsv1.DeployPetRequest{PetId: developmentVillageDogPetID},
	))
	if err != nil || response.GetError().GetCode() != wsv1.ErrorCode_PET_NOT_OWNED {
		t.Fatalf("want PET_NOT_OWNED, got %+v err=%v", response, err)
	}
}

func TestBuyPetFoodAndFeedExtendsActiveUntil(t *testing.T) {
	const playerID = uint64(47)
	fixed := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	runtime := NewRuntime()
	runtime.SetNow(func() time.Time { return fixed })
	defer runtime.Close()

	buyFood, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, petRequest(
		playerID, wsv1.Action_BUY_PET_FOOD, "00112233-4455-6677-8899-aabbccddee50",
		&wsv1.BuyPetFoodRequest{
			ShopEntryId: developmentPetFoodShopEntryID, Quantity: 2,
			ExpectedPriceVersion: developmentPetFoodPriceVersion,
		},
	))
	if err != nil || buyFood.GetError() != nil {
		t.Fatalf("buy food: %+v err=%v", buyFood, err)
	}
	if buyFood.GetBuyPetFoodResponse().GetTotalPrice() != 10 {
		t.Fatalf("total price = %d", buyFood.GetBuyPetFoodResponse().GetTotalPrice())
	}

	first, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, petRequest(
		playerID, wsv1.Action_FEED_PET, "00112233-4455-6677-8899-aabbccddee51",
		&wsv1.FeedPetRequest{},
	))
	if err != nil || first.GetError() != nil {
		t.Fatalf("first feed: %+v err=%v", first, err)
	}
	wantFirst := fixed.UnixMilli() + 86400*1000
	if first.GetFeedPetResponse().GetFoodActiveUntilMs() != wantFirst {
		t.Fatalf("first until = %d want %d", first.GetFeedPetResponse().GetFoodActiveUntilMs(), wantFirst)
	}

	second, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, petRequest(
		playerID, wsv1.Action_FEED_PET, "00112233-4455-6677-8899-aabbccddee52",
		&wsv1.FeedPetRequest{},
	))
	if err != nil || second.GetError() != nil {
		t.Fatalf("second feed: %+v err=%v", second, err)
	}
	wantSecond := wantFirst + 86400*1000
	if second.GetFeedPetResponse().GetFoodActiveUntilMs() != wantSecond {
		t.Fatalf("second until = %d want %d", second.GetFeedPetResponse().GetFoodActiveUntilMs(), wantSecond)
	}

	// 幂等重放不二次消耗
	replay, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, petRequest(
		playerID, wsv1.Action_FEED_PET, "00112233-4455-6677-8899-aabbccddee52",
		&wsv1.FeedPetRequest{},
	))
	if err != nil || !replay.Replayed {
		t.Fatalf("feed replay: %+v err=%v", replay, err)
	}
	runtime.mu.Lock()
	qty := runtime.actors[playerID].state.Inventory[developmentPetFoodItemID]
	runtime.mu.Unlock()
	if qty != 0 {
		t.Fatalf("food remaining = %d, want 0", qty)
	}
}

func TestFeedWithoutFoodFails(t *testing.T) {
	const playerID = uint64(48)
	runtime := NewRuntime()
	defer runtime.Close()
	response, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, petRequest(
		playerID, wsv1.Action_FEED_PET, "00112233-4455-6677-8899-aabbccddee60",
		&wsv1.FeedPetRequest{},
	))
	if err != nil || response.GetError().GetCode() != wsv1.ErrorCode_ITEM_NOT_OWNED {
		t.Fatalf("want ITEM_NOT_OWNED, got %+v err=%v", response, err)
	}
}
