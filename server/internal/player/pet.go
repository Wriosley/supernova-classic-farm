package player

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"sort"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"google.golang.org/protobuf/proto"
)

func ensurePetState(state *State) *datav1.PetStateRecord {
	if state.PetState == nil {
		state.PetState = &datav1.PetStateRecord{}
	}
	return state.PetState
}

func ownsPet(pet *datav1.PetStateRecord, petID uint32) bool {
	if pet == nil || petID == 0 {
		return false
	}
	for _, owned := range pet.OwnedPetIds {
		if owned == petID {
			return true
		}
	}
	return false
}

func addOwnedPet(pet *datav1.PetStateRecord, petID uint32) {
	if ownsPet(pet, petID) {
		return
	}
	pet.OwnedPetIds = append(pet.OwnedPetIds, petID)
	sort.Slice(pet.OwnedPetIds, func(i, j int) bool {
		return pet.OwnedPetIds[i] < pet.OwnedPetIds[j]
	})
}

func petFoodActive(pet *datav1.PetStateRecord, nowMS int64) bool {
	return pet != nil && pet.FoodActiveUntilMs > nowMS
}

func guardBuffActive(pet *datav1.PetStateRecord, nowMS int64) bool {
	return pet != nil && pet.ActivePetId != 0 && petFoodActive(pet, nowMS)
}

func buildPetPanel(state *State, config *ConfigSnapshot, nowMS int64) *wsv1.PetPanelView {
	pet := state.PetState
	ownedSet := map[uint32]struct{}{}
	ownedIDs := []uint32(nil)
	activePetID := uint32(0)
	foodUntil := int64(0)
	if pet != nil {
		ownedIDs = append([]uint32(nil), pet.OwnedPetIds...)
		activePetID = pet.ActivePetId
		foodUntil = pet.FoodActiveUntilMs
		for _, id := range ownedIDs {
			ownedSet[id] = struct{}{}
		}
	}
	views := make([]*wsv1.PetShopEntryView, 0)
	for _, cfg := range config.ActivePets() {
		_, owned := ownedSet[cfg.PetID]
		views = append(views, &wsv1.PetShopEntryView{
			PetId: cfg.PetID, Name: cfg.Name, PriceCoins: cfg.PriceCoins,
			GuardProbabilityBps: cfg.GuardProbabilityBPS,
			GuardPenaltyCoins:   cfg.GuardPenaltyCoins,
			ConfigVersion:       cfg.ConfigVersion, Enabled: cfg.Enabled, Owned: owned,
		})
	}
	panel := &wsv1.PetPanelView{
		Pets: views, OwnedPetIds: ownedIDs, ActivePetId: activePetID,
		FoodActiveUntilMs: foodUntil, GuardBuffActive: guardBuffActive(pet, nowMS),
	}
	if food, entry, ok := config.PrimaryPetFood(); ok {
		panel.PetFood = &wsv1.PetFoodShopView{
			ShopEntryId: entry.ShopEntryID, ItemId: food.ItemID,
			UnitPrice: entry.UnitPrice, PriceVersion: entry.PriceVersion,
			DurationSeconds: food.DurationSeconds, Enabled: entry.Enabled && food.Enabled,
		}
		panel.PetFoodQuantity = state.Inventory[food.ItemID]
	}
	return panel
}

func (r *Runtime) getPetPanel(
	a *runtimeActor, request *wsv1.WsEnvelope, config *ConfigSnapshot, now time.Time,
) *wsv1.WsEnvelope {
	nowMS := now.UnixMilli()
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId, TargetPlayerId: request.TargetPlayerId,
		StateVersion: &wsv1.StateVersion{OwnerEpoch: a.state.OwnerEpoch, PlayerSeq: a.state.PlayerSeq},
		ServerTimeMs: nowMS,
		Payload: &wsv1.WsEnvelope_GetPetPanelResponse{
			GetPetPanelResponse: &wsv1.GetPetPanelResponse{
				Panel: buildPetPanel(a.state, config, nowMS),
			},
		},
	}
}

