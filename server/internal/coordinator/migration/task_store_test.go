package migration

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/Wriosley/supernova-classic-farm/server/internal/testtcaplus"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
	"google.golang.org/protobuf/proto"
)

func TestTaskStoreDeduplicatesExactProposalAndSurvivesReload(t *testing.T) {
	ctx := context.Background()
	client := testtcaplus.New()
	store, err := NewTcaplusTaskStore(client, 1)
	if err != nil {
		t.Fatalf("NewTcaplusTaskStore: %v", err)
	}
	proposal := rebalanceTask(31, "zone-a", "zone-c", 10)

	created, changed, err := store.UpsertPlanned(ctx, proposal)
	if err != nil || !changed {
		t.Fatalf("first UpsertPlanned = (%+v, %t, %v), want changed", created, changed, err)
	}
	if len(created.TaskID) != 16 || created.Status != StatusPlanned || created.CreatedAtMS == 0 {
		t.Fatalf("created task has invalid identity/status/time: %+v", created)
	}
	replayed, changed, err := store.UpsertPlanned(ctx, proposal)
	if err != nil || changed {
		t.Fatalf("replay UpsertPlanned = (%+v, %t, %v), want unchanged", replayed, changed, err)
	}
	if !bytes.Equal(replayed.TaskID, created.TaskID) || replayed.CreatedAtMS != created.CreatedAtMS {
		t.Fatalf("replay changed immutable identity: first=%+v replay=%+v", created, replayed)
	}

	restarted, err := NewTcaplusTaskStore(client, 1)
	if err != nil {
		t.Fatalf("restart store: %v", err)
	}
	loaded, found, err := restarted.Get(ctx, proposal.ShardID)
	if err != nil || !found || !bytes.Equal(loaded.TaskID, created.TaskID) {
		t.Fatalf("restarted Get = (%+v, %t, %v)", loaded, found, err)
	}
}

func TestTaskStoreReplacementConflictCancellationAndOrder(t *testing.T) {
	ctx := context.Background()
	running := rebalanceTask(9, "zone-a", "zone-b", 4)
	running.Status = StatusRunning
	running.TaskID = bytes.Repeat([]byte{9}, 16)
	running.CreatedAtMS = 10
	running.UpdatedAtMS = 10
	memory, err := NewMemoryTaskStore(running)
	if err != nil {
		t.Fatalf("NewMemoryTaskStore: %v", err)
	}

	if _, _, err := memory.UpsertPlanned(ctx, failoverTask(9, "zone-a", "zone-c", 4)); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("replace RUNNING error = %v, want ErrTaskConflict", err)
	}

	low, _, err := memory.UpsertPlanned(ctx, rebalanceTask(12, "zone-a", "zone-b", 4))
	if err != nil {
		t.Fatalf("insert low priority: %v", err)
	}
	if _, _, err := memory.UpsertPlanned(ctx, rebalanceTask(12, "zone-a", "zone-c", 4)); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("same-priority replacement error = %v, want ErrTaskConflict", err)
	}
	high, changed, err := memory.UpsertPlanned(ctx, failoverTask(12, "zone-a", "zone-c", 4))
	if err != nil || !changed || bytes.Equal(high.TaskID, low.TaskID) {
		t.Fatalf("higher-priority replacement = (%+v, %t, %v)", high, changed, err)
	}

	drain := rebalanceTask(2, "zone-a", "zone-d", 4)
	drain.Reason, drain.Priority = ReasonDrain, PriorityDrain
	if _, _, err := memory.UpsertPlanned(ctx, drain); err != nil {
		t.Fatalf("insert drain: %v", err)
	}
	open, err := memory.LoadOpen(ctx)
	if err != nil {
		t.Fatalf("LoadOpen: %v", err)
	}
	if len(open) != 3 || open[0].ShardID != 12 || open[1].ShardID != 2 || open[2].ShardID != 9 {
		t.Fatalf("open order = %#v, want failover/drain/running by priority", open)
	}

	if err := memory.Cancel(ctx, 2, []byte("wrong"), "STALE"); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("stale Cancel error = %v, want ErrTaskConflict", err)
	}
	drainStored, found, err := memory.Get(ctx, 2)
	if err != nil || !found {
		t.Fatalf("Get drain = (%+v, %t, %v)", drainStored, found, err)
	}
	if err := memory.Cancel(ctx, 2, drainStored.TaskID, "CURRENT_MATCHES_DESIRED"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	open, err = memory.LoadOpen(ctx)
	if err != nil || len(open) != 2 {
		t.Fatalf("LoadOpen after cancel = (%#v, %v)", open, err)
	}
}

func TestTcaplusTaskStoreRetriesStaleRecordVersion(t *testing.T) {
	ctx := context.Background()
	client := &oneStaleUpdateClient{Client: testtcaplus.New()}
	store, err := NewTcaplusTaskStore(client, 1)
	if err != nil {
		t.Fatalf("NewTcaplusTaskStore: %v", err)
	}
	if _, _, err := store.UpsertPlanned(ctx, rebalanceTask(7, "zone-a", "zone-b", 3)); err != nil {
		t.Fatalf("insert: %v", err)
	}
	client.failNext = true
	got, changed, err := store.UpsertPlanned(ctx, failoverTask(7, "zone-a", "zone-c", 3))
	if err != nil || !changed || got.TargetZoneID != "zone-c" || client.updateCalls != 2 {
		t.Fatalf("replacement after stale CAS = (%+v, %t, %v), update calls=%d", got, changed, err, client.updateCalls)
	}
}

