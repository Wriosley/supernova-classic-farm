package smoke_test

import (
	"testing"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"google.golang.org/protobuf/proto"
)

func TestGeneratedMessagesRoundTrip(t *testing.T) {
	tests := []proto.Message{
		&wsv1.WsEnvelope{
			ProtocolVersion: 1,
			MessageKind:     wsv1.MessageKind_REQUEST,
			Action:          wsv1.Action_GET_PLAYER_SNAPSHOT,
			RequestId:       "00000000-0000-4000-8000-000000000001",
			TargetPlayerId:  9007199254740993,
			Payload: &wsv1.WsEnvelope_GetPlayerSnapshotRequest{
				GetPlayerSnapshotRequest: &wsv1.GetPlayerSnapshotRequest{},
			},
		},
		&datav1.PlayerCheckpointV1{
			SchemaVersion:      1,
			PlayerId:           9007199254740993,
			LogicalShardId:     42,
			OwnerEpoch:         7,
			PlayerSeq:          11,
			CheckpointRevision: 13,
			CoinBalance:        100,
			CurrentChapter: &datav1.ChapterStateRecord{
				ChapterId:           1,
				ChapterConfigVersion: 2,
				Status:              datav1.ChapterRecordStatus_IN_PROGRESS,
				ActivatedAtMs:       1,
			},
			LastAppliedConfigVersion: 2,
			CreatedAtMs:              1,
			UpdatedAtMs:              2,
		},
	}

	for _, original := range tests {
		wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(original)
		if err != nil {
			t.Fatalf("marshal %T: %v", original, err)
		}
		decoded := original.ProtoReflect().Type().New().Interface()
		if err := proto.Unmarshal(wire, decoded); err != nil {
			t.Fatalf("unmarshal %T: %v", original, err)
		}
		if !proto.Equal(original, decoded) {
			t.Fatalf("round trip changed %T", original)
		}
	}
}