func (r *Runtime) buyPet(
	a *runtimeActor,
	callerPlayerID uint64,
	request *wsv1.WsEnvelope,
	config *ConfigSnapshot,
	now time.Time,
) (*wsv1.WsEnvelope, bool) {
	requestID, err := parseRequestID(request.RequestId)
	if err != nil {
		return errorEnvelope(request, a.state, now, &wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}), false
	}
	buy := request.GetBuyPetRequest()
	fingerprint := buyPetFingerprint(callerPlayerID, request, buy)
	for _, stored := range a.state.RecentResults {
		if stored.CallerPlayerId != callerPlayerID || !bytes.Equal(stored.RequestId, requestID) {
			continue
		}
		if stored.FingerprintSchemaVersion != idempotencyFingerprintSchemaVersion ||
			stored.ProtocolVersion != request.ProtocolVersion ||
			stored.Action != uint32(request.Action) ||
			stored.TargetPlayerId != request.TargetPlayerId ||
			!bytes.Equal(stored.PayloadFingerprintSha256, fingerprint[:]) {
			return errorEnvelope(request, a.state, now, &wsv1.Error{Code: wsv1.ErrorCode_REQUEST_ID_CONFLICT}), false
		}
		return replayBuyPet(request, stored, now), false
	}

	petCfg, exists := config.Pet(buy.PetId)
	if !exists {
		return r.storeBuyPetFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_PET_NOT_FOUND}), true
	}
	if !petCfg.Enabled {
		return r.storeBuyPetFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_PET_DISABLED}), true
	}
	if buy.ExpectedConfigVersion != petCfg.ConfigVersion {
		return r.storeBuyPetFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_PRICE_CHANGED}), true
	}
	pet := ensurePetState(a.state)
	if ownsPet(pet, buy.PetId) {
		return r.storeBuyPetFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_PET_ALREADY_OWNED}), true
	}
	if a.state.Coins < petCfg.PriceCoins {
		return r.storeBuyPetFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_INSUFFICIENT_COINS}), true
	}

	a.state.Coins -= petCfg.PriceCoins
	addOwnedPet(pet, buy.PetId)
	a.state.PlayerSeq++
	a.state.CheckpointRevision++
	a.state.ConfigVersion = config.Version()
	a.state.UpdatedAtMS = now.UnixMilli()
	coinBalance := a.state.Coins
	panel := buildPetPanel(a.state, config, now.UnixMilli())
	payload := &wsv1.BuyPetResponse{
		PetId: buy.PetId, PriceCoins: petCfg.PriceCoins,
		Patch: &wsv1.PlayerStatePatch{CoinBalance: &coinBalance, CurrentChapter: a.state.Snapshot().CurrentChapter},
		Panel: panel,
	}
	body, _ := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	a.state.appendResult(&datav1.IdempotencyResultRecord{
		CallerPlayerId: callerPlayerID, RequestId: requestID,
		FingerprintSchemaVersion: idempotencyFingerprintSchemaVersion,
		ProtocolVersion:          request.ProtocolVersion, Action: uint32(request.Action),
		TargetPlayerId: request.TargetPlayerId, PayloadFingerprintSha256: fingerprint[:],
		CompletedAtMs: now.UnixMilli(), Success: true, ResultOwnerEpoch: a.state.OwnerEpoch,
		ResultPlayerSeq: a.state.PlayerSeq, ResponsePayloadType: uint32(wsv1.Action_BUY_PET),
		ResponsePayload: body,
	}, now)
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId, TargetPlayerId: request.TargetPlayerId,
		StateVersion: &wsv1.StateVersion{OwnerEpoch: a.state.OwnerEpoch, PlayerSeq: a.state.PlayerSeq},
		ServerTimeMs: now.UnixMilli(),
		Payload:      &wsv1.WsEnvelope_BuyPetResponse{BuyPetResponse: payload},
	}, true
}