func TestTaskStoresClaimRetryAndCompleteExactTask(t *testing.T) {
	tests := []struct {
		name string
		new  func(t *testing.T) TaskStore
	}{
		{name: "memory", new: func(t *testing.T) TaskStore {
			store, err := NewMemoryTaskStore()
			if err != nil {
				t.Fatal(err)
			}
			return store
		}},
		{name: "tcaplus", new: func(t *testing.T) TaskStore {
			store, err := NewTcaplusTaskStore(testtcaplus.New(), 1)
			if err != nil {
				t.Fatal(err)
			}
			return store
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store := test.new(t)
			created, _, err := store.UpsertPlanned(ctx, rebalanceTask(44, "zone-a", "zone-b", 9))
			if err != nil {
				t.Fatalf("UpsertPlanned: %v", err)
			}
			if _, err := store.Claim(ctx, 44, []byte("wrong-task-id")); !errors.Is(err, ErrTaskConflict) {
				t.Fatalf("stale Claim error = %v, want ErrTaskConflict", err)
			}
			claimed, err := store.Claim(ctx, 44, created.TaskID)
			if err != nil || claimed.Status != StatusRunning {
				t.Fatalf("Claim = (%+v, %v)", claimed, err)
			}
			replayed, err := store.Claim(ctx, 44, created.TaskID)
			if err != nil || replayed.Status != StatusRunning || !bytes.Equal(replayed.TaskID, created.TaskID) {
				t.Fatalf("Claim replay = (%+v, %v)", replayed, err)
			}
			if err := store.Retry(ctx, 44, []byte("wrong-task-id"), 1, 1234, "TEMPORARY"); !errors.Is(err, ErrTaskConflict) {
				t.Fatalf("stale Retry error = %v, want ErrTaskConflict", err)
			}
			if err := store.Retry(ctx, 44, created.TaskID, 1, 1234, "TEMPORARY"); err != nil {
				t.Fatalf("Retry: %v", err)
			}
			retried, found, err := store.Get(ctx, 44)
			if err != nil || !found || retried.Status != StatusPlanned || retried.Attempt != 1 ||
				retried.RetryAtMS != 1234 || retried.LastErrorCode != "TEMPORARY" {
				t.Fatalf("retried task = (%+v, %t, %v)", retried, found, err)
			}
			if _, err := store.Claim(ctx, 44, created.TaskID); err != nil {
				t.Fatalf("second Claim: %v", err)
			}
			if err := store.Complete(ctx, 44, []byte("wrong-task-id")); !errors.Is(err, ErrTaskConflict) {
				t.Fatalf("stale Complete error = %v, want ErrTaskConflict", err)
			}
			if err := store.Complete(ctx, 44, created.TaskID); err != nil {
				t.Fatalf("Complete: %v", err)
			}
			if err := store.Complete(ctx, 44, created.TaskID); err != nil {
				t.Fatalf("Complete replay: %v", err)
			}
			completed, found, err := store.Get(ctx, 44)
			if err != nil || !found || completed.Status != StatusCompleted {
				t.Fatalf("completed task = (%+v, %t, %v)", completed, found, err)
			}
		})
	}
}

func TestTcaplusTaskLifecycleRetriesStaleRecordVersion(t *testing.T) {
	ctx := context.Background()
	client := &oneStaleUpdateClient{Client: testtcaplus.New()}
	store, err := NewTcaplusTaskStore(client, 1)
	if err != nil {
		t.Fatal(err)
	}
	created, _, err := store.UpsertPlanned(ctx, rebalanceTask(8, "zone-a", "zone-b", 3))
	if err != nil {
		t.Fatal(err)
	}
	client.failNext = true
	client.updateCalls = 0
	claimed, err := store.Claim(ctx, 8, created.TaskID)
	if err != nil || claimed.Status != StatusRunning || client.updateCalls != 2 {
		t.Fatalf("Claim after stale CAS = (%+v, %v), update calls=%d", claimed, err, client.updateCalls)
	}
}

type oneStaleUpdateClient struct {
	*testtcaplus.Client
	failNext    bool
	updateCalls int
}

func (c *oneStaleUpdateClient) DoUpdate(message proto.Message, opt *option.PBOpt, zones ...uint32) error {
	c.updateCalls++
	if c.failNext {
		c.failNext = false
		return ErrTaskCASConflict
	}
	return c.Client.DoUpdate(message, opt, zones...)
}

func rebalanceTask(shardID uint32, source, target string, mapVersion uint64) Task {
	return Task{
		ShardID: shardID, Reason: ReasonRebalance, Priority: PriorityRebalance,
		SourceZoneID: source, SourceEndpoint: "http://" + source + ":8082",
		SourceOwnerEpoch: 2, SourceRouteVersion: 3,
		TargetZoneID: target, TargetEndpoint: "http://" + target + ":8082",
		PlannedFromMapVersion: mapVersion, PlannedFromAvailabilityVersion: 8,
	}
}

func failoverTask(shardID uint32, source, target string, mapVersion uint64) Task {
	task := rebalanceTask(shardID, source, target, mapVersion)
	task.Reason, task.Priority = ReasonFailover, PriorityFailover
	return task
}
