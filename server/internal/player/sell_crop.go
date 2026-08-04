package player

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	chapterv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/chapter"
	"google.golang.org/protobuf/proto"
)

func (r *Runtime) sellCrop(
	a *runtimeActor,
	callerPlayerID uint64,
	request *wsv1.WsEnvelope,
	config *ConfigSnapshot,
	now time.Time,
) (*wsv1.WsEnvelope, bool) {
	requestID, err := parseRequestID(request.RequestId)
	if err != nil {
		return errorEnvelope(request, a.state, now,
			&wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}), false
	}
	sell := request.GetSellCropRequest()
	fingerprint := sellCropFingerprint(callerPlayerID, request, sell)
	for _, stored := range a.state.RecentResults {
		if stored.CallerPlayerId != callerPlayerID || !bytes.Equal(stored.RequestId, requestID) {
			continue
		}
		if stored.FingerprintSchemaVersion != idempotencyFingerprintSchemaVersion ||
			stored.ProtocolVersion != request.ProtocolVersion ||
			stored.Action != uint32(request.Action) ||
			stored.TargetPlayerId != request.TargetPlayerId ||
			!bytes.Equal(stored.PayloadFingerprintSha256, fingerprint[:]) {
			return errorEnvelope(request, a.state, now,
				&wsv1.Error{Code: wsv1.ErrorCode_REQUEST_ID_CONFLICT}), false
		}
		return replaySellCrop(request, stored, now), false
	}

	if sell.CropItemId == 0 || sell.ExpectedPriceVersion == 0 || !validSellAmount(sell) {
		return r.storeSellCropFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}), true
	}
	rule, exists := config.SellRule(sell.CropItemId)
	if !exists || !rule.Enabled {
		return r.storeSellCropFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_ITEM_NOT_SELLABLE}), true
	}
	if sell.ExpectedPriceVersion != rule.PriceVersion {
		return r.storeSellCropFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_PRICE_CHANGED}), true
	}
	currentQuantity := a.state.Inventory[sell.CropItemId]
	soldQuantity := sell.GetQuantity()
	if sell.GetSellAll() {
		soldQuantity = currentQuantity
	}
	if soldQuantity == 0 || soldQuantity > currentQuantity {
		return r.storeSellCropFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_INSUFFICIENT_ITEM_QUANTITY}), true
	}
	if uint64(soldQuantity) > uint64(math.MaxInt64/rule.UnitPrice) {
		return r.storeSellCropFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_CONFIG_UNAVAILABLE, Retryable: true}), true
	}
	totalPrice := int64(soldQuantity) * rule.UnitPrice
	if a.state.Coins > math.MaxInt64-totalPrice {
		return r.storeSellCropFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_CONFIG_UNAVAILABLE, Retryable: true}), true
	}

	remaining := currentQuantity - soldQuantity
	if remaining == 0 {
		delete(a.state.Inventory, sell.CropItemId)
	} else {
		a.state.Inventory[sell.CropItemId] = remaining
	}
	a.state.Coins += totalPrice
	for index := range a.state.Tasks {
		if a.state.Tasks[index].ID == 5 {
			next := uint64(a.state.Tasks[index].Current) + uint64(soldQuantity)
			if next > uint64(a.state.Tasks[index].Target) {
				next = uint64(a.state.Tasks[index].Target)
			}
			a.state.Tasks[index].Current = uint32(next)
			break
		}
	}
	if a.state.Chapter == chapterv1.ChapterStatus_IN_PROGRESS && allTasksComplete(a.state.Tasks) {
		a.state.Chapter = chapterv1.ChapterStatus_CLAIMABLE
	}
	a.state.PlayerSeq++
	a.state.CheckpointRevision++
	a.state.ConfigVersion = config.Version()
	a.state.UpdatedAtMS = now.UnixMilli()
	coinBalance := a.state.Coins
	patch := &wsv1.PlayerStatePatch{
		CoinBalance:    &coinBalance,
		CurrentChapter: a.state.Snapshot().CurrentChapter,
	}
	if remaining == 0 {
		patch.InventoryRemovedItemIds = []uint32{sell.CropItemId}
	} else {
		patch.InventoryUpserts = []*wsv1.ItemStackView{{
			ItemId: sell.CropItemId, Quantity: remaining,
		}}
	}
	payload := &wsv1.SellCropResponse{
		CropItemId: sell.CropItemId, SoldQuantity: soldQuantity,
		UnitPrice: rule.UnitPrice, TotalPrice: totalPrice, Patch: patch,
	}
	body, _ := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	a.state.appendResult(&datav1.IdempotencyResultRecord{
		CallerPlayerId: callerPlayerID, RequestId: requestID,
		FingerprintSchemaVersion: idempotencyFingerprintSchemaVersion,
		ProtocolVersion:          request.ProtocolVersion, Action: uint32(request.Action),
		TargetPlayerId: request.TargetPlayerId, PayloadFingerprintSha256: fingerprint[:],
		CompletedAtMs: now.UnixMilli(), Success: true, ResultOwnerEpoch: LocalOwnerEpoch,
		ResultPlayerSeq: a.state.PlayerSeq, ResponsePayloadType: uint32(wsv1.Action_SELL_CROP),
		ResponsePayload: body,
	}, now)
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId, TargetPlayerId: request.TargetPlayerId,
		StateVersion: &wsv1.StateVersion{OwnerEpoch: LocalOwnerEpoch, PlayerSeq: a.state.PlayerSeq},
		ServerTimeMs: now.UnixMilli(),
		Payload:      &wsv1.WsEnvelope_SellCropResponse{SellCropResponse: payload},
	}, true
}

