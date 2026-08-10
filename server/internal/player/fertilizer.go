package player

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
	"google.golang.org/protobuf/proto"
)

func (r *Runtime) applyFertilizer(
	a *runtimeActor,
	callerPlayerID uint64,
	request *wsv1.WsEnvelope,
	config *ConfigSnapshot,
	now time.Time,
) (*wsv1.WsEnvelope, bool, DomainChanges) {
	requestID, err := parseRequestID(request.RequestId)
	if err != nil {
		return errorEnvelope(request, a.state, now, &wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}), false, DomainChanges{}
	}
	apply := request.GetApplyFertilizerRequest()
	fingerprint := fertilizerFingerprint(callerPlayerID, request, apply)
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
				&wsv1.Error{Code: wsv1.ErrorCode_REQUEST_ID_CONFLICT}), false, DomainChanges{}
		}
		return replayFertilizer(request, stored, now), false, DomainChanges{}
	}

	if apply.PlotId == 0 || apply.FertilizerItemId == 0 {
		return r.storeFertilizerFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}), true, DomainChanges{}
	}
	plot, exists := a.state.Plots[apply.PlotId]
	if !exists {
		return r.storeFertilizerFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_PLOT_NOT_FOUND}), true, DomainChanges{}
	}
	if plot.State != plotv1.PlotState_GROWING {
		return r.storeFertilizerFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_PLOT_STATE_CONFLICT, CurrentPlot: plot.View()}), true, DomainChanges{}
	}
	if plot.FertilizerEffect != nil && now.UnixMilli() < plot.FertilizerEffect.EndAtMs {
		return r.storeFertilizerFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_FERTILIZER_ALREADY_ACTIVE, CurrentPlot: plot.View()}), true, DomainChanges{}
	}
	fertilizer, exists := config.Fertilizer(apply.FertilizerItemId)
	if !exists {
		return r.storeFertilizerFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_CONFIG_UNAVAILABLE, Retryable: true}), true, DomainChanges{}
	}
	if !fertilizer.Enabled {
		return r.storeFertilizerFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_CONFIG_ENTRY_DISABLED}), true, DomainChanges{}
	}
	itemQuantity := a.state.Inventory[apply.FertilizerItemId]
	if itemQuantity == 0 {
		return r.storeFertilizerFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_ITEM_NOT_OWNED}), true, DomainChanges{}
	}
	if now.UnixMilli() > math.MaxInt64-fertilizer.DurationMS {
		return r.storeFertilizerFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_CONFIG_UNAVAILABLE, Retryable: true}), true, DomainChanges{}
	}

	before := clonePlot(plot)
	matured, err := settleGrowingPlot(plot, now.UnixMilli())
	if err != nil || matured {
		*plot = *before
		return r.storeFertilizerFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_PLOT_STATE_CONFLICT, CurrentPlot: plot.View()}), true, DomainChanges{}
	}
	effectID := fertilizerEffectID(callerPlayerID, requestID)
	plot.FertilizerEffect = &datav1.TimedEffectRecord{
		EffectInstanceId: effectID, EffectKind: datav1.EffectKind_FERTILIZER,
		EffectItemOrPestId: apply.FertilizerItemId, ConfigVersion: fertilizer.ConfigVersion,
		Modifier:  &datav1.RateDecimal6{ScaledValue: fertilizer.ModifierScaled6},
		StartAtMs: now.UnixMilli(), EndAtMs: now.UnixMilli() + fertilizer.DurationMS,
	}
	estimate, err := estimatePlotMatureAtMS(plot, now.UnixMilli())
	if err != nil {
		*plot = *before
		return r.storeFertilizerFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_CONFIG_UNAVAILABLE, Retryable: true}), true, DomainChanges{}
	}
	plot.EstimatedMatureAtMS = &estimate
	if itemQuantity == 1 {
		delete(a.state.Inventory, apply.FertilizerItemId)
	} else {
		a.state.Inventory[apply.FertilizerItemId] = itemQuantity - 1
	}
	for index := range a.state.Tasks {
		if a.state.Tasks[index].ID == 3 && a.state.Tasks[index].Current < a.state.Tasks[index].Target {
			a.state.Tasks[index].Current++
			break
		}
	}
	a.state.PlayerSeq++
	a.state.CheckpointRevision++
	a.state.ConfigVersion = config.Version()
	a.state.UpdatedAtMS = now.UnixMilli()
	patch := &wsv1.PlayerStatePatch{
		PlotUpserts:    []*wsv1.PlotView{plot.View()},
		CurrentChapter: a.state.Snapshot().CurrentChapter,
	}
	if itemQuantity == 1 {
		patch.InventoryRemovedItemIds = []uint32{apply.FertilizerItemId}
	} else {
		patch.InventoryUpserts = []*wsv1.ItemStackView{{
			ItemId: apply.FertilizerItemId, Quantity: itemQuantity - 1,
		}}
	}
	payload := &wsv1.ApplyFertilizerResponse{
		ConsumedFertilizerItemId: apply.FertilizerItemId,
		EffectInstanceId:         formatUUIDBytes(effectID),
		Patch:                    patch,
	}
	body, _ := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	a.state.appendResult(&datav1.IdempotencyResultRecord{
		CallerPlayerId: callerPlayerID, RequestId: requestID,
		FingerprintSchemaVersion: idempotencyFingerprintSchemaVersion,
		ProtocolVersion:          request.ProtocolVersion, Action: uint32(request.Action),
		TargetPlayerId: request.TargetPlayerId, PayloadFingerprintSha256: fingerprint[:],
		CompletedAtMs: now.UnixMilli(), Success: true, ResultOwnerEpoch: a.state.OwnerEpoch,
		ResultPlayerSeq: a.state.PlayerSeq, ResponsePayloadType: uint32(wsv1.Action_APPLY_FERTILIZER),
		ResponsePayload: body,
	}, now)
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId, TargetPlayerId: request.TargetPlayerId,
		StateVersion: &wsv1.StateVersion{OwnerEpoch: a.state.OwnerEpoch, PlayerSeq: a.state.PlayerSeq},
		ServerTimeMs: now.UnixMilli(),
		Payload: &wsv1.WsEnvelope_ApplyFertilizerResponse{
			ApplyFertilizerResponse: payload,
		},
	}, true, DomainChanges{}.PlotChanged(apply.PlotId)
}

