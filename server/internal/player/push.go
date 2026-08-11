package player

import (
	"context"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	reasonv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/reason"
)

type MaturityEvent struct {
	PlayerID     uint64
	OwnerEpoch   uint64
	PlayerSeq    uint64
	ServerTimeMS int64
	Plot         *wsv1.PlotView
	Stealable    bool
}

func (e MaturityEvent) Envelope() *wsv1.WsEnvelope {
	ownerEpoch := e.OwnerEpoch
	if ownerEpoch == 0 {
		ownerEpoch = LocalOwnerEpoch
	}
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion,
		MessageKind:     wsv1.MessageKind_PUSH,
		Action:          wsv1.Action_PLAYER_STATE_CHANGED,
		TargetPlayerId:  e.PlayerID,
		StateVersion: &wsv1.StateVersion{
			OwnerEpoch: ownerEpoch,
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