func (r *Runtime) deployPet(
	a *runtimeActor,
	callerPlayerID uint64,
	request *wsv1.WsEnvelope,
	config *ConfigSnapshot,
	now time.Time,
) (*wsv1.WsEnvelope, bool) {
	requestID, err := parseRequestID(request.RequestId)
	if err != nil {
		return errorEnvelope(request, a.state, now, &wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}), false
	}
	deploy := request.GetDeployPetRequest()
	fingerprint := deployPetFingerprint(callerPlayerID, request, deploy)
	for _, stored := range a.state.RecentResults {
		if stored.CallerPlayerId != callerPlayerID || !bytes.Equal(stored.RequestId, requestID) {
			continue
		}
		if stored.FingerprintSchemaVersion != idempotencyFingerprintSchemaVersion ||
			stored.ProtocolVersion != request.ProtocolVersion ||
			stored.Action != uint32(request.Action) ||
			stored.TargetPlayerId != request.TargetPlayerId ||
			!bytes.Equal(stored.PayloadFingerprintSha256, fingerprint[:]) {
			return errorEnvelope(request, a.state, now, &wsv1.Error{Code: wsv1.ErrorCode_REQUEST_ID_CONFLICT}), false
		}
		return replayDeployPet(request, stored, now), false
	}

	if deploy.PetId == 0 {
		return r.storeDeployPetFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}), true
	}
	if _, exists := config.Pet(deploy.PetId); !exists {
		return r.storeDeployPetFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_PET_NOT_FOUND}), true
	}
	pet := ensurePetState(a.state)
	if !ownsPet(pet, deploy.PetId) {
		return r.storeDeployPetFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_PET_NOT_OWNED}), true
	}

	pet.ActivePetId = deploy.PetId
	a.state.PlayerSeq++
	a.state.CheckpointRevision++
	a.state.ConfigVersion = config.Version()
	a.state.UpdatedAtMS = now.UnixMilli()
	panel := buildPetPanel(a.state, config, now.UnixMilli())
	payload := &wsv1.DeployPetResponse{
		ActivePetId: pet.ActivePetId,
		Patch:       &wsv1.PlayerStatePatch{CurrentChapter: a.state.Snapshot().CurrentChapter},
		Panel:       panel,
	}
	body, _ := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	a.state.appendResult(&datav1.IdempotencyResultRecord{
		CallerPlayerId: callerPlayerID, RequestId: requestID,
		FingerprintSchemaVersion: idempotencyFingerprintSchemaVersion,
		ProtocolVersion:          request.ProtocolVersion, Action: uint32(request.Action),
		TargetPlayerId: request.TargetPlayerId, PayloadFingerprintSha256: fingerprint[:],
		CompletedAtMs: now.UnixMilli(), Success: true, ResultOwnerEpoch: a.state.OwnerEpoch,
		ResultPlayerSeq: a.state.PlayerSeq, ResponsePayloadType: uint32(wsv1.Action_DEPLOY_PET),
		ResponsePayload: body,
	}, now)
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId, TargetPlayerId: request.TargetPlayerId,
		StateVersion: &wsv1.StateVersion{OwnerEpoch: a.state.OwnerEpoch, PlayerSeq: a.state.PlayerSeq},
		ServerTimeMs: now.UnixMilli(),
		Payload:      &wsv1.WsEnvelope_DeployPetResponse{DeployPetResponse: payload},
	}, true
}

