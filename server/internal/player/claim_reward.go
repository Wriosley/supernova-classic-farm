package player

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	eventv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/event"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	chapterv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/chapter"
	"google.golang.org/protobuf/proto"
)

func (r *Runtime) claimChapterReward(
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
	claim := request.GetClaimChapterRewardRequest()
	fingerprint := claimRewardFingerprint(callerPlayerID, request, claim)
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
		return replayClaimReward(request, stored, now), false
	}

	if claim.ChapterId == 0 {
		return r.storeClaimRewardFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}), true
	}
	if claim.ChapterId != a.state.ChapterID {
		return r.storeClaimRewardFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_CHAPTER_NOT_FOUND}), true
	}
	if a.state.Chapter == chapterv1.ChapterStatus_CLAIMED {
		return r.storeClaimRewardFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_CHAPTER_REWARD_ALREADY_CLAIMED}), true
	}
	if a.state.Chapter != chapterv1.ChapterStatus_CLAIMABLE || !allTasksComplete(a.state.Tasks) {
		return r.storeClaimRewardFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_CHAPTER_NOT_CLAIMABLE}), true
	}
	chapter, exists := config.Chapter(a.state.ChapterID)
	if !exists || chapter.ConfigVersion != a.state.ChapterConfigVersion ||
		chapter.NextChapterID == 0 {
		return r.storeClaimRewardFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_CONFIG_UNAVAILABLE, Retryable: true}), true
	}
	nextChapter, exists := config.Chapter(chapter.NextChapterID)
	if !exists {
		return r.storeClaimRewardFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_CONFIG_UNAVAILABLE, Retryable: true}), true
	}
	if a.state.Coins > math.MaxInt64-chapter.RewardCoins {
		return r.storeClaimRewardFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_CONFIG_UNAVAILABLE, Retryable: true}), true
	}

	inventoryAfter := make(map[uint32]uint32, len(a.state.Inventory)+len(chapter.RewardItems))
	for itemID, quantity := range a.state.Inventory {
		inventoryAfter[itemID] = quantity
	}
	itemsAdded := make([]*wsv1.ItemStackView, 0, len(chapter.RewardItems))
	itemsPending := make([]*wsv1.ItemStackView, 0, len(chapter.RewardItems))
	for _, reward := range chapter.RewardItems {
		current := inventoryAfter[reward.ItemID]
		var capacity uint32
		if current > 0 {
			capacity = inventoryStackLimit - current
		} else if len(inventoryAfter) < inventoryTypeLimit {
			capacity = inventoryStackLimit
		}
		added := reward.Quantity
		if added > capacity {
			added = capacity
		}
		if added > 0 {
			inventoryAfter[reward.ItemID] = current + added
			itemsAdded = append(itemsAdded, &wsv1.ItemStackView{
				ItemId: reward.ItemID, Quantity: added,
			})
		}
		if pending := reward.Quantity - added; pending > 0 {
			itemsPending = append(itemsPending, &wsv1.ItemStackView{
				ItemId: reward.ItemID, Quantity: pending,
			})
		}
	}

	nextPlayerSeq := a.state.PlayerSeq + 1
	var pendingRecord *datav1.PendingOutboxRecord
	if len(itemsPending) > 0 {
		pendingRecord, err = buildRewardMailOutbox(
			callerPlayerID, requestID, chapter, itemsPending,
			a.state.OwnerEpoch, nextPlayerSeq, now,
		)
		if err != nil {
			return r.storeClaimRewardFailure(a, request, requestID, fingerprint, config.Version(), now,
				&wsv1.Error{Code: wsv1.ErrorCode_CONFIG_UNAVAILABLE, Retryable: true}), true
		}
	}

	a.state.Coins += chapter.RewardCoins
	a.state.Inventory = inventoryAfter
	a.state.ChapterID = nextChapter.ChapterID
	a.state.ChapterConfigVersion = nextChapter.ConfigVersion
	a.state.Chapter = chapterv1.ChapterStatus_IN_PROGRESS
	a.state.ChapterActivatedAtMS = now.UnixMilli()
	a.state.Tasks = append([]Task(nil), nextChapter.Tasks...)
	a.state.PlayerSeq = nextPlayerSeq
	a.state.CheckpointRevision++
	a.state.ConfigVersion = config.Version()
	a.state.UpdatedAtMS = now.UnixMilli()
	if pendingRecord != nil {
		a.state.PendingOutbox = append(a.state.PendingOutbox, pendingRecord)
	}

	coinBalance := a.state.Coins
	patch := &wsv1.PlayerStatePatch{
		CoinBalance:    &coinBalance,
		CurrentChapter: a.state.Snapshot().CurrentChapter,
	}
	for _, added := range itemsAdded {
		patch.InventoryUpserts = append(patch.InventoryUpserts, &wsv1.ItemStackView{
			ItemId: added.ItemId, Quantity: a.state.Inventory[added.ItemId],
		})
	}
	payload := &wsv1.ClaimChapterRewardResponse{
		ChapterId: claim.ChapterId, CoinGranted: chapter.RewardCoins,
		ItemsAddedToInventory: itemsAdded, ItemsPendingMail: itemsPending,
		Patch: patch,
	}
	body, _ := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	result := &datav1.IdempotencyResultRecord{
		CallerPlayerId: callerPlayerID, RequestId: requestID,
		FingerprintSchemaVersion: idempotencyFingerprintSchemaVersion,
		ProtocolVersion:          request.ProtocolVersion, Action: uint32(request.Action),
		TargetPlayerId: request.TargetPlayerId, PayloadFingerprintSha256: fingerprint[:],
		CompletedAtMs: now.UnixMilli(), Success: true, ResultOwnerEpoch: a.state.OwnerEpoch,
		ResultPlayerSeq:     a.state.PlayerSeq,
		ResponsePayloadType: uint32(wsv1.Action_CLAIM_CHAPTER_REWARD),
		ResponsePayload:     body,
	}
	if pendingRecord != nil {
		result.OutboxIds = [][]byte{append([]byte(nil), pendingRecord.EventId...)}
	}
	a.state.appendResult(result, now)
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId, TargetPlayerId: request.TargetPlayerId,
		StateVersion: &wsv1.StateVersion{OwnerEpoch: a.state.OwnerEpoch, PlayerSeq: a.state.PlayerSeq},
		ServerTimeMs: now.UnixMilli(),
		Payload: &wsv1.WsEnvelope_ClaimChapterRewardResponse{
			ClaimChapterRewardResponse: payload,
		},
	}, true
}

