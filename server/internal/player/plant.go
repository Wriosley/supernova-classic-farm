package player

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
	"google.golang.org/protobuf/proto"
)

func (r *Runtime) plant(
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
	plant := request.GetPlantRequest()
	fingerprint := plantFingerprint(callerPlayerID, request, plant)
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
		return replayPlant(request, stored, now), false
	}

	if plant.PlotId == 0 || plant.SeedItemId == 0 {
		return r.storePlantFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}), true
	}
	plot, exists := a.state.Plots[plant.PlotId]
	if !exists {
		return r.storePlantFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_PLOT_NOT_FOUND}), true
	}
	if plot.State != plotv1.PlotState_EMPTY {
		return r.storePlantFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_PLOT_STATE_CONFLICT, CurrentPlot: plot.View()}), true
	}
	crop, exists := config.CropForSeed(plant.SeedItemId)
	if !exists {
		return r.storePlantFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_CONFIG_UNAVAILABLE, Retryable: true}), true
	}
	if !crop.Enabled {
		return r.storePlantFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_CONFIG_ENTRY_DISABLED}), true
	}
	seedQuantity := a.state.Inventory[plant.SeedItemId]
	if seedQuantity == 0 {
		return r.storePlantFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_ITEM_NOT_OWNED}), true
	}
	estimatedMatureAtMS, err := estimateMatureAtMS(
		now.UnixMilli(), crop.MaturityValueScaled9, crop.BaseGrowthRateScaled6,
	)
	if err != nil {
		return r.storePlantFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_CONFIG_UNAVAILABLE, Retryable: true}), true
	}

	if seedQuantity == 1 {
		delete(a.state.Inventory, plant.SeedItemId)
	} else {
		a.state.Inventory[plant.SeedItemId] = seedQuantity - 1
	}
	*plot = Plot{
		ID: plot.ID, State: plotv1.PlotState_GROWING,
		CropID: crop.CropID, CropItemID: crop.CropItemID,
		CropConfigVersion: crop.ConfigVersion, PlantedAtMS: now.UnixMilli(),
		MaturityValueScaled9:  crop.MaturityValueScaled9,
		BaseGrowthRateScaled6: crop.BaseGrowthRateScaled6,
		BaseYield:             crop.BaseYield, SettledGrowthValueScaled9: 0,
		LastSettledAtMS: now.UnixMilli(), EstimatedMatureAtMS: &estimatedMatureAtMS,
	}
	for index := range a.state.Tasks {
		if a.state.Tasks[index].ID == 2 && a.state.Tasks[index].Current < a.state.Tasks[index].Target {
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
	if seedQuantity == 1 {
		patch.InventoryRemovedItemIds = []uint32{plant.SeedItemId}
	} else {
		patch.InventoryUpserts = []*wsv1.ItemStackView{{
			ItemId: plant.SeedItemId, Quantity: seedQuantity - 1,
		}}
	}
	payload := &wsv1.PlantResponse{
		ConsumedSeedItemId: plant.SeedItemId,
		Patch:              patch,
	}
	body, _ := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	a.state.appendResult(&datav1.IdempotencyResultRecord{
		CallerPlayerId: callerPlayerID, RequestId: requestID,
		FingerprintSchemaVersion: idempotencyFingerprintSchemaVersion,
		ProtocolVersion:          request.ProtocolVersion, Action: uint32(request.Action),
		TargetPlayerId: request.TargetPlayerId, PayloadFingerprintSha256: fingerprint[:],
		CompletedAtMs: now.UnixMilli(), Success: true, ResultOwnerEpoch: LocalOwnerEpoch,
		ResultPlayerSeq: a.state.PlayerSeq, ResponsePayloadType: uint32(wsv1.Action_PLANT),
		ResponsePayload: body,
	}, now)
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId, TargetPlayerId: request.TargetPlayerId,
		StateVersion: &wsv1.StateVersion{OwnerEpoch: LocalOwnerEpoch, PlayerSeq: a.state.PlayerSeq},
		ServerTimeMs: now.UnixMilli(),
		Payload:      &wsv1.WsEnvelope_PlantResponse{PlantResponse: payload},
	}, true
}

func (r *Runtime) storePlantFailure(
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
		ResultPlayerSeq: a.state.PlayerSeq, ResponsePayloadType: uint32(wsv1.Action_PLANT),
		ErrorPayload: body,
	}, now)
	return errorEnvelope(request, a.state, now, failure)
}

func replayPlant(request *wsv1.WsEnvelope, stored *datav1.IdempotencyResultRecord, now time.Time) *wsv1.WsEnvelope {
	response := &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId, TargetPlayerId: request.TargetPlayerId,
		StateVersion: &wsv1.StateVersion{OwnerEpoch: stored.ResultOwnerEpoch, PlayerSeq: stored.ResultPlayerSeq},
		ServerTimeMs: now.UnixMilli(), Replayed: true,
	}
	if stored.Success {
		payload := &wsv1.PlantResponse{}
		if proto.Unmarshal(stored.ResponsePayload, payload) == nil {
			response.Payload = &wsv1.WsEnvelope_PlantResponse{PlantResponse: payload}
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

func plantFingerprint(callerPlayerID uint64, envelope *wsv1.WsEnvelope, request *wsv1.PlantRequest) [sha256.Size]byte {
	body := make([]byte, 0, 36)
	body = binary.BigEndian.AppendUint32(body, idempotencyFingerprintSchemaVersion)
	body = binary.BigEndian.AppendUint32(body, envelope.ProtocolVersion)
	body = binary.BigEndian.AppendUint32(body, uint32(envelope.Action))
	body = binary.BigEndian.AppendUint64(body, callerPlayerID)
	body = binary.BigEndian.AppendUint64(body, envelope.TargetPlayerId)
	body = binary.BigEndian.AppendUint32(body, request.PlotId)
	body = binary.BigEndian.AppendUint32(body, request.SeedItemId)
	return sha256.Sum256(body)
}