func (r *Runtime) buyPetFood(
	a *runtimeActor,
	callerPlayerID uint64,
	request *wsv1.WsEnvelope,
	config *ConfigSnapshot,
	now time.Time,
) (*wsv1.WsEnvelope, bool) {
	requestID, err := parseRequestID(request.RequestId)
	if err != nil {
		return errorEnvelope(request, a.state, now, &wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}), false
	}
	buy := request.GetBuyPetFoodRequest()
	fingerprint := buyPetFoodFingerprint(callerPlayerID, request, buy)
	for _, stored := range a.state.RecentResults {
		if stored.CallerPlayerId != callerPlayerID || !bytes.Equal(stored.RequestId, requestID) {
			continue
		}
		if stored.FingerprintSchemaVersion != idempotencyFingerprintSchemaVersion ||
			stored.ProtocolVersion != request.ProtocolVersion ||
			stored.Action != uint32(request.Action) ||
			stored.TargetPlayerId != request.TargetPlayerId ||
			!bytes.Equal(stored.PayloadFingerprintSha256, fingerprint[:]) {
			return errorEnvelope(request, a.state, now, &wsv1.Error{Code: wsv1.ErrorCode_REQUEST_ID_CONFLICT}), false
		}
		return replayBuyPetFood(request, stored, now), false
	}

	if buy.Quantity == 0 || buy.Quantity > 50 {
		return r.storeBuyPetFoodFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}), true
	}
	entry, exists := config.ShopEntry(buy.ShopEntryId)
	if !exists {
		return r.storeBuyPetFoodFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_SHOP_ENTRY_NOT_FOUND}), true
	}
	food, foodOK := config.PetFood(entry.ItemID)
	if !foodOK || !food.Enabled {
		return r.storeBuyPetFoodFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_SHOP_ENTRY_NOT_FOUND}), true
	}
	if !entry.Enabled {
		return r.storeBuyPetFoodFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_SHOP_ENTRY_DISABLED}), true
	}
	if buy.ExpectedPriceVersion != entry.PriceVersion {
		return r.storeBuyPetFoodFailure(a, request, requestID, fingerprint, config.Version(), now, &wsv1.Error{
			Code:            wsv1.ErrorCode_PRICE_CHANGED,
			LatestShopEntry: entry.View(),
		}), true
	}
	if uint64(buy.Quantity) > uint64(math.MaxInt64/entry.UnitPrice) {
		return r.storeBuyPetFoodFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}), true
	}
	totalPrice := int64(buy.Quantity) * entry.UnitPrice
	if a.state.Coins < totalPrice {
		return r.storeBuyPetFoodFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_INSUFFICIENT_COINS}), true
	}
	currentQuantity := a.state.Inventory[entry.ItemID]
	if uint64(currentQuantity)+uint64(buy.Quantity) > 300 {
		return r.storeBuyPetFoodFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_INVENTORY_STACK_LIMIT}), true
	}

	a.state.Coins -= totalPrice
	a.state.Inventory[entry.ItemID] = currentQuantity + buy.Quantity
	a.state.PlayerSeq++
	a.state.CheckpointRevision++
	a.state.ConfigVersion = config.Version()
	a.state.UpdatedAtMS = now.UnixMilli()
	coinBalance := a.state.Coins
	panel := buildPetPanel(a.state, config, now.UnixMilli())
	payload := &wsv1.BuyPetFoodResponse{
		ShopEntryId: buy.ShopEntryId, ItemId: entry.ItemID, Quantity: buy.Quantity,
		UnitPrice: entry.UnitPrice, TotalPrice: totalPrice,
		Patch: &wsv1.PlayerStatePatch{
			CoinBalance: &coinBalance,
			InventoryUpserts: []*wsv1.ItemStackView{{
				ItemId: entry.ItemID, Quantity: a.state.Inventory[entry.ItemID],
			}},
			CurrentChapter: a.state.Snapshot().CurrentChapter,
		},
		Panel: panel,
	}
	body, _ := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	a.state.appendResult(&datav1.IdempotencyResultRecord{
		CallerPlayerId: callerPlayerID, RequestId: requestID,
		FingerprintSchemaVersion: idempotencyFingerprintSchemaVersion,
		ProtocolVersion:          request.ProtocolVersion, Action: uint32(request.Action),
		TargetPlayerId: request.TargetPlayerId, PayloadFingerprintSha256: fingerprint[:],
		CompletedAtMs: now.UnixMilli(), Success: true, ResultOwnerEpoch: a.state.OwnerEpoch,
		ResultPlayerSeq: a.state.PlayerSeq, ResponsePayloadType: uint32(wsv1.Action_BUY_PET_FOOD),
		ResponsePayload: body,
	}, now)
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId, TargetPlayerId: request.TargetPlayerId,
		StateVersion: &wsv1.StateVersion{OwnerEpoch: a.state.OwnerEpoch, PlayerSeq: a.state.PlayerSeq},
		ServerTimeMs: now.UnixMilli(),
		Payload:      &wsv1.WsEnvelope_BuyPetFoodResponse{BuyPetFoodResponse: payload},
	}, true
}