func buildRewardMailOutbox(
	playerID uint64,
	requestID []byte,
	chapter ChapterConfig,
	pending []*wsv1.ItemStackView,
	ownerEpoch uint64,
	playerSeq uint64,
	now time.Time,
) (*datav1.PendingOutboxRecord, error) {
	payload := &eventv1.CreateRewardMailV1{
		RecipientPlayerId: playerID,
		SubjectTextKey:    "mail.chapter_reward.subject",
		BodyTextKey:       "mail.chapter_reward.body",
		Source: &eventv1.RewardMailSourceV1{
			ChapterId: chapter.ChapterID, ChapterConfigVersion: chapter.ConfigVersion,
			RequestId: append([]byte(nil), requestID...),
		},
	}
	for _, item := range pending {
		payload.Attachments = append(payload.Attachments, &eventv1.RewardMailAttachmentV1{
			ItemId: item.ItemId, Quantity: item.Quantity,
		})
	}
	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(body) > 48<<10 {
		return nil, errors.New("reward mail payload exceeds 48 KiB")
	}
	digest := sha256.Sum256(body)
	return &datav1.PendingOutboxRecord{
		EventId:              rewardMailEventID(playerID, requestID),
		EventType:            datav1.OutboxEventType_CREATE_REWARD_MAIL,
		EventContractVersion: 1, AggregatePlayerId: playerID,
		CausedByRequestId: append([]byte(nil), requestID...),
		CreatedOwnerEpoch: ownerEpoch, CreatedPlayerSeq: playerSeq,
		CreatedAtMs: now.UnixMilli(), Payload: body, PayloadSha256: digest[:],
	}, nil
}

func rewardMailEventID(playerID uint64, requestID []byte) []byte {
	body := make([]byte, 0, len(requestID)+32)
	body = append(body, "chapter-reward-mail:"...)
	body = binary.BigEndian.AppendUint64(body, playerID)
	body = append(body, requestID...)
	sum := sha256.Sum256(body)
	id := append([]byte(nil), sum[:16]...)
	id[6] = (id[6] & 0x0f) | 0x50
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}

func (r *Runtime) storeClaimRewardFailure(
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
		ResultPlayerSeq:     a.state.PlayerSeq,
		ResponsePayloadType: uint32(wsv1.Action_CLAIM_CHAPTER_REWARD),
		ErrorPayload:        body,
	}, now)
	return errorEnvelope(request, a.state, now, failure)
}

func replayClaimReward(
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
		payload := &wsv1.ClaimChapterRewardResponse{}
		if proto.Unmarshal(stored.ResponsePayload, payload) == nil {
			response.Payload = &wsv1.WsEnvelope_ClaimChapterRewardResponse{
				ClaimChapterRewardResponse: payload,
			}
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

func claimRewardFingerprint(
	callerPlayerID uint64,
	envelope *wsv1.WsEnvelope,
	request *wsv1.ClaimChapterRewardRequest,
) [sha256.Size]byte {
	body := make([]byte, 0, 32)
	body = binary.BigEndian.AppendUint32(body, idempotencyFingerprintSchemaVersion)
	body = binary.BigEndian.AppendUint32(body, envelope.ProtocolVersion)
	body = binary.BigEndian.AppendUint32(body, uint32(envelope.Action))
	body = binary.BigEndian.AppendUint64(body, callerPlayerID)
	body = binary.BigEndian.AppendUint64(body, envelope.TargetPlayerId)
	body = binary.BigEndian.AppendUint32(body, request.ChapterId)
	return sha256.Sum256(body)
}
