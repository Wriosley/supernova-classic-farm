package player

import (
	"context"
	"testing"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
)

func TestCareerAndCompendiumCheckpointRoundTrip(t *testing.T) {
	state := NewDevelopmentState(42)
	state.Career = &datav1.PlayerCareerRecord{
		TotalHarvestedCropQuantity: 12, TotalStolenCropQuantity: 3,
	}
	state.CropCompendium = &datav1.CropCompendiumRecord{UnlockedCropIds: []uint32{2003, 2001}}
	checkpoint, err := state.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Career.GetTotalHarvestedCropQuantity() != 12 ||
		len(checkpoint.CropCompendium.GetUnlockedCropIds()) != 2 ||
		checkpoint.CropCompendium.UnlockedCropIds[0] != 2001 {
		t.Fatalf("checkpoint career/compendium = %+v / %+v", checkpoint.Career, checkpoint.CropCompendium)
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
	if restored.Career.TotalHarvestedCropQuantity != 12 ||
		restored.Career.TotalStolenCropQuantity != 3 ||
		len(restored.CropCompendium.UnlockedCropIds) != 2 {
		t.Fatalf("restored = career=%+v compendium=%+v", restored.Career, restored.CropCompendium)
	}
}

func TestHarvestCareerAndCompendium(t *testing.T) {
	const playerID = uint64(50)
	fixed := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	runtime := NewRuntime()
	runtime.SetNow(func() time.Time { return fixed })
	defer runtime.Close()

	if _, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		buySeedsRequest(playerID, "00112233-4455-6677-8899-aabbccddee70", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		plantRequest(playerID, "00112233-4455-6677-8899-aabbccddee71", 1, developmentSeedItemID)); err != nil {
		t.Fatal(err)
	}
	runtime.SetNow(func() time.Time { return fixed.Add(200 * time.Second) })
	harvest, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_HARVEST, RequestId: "00112233-4455-6677-8899-aabbccddee72",
		TargetPlayerId: playerID,
		Payload:        &wsv1.WsEnvelope_HarvestRequest{HarvestRequest: &wsv1.HarvestRequest{PlotId: 1}},
	})
	if err != nil || harvest.GetError() != nil {
		t.Fatalf("harvest: %+v err=%v", harvest, err)
	}
	qty := harvest.GetHarvestResponse().GetHarvestedQuantity()
	if qty != developmentBaseYield {
		t.Fatalf("harvested = %d", qty)
	}
	runtime.mu.Lock()
	actor := runtime.actors[playerID]
	runtime.mu.Unlock()
	if actor.state.Career.GetTotalHarvestedCropQuantity() != uint64(qty) {
		t.Fatalf("career harvested = %d", actor.state.Career.GetTotalHarvestedCropQuantity())
	}
	if len(actor.state.CropCompendium.GetUnlockedCropIds()) != 1 ||
		actor.state.CropCompendium.UnlockedCropIds[0] != developmentCropID {
		t.Fatalf("compendium = %+v", actor.state.CropCompendium)
	}

	// 重放不重复累计
	replay, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_HARVEST, RequestId: "00112233-4455-6677-8899-aabbccddee72",
		TargetPlayerId: playerID,
		Payload:        &wsv1.WsEnvelope_HarvestRequest{HarvestRequest: &wsv1.HarvestRequest{PlotId: 1}},
	})
	if err != nil || !replay.Replayed {
		t.Fatalf("replay: %+v err=%v", replay, err)
	}
	if actor.state.Career.GetTotalHarvestedCropQuantity() != uint64(qty) {
		t.Fatalf("career changed on replay")
	}
}