func (r *Runtime) feedPet(
	a *runtimeActor,
	callerPlayerID uint64,
	request *wsv1.WsEnvelope,
	config *ConfigSnapshot,
	now time.Time,
) (*wsv1.WsEnvelope, bool) {
	requestID, err := parseRequestID(request.RequestId)
	if err != nil {
		return errorEnvelope(request, a.state, now, &wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}), false
	}
	fingerprint := feedPetFingerprint(callerPlayerID, request)
	for _, stored := range a.state.RecentResults {
		if stored.CallerPlayerId != callerPlayerID || !bytes.Equal(stored.RequestId, requestID) {
			continue
		}
		if stored.FingerprintSchemaVersion != idempotencyFingerprintSchemaVersion ||
			stored.ProtocolVersion != request.ProtocolVersion ||
			stored.Action != uint32(request.Action) ||
			stored.TargetPlayerId != request.TargetPlayerId ||
			!bytes.Equal(stored.PayloadFingerprintSha256, fingerprint[:]) {
			return errorEnvelope(request, a.state, now, &wsv1.Error{Code: wsv1.ErrorCode_REQUEST_ID_CONFLICT}), false
		}
		return replayFeedPet(request, stored, now), false
	}

	food, _, ok := config.PrimaryPetFood()
	if !ok || !food.Enabled {
		return r.storeFeedPetFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_CONFIG_UNAVAILABLE, Retryable: true}), true
	}
	quantity := a.state.Inventory[food.ItemID]
	if quantity == 0 {
		return r.storeFeedPetFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_ITEM_NOT_OWNED}), true
	}

	nowMS := now.UnixMilli()
	durationMS := int64(food.DurationSeconds) * 1000
	pet := ensurePetState(a.state)
	if pet.FoodActiveUntilMs > nowMS {
		pet.FoodActiveUntilMs += durationMS
	} else {
		pet.FoodActiveUntilMs = nowMS + durationMS
	}
	a.state.Inventory[food.ItemID] = quantity - 1
	if a.state.Inventory[food.ItemID] == 0 {
		delete(a.state.Inventory, food.ItemID)
	}
	a.state.PlayerSeq++
	a.state.CheckpointRevision++
	a.state.ConfigVersion = config.Version()
	a.state.UpdatedAtMS = nowMS

	var removed []uint32
	var upserts []*wsv1.ItemStackView
	if remaining, exists := a.state.Inventory[food.ItemID]; exists {
		upserts = []*wsv1.ItemStackView{{ItemId: food.ItemID, Quantity: remaining}}
	} else {
		removed = []uint32{food.ItemID}
	}
	panel := buildPetPanel(a.state, config, nowMS)
	payload := &wsv1.FeedPetResponse{
		FoodActiveUntilMs: pet.FoodActiveUntilMs,
		Patch: &wsv1.PlayerStatePatch{
			InventoryUpserts:        upserts,
			InventoryRemovedItemIds: removed,
			CurrentChapter:          a.state.Snapshot().CurrentChapter,
		},
		Panel: panel,
	}
	body, _ := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	a.state.appendResult(&datav1.IdempotencyResultRecord{
		CallerPlayerId: callerPlayerID, RequestId: requestID,
		FingerprintSchemaVersion: idempotencyFingerprintSchemaVersion,
		ProtocolVersion:          request.ProtocolVersion, Action: uint32(request.Action),
		TargetPlayerId: request.TargetPlayerId, PayloadFingerprintSha256: fingerprint[:],
		CompletedAtMs: nowMS, Success: true, ResultOwnerEpoch: a.state.OwnerEpoch,
		ResultPlayerSeq: a.state.PlayerSeq, ResponsePayloadType: uint32(wsv1.Action_FEED_PET),
		ResponsePayload: body,
	}, now)
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId, TargetPlayerId: request.TargetPlayerId,
		StateVersion: &wsv1.StateVersion{OwnerEpoch: a.state.OwnerEpoch, PlayerSeq: a.state.PlayerSeq},
		ServerTimeMs: nowMS,
		Payload:      &wsv1.WsEnvelope_FeedPetResponse{FeedPetResponse: payload},
	}, true
}

