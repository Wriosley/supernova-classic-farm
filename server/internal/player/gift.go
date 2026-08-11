package player

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	eventv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/event"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"google.golang.org/protobuf/proto"
)

const (
	minFriendGiftQuantity = 1
	maxFriendGiftQuantity = 10
	defaultGiftSenderName = "好友"
)

func (r *Runtime) sendFriendGift(
	a *runtimeActor,
	callerPlayerID uint64,
	request *wsv1.WsEnvelope,
	config *ConfigSnapshot,
	senderDisplayName string,
	now time.Time,
) (*wsv1.WsEnvelope, bool) {
	requestID, err := parseRequestID(request.RequestId)
	if err != nil {
		return errorEnvelope(request, a.state, now,
			&wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}), false
	}
	gift := request.GetSendFriendGiftRequest()
	fingerprint := sendFriendGiftFingerprint(callerPlayerID, request, gift)
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
		return replaySendFriendGift(request, stored, now), false
	}

	if gift == nil || gift.RecipientPlayerId == 0 || gift.CropItemId == 0 ||
		gift.Quantity < minFriendGiftQuantity || gift.Quantity > maxFriendGiftQuantity {
		return r.storeSendFriendGiftFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}), true
	}
	if gift.RecipientPlayerId == callerPlayerID {
		return r.storeSendFriendGiftFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_CANNOT_FRIEND_SELF}), true
	}
	if !config.IsCropItem(gift.CropItemId) {
		return r.storeSendFriendGiftFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_ITEM_NOT_SELLABLE}), true
	}
	currentQuantity := a.state.Inventory[gift.CropItemId]
	if gift.Quantity > currentQuantity {
		return r.storeSendFriendGiftFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_INSUFFICIENT_ITEM_QUANTITY}), true
	}

	displayName := strings.TrimSpace(senderDisplayName)
	if displayName == "" {
		displayName = defaultGiftSenderName
	}
	if utf8.RuneCountInString(displayName) > 32 {
		displayName = string([]rune(displayName)[:32])
	}

	nextPlayerSeq := a.state.PlayerSeq + 1
	pendingRecord, err := buildGiftMailOutbox(
		callerPlayerID, displayName, gift.RecipientPlayerId,
		gift.CropItemId, gift.Quantity, requestID,
		a.state.OwnerEpoch, nextPlayerSeq, now,
	)
	if err != nil {
		return r.storeSendFriendGiftFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_CONFIG_UNAVAILABLE, Retryable: true}), true
	}

	remaining := currentQuantity - gift.Quantity
	if remaining == 0 {
		delete(a.state.Inventory, gift.CropItemId)
	} else {
		a.state.Inventory[gift.CropItemId] = remaining
	}
	a.state.PlayerSeq = nextPlayerSeq
	a.state.CheckpointRevision++
	a.state.ConfigVersion = config.Version()
	a.state.UpdatedAtMS = now.UnixMilli()
	a.state.PendingOutbox = append(a.state.PendingOutbox, pendingRecord)

	patch := &wsv1.PlayerStatePatch{}
	if remaining == 0 {
		patch.InventoryRemovedItemIds = []uint32{gift.CropItemId}
	} else {
		patch.InventoryUpserts = []*wsv1.ItemStackView{{
			ItemId: gift.CropItemId, Quantity: remaining,
		}}
	}
	payload := &wsv1.SendFriendGiftResponse{
		OutboxEventId: append([]byte(nil), pendingRecord.EventId...),
		CropItemId:    gift.CropItemId,
		Quantity:      gift.Quantity,
		Patch:         patch,
	}
	body, _ := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	a.state.appendResult(&datav1.IdempotencyResultRecord{
		CallerPlayerId: callerPlayerID, RequestId: requestID,
		FingerprintSchemaVersion: idempotencyFingerprintSchemaVersion,
		ProtocolVersion:          request.ProtocolVersion, Action: uint32(request.Action),
		TargetPlayerId: request.TargetPlayerId, PayloadFingerprintSha256: fingerprint[:],
		CompletedAtMs: now.UnixMilli(), Success: true, ResultOwnerEpoch: a.state.OwnerEpoch,
		ResultPlayerSeq:     a.state.PlayerSeq,
		ResponsePayloadType: uint32(wsv1.Action_SEND_FRIEND_GIFT),
		ResponsePayload:     body,
		OutboxIds:           [][]byte{append([]byte(nil), pendingRecord.EventId...)},
	}, now)
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId, TargetPlayerId: request.TargetPlayerId,
		StateVersion: &wsv1.StateVersion{OwnerEpoch: a.state.OwnerEpoch, PlayerSeq: a.state.PlayerSeq},
		ServerTimeMs: now.UnixMilli(),
		Payload: &wsv1.WsEnvelope_SendFriendGiftResponse{
			SendFriendGiftResponse: payload,
		},
	}, true
}

