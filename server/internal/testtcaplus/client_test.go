package testtcaplus

import (
	"bytes"
	"testing"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
	"google.golang.org/protobuf/proto"
)

func TestMigrationTaskUsesLogicalShardKeyAndRoundTripsAllFields(t *testing.T) {
	client := New()
	want := &tcaplusv1.MigrationTask{
		LogicalShardId:             73,
		TaskId:                     []byte("1234567890abcdef"),
		Reason:                     "REBALANCE",
		Status:                     "PLANNED",
		Priority:                   100,
		SourceZoneId:               "zone-a",
		SourceEndpoint:             "http://zone-a:8082",
		SourceOwnerEpoch:           9,
		SourceRouteVersion:         12,
		TargetZoneId:               "zone-c",
		TargetEndpoint:             "http://zone-c:8082",
		PlannedFromMapVersion:      4117,
		PlannedAvailabilityVersion: 24,
		Attempt:                    2,
		RetryAtMs:                  1_786_579_201_000,
		LastErrorCode:              "ZONE_BUSY",
		CreatedAtMs:                1_786_579_200_000,
		UpdatedAtMs:                1_786_579_200_500,
	}
	insertOpt := &option.PBOpt{}
	if err := client.DoInsert(want, insertOpt); err != nil {
		t.Fatalf("DoInsert: %v", err)
	}
	if insertOpt.Version != 1 {
		t.Fatalf("insert version = %d, want 1", insertOpt.Version)
	}

	// Mutating the caller's record must not mutate the stored record.
	want.TaskId[0] = 'x'
	got := &tcaplusv1.MigrationTask{LogicalShardId: 73}
	getOpt := &option.PBOpt{}
	if err := client.DoGet(got, getOpt); err != nil {
		t.Fatalf("DoGet: %v", err)
	}
	if getOpt.Version != 1 {
		t.Fatalf("get version = %d, want 1", getOpt.Version)
	}
	want.TaskId[0] = '1'
	if !proto.Equal(got, want) {
		t.Fatalf("round trip mismatch:\n got: %v\nwant: %v", got, want)
	}

	other := proto.Clone(want).(*tcaplusv1.MigrationTask)
	other.LogicalShardId = 74
	other.TaskId = bytes.Repeat([]byte{2}, 16)
	if err := client.DoInsert(other, &option.PBOpt{}); err != nil {
		t.Fatalf("insert other shard: %v", err)
	}
	loadedOther := &tcaplusv1.MigrationTask{LogicalShardId: 74}
	if err := client.DoGet(loadedOther, &option.PBOpt{}); err != nil {
		t.Fatalf("get other shard: %v", err)
	}
	if !proto.Equal(loadedOther, other) {
		t.Fatalf("logical shard keys collided: got %v, want %v", loadedOther, other)
	}
}

func TestMigrationTaskFieldNamesFitTcaplusLimit(t *testing.T) {
	fields := (&tcaplusv1.MigrationTask{}).ProtoReflect().Descriptor().Fields()
	for index := 0; index < fields.Len(); index++ {
		field := fields.Get(index)
		if len(field.Name()) > 31 {
			t.Errorf("field %q has %d characters, Tcaplus limit is 31", field.Name(), len(field.Name()))
		}
	}
}