// evaluateStealGuard 在合法偷菜 apply 内冻结护主结果：出战宠物一律 100% 触发，
// 罚款金额随机 1~10 并写入 GuardPenaltyConfigured，重试不得重新抽取。
func (r *Runtime) evaluateStealGuard(state *State, config *ConfigSnapshot, nowMS int64) *wsv1.StealGuardOutcome {
	outcome := &wsv1.StealGuardOutcome{}
	pet := state.PetState
	if pet == nil || pet.ActivePetId == 0 {
		return outcome
	}
	petCfg, exists := config.Pet(pet.ActivePetId)
	if !exists || !petCfg.Enabled {
		return outcome
	}
	outcome.PetId = pet.ActivePetId
	outcome.PetConfigVersion = petCfg.ConfigVersion
	outcome.GuardProbabilityBps = 10000
	outcome.FoodActive = petFoodActive(pet, nowMS)
	outcome.GuardTriggered = true
	outcome.GuardPenaltyConfigured = int64(r.rollIntn(10) + 1)
	return outcome
}

func (r *Runtime) rollBPS() uint32 {
	if r != nil && r.randBPS != nil {
		return r.randBPS() % 10000
	}
	return uint32(time.Now().UnixNano() % 10000)
}

// rollIntn 返回 [0, n)。n<=0 时返回 0。
func (r *Runtime) rollIntn(n int) int {
	if n <= 0 {
		return 0
	}
	if r != nil && r.randIntn != nil {
		return r.randIntn(n)
	}
	return int(time.Now().UnixNano() % int64(n))
}

func buyPetFingerprint(callerPlayerID uint64, request *wsv1.WsEnvelope, buy *wsv1.BuyPetRequest) [sha256.Size]byte {
	hasher := sha256.New()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], callerPlayerID)
	hasher.Write(buf[:])
	binary.BigEndian.PutUint32(buf[:4], uint32(request.Action))
	hasher.Write(buf[:4])
	binary.BigEndian.PutUint32(buf[:4], buy.GetPetId())
	hasher.Write(buf[:4])
	binary.BigEndian.PutUint64(buf[:], buy.GetExpectedConfigVersion())
	hasher.Write(buf[:])
	var out [sha256.Size]byte
	copy(out[:], hasher.Sum(nil))
	return out
}

func deployPetFingerprint(callerPlayerID uint64, request *wsv1.WsEnvelope, deploy *wsv1.DeployPetRequest) [sha256.Size]byte {
	hasher := sha256.New()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], callerPlayerID)
	hasher.Write(buf[:])
	binary.BigEndian.PutUint32(buf[:4], uint32(request.Action))
	hasher.Write(buf[:4])
	binary.BigEndian.PutUint32(buf[:4], deploy.GetPetId())
	hasher.Write(buf[:4])
	var out [sha256.Size]byte
	copy(out[:], hasher.Sum(nil))
	return out
}

