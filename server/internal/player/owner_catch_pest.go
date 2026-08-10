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

// catchPest is the free owner-side CATCH_PEST command: remove an active pest
// from a GROWING plot and recompute the remaining maturity estimate. It does
// not spend friend action chances; visitors continue to use CATCH_PEST_FOR_FRIEND.
func (r *Runtime) catchPest(
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
	catch := request.GetCatchPestRequest()
	fingerprint := catchPestFingerprint(callerPlayerID, request, catch)
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
		return replayCatchPest(request, stored, now), false, DomainChanges{}
	}

	if catch.PlotId == 0 {
		return r.storeCatchPestFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT}), true, DomainChanges{}
	}
	plot, exists := a.state.Plots[catch.PlotId]
	if !exists {
		return r.storeCatchPestFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_PLOT_NOT_FOUND}), true, DomainChanges{}
	}
	if plot.State != plotv1.PlotState_GROWING || plot.PestEffect == nil {
		return r.storeCatchPestFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_PLOT_STATE_CONFLICT, CurrentPlot: plot.View()}), true, DomainChanges{}
	}

	before := clonePlot(plot)
	matured, settleErr := settleGrowingPlot(plot, now.UnixMilli())
	if settleErr != nil || matured || plot.PestEffect == nil {
		*plot = *before
		return r.storeCatchPestFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_PLOT_STATE_CONFLICT, CurrentPlot: plot.View()}), true, DomainChanges{}
	}
	plot.PestEffect = nil
	estimate, estimateErr := estimatePlotMatureAtMS(plot, now.UnixMilli())
	if estimateErr != nil {
		*plot = *before
		return r.storeCatchPestFailure(a, request, requestID, fingerprint, config.Version(), now,
			&wsv1.Error{Code: wsv1.ErrorCode_CONFIG_UNAVAILABLE, Retryable: true}), true, DomainChanges{}
	}
	plot.EstimatedMatureAtMS = &estimate
	a.state.PlayerSeq++
	a.state.CheckpointRevision++
	a.state.ConfigVersion = config.Version()
	a.state.UpdatedAtMS = now.UnixMilli()
	payload := &wsv1.CatchPestResponse{
		Patch: &wsv1.PlayerStatePatch{
			PlotUpserts: []*wsv1.PlotView{plot.View()},
		},
	}
	body, _ := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	a.state.appendResult(&datav1.IdempotencyResultRecord{
		CallerPlayerId: callerPlayerID, RequestId: requestID,
		FingerprintSchemaVersion: idempotencyFingerprintSchemaVersion,
		ProtocolVersion:          request.ProtocolVersion, Action: uint32(request.Action),
		TargetPlayerId: request.TargetPlayerId, PayloadFingerprintSha256: fingerprint[:],
		CompletedAtMs: now.UnixMilli(), Success: true, ResultOwnerEpoch: a.state.OwnerEpoch,
		ResultPlayerSeq:     a.state.PlayerSeq,
		ResponsePayloadType: uint32(wsv1.Action_CATCH_PEST),
		ResponsePayload:     body,
	}, now)
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_RESPONSE,
		Action: request.Action, RequestId: request.RequestId, TargetPlayerId: request.TargetPlayerId,
		StateVersion: &wsv1.StateVersion{OwnerEpoch: a.state.OwnerEpoch, PlayerSeq: a.state.PlayerSeq},
		ServerTimeMs: now.UnixMilli(),
		Payload: &wsv1.WsEnvelope_CatchPestResponse{
			CatchPestResponse: payload,
		},
	}, true, DomainChanges{}.PlotChanged(catch.PlotId)
}

func (r *Runtime) storeCatchPestFailure(
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
		ResponsePayloadType: uint32(wsv1.Action_CATCH_PEST),
		ErrorPayload:        body,
	}, now)
	return errorEnvelope(request, a.state, now, failure)
}

func replayCatchPest(
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
		payload := &wsv1.CatchPestResponse{}
		if proto.Unmarshal(stored.ResponsePayload, payload) == nil {
			response.Payload = &wsv1.WsEnvelope_CatchPestResponse{CatchPestResponse: payload}
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

func catchPestFingerprint(
	callerPlayerID uint64,
	envelope *wsv1.WsEnvelope,
	request *wsv1.CatchPestRequest,
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