func TestHarvestAfterStealCountsNetQuantity(t *testing.T) {
	const playerID = uint64(51)
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	state := ownerStateWithMaturePlot(playerID, 1, now)
	state.Plots[1].StolenQuantity = 2
	state.Plots[1].BaseYield = 6
	store := &recordingCheckpointStore{state: state}
	runtime := NewRuntime()
	runtime.store = store
	runtime.SetNow(func() time.Time { return now })
	defer runtime.Close()

	response, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_HARVEST, RequestId: "00112233-4455-6677-8899-aabbccddee80",
		TargetPlayerId: playerID,
		Payload:        &wsv1.WsEnvelope_HarvestRequest{HarvestRequest: &wsv1.HarvestRequest{PlotId: 1}},
	})
	if err != nil || response.GetError() != nil {
		t.Fatalf("harvest: %+v err=%v", response, err)
	}
	if response.GetHarvestResponse().GetHarvestedQuantity() != 4 {
		t.Fatalf("harvested = %d", response.GetHarvestResponse().GetHarvestedQuantity())
	}
	runtime.mu.Lock()
	career := runtime.actors[playerID].state.Career
	runtime.mu.Unlock()
	if career.GetTotalHarvestedCropQuantity() != 4 {
		t.Fatalf("career = %d", career.GetTotalHarvestedCropQuantity())
	}
}

func TestStealCareerDoesNotUnlockCompendium(t *testing.T) {
	const ownerID = uint64(11)
	const visitorID = uint64(60)
	const plotID = uint32(1)
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)

	ownerRuntime := NewRuntime()
	ownerRuntime.store = &recordingCheckpointStore{state: ownerStateWithMaturePlot(ownerID, plotID, now)}
	ownerRuntime.SetNow(func() time.Time { return now })
	defer ownerRuntime.Close()

	visitorState := visitorStateWithStealTask(visitorID, now)
	visitorRuntime := NewRuntime()
	visitorRuntime.store = &recordingCheckpointStore{state: visitorState}
	visitorRuntime.SetNow(func() time.Time { return now })
	defer visitorRuntime.Close()

	interactionID := interactionIDFixture(0xC1)
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
	if response.GetVisitorPatch().GetCareer().GetTotalStolenCropQuantity() != 1 {
		t.Fatalf("stolen career = %+v", response.GetVisitorPatch().GetCareer())
	}
	visitorRuntime.mu.Lock()
	state := visitorRuntime.actors[visitorID].state
	visitorRuntime.mu.Unlock()
	if state.Career.GetTotalStolenCropQuantity() != 1 ||
		state.Career.GetTotalHarvestedCropQuantity() != 0 {
		t.Fatalf("career = %+v", state.Career)
	}
	if state.CropCompendium != nil && len(state.CropCompendium.UnlockedCropIds) != 0 {
		t.Fatalf("compendium unlocked by steal: %+v", state.CropCompendium)
	}
}

