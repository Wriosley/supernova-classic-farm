package player

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	reasonv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/reason"
	"google.golang.org/protobuf/proto"
)

type MaturityEvent struct {
	PlayerID     uint64
	PlayerSeq    uint64
	ServerTimeMS int64
	Plot         *wsv1.PlotView
}

func (e MaturityEvent) Envelope() *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion,
		MessageKind:     wsv1.MessageKind_PUSH,
		Action:          wsv1.Action_PLAYER_STATE_CHANGED,
		TargetPlayerId:  e.PlayerID,
		StateVersion: &wsv1.StateVersion{
			OwnerEpoch: LocalOwnerEpoch,
			PlayerSeq:  e.PlayerSeq,
		},
		ServerTimeMs: e.ServerTimeMS,
		Payload: &wsv1.WsEnvelope_PlayerStateChangedPush{
			PlayerStateChangedPush: &wsv1.PlayerStateChangedPush{
				Reason: reasonv1.StateChangeReason_MATURED,
				Patch: &wsv1.PlayerStatePatch{
					PlotUpserts: []*wsv1.PlotView{e.Plot},
				},
			},
		},
	}
}

type PushForwarder interface {
	Forward(context.Context, *wsv1.WsEnvelope) error
}

type HTTPPushForwarder struct {
	client   *http.Client
	endpoint string
}

func NewHTTPPushForwarder(client *http.Client, endpoint string) (*HTTPPushForwarder, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() == "" {
		return nil, errors.New("push endpoint must be an HTTP URL")
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, errors.New("push endpoint must use a loopback host")
	}
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	return &HTTPPushForwarder{
		client: client, endpoint: strings.TrimRight(endpoint, "/"),
	}, nil
}

func (f *HTTPPushForwarder) Forward(ctx context.Context, envelope *wsv1.WsEnvelope) error {
	if f == nil || f.client == nil || f.endpoint == "" {
		return errors.New("push forwarder is not configured")
	}
	body, err := proto.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal player push: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, f.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-protobuf")
	response, err := f.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("Gate push endpoint returned %s", response.Status)
	}
	return nil
}
