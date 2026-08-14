package main

import (
	"bytes"
	"context"
	"testing"

	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type recordingGiftNotifier struct{ eventIDs [][]byte }

func (n *recordingGiftNotifier) Notify(eventID []byte) {
	n.eventIDs = append(n.eventIDs, append([]byte(nil), eventID...))
}

func TestNotifyGiftOutboxFromSuccessfulResponse(t *testing.T) {
	eventID := bytes.Repeat([]byte{7}, 16)
	notifier := &recordingGiftNotifier{}
	notifyGiftOutbox(notifier, &wsv1.WsEnvelope{
		Action: wsv1.Action_SEND_FRIEND_GIFT,
		Payload: &wsv1.WsEnvelope_SendFriendGiftResponse{
			SendFriendGiftResponse: &wsv1.SendFriendGiftResponse{OutboxEventId: eventID},
		},
	})
	if len(notifier.eventIDs) != 1 || !bytes.Equal(notifier.eventIDs[0], eventID) {
		t.Fatalf("notifications=%x", notifier.eventIDs)
	}
}

func TestNotifyGiftOutboxIgnoresFailedOrEmptyResponse(t *testing.T) {
	notifier := &recordingGiftNotifier{}
	notifyGiftOutbox(notifier, &wsv1.WsEnvelope{
		Action: wsv1.Action_SEND_FRIEND_GIFT,
		Error:  &wsv1.Error{Code: wsv1.ErrorCode_INSUFFICIENT_ITEM_QUANTITY},
		Payload: &wsv1.WsEnvelope_SendFriendGiftResponse{
			SendFriendGiftResponse: &wsv1.SendFriendGiftResponse{OutboxEventId: bytes.Repeat([]byte{8}, 16)},
		},
	})
	notifyGiftOutbox(notifier, &wsv1.WsEnvelope{Action: wsv1.Action_SEND_FRIEND_GIFT})
	notifyGiftOutbox(nil, &wsv1.WsEnvelope{Action: wsv1.Action_SEND_FRIEND_GIFT})
	if len(notifier.eventIDs) != 0 {
		t.Fatalf("notifications=%x", notifier.eventIDs)
	}
}

func TestGameCommandRPCServerReturnsSnapshot(t *testing.T) {
	runtime := player.NewRuntime()
	defer runtime.Close()
	server := newGameCommandRPCServer(
		runtime, localAuthorization{}, nil, nil, "local-gateway", nil,
	)
	response, err := server.ExecutePlayerCommand(
		context.Background(), rpcCommandRequest(42, 1, "local-gateway"),
	)
	if err != nil {
		t.Fatal(err)
	}
	envelope := response.GetEnvelope()
	if envelope.GetRequestId() != "grpc-request" ||
		envelope.GetGetPlayerSnapshotResponse().GetSnapshot().GetCoinBalance() !=
			player.InitialCoinBalance {
		t.Fatalf("response = %+v", envelope)
	}
}

func TestGameCommandRPCServerRejectsStaleOwner(t *testing.T) {
	runtime := player.NewRuntime()
	defer runtime.Close()
	server := newGameCommandRPCServer(
		runtime, localAuthorization{}, nil, nil, "local-gateway", nil,
	)
	_, err := server.ExecutePlayerCommand(
		context.Background(), rpcCommandRequest(42, 2, "local-gateway"),
	)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("status = %v, want FailedPrecondition", status.Code(err))
	}
}

func TestGameCommandRPCServerRejectsWrongGateway(t *testing.T) {
	runtime := player.NewRuntime()
	defer runtime.Close()
	server := newGameCommandRPCServer(
		runtime, localAuthorization{}, nil, nil, "local-gateway", nil,
	)
	_, err := server.ExecutePlayerCommand(
		context.Background(), rpcCommandRequest(42, 1, "forged-gateway"),
	)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status = %v, want InvalidArgument", status.Code(err))
	}
}

func rpcCommandRequest(
	playerID uint64,
	epoch uint64,
	gatewayID string,
) *rpcv1.ExecutePlayerCommandRequest {
	return &rpcv1.ExecutePlayerCommandRequest{
		CallerPlayerId: playerID,
		GateId:         gatewayID,
		Route: &rpcv1.CommittedRoute{
			LogicalShardId: routing.ShardForPlayer(playerID),
			OwnerZoneId:    routing.DefaultZoneID,
			OwnerEpoch:     epoch,
			RouteVersion:   1,
		},
		Envelope: &wsv1.WsEnvelope{
			ProtocolVersion: player.ProtocolVersion,
			MessageKind:     wsv1.MessageKind_REQUEST,
			Action:          wsv1.Action_GET_PLAYER_SNAPSHOT,
			RequestId:       "grpc-request",
			TargetPlayerId:  playerID,
			Payload: &wsv1.WsEnvelope_GetPlayerSnapshotRequest{
				GetPlayerSnapshotRequest: &wsv1.GetPlayerSnapshotRequest{},
			},
		},
	}
}
