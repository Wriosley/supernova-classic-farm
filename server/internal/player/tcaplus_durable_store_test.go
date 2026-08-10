package player

import (
	"context"
	"errors"
	"testing"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/testtcaplus"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
	"google.golang.org/protobuf/proto"
)

func TestTcaplusDurableStoreChecksFenceAndPersistsOutbox(t *testing.T) {
	ctx := context.Background()
	client := testtcaplus.New()
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	checkpoint := NewInitialCheckpoint(42, now)
	if err := client.DoInsert(&tcaplusv1.ShardFence{
		LogicalShardId: checkpoint.LogicalShardId,
		OwnerZoneId:    "zone-a", OwnerEpoch: checkpoint.OwnerEpoch,
		RouteVersion: 1,
	}, &option.PBOpt{}, 1); err != nil {
		t.Fatal(err)
	}
	base, _ := NewTcaplusCheckpointStoreWithClient(client, 1)
	durable, _ := NewTcaplusDurableCheckpointStore(
		base, client, 1, "zone-a",
	)
	if result, err := durable.Create(ctx, checkpoint); err != nil ||
		result.Status != CheckpointWriteApplied {
		t.Fatalf("Create status=%d error=%v", result.Status, err)
	}
	loaded, err := durable.Load(ctx, checkpoint.PlayerId)
	if err != nil {
		t.Fatal(err)
	}
	next, err := loaded.State.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	next.PlayerSeq++
	next.CheckpointRevision++
	next.UpdatedAtMs++
	chapter, _ := NewDevelopmentConfigSnapshot().Chapter(InitialChapterID)
	pending, err := buildRewardMailOutbox(
		42,
		[]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
		chapter,
		[]*wsv1.ItemStackView{{ItemId: developmentNextSeedItemID, Quantity: 3}},
		next.OwnerEpoch,
		next.PlayerSeq,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	next.PendingOutbox = append(next.PendingOutbox, pending)
	result, err := durable.SaveCAS(ctx, CheckpointWrite{
		Checkpoint: next, ExpectedRevision: loaded.PersistedRevision,
		ExpectedToken: loaded.Token,
	})
	if err != nil || result.Status != CheckpointWriteApplied {
		t.Fatalf("SaveCAS status=%d error=%v", result.Status, err)
	}
	outbox := &tcaplusv1.PlayerOutbox{EventId: pending.EventId}
	if err := client.DoGet(outbox, &option.PBOpt{}, 1); err != nil {
		t.Fatal(err)
	}
	if outbox.AggregatePlayerId != 42 ||
		outbox.RelayStatus != tcaplusOutboxPending {
		t.Fatalf("persisted Outbox = %+v", outbox)
	}

	stale := proto.Clone(next).(*datav1.PlayerCheckpointV1)
	stale.CheckpointRevision++
	fence := &tcaplusv1.ShardFence{LogicalShardId: next.LogicalShardId}
	fenceOpt := &option.PBOpt{}
	if err := client.DoGet(fence, fenceOpt, 1); err != nil {
		t.Fatal(err)
	}
	fence.OwnerZoneId = "zone-b"
	if err := client.DoUpdate(
		fence,
		&option.PBOpt{
			Version:       fenceOpt.Version,
			VersionPolicy: option.CheckDataVersionAutoIncrease,
		},
		1,
	); err != nil {
		t.Fatal(err)
	}
	fenced, err := durable.SaveCAS(ctx, CheckpointWrite{
		Checkpoint: stale, ExpectedRevision: next.CheckpointRevision,
		ExpectedToken: result.NewToken,
	})
	if err != nil || fenced.Status != CheckpointWriteFenced {
		t.Fatalf("fenced SaveCAS status=%d error=%v", fenced.Status, err)
	}
}

func TestTcaplusDurableCreateInitialRejectsWrongFence(t *testing.T) {
	ctx := context.Background()
	client := testtcaplus.New()
	checkpoint := NewInitialCheckpoint(99, time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC))
	if err := client.DoInsert(&tcaplusv1.ShardFence{
		LogicalShardId: checkpoint.LogicalShardId,
		OwnerZoneId:    "zone-b", OwnerEpoch: checkpoint.OwnerEpoch,
		RouteVersion: 1,
	}, &option.PBOpt{}, 1); err != nil {
		t.Fatal(err)
	}
	base, _ := NewTcaplusCheckpointStoreWithClient(client, 1)
	durable, _ := NewTcaplusDurableCheckpointStore(base, client, 1, "zone-a")
	result, err := durable.CreateInitial(ctx, checkpoint)
	if err != nil || result.Status != CheckpointWriteFenced {
		t.Fatalf("CreateInitial status=%d error=%v, want Fenced", result.Status, err)
	}
	if _, err := base.Load(ctx, checkpoint.PlayerId); !errors.Is(err, ErrCheckpointNotFound) {
		t.Fatalf("checkpoint should remain absent after fenced create: %v", err)
	}
}