func validSellAmount(request *wsv1.SellCropRequest) bool {
	switch amount := request.GetAmount().(type) {
	case *wsv1.SellCropRequest_Quantity:
		return amount.Quantity > 0
	case *wsv1.SellCropRequest_SellAll:
		return amount.SellAll
	default:
		return false
	}
}

func allTasksComplete(tasks []Task) bool {
	if len(tasks) == 0 {
		return false
	}
	for _, task := range tasks {
		if task.Target == 0 || task.Current < task.Target {
			return false
		}
	}
	return true
}

func (r *Runtime) storeSellCropFailure(
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
		CompletedAtMs: now.UnixMilli(), Success: false, ResultOwnerEpoch: LocalOwnerEpoch,
		ResultPlayerSeq: a.state.PlayerSeq, ResponsePayloadType: uint32(wsv1.Action_SELL_CROP),
		ErrorPayload: body,
	}, now)
	return errorEnvelope(request, a.state, now, failure)
}

func replaySellCrop(
	request *wsv1.WsEnvelope,
	stored *datav1.IdempotencyResultRecord,
	now time.Time,
) *wsv1.WsEnvelope {
	response := &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId, TargetPlayerId: request.TargetPlayerId,
		StateVersion: &wsv1.StateVersion{
			OwnerEpoch: stored.ResultOwnerEpoch, PlayerSeq: stored.ResultPlayerSeq,
		},
		ServerTimeMs: now.UnixMilli(), Replayed: true,
	}
	if stored.Success {
		payload := &wsv1.SellCropResponse{}
		if proto.Unmarshal(stored.ResponsePayload, payload) == nil {
			response.Payload = &wsv1.WsEnvelope_SellCropResponse{SellCropResponse: payload}
			return response
		}
	} else {
		failure := &wsv1.Error{}
		if proto.Unmarshal(stored.ErrorPayload, failure) == nil {
			response.Error = failure
			return response
		}
	}
	response.Error = &wsv1.Error{Code: wsv1.ErrorCode_REQUEST_OUTCOME_UNKNOWN}
	return response
}

func sellCropFingerprint(
	callerPlayerID uint64,
	envelope *wsv1.WsEnvelope,
	request *wsv1.SellCropRequest,
) [sha256.Size]byte {
	body := make([]byte, 0, 41)
	body = binary.BigEndian.AppendUint32(body, idempotencyFingerprintSchemaVersion)
	body = binary.BigEndian.AppendUint32(body, envelope.ProtocolVersion)
	body = binary.BigEndian.AppendUint32(body, uint32(envelope.Action))
	body = binary.BigEndian.AppendUint64(body, callerPlayerID)
	body = binary.BigEndian.AppendUint64(body, envelope.TargetPlayerId)
	body = binary.BigEndian.AppendUint32(body, request.CropItemId)
	body = binary.BigEndian.AppendUint64(body, request.ExpectedPriceVersion)
	switch amount := request.GetAmount().(type) {
	case *wsv1.SellCropRequest_Quantity:
		body = append(body, 1)
		body = binary.BigEndian.AppendUint32(body, amount.Quantity)
	case *wsv1.SellCropRequest_SellAll:
		body = append(body, 2)
		if amount.SellAll {
			body = append(body, 1)
		} else {
			body = append(body, 0)
		}
	default:
		body = append(body, 0)
	}
	return sha256.Sum256(body)
}
