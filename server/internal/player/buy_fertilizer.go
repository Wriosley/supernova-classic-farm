package player

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"google.golang.org/protobuf/proto"
)

func (r *Runtime) buyFertilizer(
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
	buy := request.GetBuyFertilizerRequest()
	fingerprint := buyFertilizerFingerprint(callerPlayerID, request, buy)
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
		return replayBuyFertilizer(request, stored, now), false
	}

	if buy.Quantity == 0 || buy.Quantity > 50 {
		return r.storeBuyFertilizerFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}), true
	}
	entry, exists := config.ShopEntry(buy.ShopEntryId)
	if !exists || entry.ItemID != BasicFertilizerID {
		return r.storeBuyFertilizerFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_SHOP_ENTRY_NOT_FOUND}), true
	}
	if !entry.Enabled {
		return r.storeBuyFertilizerFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_SHOP_ENTRY_DISABLED}), true
	}
	if buy.ExpectedPriceVersion != entry.PriceVersion {
		return r.storeBuyFertilizerFailure(a, request, requestID, fingerprint, config.Version(), now, &wsv1.Error{
			Code:            wsv1.ErrorCode_PRICE_CHANGED,
			LatestShopEntry: entry.View(),
		}), true
	}
	if uint64(buy.Quantity) > uint64(math.MaxInt64/entry.UnitPrice) {
		return r.storeBuyFertilizerFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}), true
	}
	totalPrice := int64(buy.Quantity) * entry.UnitPrice
	if a.state.Coins < totalPrice {
		return r.storeBuyFertilizerFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_INSUFFICIENT_COINS}), true
	}
	currentQuantity := a.state.Inventory[entry.ItemID]
	if uint64(currentQuantity)+uint64(buy.Quantity) > 300 {
		return r.storeBuyFertilizerFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_INVENTORY_STACK_LIMIT}), true
	}

	a.state.Coins -= totalPrice
	a.state.Inventory[entry.ItemID] = currentQuantity + buy.Quantity
	a.state.PlayerSeq++
	a.state.CheckpointRevision++
	a.state.ConfigVersion = config.Version()
	a.state.UpdatedAtMS = now.UnixMilli()
	coinBalance := a.state.Coins
	payload := &wsv1.BuyFertilizerResponse{
		ShopEntryId: buy.ShopEntryId, ItemId: entry.ItemID, Quantity: buy.Quantity,
		UnitPrice: entry.UnitPrice, TotalPrice: totalPrice,
		Patch: &wsv1.PlayerStatePatch{
			CoinBalance: &coinBalance,
			InventoryUpserts: []*wsv1.ItemStackView{{
				ItemId: entry.ItemID, Quantity: a.state.Inventory[entry.ItemID],
			}},
			CurrentChapter: a.state.Snapshot().CurrentChapter,
		},
	}
	body, _ := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	a.state.appendResult(&datav1.IdempotencyResultRecord{
		CallerPlayerId: callerPlayerID, RequestId: requestID,
		FingerprintSchemaVersion: idempotencyFingerprintSchemaVersion,
		ProtocolVersion:          request.ProtocolVersion, Action: uint32(request.Action),
		TargetPlayerId: request.TargetPlayerId, PayloadFingerprintSha256: fingerprint[:],
		CompletedAtMs: now.UnixMilli(), Success: true, ResultOwnerEpoch: a.state.OwnerEpoch,
		ResultPlayerSeq: a.state.PlayerSeq, ResponsePayloadType: uint32(wsv1.Action_BUY_FERTILIZER),
		ResponsePayload: body,
	}, now)
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId, TargetPlayerId: request.TargetPlayerId,
		StateVersion: &wsv1.StateVersion{OwnerEpoch: a.state.OwnerEpoch, PlayerSeq: a.state.PlayerSeq},
		ServerTimeMs: now.UnixMilli(),
		Payload:      &wsv1.WsEnvelope_BuyFertilizerResponse{BuyFertilizerResponse: payload},
	}, true
}

func (r *Runtime) storeBuyFertilizerFailure(
	a *runtimeActor,
	request *wsv1.WsEnvelope,
	requestID []byte,
	fingerprint [sha256.Size]byte,
	configVersion uint64,
	now time.Time,
	failure *wsv1.Error,
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
		ResultPlayerSeq: a.state.PlayerSeq, ResponsePayloadType: uint32(wsv1.Action_BUY_FERTILIZER),
		ErrorPayload: body,
	}, now)
	return errorEnvelope(request, a.state, now, failure)
}

func replayBuyFertilizer(request *wsv1.WsEnvelope, stored *datav1.IdempotencyResultRecord, now time.Time) *wsv1.WsEnvelope {
	response := &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId, TargetPlayerId: request.TargetPlayerId,
		StateVersion: &wsv1.StateVersion{OwnerEpoch: stored.ResultOwnerEpoch, PlayerSeq: stored.ResultPlayerSeq},
		ServerTimeMs: now.UnixMilli(), Replayed: true,
	}
	if stored.Success {
		payload := &wsv1.BuyFertilizerResponse{}
		if proto.Unmarshal(stored.ResponsePayload, payload) == nil {
			response.Payload = &wsv1.WsEnvelope_BuyFertilizerResponse{BuyFertilizerResponse: payload}
			return response
		}
	} else {
		failure := &wsv1.Error{}
		if proto.Unmarshal(stored.ErrorPayload, failure) == nil {
			response.Error = failure
			return response
		}
	}
	response.Error = &wsv1.Error{Code: wsv1.ErrorCode_REQUEST_OUTCOME_UNKNOWN, Retryable: false}
	return response
}

func buyFertilizerFingerprint(callerPlayerID uint64, envelope *wsv1.WsEnvelope, request *wsv1.BuyFertilizerRequest) [sha256.Size]byte {
	body := make([]byte, 0, 44)
	appendUint32 := func(value uint32) { body = binary.BigEndian.AppendUint32(body, value) }
	appendUint64 := func(value uint64) { body = binary.BigEndian.AppendUint64(body, value) }
	appendUint32(idempotencyFingerprintSchemaVersion)
	appendUint32(envelope.ProtocolVersion)
	appendUint32(uint32(envelope.Action))
	appendUint64(callerPlayerID)
	appendUint64(envelope.TargetPlayerId)
	appendUint32(request.ShopEntryId)
	appendUint32(request.Quantity)
	appendUint64(request.ExpectedPriceVersion)
	return sha256.Sum256(body)
}