func buildGiftMailOutbox(
	senderPlayerID uint64,
	senderDisplayName string,
	recipientPlayerID uint64,
	cropItemID, quantity uint32,
	requestID []byte,
	ownerEpoch, playerSeq uint64,
	now time.Time,
) (*datav1.PendingOutboxRecord, error) {
	payload := &eventv1.CreateGiftMailV1{
		SenderPlayerId:     senderPlayerID,
		SenderDisplayName:  senderDisplayName,
		RecipientPlayerId:  recipientPlayerID,
		CropItemId:         cropItemID,
		Quantity:           quantity,
		CreatedAtMs:        now.UnixMilli(),
	}
	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(body) > 48<<10 {
		return nil, errors.New("gift mail payload exceeds 48 KiB")
	}
	digest := sha256.Sum256(body)
	return &datav1.PendingOutboxRecord{
		EventId:              giftMailEventID(senderPlayerID, requestID),
		EventType:            datav1.OutboxEventType_CREATE_GIFT_MAIL,
		EventContractVersion: 1,
		AggregatePlayerId:    senderPlayerID,
		CausedByRequestId:    append([]byte(nil), requestID...),
		CreatedOwnerEpoch:    ownerEpoch,
		CreatedPlayerSeq:     playerSeq,
		CreatedAtMs:          now.UnixMilli(),
		Payload:              body,
		PayloadSha256:        digest[:],
	}, nil
}

func giftMailEventID(senderPlayerID uint64, requestID []byte) []byte {
	body := make([]byte, 0, len(requestID)+32)
	body = append(body, "friend-gift-mail:"...)
	body = binary.BigEndian.AppendUint64(body, senderPlayerID)
	body = append(body, requestID...)
	sum := sha256.Sum256(body)
	id := append([]byte(nil), sum[:16]...)
	id[6] = (id[6] & 0x0f) | 0x50
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}

func (r *Runtime) storeSendFriendGiftFailure(
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
		ResultPlayerSeq: a.state.PlayerSeq, ResponsePayloadType: uint32(wsv1.Action_SEND_FRIEND_GIFT),
		ResponsePayload: body,
	}, now)
	return errorEnvelope(request, a.state, now, failure)
}

func replaySendFriendGift(
	request *wsv1.WsEnvelope, stored *datav1.IdempotencyResultRecord, now time.Time,
) *wsv1.WsEnvelope {
	if !stored.Success {
		failure := &wsv1.Error{}
		_ = proto.Unmarshal(stored.ResponsePayload, failure)
		response := errorEnvelope(request, &State{
			OwnerEpoch: stored.ResultOwnerEpoch, PlayerSeq: stored.ResultPlayerSeq,
		}, now, failure)
		response.Replayed = true
		return response
	}
	payload := &wsv1.SendFriendGiftResponse{}
	_ = proto.Unmarshal(stored.ResponsePayload, payload)
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId, TargetPlayerId: request.TargetPlayerId,
		StateVersion: &wsv1.StateVersion{
			OwnerEpoch: stored.ResultOwnerEpoch, PlayerSeq: stored.ResultPlayerSeq,
		},
		ServerTimeMs: now.UnixMilli(), Replayed: true,
		Payload: &wsv1.WsEnvelope_SendFriendGiftResponse{SendFriendGiftResponse: payload},
	}
}

func sendFriendGiftFingerprint(
	callerPlayerID uint64,
	envelope *wsv1.WsEnvelope,
	request *wsv1.SendFriendGiftRequest,
) [sha256.Size]byte {
	body := make([]byte, 0, 40)
	body = binary.BigEndian.AppendUint32(body, idempotencyFingerprintSchemaVersion)
	body = binary.BigEndian.AppendUint32(body, envelope.ProtocolVersion)
	body = binary.BigEndian.AppendUint32(body, uint32(envelope.Action))
	body = binary.BigEndian.AppendUint64(body, callerPlayerID)
	body = binary.BigEndian.AppendUint64(body, envelope.TargetPlayerId)
	if request != nil {
		body = binary.BigEndian.AppendUint64(body, request.RecipientPlayerId)
		body = binary.BigEndian.AppendUint32(body, request.CropItemId)
		body = binary.BigEndian.AppendUint32(body, request.Quantity)
	}
	return sha256.Sum256(body)
}