func buyPetFoodFingerprint(callerPlayerID uint64, request *wsv1.WsEnvelope, buy *wsv1.BuyPetFoodRequest) [sha256.Size]byte {
	hasher := sha256.New()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], callerPlayerID)
	hasher.Write(buf[:])
	binary.BigEndian.PutUint32(buf[:4], uint32(request.Action))
	hasher.Write(buf[:4])
	binary.BigEndian.PutUint32(buf[:4], buy.GetShopEntryId())
	hasher.Write(buf[:4])
	binary.BigEndian.PutUint32(buf[:4], buy.GetQuantity())
	hasher.Write(buf[:4])
	binary.BigEndian.PutUint64(buf[:], buy.GetExpectedPriceVersion())
	hasher.Write(buf[:])
	var out [sha256.Size]byte
	copy(out[:], hasher.Sum(nil))
	return out
}

func feedPetFingerprint(callerPlayerID uint64, request *wsv1.WsEnvelope) [sha256.Size]byte {
	hasher := sha256.New()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], callerPlayerID)
	hasher.Write(buf[:])
	binary.BigEndian.PutUint32(buf[:4], uint32(request.Action))
	hasher.Write(buf[:4])
	var out [sha256.Size]byte
	copy(out[:], hasher.Sum(nil))
	return out
}

func (r *Runtime) storeBuyPetFailure(
	a *runtimeActor, request *wsv1.WsEnvelope, requestID []byte, fingerprint [sha256.Size]byte,
	configVersion uint64, now time.Time, failure *wsv1.Error,
) *wsv1.WsEnvelope {
	return storePetCommandFailure(a, request, requestID, fingerprint, configVersion, now, failure, wsv1.Action_BUY_PET)
}

func (r *Runtime) storeDeployPetFailure(
	a *runtimeActor, request *wsv1.WsEnvelope, requestID []byte, fingerprint [sha256.Size]byte,
	configVersion uint64, now time.Time, failure *wsv1.Error,
) *wsv1.WsEnvelope {
	return storePetCommandFailure(a, request, requestID, fingerprint, configVersion, now, failure, wsv1.Action_DEPLOY_PET)
}

func (r *Runtime) storeBuyPetFoodFailure(
	a *runtimeActor, request *wsv1.WsEnvelope, requestID []byte, fingerprint [sha256.Size]byte,
	configVersion uint64, now time.Time, failure *wsv1.Error,
) *wsv1.WsEnvelope {
	return storePetCommandFailure(a, request, requestID, fingerprint, configVersion, now, failure, wsv1.Action_BUY_PET_FOOD)
}

func (r *Runtime) storeFeedPetFailure(
	a *runtimeActor, request *wsv1.WsEnvelope, requestID []byte, fingerprint [sha256.Size]byte,
	configVersion uint64, now time.Time, failure *wsv1.Error,
) *wsv1.WsEnvelope {
	return storePetCommandFailure(a, request, requestID, fingerprint, configVersion, now, failure, wsv1.Action_FEED_PET)
}

func storePetCommandFailure(
	a *runtimeActor, request *wsv1.WsEnvelope, requestID []byte, fingerprint [sha256.Size]byte,
	configVersion uint64, now time.Time, failure *wsv1.Error, action wsv1.Action,
) *wsv1.WsEnvelope {
	body, _ := proto.MarshalOptions{Deterministic: true}.Marshal(failure)
	a.state.CheckpointRevision++
	a.state.ConfigVersion = configVersion
	a.state.UpdatedAtMS = now.UnixMilli()
	a.state.appendResult(&datav1.IdempotencyResultRecord{
		CallerPlayerId: request.TargetPlayerId, RequestId: requestID,
		FingerprintSchemaVersion: idempotencyFingerprintSchemaVersion,
		ProtocolVersion:          request.ProtocolVersion, Action: uint32(request.Action),
		TargetPlayerId: request.TargetPlayerId, PayloadFingerprintSha256: fingerprint[:],
		CompletedAtMs: now.UnixMilli(), Success: false, ResultOwnerEpoch: a.state.OwnerEpoch,
		ResultPlayerSeq: a.state.PlayerSeq, ResponsePayloadType: uint32(action),
		ErrorPayload: body,
	}, now)
	return errorEnvelope(request, a.state, now, failure)
}