func (r *Runtime) storeFertilizerFailure(
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
		ResultPlayerSeq: a.state.PlayerSeq, ResponsePayloadType: uint32(wsv1.Action_APPLY_FERTILIZER),
		ErrorPayload: body,
	}, now)
	return errorEnvelope(request, a.state, now, failure)
}

func replayFertilizer(request *wsv1.WsEnvelope, stored *datav1.IdempotencyResultRecord, now time.Time) *wsv1.WsEnvelope {
	response := &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId, TargetPlayerId: request.TargetPlayerId,
		StateVersion: &wsv1.StateVersion{OwnerEpoch: stored.ResultOwnerEpoch, PlayerSeq: stored.ResultPlayerSeq},
		ServerTimeMs: now.UnixMilli(), Replayed: true,
	}
	if stored.Success {
		payload := &wsv1.ApplyFertilizerResponse{}
		if proto.Unmarshal(stored.ResponsePayload, payload) == nil {
			response.Payload = &wsv1.WsEnvelope_ApplyFertilizerResponse{ApplyFertilizerResponse: payload}
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

func fertilizerFingerprint(
	callerPlayerID uint64,
	envelope *wsv1.WsEnvelope,
	request *wsv1.ApplyFertilizerRequest,
) [sha256.Size]byte {
	body := make([]byte, 0, 36)
	body = binary.BigEndian.AppendUint32(body, idempotencyFingerprintSchemaVersion)
	body = binary.BigEndian.AppendUint32(body, envelope.ProtocolVersion)
	body = binary.BigEndian.AppendUint32(body, uint32(envelope.Action))
	body = binary.BigEndian.AppendUint64(body, callerPlayerID)
	body = binary.BigEndian.AppendUint64(body, envelope.TargetPlayerId)
	body = binary.BigEndian.AppendUint32(body, request.PlotId)
	body = binary.BigEndian.AppendUint32(body, request.FertilizerItemId)
	return sha256.Sum256(body)
}

func fertilizerEffectID(playerID uint64, requestID []byte) []byte {
	body := make([]byte, 0, len(requestID)+8+18)
	body = append(body, "fertilizer-effect:"...)
	body = binary.BigEndian.AppendUint64(body, playerID)
	body = append(body, requestID...)
	sum := sha256.Sum256(body)
	id := append([]byte(nil), sum[:16]...)
	id[6] = (id[6] & 0x0f) | 0x50
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}

func formatUUIDBytes(value []byte) string {
	if len(value) != 16 {
		return ""
	}
	encoded := make([]byte, 32)
	hex.Encode(encoded, value)
	return string(encoded[0:8]) + "-" + string(encoded[8:12]) + "-" +
		string(encoded[12:16]) + "-" + string(encoded[16:20]) + "-" +
		string(encoded[20:32])
}

func clonePlot(plot *Plot) *Plot {
	if plot == nil {
		return nil
	}
	cloned := *plot
	if plot.EstimatedMatureAtMS != nil {
		estimate := *plot.EstimatedMatureAtMS
		cloned.EstimatedMatureAtMS = &estimate
	}
	if plot.FertilizerEffect != nil {
		cloned.FertilizerEffect = proto.Clone(plot.FertilizerEffect).(*datav1.TimedEffectRecord)
	}
	if plot.PestEffect != nil {
		cloned.PestEffect = proto.Clone(plot.PestEffect).(*datav1.TimedEffectRecord)
	}
	return &cloned
}