func TestPublicFarmSnapshotIncludesCareerAndCompendium(t *testing.T) {
	const ownerID = uint64(70)
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	state := developmentStateAt(ownerID, now)
	state.Career = &datav1.PlayerCareerRecord{TotalHarvestedCropQuantity: 9, TotalStolenCropQuantity: 2}
	state.CropCompendium = &datav1.CropCompendiumRecord{UnlockedCropIds: []uint32{2001}}
	runtime := NewRuntime()
	runtime.store = &recordingCheckpointStore{state: state}
	runtime.SetNow(func() time.Time { return now })
	defer runtime.Close()

	snapshot, err := runtime.BuildPublicFarmSnapshot(context.Background(), ownerID, LocalOwnerEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.GetCareer().GetTotalHarvestedCropQuantity() != 9 ||
		snapshot.GetCareer().GetTotalStolenCropQuantity() != 2 {
		t.Fatalf("public career = %+v", snapshot.GetCareer())
	}
	// Visitors may read the unlocked crop list, and nothing else from the
	// owner's private compendium record.
	unlocked := snapshot.GetCropCompendium().GetUnlockedCropIds()
	if len(unlocked) != 1 || unlocked[0] != 2001 {
		t.Fatalf("public compendium = %+v", snapshot.GetCropCompendium())
	}
	private := state.Snapshot()
	if private.GetCropCompendium().GetUnlockedCropIds()[0] != 2001 {
		t.Fatalf("private snapshot missing compendium")
	}
}

func TestMultiCropBuyPlantHarvestSell(t *testing.T) {
	const playerID = uint64(80)
	fixed := time.Date(2026, 8, 11, 8, 0, 0, 0, time.UTC)
	runtime := NewRuntime()
	runtime.SetNow(func() time.Time { return fixed })
	defer runtime.Close()

	shop, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_GET_SHOP, RequestId: "00112233-4455-6677-8899-aabbccddee91",
		TargetPlayerId: playerID,
		Payload:        &wsv1.WsEnvelope_GetShopRequest{GetShopRequest: &wsv1.GetShopRequest{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(shop.GetGetShopResponse().GetCrops()) != 11 {
		t.Fatalf("shop crops = %d", len(shop.GetGetShopResponse().GetCrops()))
	}

	// 激活 Actor 并充值
	_, _ = runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		buySeedsRequest(playerID, "00112233-4455-6677-8899-aabbccddeea0", 1))
	runtime.mu.Lock()
	runtime.actors[playerID].state.Coins = 1000
	runtime.mu.Unlock()

	for i, def := range developmentExtraCrops()[:3] {
		buyResp, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
			buySeedsQuoteRequest(
				playerID, requestIDFor(0xA0+byte(i)), def.SeedShopEntryID, 1, def.SeedPriceVersion,
			))
		if err != nil || buyResp.GetError() != nil {
			t.Fatalf("buy %s: %+v err=%v", def.Name, buyResp, err)
		}
		plantResp, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
			plantRequest(playerID, requestIDFor(0xB0+byte(i)), uint32(i+1), def.SeedItemID))
		if err != nil || plantResp.GetError() != nil {
			t.Fatalf("plant %s: %+v err=%v", def.Name, plantResp, err)
		}
		runtime.SetNow(func() time.Time {
			return fixed.Add(time.Duration(def.MaturityScaled9/1_000_000_000+5) * time.Second)
		})
		harvestResp, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, &wsv1.WsEnvelope{
			ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_REQUEST,
			Action: wsv1.Action_HARVEST, RequestId: requestIDFor(0xC0 + byte(i)),
			TargetPlayerId: playerID,
			Payload: &wsv1.WsEnvelope_HarvestRequest{HarvestRequest: &wsv1.HarvestRequest{
				PlotId: uint32(i + 1),
			}},
		})
		if err != nil || harvestResp.GetError() != nil {
			t.Fatalf("harvest %s: %+v err=%v", def.Name, harvestResp, err)
		}
		if harvestResp.GetHarvestResponse().GetCropItemId() != def.CropItemID {
			t.Fatalf("%s crop item mismatch", def.Name)
		}
		sellResp, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, &wsv1.WsEnvelope{
			ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_REQUEST,
			Action: wsv1.Action_SELL_CROP, RequestId: requestIDFor(0xD0 + byte(i)),
			TargetPlayerId: playerID,
			Payload: &wsv1.WsEnvelope_SellCropRequest{SellCropRequest: &wsv1.SellCropRequest{
				CropItemId: def.CropItemID, ExpectedPriceVersion: developmentCropSellPriceVersion,
				Amount: &wsv1.SellCropRequest_Quantity{Quantity: def.BaseYield},
			}},
		})
		if err != nil || sellResp.GetError() != nil {
			t.Fatalf("sell %s: %+v err=%v", def.Name, sellResp, err)
		}
		runtime.SetNow(func() time.Time { return fixed })
	}
	runtime.mu.Lock()
	compendium := runtime.actors[playerID].state.CropCompendium
	runtime.mu.Unlock()
	if len(compendium.GetUnlockedCropIds()) != 3 {
		t.Fatalf("unlocked = %+v", compendium)
	}
}

func requestIDFor(fill byte) string {
	b := make([]byte, 16)
	for i := range b {
		b[i] = fill
	}
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 36)
	pos := 0
	for i, v := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out[pos] = '-'
			pos++
		}
		out[pos] = hexdigits[v>>4]
		out[pos+1] = hexdigits[v&0x0f]
		pos += 2
	}
	return string(out)
}
