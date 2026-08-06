package player

import (
	"context"
	"testing"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
	"google.golang.org/protobuf/proto"
)

func catchPestRequest(playerID uint64, requestID string, plotID uint32) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_CATCH_PEST, RequestId: requestID, TargetPlayerId: playerID,
		Payload: &wsv1.WsEnvelope_CatchPestRequest{
			CatchPestRequest: &wsv1.CatchPestRequest{PlotId: plotID},
		},
	}
}

func growingWithPestState(playerID uint64, now time.Time) *State {
	state := NewDevelopmentState(playerID)
	state.PlayerSeq = 7
	state.CheckpointRevision = 8
	state.CreatedAtMS = now.Add(-5 * time.Minute).UnixMilli()
	state.UpdatedAtMS = now.UnixMilli()
	plot := friendGrowingPlotAt(InitialPlotID, now)
	plot.PestEffect = &datav1.TimedEffectRecord{
		EffectInstanceId:   []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		EffectKind:         datav1.EffectKind_PEST,
		EffectItemOrPestId: 1,
		ConfigVersion:      1,
		Modifier:           &datav1.RateDecimal6{ScaledValue: -300_000},
		StartAtMs:          now.UnixMilli(),
		EndAtMs:            now.Add(2 * time.Minute).UnixMilli(),
		SourcePlayerId:     uint64Ptr(99),
	}
	state.Plots[InitialPlotID] = plot
	return state
}

func TestOwnerCatchPestIsIdempotentAndClearsPest(t *testing.T) {
	const playerID = uint64(42)
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: growingWithPestState(playerID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	defer runtime.Close()

	request := catchPestRequest(
		playerID, "00112233-4455-6677-8899-aabbccddee91", InitialPlotID,
	)
	first, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, request)
	if err != nil {
		t.Fatal(err)
	}
	caught := first.GetCatchPestResponse()
	if first.GetError() != nil || caught == nil ||
		first.GetStateVersion().GetPlayerSeq() != 8 {
		t.Fatalf("unexpected CATCH_PEST response: %+v", first)
	}
	plot := caught.GetPatch().GetPlotUpserts()[0]
	if plot.GetPlotState() != plotv1.PlotState_GROWING || plot.GetPestEffect() != nil {
		t.Fatalf("unexpected CATCH_PEST plot: %+v", plot)
	}

	replay, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, request)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.GetReplayed() || replay.GetStateVersion().GetPlayerSeq() != 8 ||
		!proto.Equal(replay.GetCatchPestResponse(), caught) {
		t.Fatalf("unexpected CATCH_PEST replay: %+v", replay)
	}

	if err := runtime.flushDirty(context.Background()); err != nil {
		t.Fatal(err)
	}
	checkpoint := store.saved[0]
	savedPlot := checkpoint.Plots[0]
	if savedPlot.PestEffect != nil || savedPlot.State != datav1.PlotRecordState_GROWING {
		t.Fatalf("unexpected CATCH_PEST checkpoint plot: %+v", savedPlot)
	}
}

func TestOwnerCatchPestRejectsPlotsWithoutPest(t *testing.T) {
	const playerID = uint64(42)
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	state := growingWithPestState(playerID, now)
	state.Plots[InitialPlotID].PestEffect = nil
	store := &recordingCheckpointStore{state: state}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	defer runtime.Close()

	response, err := runtime.Handle(
		context.Background(), playerID, LocalOwnerEpoch,
		catchPestRequest(playerID, "00112233-4455-6677-8899-aabbccddee92", InitialPlotID),
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.GetError().GetCode() != wsv1.ErrorCode_PLOT_STATE_CONFLICT {
		t.Fatalf("expected PLOT_STATE_CONFLICT, got %+v", response.GetError())
	}
}
