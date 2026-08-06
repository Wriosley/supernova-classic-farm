package player

import (
	"testing"
	"time"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
)

func TestMaturityEventBuildsPlayerStateChangedEnvelope(t *testing.T) {
	envelope := (MaturityEvent{
		PlayerID: 7, PlayerSeq: 3, ServerTimeMS: time.Now().UnixMilli(),
		Plot: &wsv1.PlotView{PlotId: 1, PlotState: plotv1.PlotState_MATURE},
	}).Envelope()
	if envelope.GetTargetPlayerId() != 7 ||
		envelope.GetStateVersion().GetPlayerSeq() != 3 ||
		envelope.GetPlayerStateChangedPush().GetPatch().GetPlotUpserts()[0].GetPlotState() != plotv1.PlotState_MATURE {
		t.Fatalf("push = %+v", envelope)
	}
}
