package player

import (
	"context"
	"testing"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	chapterv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/chapter"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
	"google.golang.org/protobuf/proto"
)

func cleanPlotRequest(playerID uint64, requestID string, plotID uint32) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion, MessageKind: wsv1.MessageKind_REQUEST,
		Action: wsv1.Action_CLEAN_PLOT, RequestId: requestID, TargetPlayerId: playerID,
		Payload: &wsv1.WsEnvelope_CleanPlotRequest{
			CleanPlotRequest: &wsv1.CleanPlotRequest{PlotId: plotID},
		},
	}
}

func needCleanupState(playerID uint64, now time.Time) *State {
	state := NewDevelopmentState(playerID)
	state.PlayerSeq = 7
	state.CheckpointRevision = 8
	state.CreatedAtMS = now.Add(-5 * time.Minute).UnixMilli()
	state.UpdatedAtMS = now.UnixMilli()
	state.Coins = 29
	state.Inventory = map[uint32]uint32{
		developmentSeedItemID:     2,
		BasicFertilizerID:         1,
		developmentNextSeedItemID: 3,
	}
	state.ChapterID = developmentNextChapterID
	state.ChapterConfigVersion = ServerConfigVersion
	state.Chapter = chapterv1.ChapterStatus_IN_PROGRESS
	state.ChapterActivatedAtMS = now.Add(-time.Minute).UnixMilli()
	state.Tasks = nil
	state.Plots[InitialPlotID] = &Plot{
		ID: InitialPlotID, State: plotv1.PlotState_NEED_CLEANUP,
		CropID: 2001, CropItemID: 1002, CropConfigVersion: 1,
		PlantedAtMS: now.Add(-3 * time.Minute).UnixMilli(),
		BaseYield:   3,
	}
	return state
}

func TestCleanPlotIsIdempotentAndPersistsEmptyPlot(t *testing.T) {
	const playerID = uint64(42)
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	store := &recordingCheckpointStore{state: needCleanupState(playerID, now)}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return now }
	defer runtime.Close()

	request := cleanPlotRequest(
		playerID, "00112233-4455-6677-8899-aabbccddee81", InitialPlotID,
	)
	first, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, request)
	if err != nil {
		t.Fatal(err)
	}
	cleaned := first.GetCleanPlotResponse()
	plot := cleaned.GetPatch().GetPlotUpserts()[0]
	if first.GetError() != nil ||
		first.GetStateVersion().GetPlayerSeq() != 8 ||
		plot.GetPlotState() != plotv1.PlotState_EMPTY ||
		plot.GetCropId() != 0 || plot.GetCropConfigVersion() != 0 ||
		cleaned.GetPatch().GetCurrentChapter() != nil {
		t.Fatalf("unexpected CLEAN_PLOT response: %+v", first)
	}
	replay, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, request)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.GetReplayed() || replay.GetStateVersion().GetPlayerSeq() != 8 ||
		!proto.Equal(replay.GetCleanPlotResponse(), cleaned) {
		t.Fatalf("unexpected CLEAN_PLOT replay: %+v", replay)
	}

	if err := runtime.flushDirty(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 1 || store.expectedRevision[0] != 8 {
		t.Fatalf("unexpected checkpoint writes: count=%d expected=%v",
			len(store.saved), store.expectedRevision)
	}
	checkpoint := store.saved[0]
	if checkpoint.PlayerSeq != 8 || checkpoint.CheckpointRevision != 9 ||
		checkpoint.Plots[0].State != datav1.PlotRecordState_EMPTY ||
		checkpoint.Plots[0].CropId != 0 || checkpoint.Plots[0].CropItemId != 0 ||
		checkpoint.Plots[0].PlantedAtMs != 0 {
		t.Fatalf("unexpected CLEAN_PLOT checkpoint: %+v", checkpoint)
	}
}

func TestCleanPlotRejectsMissingAndNonCleanupPlotsAtomically(t *testing.T) {
	tests := []struct {
		name      string
		plotID    uint32
		prepare   func(*State)
		wantError wsv1.ErrorCode
	}{
		{
			name: "missing plot", plotID: 99, prepare: func(*State) {},
			wantError: wsv1.ErrorCode_PLOT_NOT_FOUND,
		},
		{
			name: "empty plot", plotID: InitialPlotID,
			prepare: func(state *State) {
				state.Plots[InitialPlotID] = &Plot{
					ID: InitialPlotID, State: plotv1.PlotState_EMPTY,
				}
			},
			wantError: wsv1.ErrorCode_PLOT_STATE_CONFLICT,
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
			state := needCleanupState(42, now)
			test.prepare(state)
			store := &recordingCheckpointStore{state: state}
			runtime := NewRuntime()
			runtime.store = store
			runtime.now = func() time.Time { return now }
			defer runtime.Close()

			requestIDs := []string{
				"00112233-4455-6677-8899-aabbccddee82",
				"00112233-4455-6677-8899-aabbccddee83",
			}
			response, err := runtime.Handle(
				context.Background(), 42, LocalOwnerEpoch,
				cleanPlotRequest(42, requestIDs[index], test.plotID),
			)
			if err != nil {
				t.Fatal(err)
			}
			actorState := runtime.actors[42].state
			if response.GetError().GetCode() != test.wantError ||
				actorState.PlayerSeq != 7 || actorState.Coins != 29 {
				t.Fatalf("clean failure was not atomic: response=%+v state=%+v",
					response, actorState)
			}
		})
	}
}
