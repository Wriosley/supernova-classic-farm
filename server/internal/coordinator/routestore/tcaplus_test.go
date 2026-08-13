package routestore

import (
	"context"
	"errors"
	"sync"
	"testing"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/tcaplusdb"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"github.com/Wriosley/supernova-classic-farm/server/internal/testtcaplus"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
	"google.golang.org/protobuf/proto"
)

func TestTcaplusStoreConcurrentBootstrapLoadsWinner(t *testing.T) {
	client := testtcaplus.New()
	stores := []*TcaplusStore{newTestTcaplusStore(t, client), newTestTcaplusStore(t, client)}
	candidate := testSnapshot(t)
	var wg sync.WaitGroup
	errs := make(chan error, len(stores))
	for _, store := range stores {
		wg.Add(1)
		go func(store *TcaplusStore) {
			defer wg.Done()
			loaded, _, err := store.BootstrapIfEmpty(context.Background(), candidate)
			if err == nil && len(loaded.Entries) != int(routing.ShardCount) {
				err = errors.New("bootstrap returned incomplete route set")
			}
			errs <- err
		}(store)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestTcaplusStoreBootstrapReloadAndExactReplay(t *testing.T) {
	ctx := context.Background()
	client := testtcaplus.New()
	store := newTestTcaplusStore(t, client)
	loaded, created, err := store.BootstrapIfEmpty(ctx, testSnapshot(t))
	if err != nil || !created || len(loaded.Entries) != int(routing.ShardCount) {
		t.Fatalf("bootstrap created=%v entries=%d err=%v", created, len(loaded.Entries), err)
	}
	prepared := nextPreparing(loaded.Entries[42])
	committed, err := store.CommitPreparing(ctx, prepared, 1)
	if err != nil || committed.Metadata.MapVersion != 2 {
		t.Fatalf("commit preparing map=%d err=%v", committed.Metadata.MapVersion, err)
	}
	replayed, err := store.CommitPreparing(ctx, prepared, 1)
	if err != nil || replayed.Metadata.MapVersion != 2 || replayed.Entries[42] != prepared {
		t.Fatalf("exact replay map=%d route=%+v err=%v", replayed.Metadata.MapVersion, replayed.Entries[42], err)
	}
	active := nextActive(prepared)
	committed, err = store.CommitActive(ctx, active, 2)
	if err != nil || committed.Metadata.MapVersion != 3 {
		t.Fatalf("commit active map=%d err=%v", committed.Metadata.MapVersion, err)
	}
	replayed, err = store.CommitActive(ctx, active, 2)
	if err != nil || replayed.Metadata.MapVersion != 3 || replayed.Entries[42] != active {
		t.Fatalf("active replay map=%d route=%+v err=%v", replayed.Metadata.MapVersion, replayed.Entries[42], err)
	}
	reloaded, created, err := newTestTcaplusStore(t, client).BootstrapIfEmpty(ctx, testSnapshot(t))
	if err != nil || created || reloaded.Entries[42] != active {
		t.Fatalf("reload changed current: created=%v route=%+v err=%v", created, reloaded.Entries[42], err)
	}
}

func TestTcaplusStoreResumesPartialBootstrap(t *testing.T) {
	ctx := context.Background()
	client := &insertCountingClient{Client: testtcaplus.New()}
	candidate := testSnapshot(t)
	insertCandidateRoutes(t, client, candidate, 1995)
	client.routeInserts = 0

	loaded, created, err := newTestTcaplusStore(t, client).BootstrapIfEmpty(ctx, candidate)
	if err != nil || !created || len(loaded.Entries) != int(routing.ShardCount) {
		t.Fatalf("resume created=%v entries=%d err=%v", created, len(loaded.Entries), err)
	}
	if want := int(routing.ShardCount) - 1995; client.routeInserts != want || client.metaInserts != 1 {
		t.Fatalf("resume inserts routes=%d want=%d meta=%d", client.routeInserts, want, client.metaInserts)
	}
}

func TestTcaplusStoreRejectsConflictingPartialBootstrap(t *testing.T) {
	ctx := context.Background()
	client := testtcaplus.New()
	candidate := testSnapshot(t)
	insertCandidateRoutes(t, client, candidate, 10)
	record := &tcaplusv1.ShardRoute{LogicalShardId: 5}
	opt := &option.PBOpt{}
	if err := client.DoGet(record, opt); err != nil {
		t.Fatal(err)
	}
	record.OwnerZoneId = "conflicting-zone"
	if err := client.DoUpdate(record, &option.PBOpt{Version: opt.Version}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := newTestTcaplusStore(t, client).BootstrapIfEmpty(ctx, candidate); !errors.Is(err, ErrRouteStoreCorrupt) {
		t.Fatalf("conflicting partial bootstrap error=%v", err)
	}
	meta := &tcaplusv1.ShardMapMeta{MapId: shardMapMetaID}
	if err := client.DoGet(meta, &option.PBOpt{}); !tcaplusdb.IsNotFound(err) {
		t.Fatalf("metadata unexpectedly created: %v", err)
	}
	stored := &tcaplusv1.ShardRoute{LogicalShardId: 5}
	if err := client.DoGet(stored, &option.PBOpt{}); err != nil || stored.OwnerZoneId != "conflicting-zone" {
		t.Fatalf("conflicting route overwritten: owner=%q err=%v", stored.OwnerZoneId, err)
	}
}

func TestTcaplusStoreRetriesInterruptedBootstrap(t *testing.T) {
	client := &cancelAfterRouteInsertsClient{Client: testtcaplus.New(), limit: 50}
	candidate := testSnapshot(t)
	ctx, cancel := context.WithCancel(context.Background())
	client.cancel = cancel
	if _, _, err := newTestTcaplusStore(t, client).BootstrapIfEmpty(ctx, candidate); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted bootstrap error=%v", err)
	}
	if err := client.DoGet(&tcaplusv1.ShardMapMeta{MapId: shardMapMetaID}, &option.PBOpt{}); !tcaplusdb.IsNotFound(err) {
		t.Fatalf("metadata created before routes completed: %v", err)
	}

	client.limit = 0
	client.cancel = nil
	loaded, created, err := newTestTcaplusStore(t, client).BootstrapIfEmpty(context.Background(), candidate)
	if err != nil || !created || len(loaded.Entries) != int(routing.ShardCount) {
		t.Fatalf("retry created=%v entries=%d err=%v", created, len(loaded.Entries), err)
	}
}

func TestTcaplusStoreFailsClosedOnIncompatibleOrIncompleteCurrent(t *testing.T) {
	for name, corrupt := range map[string]func(t *testing.T, client *testtcaplus.Client){
		"algorithm": func(t *testing.T, client *testtcaplus.Client) {
			meta := &tcaplusv1.ShardMapMeta{MapId: 1}
			opt := &option.PBOpt{}
			if err := client.DoGet(meta, opt); err != nil {
				t.Fatal(err)
			}
			meta.AssignmentAlgorithmVersion = 99
			if err := client.DoUpdate(meta, &option.PBOpt{Version: opt.Version}); err != nil {
				t.Fatal(err)
			}
		},
		"missing route": func(t *testing.T, client *testtcaplus.Client) {
			if err := client.DoDelete(&tcaplusv1.ShardRoute{LogicalShardId: 42}, &option.PBOpt{}); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			client := testtcaplus.New()
			store := newTestTcaplusStore(t, client)
			if _, _, err := store.BootstrapIfEmpty(context.Background(), testSnapshot(t)); err != nil {
				t.Fatal(err)
			}
			corrupt(t, client)
			if _, err := store.Load(context.Background()); !errors.Is(err, ErrRouteStoreCorrupt) {
				t.Fatalf("corrupt current error=%v", err)
			}
		})
	}
}

func TestTcaplusStoreLoadRecoversPendingBeforeRouteWrite(t *testing.T) {
	client, store, prepared := bootstrappedFailingStore(t)
	client.failAt = 2
	if _, err := store.CommitPreparing(context.Background(), prepared, 1); err == nil {
		t.Fatal("commit unexpectedly survived injected route write failure")
	}
	client.failAt = 0
	loaded, err := newTestTcaplusStore(t, client).Load(context.Background())
	if err != nil || loaded.Metadata.MapVersion != 2 || loaded.Entries[42] != prepared {
		t.Fatalf("pending-before-route recovery map=%d route=%+v err=%v", loaded.Metadata.MapVersion, loaded.Entries[42], err)
	}
}

func TestTcaplusStoreLoadRecoversRouteBeforeMetaFinalize(t *testing.T) {
	client, store, prepared := bootstrappedFailingStore(t)
	client.failAt = 3
	if _, err := store.CommitPreparing(context.Background(), prepared, 1); err == nil {
		t.Fatal("commit unexpectedly survived injected finalize failure")
	}
	client.failAt = 0
	loaded, err := newTestTcaplusStore(t, client).Load(context.Background())
	if err != nil || loaded.Metadata.MapVersion != 2 || loaded.Entries[42] != prepared {
		t.Fatalf("route-before-finalize recovery map=%d route=%+v err=%v", loaded.Metadata.MapVersion, loaded.Entries[42], err)
	}
}

func TestTcaplusStoreRejectsConflictingOrIncompletePending(t *testing.T) {
	t.Run("route conflicts with pending target", func(t *testing.T) {
		client, store, prepared := bootstrappedFailingStore(t)
		client.failAt = 2
		if _, err := store.CommitPreparing(context.Background(), prepared, 1); err == nil {
			t.Fatal("expected injected failure")
		}
		client.failAt = 0
		record := &tcaplusv1.ShardRoute{LogicalShardId: 42}
		opt := &option.PBOpt{}
		if err := client.DoGet(record, opt); err != nil {
			t.Fatal(err)
		}
		record.OwnerZoneId = "conflicting-zone"
		if err := client.DoUpdate(record, &option.PBOpt{Version: opt.Version}); err != nil {
			t.Fatal(err)
		}
		if _, err := newTestTcaplusStore(t, client).Load(context.Background()); !errors.Is(err, ErrRouteStoreCorrupt) {
			t.Fatalf("conflicting pending error=%v", err)
		}
	})

	t.Run("pending fields incomplete", func(t *testing.T) {
		client := testtcaplus.New()
		store := newTestTcaplusStore(t, client)
		if _, _, err := store.BootstrapIfEmpty(context.Background(), testSnapshot(t)); err != nil {
			t.Fatal(err)
		}
		meta := &tcaplusv1.ShardMapMeta{MapId: 1}
		opt := &option.PBOpt{}
		if err := client.DoGet(meta, opt); err != nil {
			t.Fatal(err)
		}
		meta.HasPendingCommit = true
		meta.PendingShardId = 42
		meta.PendingMapVersion = 2
		meta.PendingRouteVersion = 2
		meta.PendingState = string(routing.RouteStatePreparing)
		if err := client.DoUpdate(meta, &option.PBOpt{Version: opt.Version}); err != nil {
			t.Fatal(err)
		}
		if _, err := newTestTcaplusStore(t, client).Load(context.Background()); !errors.Is(err, ErrRouteStoreCorrupt) {
			t.Fatalf("incomplete pending error=%v", err)
		}
	})
}

func TestTcaplusStoreRejectsUnrelatedCommitWhilePending(t *testing.T) {
	client, store, prepared := bootstrappedFailingStore(t)
	client.failAt = 2
	if _, err := store.CommitPreparing(context.Background(), prepared, 1); err == nil {
		t.Fatal("expected injected failure")
	}
	client.failAt = 0
	loaded := testSnapshot(t)
	unrelated := nextPreparing(loaded.Entries[43])
	if _, err := store.CommitPreparing(context.Background(), unrelated, 1); !errors.Is(err, ErrRouteConflict) {
		t.Fatalf("unrelated pending commit error=%v", err)
	}
}

type failUpdateClient struct {
	*testtcaplus.Client
	updates int
	failAt  int
}

type insertCountingClient struct {
	*testtcaplus.Client
	routeInserts int
	metaInserts  int
}

func (c *insertCountingClient) DoInsert(message proto.Message, opt *option.PBOpt, zones ...uint32) error {
	err := c.Client.DoInsert(message, opt, zones...)
	if err == nil {
		switch message.(type) {
		case *tcaplusv1.ShardRoute:
			c.routeInserts++
		case *tcaplusv1.ShardMapMeta:
			c.metaInserts++
		}
	}
	return err
}

type cancelAfterRouteInsertsClient struct {
	*testtcaplus.Client
	limit  int
	count  int
	cancel context.CancelFunc
}

func (c *cancelAfterRouteInsertsClient) DoInsert(message proto.Message, opt *option.PBOpt, zones ...uint32) error {
	err := c.Client.DoInsert(message, opt, zones...)
	if err == nil {
		if _, ok := message.(*tcaplusv1.ShardRoute); ok {
			c.count++
			if c.limit > 0 && c.count == c.limit && c.cancel != nil {
				c.cancel()
			}
		}
	}
	return err
}

func insertCandidateRoutes(t *testing.T, client TcaplusClient, candidate Snapshot, count int) {
	t.Helper()
	for index := 0; index < count; index++ {
		record, err := routeRecord(candidate.Entries[index], candidate.Metadata.MapVersion)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.DoInsert(record, &option.PBOpt{}); err != nil {
			t.Fatal(err)
		}
	}
}

func (c *failUpdateClient) DoUpdate(message proto.Message, opt *option.PBOpt, zones ...uint32) error {
	c.updates++
	if c.failAt > 0 && c.updates == c.failAt {
		return errors.New("injected Tcaplus update failure")
	}
	return c.Client.DoUpdate(message, opt, zones...)
}

func newTestTcaplusStore(t *testing.T, client TcaplusClient) *TcaplusStore {
	t.Helper()
	store, err := NewTcaplusStore(client, 1)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func bootstrappedFailingStore(t *testing.T) (*failUpdateClient, *TcaplusStore, routing.RouteEntry) {
	t.Helper()
	client := &failUpdateClient{Client: testtcaplus.New()}
	store := newTestTcaplusStore(t, client)
	loaded, _, err := store.BootstrapIfEmpty(context.Background(), testSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	client.updates = 0
	return client, store, nextPreparing(loaded.Entries[42])
}
