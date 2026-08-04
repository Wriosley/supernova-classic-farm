package player

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
	"google.golang.org/protobuf/proto"
)

func TestHTTPPushForwarderSendsProtobufEnvelope(t *testing.T) {
	received := make(chan *wsv1.WsEnvelope, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		envelope := &wsv1.WsEnvelope{}
		if err := proto.Unmarshal(readAll(t, r), envelope); err != nil {
			t.Fatal(err)
		}
		received <- envelope
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	forwarder, err := NewHTTPPushForwarder(server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	envelope := (MaturityEvent{
		PlayerID: 7, PlayerSeq: 3, ServerTimeMS: time.Now().UnixMilli(),
		Plot: &wsv1.PlotView{PlotId: 1, PlotState: plotv1.PlotState_MATURE},
	}).Envelope()
	if err := forwarder.Forward(context.Background(), envelope); err != nil {
		t.Fatal(err)
	}
	if push := <-received; push.GetTargetPlayerId() != 7 ||
		push.GetStateVersion().GetPlayerSeq() != 3 ||
		push.GetPlayerStateChangedPush().GetPatch().GetPlotUpserts()[0].GetPlotState() != plotv1.PlotState_MATURE {
		t.Fatalf("received push = %+v", push)
	}
}

func readAll(t *testing.T, r *http.Request) []byte {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