func replayBuyPet(request *wsv1.WsEnvelope, stored *datav1.IdempotencyResultRecord, now time.Time) *wsv1.WsEnvelope {
	response := baseReplayEnvelope(request, stored, now)
	if stored.Success {
		payload := &wsv1.BuyPetResponse{}
		if proto.Unmarshal(stored.ResponsePayload, payload) == nil {
			response.Payload = &wsv1.WsEnvelope_BuyPetResponse{BuyPetResponse: payload}
			return response
		}
	} else if failure := unmarshalStoredError(stored); failure != nil {
		response.Error = failure
		return response
	}
	response.Error = &wsv1.Error{Code: wsv1.ErrorCode_REQUEST_OUTCOME_UNKNOWN}
	return response
}

func replayDeployPet(request *wsv1.WsEnvelope, stored *datav1.IdempotencyResultRecord, now time.Time) *wsv1.WsEnvelope {
	response := baseReplayEnvelope(request, stored, now)
	if stored.Success {
		payload := &wsv1.DeployPetResponse{}
		if proto.Unmarshal(stored.ResponsePayload, payload) == nil {
			response.Payload = &wsv1.WsEnvelope_DeployPetResponse{DeployPetResponse: payload}
			return response
		}
	} else if failure := unmarshalStoredError(stored); failure != nil {
		response.Error = failure
		return response
	}
	response.Error = &wsv1.Error{Code: wsv1.ErrorCode_REQUEST_OUTCOME_UNKNOWN}
	return response
}

func replayBuyPetFood(request *wsv1.WsEnvelope, stored *datav1.IdempotencyResultRecord, now time.Time) *wsv1.WsEnvelope {
	response := baseReplayEnvelope(request, stored, now)
	if stored.Success {
		payload := &wsv1.BuyPetFoodResponse{}
		if proto.Unmarshal(stored.ResponsePayload, payload) == nil {
			response.Payload = &wsv1.WsEnvelope_BuyPetFoodResponse{BuyPetFoodResponse: payload}
			return response
		}
	} else if failure := unmarshalStoredError(stored); failure != nil {
		response.Error = failure
		return response
	}
	response.Error = &wsv1.Error{Code: wsv1.ErrorCode_REQUEST_OUTCOME_UNKNOWN}
	return response
}

func replayFeedPet(request *wsv1.WsEnvelope, stored *datav1.IdempotencyResultRecord, now time.Time) *wsv1.WsEnvelope {
	response := baseReplayEnvelope(request, stored, now)
	if stored.Success {
		payload := &wsv1.FeedPetResponse{}
		if proto.Unmarshal(stored.ResponsePayload, payload) == nil {
			response.Payload = &wsv1.WsEnvelope_FeedPetResponse{FeedPetResponse: payload}
			return response
		}
	} else if failure := unmarshalStoredError(stored); failure != nil {
		response.Error = failure
		return response
	}
	response.Error = &wsv1.Error{Code: wsv1.ErrorCode_REQUEST_OUTCOME_UNKNOWN}
	return response
}

func baseReplayEnvelope(request *wsv1.WsEnvelope, stored *datav1.IdempotencyResultRecord, now time.Time) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId, TargetPlayerId: request.TargetPlayerId,
		StateVersion: &wsv1.StateVersion{OwnerEpoch: stored.ResultOwnerEpoch, PlayerSeq: stored.ResultPlayerSeq},
		ServerTimeMs: now.UnixMilli(), Replayed: true,
	}
}

func unmarshalStoredError(stored *datav1.IdempotencyResultRecord) *wsv1.Error {
	failure := &wsv1.Error{}
	if proto.Unmarshal(stored.ErrorPayload, failure) == nil {
		return failure
	}
	return nil
}
