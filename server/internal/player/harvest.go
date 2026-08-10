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

const (
	inventoryTypeLimit  = 100
	inventoryStackLimit = 300
)

func (r *Runtime) harvest(
	a *runtimeActor,
	callerPlayerID uint64,
	request *wsv1.WsEnvelope,
	config *ConfigSnapshot,
	now time.Time,
) (*wsv1.WsEnvelope, bool, DomainChanges) {
	requestID, err := parseRequestID(request.RequestId)
	if err != nil {
		return errorEnvelope(request, a.state, now,
			&wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}), false, DomainChanges{}
	}
	harvest := request.GetHarvestRequest()
	fingerprint := harvestFingerprint(callerPlayerID, request, harvest)
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
		return replayHarvest(request, stored, now), false, DomainChanges{}
	}

	if harvest.PlotId == 0 {
		return r.storeHarvestFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}), true, DomainChanges{}
	}
	plot, exists := a.state.Plots[harvest.PlotId]
	if !exists {
		return r.storeHarvestFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_PLOT_NOT_FOUND}), true, DomainChanges{}
	}
	if plot.State == plotv1.PlotState_GROWING {
		return r.storeHarvestFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_CROP_NOT_MATURE, CurrentPlot: plot.View()}), true, DomainChanges{}
	}
	if plot.State != plotv1.PlotState_MATURE {
		return r.storeHarvestFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_PLOT_STATE_CONFLICT, CurrentPlot: plot.View()}), true, DomainChanges{}
	}

	harvestedQuantity := plot.BaseYield - plot.StolenQuantity
	currentQuantity := a.state.Inventory[plot.CropItemID]
	if harvestedQuantity > 0 && currentQuantity == 0 && len(a.state.Inventory) >= inventoryTypeLimit {
		return r.storeHarvestFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_INVENTORY_TYPE_LIMIT}), true, DomainChanges{}
	}
	if uint64(currentQuantity)+uint64(harvestedQuantity) > inventoryStackLimit {
		return r.storeHarvestFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_INVENTORY_STACK_LIMIT}), true, DomainChanges{}
	}

	if harvestedQuantity > 0 {
		career := ensureCareer(a.state)
		nextHarvested, err := checkedAddUint64(career.TotalHarvestedCropQuantity, uint64(harvestedQuantity))
		if err != nil {
			return r.storeHarvestFailure(a, request, requestID, fingerprint, config.Version(), now,
				&wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}), true, DomainChanges{}
		}
		a.state.Inventory[plot.CropItemID] = currentQuantity + harvestedQuantity
		career.TotalHarvestedCropQuantity = nextHarvested
		unlockCrop(a.state, plot.CropID)
	}
	plot.State = plotv1.PlotState_NEED_CLEANUP
	plot.MaturityValueScaled9 = 0
	plot.BaseGrowthRateScaled6 = 0
	plot.SettledGrowthValueScaled9 = 0
	plot.LastSettledAtMS = 0
	plot.EstimatedMatureAtMS = nil
	plot.FertilizerEffect = nil
	plot.PestEffect = nil
	for index := range a.state.Tasks {
		if a.state.Tasks[index].ID == 4 &&
			a.state.Tasks[index].Current < a.state.Tasks[index].Target {
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
		Career:         careerView(a.state),
		CropCompendium: compendiumView(a.state),
	}
	if harvestedQuantity > 0 {
		patch.InventoryUpserts = []*wsv1.ItemStackView{{
			ItemId: plot.CropItemID, Quantity: currentQuantity + harvestedQuantity,
		}}
	}
	payload := &wsv1.HarvestResponse{
		CropItemId: plot.CropItemID, HarvestedQuantity: harvestedQuantity,
		Patch: patch,
	}
	body, _ := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	a.state.appendResult(&datav1.IdempotencyResultRecord{
		CallerPlayerId: callerPlayerID, RequestId: requestID,
		FingerprintSchemaVersion: idempotencyFingerprintSchemaVersion,
		ProtocolVersion:          request.ProtocolVersion, Action: uint32(request.Action),
		TargetPlayerId: request.TargetPlayerId, PayloadFingerprintSha256: fingerprint[:],
		CompletedAtMs: now.UnixMilli(), Success: true, ResultOwnerEpoch: a.state.OwnerEpoch,
		ResultPlayerSeq: a.state.PlayerSeq, ResponsePayloadType: uint32(wsv1.Action_HARVEST),
		ResponsePayload: body,
	}, now)
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId, TargetPlayerId: request.TargetPlayerId,
		StateVersion: &wsv1.StateVersion{OwnerEpoch: a.state.OwnerEpoch, PlayerSeq: a.state.PlayerSeq},
		ServerTimeMs: now.UnixMilli(),
		Payload:      &wsv1.WsEnvelope_HarvestResponse{HarvestResponse: payload},
	}, true, DomainChanges{}.PlotChanged(harvest.PlotId)
}

func (r *Runtime) storeHarvestFailure(
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
		ResultPlayerSeq: a.state.PlayerSeq, ResponsePayloadType: uint32(wsv1.Action_HARVEST),
		ErrorPayload: body,
	}, now)
	return errorEnvelope(request, a.state, now, failure)
}

func replayHarvest(
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
		payload := &wsv1.HarvestResponse{}
		if proto.Unmarshal(stored.ResponsePayload, payload) == nil {
			response.Payload = &wsv1.WsEnvelope_HarvestResponse{HarvestResponse: payload}
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

func harvestFingerprint(
	callerPlayerID uint64,
	envelope *wsv1.WsEnvelope,
	request *wsv1.HarvestRequest,
) [sha256.Size]byte {
	body := make([]byte, 0, 32)
	body = binary.BigEndian.AppendUint32(body, idempotencyFingerprintSchemaVersion)
	body = binary.BigEndian.AppendUint32(body, envelope.ProtocolVersion)
	body = binary.BigEndian.AppendUint32(body, uint32(envelope.Action))
	body = binary.BigEndian.AppendUint64(body, callerPlayerID)
	body = binary.BigEndian.AppendUint64(body, envelope.TargetPlayerId)
	body = binary.BigEndian.AppendUint32(body, request.PlotId)
	return sha256.Sum256(body)
}
