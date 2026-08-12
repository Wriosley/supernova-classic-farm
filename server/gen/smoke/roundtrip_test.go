package smoke_test

import (
	"testing"

	coordinatorv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/coordinator"
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
				ChapterId:            1,
				ChapterConfigVersion: 2,
				Status:               datav1.ChapterRecordStatus_IN_PROGRESS,
				ActivatedAtMs:        1,
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

func TestCoordinatorWatchMessagesRoundTrip(t *testing.T) {
	subscribe := &coordinatorv1.WatchRoutesRequest{
		Payload: &coordinatorv1.WatchRoutesRequest_Subscribe{
			Subscribe: &coordinatorv1.Subscribe{
				SubscriberId:            "gate-a",
				Kind:                    coordinatorv1.SubscriberKind_SUBSCRIBER_KIND_GATE,
				LastMapVersion:          6,
				LastAvailabilityVersion: 3,
			},
		},
	}
	routeBatch := &coordinatorv1.WatchRoutesResponse{
		Payload: &coordinatorv1.WatchRoutesResponse_RouteBatch{
			RouteBatch: &coordinatorv1.RouteBatch{
				PreviousMapVersion: 6,
				MapVersion:         7,
				Routes: []*datav1.ShardRouteEntry{{
					ShardId:       42,
					OwnerEndpoint: "http://zone-a:8082",
				}},
			},
		},
	}

	for _, original := range []proto.Message{subscribe, routeBatch} {
		wire, err := proto.Marshal(original)
		if err != nil {
			t.Fatal(err)
		}
		decoded := original.ProtoReflect().Type().New().Interface()
		if err := proto.Unmarshal(wire, decoded); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(original, decoded) {
			t.Fatalf("round trip changed %T", original)
		}
	}

	gotSubscribe := subscribe.GetSubscribe()
	if gotSubscribe.GetSubscriberId() != "gate-a" ||
		gotSubscribe.GetLastMapVersion() != 6 ||
		gotSubscribe.GetLastAvailabilityVersion() != 3 {
		t.Fatalf("unexpected subscribe: %+v", gotSubscribe)
	}
	gotBatch := routeBatch.GetRouteBatch()
	if gotBatch.GetPreviousMapVersion() != 6 || gotBatch.GetMapVersion() != 7 ||
		gotBatch.GetRoutes()[0].GetShardId() != 42 ||
		gotBatch.GetRoutes()[0].GetOwnerEndpoint() != "http://zone-a:8082" {
		t.Fatalf("unexpected route batch: %+v", gotBatch)
	}
}

func TestShardMapSnapshotRouteMetadataRoundTrip(t *testing.T) {
	original := &datav1.ShardMapSnapshot{
		ShardCount:                 4096,
		HashAlgorithmVersion:       1,
		MapVersion:                 7,
		AssignmentAlgorithmVersion: 1,
		Entries: []*datav1.ShardRouteEntry{{
			ShardId:       42,
			OwnerEndpoint: "http://zone-a:8082",
		}},
	}
	wire, err := proto.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	got := &datav1.ShardMapSnapshot{}
	if err := proto.Unmarshal(wire, got); err != nil {
		t.Fatal(err)
	}
	if got.GetAssignmentAlgorithmVersion() != 1 {
		t.Fatalf("assignment_algorithm_version=%d want 1", got.GetAssignmentAlgorithmVersion())
	}
	if got.GetEntries()[0].GetOwnerEndpoint() != "http://zone-a:8082" {
		t.Fatalf("owner_endpoint=%q", got.GetEntries()[0].GetOwnerEndpoint())
	}
}

func TestRoutingLifecycleErrorCodeNumbers(t *testing.T) {
	tests := []struct {
		code wsv1.ErrorCode
		want int32
	}{
		{code: wsv1.ErrorCode_ZONE_MIGRATING, want: 204},
		{code: wsv1.ErrorCode_ZONE_UNAVAILABLE, want: 205},
		{code: wsv1.ErrorCode_ZONE_WARMING_UP, want: 206},
		{code: wsv1.ErrorCode_STORAGE_UNAVAILABLE, want: 207},
	}
	for _, test := range tests {
		if got := int32(test.code); got != test.want {
			t.Errorf("%s=%d want %d", test.code, got, test.want)
		}
	}
}
