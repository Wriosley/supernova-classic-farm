package player

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/protobuf/proto"
)

type checkpointLoaderFunc func(context.Context, uint64) (*State, error)

func (f checkpointLoaderFunc) Load(ctx context.Context, playerID uint64) (LoadedCheckpoint, error) {
	state, err := f(ctx, playerID)
	if err != nil {
		return LoadedCheckpoint{}, err
	}
	return LoadedCheckpoint{
		State: state, PersistedRevision: state.CheckpointRevision,
	}, nil
}

func (f checkpointLoaderFunc) SaveCAS(context.Context, CheckpointWrite) (CheckpointWriteResult, error) {
	return CheckpointWriteResult{Status: CheckpointWriteRetryableFailure},
		errors.New("test checkpoint store is read-only")
}

type recordingCheckpointStore struct {
	state            *State
	saved            []*datav1.PlayerCheckpointV1
	expectedRevision []uint64
	token            StoreToken
	expectedToken    []StoreToken
	newToken         StoreToken
}

type ambiguousCheckpointStore struct {
	state *State
}

func (s *ambiguousCheckpointStore) Load(context.Context, uint64) (LoadedCheckpoint, error) {
	checkpoint, err := s.state.Checkpoint()
	if err != nil {
		return LoadedCheckpoint{}, err
	}
	state, err := StateFromCheckpoint(checkpoint)
	if err != nil {
		return LoadedCheckpoint{}, err
	}
	return LoadedCheckpoint{
		State: state, PersistedRevision: state.CheckpointRevision,
	}, nil
}

func (s *ambiguousCheckpointStore) SaveCAS(
	_ context.Context,
	write CheckpointWrite,
) (CheckpointWriteResult, error) {
	state, err := StateFromCheckpoint(write.Checkpoint)
	if err != nil {
		return CheckpointWriteResult{Status: CheckpointWriteRetryableFailure}, err
	}
	s.state = state
	return CheckpointWriteResult{Status: CheckpointWriteRetryableFailure},
		errors.New("connection lost after commit")
}

func (s *recordingCheckpointStore) Load(context.Context, uint64) (LoadedCheckpoint, error) {
	return LoadedCheckpoint{
		State: s.state, PersistedRevision: s.state.CheckpointRevision,
		Token: cloneStoreToken(s.token),
	}, nil
}

func (s *recordingCheckpointStore) SaveCAS(_ context.Context, write CheckpointWrite) (CheckpointWriteResult, error) {
	s.saved = append(s.saved, write.Checkpoint)
	s.expectedRevision = append(s.expectedRevision, write.ExpectedRevision)
	s.expectedToken = append(s.expectedToken, cloneStoreToken(write.ExpectedToken))
	return CheckpointWriteResult{
		Status: CheckpointWriteApplied, NewToken: cloneStoreToken(s.newToken),
	}, nil
}

func snapshotRequest(playerID uint64, requestID string) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion,
		MessageKind:     wsv1.MessageKind_REQUEST,
		Action:          wsv1.Action_GET_PLAYER_SNAPSHOT,
		RequestId:       requestID,
		TargetPlayerId:  playerID,
		Payload: &wsv1.WsEnvelope_GetPlayerSnapshotRequest{
			GetPlayerSnapshotRequest: &wsv1.GetPlayerSnapshotRequest{},
		},
	}
}

func buySeedsRequest(playerID uint64, requestID string, quantity uint32) *wsv1.WsEnvelope {
	return buySeedsQuoteRequest(playerID, requestID, 5001, quantity, 8)
}

func buySeedsQuoteRequest(
	playerID uint64,
	requestID string,
	shopEntryID uint32,
	quantity uint32,
	priceVersion uint64,
) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion,
		MessageKind:     wsv1.MessageKind_REQUEST,
		Action:          wsv1.Action_BUY_SEEDS,
		RequestId:       requestID,
		TargetPlayerId:  playerID,
		Payload: &wsv1.WsEnvelope_BuySeedsRequest{
			BuySeedsRequest: &wsv1.BuySeedsRequest{
				ShopEntryId: shopEntryID, Quantity: quantity, ExpectedPriceVersion: priceVersion,
			},
		},
	}
}

func buyFertilizerRequest(playerID uint64, requestID string, quantity uint32) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion,
		MessageKind:     wsv1.MessageKind_REQUEST,
		Action:          wsv1.Action_BUY_FERTILIZER,
		RequestId:       requestID,
		TargetPlayerId:  playerID,
		Payload: &wsv1.WsEnvelope_BuyFertilizerRequest{
			BuyFertilizerRequest: &wsv1.BuyFertilizerRequest{
				ShopEntryId: developmentFertilizerShopEntryID, Quantity: quantity,
				ExpectedPriceVersion: developmentFertilizerPriceVersion,
			},
		},
	}
}

func getShopRequest(playerID uint64, requestID string) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion,
		MessageKind:     wsv1.MessageKind_REQUEST,
		Action:          wsv1.Action_GET_SHOP,
		RequestId:       requestID,
		TargetPlayerId:  playerID,
		Payload: &wsv1.WsEnvelope_GetShopRequest{
			GetShopRequest: &wsv1.GetShopRequest{},
		},
	}
}

func plantRequest(playerID uint64, requestID string, plotID, seedItemID uint32) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion,
		MessageKind:     wsv1.MessageKind_REQUEST,
		Action:          wsv1.Action_PLANT,
		RequestId:       requestID,
		TargetPlayerId:  playerID,
		Payload: &wsv1.WsEnvelope_PlantRequest{
			PlantRequest: &wsv1.PlantRequest{PlotId: plotID, SeedItemId: seedItemID},
		},
	}
}

func fertilizerRequest(playerID uint64, requestID string, plotID, itemID uint32) *wsv1.WsEnvelope {
	return &wsv1.WsEnvelope{
		ProtocolVersion: ProtocolVersion,
		MessageKind:     wsv1.MessageKind_REQUEST,
		Action:          wsv1.Action_APPLY_FERTILIZER,
		RequestId:       requestID,
		TargetPlayerId:  playerID,
		Payload: &wsv1.WsEnvelope_ApplyFertilizerRequest{
			ApplyFertilizerRequest: &wsv1.ApplyFertilizerRequest{
				PlotId: plotID, FertilizerItemId: itemID,
			},
		},
	}
}

func TestHandleReturnsCorrelatedInitialSnapshot(t *testing.T) {
	runtime := NewRuntime()
	defer runtime.Close()
	fixedTime := time.UnixMilli(1_753_888_000_123)
	runtime.now = func() time.Time { return fixedTime }

	response, err := runtime.Handle(context.Background(), 42, LocalOwnerEpoch, snapshotRequest(42, "request-42"))
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if response.MessageKind != wsv1.MessageKind_RESPONSE ||
		response.Action != wsv1.Action_GET_PLAYER_SNAPSHOT ||
		response.RequestId != "request-42" {
		t.Fatalf("response correlation fields = %+v", response)
	}
	if response.ServerTimeMs != fixedTime.UnixMilli() {
		t.Fatalf("server_time_ms = %d", response.ServerTimeMs)
	}
	if response.StateVersion.GetOwnerEpoch() != LocalOwnerEpoch || response.StateVersion.GetPlayerSeq() != 0 {
		t.Fatalf("state_version = %+v", response.StateVersion)
	}

	snapshot := response.GetGetPlayerSnapshotResponse().GetSnapshot()
	if snapshot.GetPlayerId() != 42 || snapshot.GetCoinBalance() != InitialCoinBalance {
		t.Fatalf("snapshot identity/coins = %+v", snapshot)
	}
	if len(snapshot.Inventory) != 1 ||
		snapshot.Inventory[0].GetItemId() != BasicFertilizerID ||
		snapshot.Inventory[0].GetQuantity() != 1 {
		t.Fatalf("initial inventory = %+v", snapshot.Inventory)
	}
	if len(snapshot.Plots) < 1 || snapshot.Plots[0].GetPlotState() != plotv1.PlotState_EMPTY {
		t.Fatalf("initial plots = %+v", snapshot.Plots)
	}
	if snapshot.CurrentChapter == nil || len(snapshot.CurrentChapter.Tasks) != 5 {
		t.Fatalf("initial chapter = %+v", snapshot.CurrentChapter)
	}
}

func TestHandleUsesActivationEpochAndRejectsMismatchedExistingActor(t *testing.T) {
	runtime := NewRuntime()
	defer runtime.Close()

	response, err := runtime.Handle(context.Background(), 42, 2, snapshotRequest(42, "epoch-2"))
	if err != nil || response.GetStateVersion().GetOwnerEpoch() != 2 {
		t.Fatalf("epoch-2 activation = %+v, %v", response, err)
	}
	if _, err := runtime.Handle(context.Background(), 42, 1,
		snapshotRequest(42, "stale-epoch")); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("stale epoch error = %v, want ErrNotOwner", err)
	}
	if _, err := runtime.Handle(context.Background(), 42, LocalOwnerEpoch, snapshotRequest(43, "target")); !errors.Is(err, ErrForbiddenTarget) {
		t.Fatalf("wrong target error = %v, want ErrForbiddenTarget", err)
	}
}

func TestRuntimeReportsActiveActorsByShard(t *testing.T) {
	runtime := NewRuntime()
	defer runtime.Close()
	const playerID = uint64(42)
	shardID := routing.ShardForPlayer(playerID)
	if runtime.HasActiveActorsForShard(shardID) {
		t.Fatal("inactive shard reported an Actor")
	}
	if _, err := runtime.Handle(context.Background(), playerID, 2,
		snapshotRequest(playerID, "activate-for-drain")); err != nil {
		t.Fatal(err)
	}
	if !runtime.HasActiveActorsForShard(shardID) {
		t.Fatal("activated shard did not report an Actor")
	}
	checkpoint, err := runtime.actors[playerID].state.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.OwnerEpoch != 2 {
		t.Fatalf("checkpoint owner_epoch = %d, want 2", checkpoint.OwnerEpoch)
	}
}

func TestRuntimeDrainShardFlushesAndEvictsActiveActor(t *testing.T) {
	const playerID = uint64(42)
	state := NewDevelopmentState(playerID)
	store := &recordingCheckpointStore{state: state}
	runtime, err := NewRuntimeWithStore(store)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	response, err := runtime.Handle(
		context.Background(), playerID, 1,
		buySeedsRequest(playerID, "00112233-4455-6677-8899-aabbccddee21", 1),
	)
	if err != nil || response.GetError() != nil {
		t.Fatalf("buy before drain failed: response=%+v error=%v", response, err)
	}
	manifest, err := runtime.DrainShardForMigration(
		context.Background(), routing.ShardForPlayer(playerID), 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest) != 1 ||
		manifest[0].PlayerID != playerID ||
		manifest[0].CheckpointRevision != 2 ||
		runtime.HasActiveActorsForShard(routing.ShardForPlayer(playerID)) {
		t.Fatalf("unexpected drain manifest/state: %+v", manifest)
	}
	if len(store.saved) != 1 ||
		store.saved[0].CoinBalance != 8 ||
		store.expectedRevision[0] != 1 {
		t.Fatalf("unexpected final checkpoint: %+v", store.saved)
	}
}

func TestRuntimeLazyEpochAdoptionAdvancesOnlyCheckpointRevision(t *testing.T) {
	const playerID = uint64(42)
	state := NewDevelopmentState(playerID)
	store := &recordingCheckpointStore{state: state}
	runtime, err := NewRuntimeWithStore(store)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	response, err := runtime.Handle(
		context.Background(), playerID, 2,
		snapshotRequest(playerID, "epoch-adoption"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.GetStateVersion().GetOwnerEpoch() != 2 ||
		response.GetStateVersion().GetPlayerSeq() != 0 {
		t.Fatalf("unexpected adopted state version: %+v", response.StateVersion)
	}
	if err := runtime.flushDirty(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 1 ||
		store.saved[0].OwnerEpoch != 2 ||
		store.saved[0].PlayerSeq != 0 ||
		store.saved[0].CheckpointRevision != 2 {
		t.Fatalf("unexpected epoch-adoption checkpoint: %+v", store.saved)
	}
}

func TestRuntimeDrainReconcilesAmbiguousFinalFlushCommit(t *testing.T) {
	const playerID = uint64(42)
	store := &ambiguousCheckpointStore{state: NewDevelopmentState(playerID)}
	runtime, err := NewRuntimeWithStore(store)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	response, err := runtime.Handle(
		context.Background(), playerID, 1,
		buySeedsRequest(playerID, "00112233-4455-6677-8899-aabbccddee22", 1),
	)
	if err != nil || response.GetError() != nil {
		t.Fatalf("buy before ambiguous drain failed: response=%+v error=%v", response, err)
	}
	manifest, err := runtime.DrainShardForMigration(
		context.Background(), routing.ShardForPlayer(playerID), 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest) != 1 || store.state.Coins != 8 ||
		runtime.HasActiveActorsForShard(routing.ShardForPlayer(playerID)) {
		t.Fatalf("ambiguous commit was not reconciled: %+v", manifest)
	}
}

func TestRuntimePrepareShardIsIdempotentAndDoesNotPrewarmActors(t *testing.T) {
	const playerID = uint64(42)
	state := NewDevelopmentState(playerID)
	store := &recordingCheckpointStore{state: state}
	runtime, err := NewRuntimeWithStore(store)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	manifest := []DrainedPlayer{{PlayerID: playerID, OwnerEpoch: 1, CheckpointRevision: state.CheckpointRevision}}
	shardID := routing.ShardForPlayer(playerID)
	for attempt := 0; attempt < 2; attempt++ {
		if err := runtime.PrepareShardForMigration(context.Background(), shardID, 2, manifest); err != nil {
			t.Fatalf("Prepare attempt %d: %v", attempt, err)
		}
		if runtime.HasActiveActorsForShard(shardID) {
			t.Fatalf("Prepare attempt %d prewarmed an Actor", attempt)
		}
	}
	if store.state.OwnerEpoch != 2 || store.state.CheckpointRevision != manifest[0].CheckpointRevision+1 || len(store.saved) != 1 {
		t.Fatalf("prepared checkpoint=%+v saves=%d", store.state, len(store.saved))
	}
}

func TestRuntimeDrainFlushFailureKeepsActorForRetry(t *testing.T) {
	const playerID = uint64(42)
	runtime, err := NewRuntimeWithStore(&failingDrainStore{persisted: NewDevelopmentState(playerID)})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, err := runtime.Handle(context.Background(), playerID, 1,
		buySeedsRequest(playerID, "00112233-4455-6677-8899-aabbccddee24", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.DrainShardForMigration(context.Background(), routing.ShardForPlayer(playerID), 1); err == nil {
		t.Fatal("Drain succeeded despite final flush failure")
	}
	if !runtime.HasActiveActorsForShard(routing.ShardForPlayer(playerID)) {
		t.Fatal("failed Drain evicted Actor instead of preserving it for retry")
	}
}

type failingDrainStore struct{ persisted *State }

func (store *failingDrainStore) Load(ctx context.Context, playerID uint64) (LoadedCheckpoint, error) {
	checkpoint, err := store.persisted.Checkpoint()
	if err != nil {
		return LoadedCheckpoint{}, err
	}
	state, err := StateFromCheckpoint(checkpoint)
	return LoadedCheckpoint{State: state, PersistedRevision: state.CheckpointRevision}, err
}

func (store *failingDrainStore) SaveCAS(context.Context, CheckpointWrite) (CheckpointWriteResult, error) {
	return CheckpointWriteResult{}, errors.New("injected final flush failure")
}

func TestRuntimeActivatesFromCheckpointLoaderOnce(t *testing.T) {
	var calls atomic.Int32
	runtime, err := NewRuntimeWithStore(checkpointLoaderFunc(func(_ context.Context, playerID uint64) (*State, error) {
		calls.Add(1)
		state := NewDevelopmentState(playerID)
		state.Coins = 77
		state.PlayerSeq = 9
		return state, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	for _, requestID := range []string{"first", "second"} {
		response, handleErr := runtime.Handle(
			context.Background(), 42, LocalOwnerEpoch, snapshotRequest(42, requestID),
		)
		if handleErr != nil {
			t.Fatal(handleErr)
		}
		if response.StateVersion.GetPlayerSeq() != 9 ||
			response.GetGetPlayerSnapshotResponse().GetSnapshot().GetCoinBalance() != 77 {
			t.Fatalf("response did not use loaded state: %+v", response)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("checkpoint load calls = %d, want 1", calls.Load())
	}
}

func TestRuntimePreservesAndAdvancesOpaqueStoreToken(t *testing.T) {
	const playerID = uint64(42)
	store := &recordingCheckpointStore{
		state:    NewDevelopmentState(playerID),
		token:    StoreToken("mysql-or-tcaplus-v1"),
		newToken: StoreToken("mysql-or-tcaplus-v2"),
	}
	runtime := NewRuntime()
	runtime.store = store
	defer runtime.Close()

	response, err := runtime.Handle(
		context.Background(), playerID, LocalOwnerEpoch,
		buySeedsRequest(playerID, "00112233-4455-6677-8899-aabbccddee23", 1),
	)
	if err != nil || response.GetError() != nil {
		t.Fatalf("buy before token flush failed: response=%+v error=%v", response, err)
	}
	if err := runtime.flushDirty(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.expectedToken) != 1 ||
		!bytes.Equal(store.expectedToken[0], store.token) {
		t.Fatalf("SaveCAS expected token = %q, want %q", store.expectedToken, store.token)
	}
	if !bytes.Equal(runtime.actors[playerID].persistedToken, store.newToken) {
		t.Fatalf(
			"persisted token = %q, want %q",
			runtime.actors[playerID].persistedToken, store.newToken,
		)
	}
}

func TestRuntimeDoesNotCreateDefaultStateWhenCheckpointLoadFails(t *testing.T) {
	// Load NotFound 但 Store 不支持 CreateInitial 时，必须失败，不能退回内存默认农田。
	runtime, err := NewRuntimeWithStore(checkpointLoaderFunc(func(context.Context, uint64) (*State, error) {
		return nil, ErrCheckpointNotFound
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, err := runtime.Handle(
		context.Background(), 42, LocalOwnerEpoch, snapshotRequest(42, "missing"),
	); !errors.Is(err, ErrInitialCheckpointUnsupported) {
		t.Fatalf("Handle() error = %v, want ErrInitialCheckpointUnsupported", err)
	}
}

func TestRuntimeNeverTreatsRetryableLoadFailureAsNewPlayer(t *testing.T) {
	runtime, err := NewRuntimeWithStore(checkpointLoaderFunc(func(context.Context, uint64) (*State, error) {
		return nil, ErrCheckpointRetryable
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	_, err = runtime.Handle(
		context.Background(), 42, LocalOwnerEpoch, snapshotRequest(42, "retryable"),
	)
	if err == nil || errors.Is(err, ErrInitialCheckpointUnsupported) {
		t.Fatalf("Handle() error = %v, want wrapped retryable load failure", err)
	}
	if !errors.Is(err, ErrCheckpointRetryable) {
		t.Fatalf("Handle() error = %v, want ErrCheckpointRetryable", err)
	}
}

func TestBuySeedsIsIdempotentAndFlushesCheckpointCAS(t *testing.T) {
	const playerID = uint64(42)
	fixedNow := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	state := NewDevelopmentState(playerID)
	state.CreatedAtMS = fixedNow.UnixMilli()
	state.UpdatedAtMS = fixedNow.UnixMilli()
	store := &recordingCheckpointStore{state: state}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time {
		return fixedNow
	}
	defer runtime.Close()

	requestID := "00112233-4455-6677-8899-aabbccddeeff"
	first, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		buySeedsRequest(playerID, requestID, 3))
	if err != nil {
		t.Fatal(err)
	}
	if first.GetError() != nil || first.GetReplayed() ||
		first.GetStateVersion().GetPlayerSeq() != 1 ||
		first.GetBuySeedsResponse().GetPatch().GetCoinBalance() != 4 {
		t.Fatalf("unexpected first BUY_SEEDS response: %+v", first)
	}

	replay, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		buySeedsRequest(playerID, requestID, 3))
	if err != nil {
		t.Fatal(err)
	}
	if !replay.GetReplayed() || replay.GetStateVersion().GetPlayerSeq() != 1 {
		t.Fatalf("unexpected replay response: %+v", replay)
	}

	conflict, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		buySeedsRequest(playerID, requestID, 1))
	if err != nil {
		t.Fatal(err)
	}
	if conflict.GetError().GetCode() != wsv1.ErrorCode_REQUEST_ID_CONFLICT {
		t.Fatalf("unexpected conflict response: %+v", conflict)
	}

	if err := runtime.flushDirty(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 1 || store.expectedRevision[0] != 1 {
		t.Fatalf("unexpected checkpoint writes: count=%d expected=%v", len(store.saved), store.expectedRevision)
	}
	checkpoint := store.saved[0]
	if checkpoint.PlayerSeq != 1 || checkpoint.CheckpointRevision != 2 ||
		checkpoint.CoinBalance != 4 || len(checkpoint.RecentResults) != 1 {
		t.Fatalf("unexpected dirty checkpoint: %+v", checkpoint)
	}
}

func TestBuyFertilizerIsIdempotentAndDoesNotAdvanceSeedTask(t *testing.T) {
	const playerID = uint64(42)
	runtime := NewRuntime()
	defer runtime.Close()
	requestID := "00112233-4455-6677-8899-aabbccddee10"

	first, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		buyFertilizerRequest(playerID, requestID, 2))
	if err != nil {
		t.Fatal(err)
	}
	if first.GetError() != nil || first.GetStateVersion().GetPlayerSeq() != 1 ||
		first.GetBuyFertilizerResponse().GetTotalPrice() != 4 ||
		first.GetBuyFertilizerResponse().GetPatch().GetInventoryUpserts()[0].GetQuantity() != 3 {
		t.Fatalf("unexpected first BUY_FERTILIZER response: %+v", first)
	}
	if task := first.GetBuyFertilizerResponse().GetPatch().GetCurrentChapter().GetTasks()[0]; task.GetCurrentValue() != 0 {
		t.Fatalf("fertilizer purchase advanced seed task: %+v", task)
	}
	replay, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		buyFertilizerRequest(playerID, requestID, 2))
	if err != nil {
		t.Fatal(err)
	}
	if !replay.GetReplayed() || replay.GetStateVersion().GetPlayerSeq() != 1 {
		t.Fatalf("unexpected fertilizer replay: %+v", replay)
	}
	tooMany, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		buyFertilizerRequest(playerID, "00112233-4455-6677-8899-aabbccddee11", 51))
	if err != nil {
		t.Fatal(err)
	}
	if tooMany.GetError().GetCode() != wsv1.ErrorCode_INVALID_ARGUMENT {
		t.Fatalf("quantity > 50 response: %+v", tooMany)
	}
}

func TestGetShopUsesAtomicallyReplacedConfigSnapshot(t *testing.T) {
	runtime := NewRuntime()
	defer runtime.Close()
	snapshot, err := NewConfigSnapshot(2, []ShopEntry{
		{ShopEntryID: 7002, ItemID: 1002, UnitPrice: 3, PriceVersion: 4, Enabled: true},
		{ShopEntryID: 7001, ItemID: 1001, UnitPrice: 2, PriceVersion: 3, Enabled: true},
		{ShopEntryID: 7003, ItemID: 1003, UnitPrice: 4, PriceVersion: 5, Enabled: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReplaceConfig(snapshot); err != nil {
		t.Fatal(err)
	}

	response, err := runtime.Handle(context.Background(), 42, LocalOwnerEpoch,
		getShopRequest(42, "shop-request"))
	if err != nil {
		t.Fatal(err)
	}
	shop := response.GetGetShopResponse()
	if response.GetStateVersion() != nil || shop.GetServerConfigVersion() != 2 ||
		len(shop.GetEntries()) != 2 ||
		shop.GetEntries()[0].GetShopEntryId() != 7001 ||
		shop.GetEntries()[1].GetShopEntryId() != 7002 {
		t.Fatalf("unexpected GET_SHOP response: %+v", response)
	}
	if len(runtime.actors) != 0 {
		t.Fatal("GET_SHOP activated a Player Actor")
	}
}

func TestBuySeedsUsesPinnedReplacementConfig(t *testing.T) {
	runtime := NewRuntime()
	defer runtime.Close()
	snapshot, err := NewConfigSnapshot(9, []ShopEntry{{
		ShopEntryID: 6001, ItemID: 2001, UnitPrice: 3, PriceVersion: 2, Enabled: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReplaceConfig(snapshot); err != nil {
		t.Fatal(err)
	}

	response, err := runtime.Handle(context.Background(), 42, LocalOwnerEpoch,
		buySeedsQuoteRequest(42, "00112233-4455-6677-8899-aabbccddee00", 6001, 2, 2))
	if err != nil {
		t.Fatal(err)
	}
	buy := response.GetBuySeedsResponse()
	if response.GetError() != nil || buy.GetItemId() != 2001 ||
		buy.GetUnitPrice() != 3 || buy.GetTotalPrice() != 6 ||
		buy.GetPatch().GetInventoryUpserts()[0].GetItemId() != 2001 {
		t.Fatalf("BUY_SEEDS did not use replacement config: %+v", response)
	}
}

func TestPlantIsIdempotentAndBatchesWithBuyInOneCheckpoint(t *testing.T) {
	const playerID = uint64(42)
	fixedNow := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	state := NewDevelopmentState(playerID)
	state.CreatedAtMS = fixedNow.UnixMilli()
	state.UpdatedAtMS = fixedNow.UnixMilli()
	store := &recordingCheckpointStore{state: state}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return fixedNow }
	defer runtime.Close()

	if _, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		buySeedsRequest(playerID, "00112233-4455-6677-8899-aabbccddee01", 3)); err != nil {
		t.Fatal(err)
	}
	request := plantRequest(playerID, "00112233-4455-6677-8899-aabbccddee02", 1, 1001)
	first, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, request)
	if err != nil {
		t.Fatal(err)
	}
	plant := first.GetPlantResponse()
	plot := plant.GetPatch().GetPlotUpserts()[0]
	if first.GetError() != nil || first.GetStateVersion().GetPlayerSeq() != 2 ||
		plant.GetPatch().GetInventoryUpserts()[0].GetQuantity() != 2 ||
		plot.GetPlotState() != plotv1.PlotState_GROWING ||
		plot.GetCropId() != 2001 ||
		plot.GetEstimatedMatureAtMs() != fixedNow.Add(100*time.Second).UnixMilli() {
		t.Fatalf("unexpected PLANT response: %+v", first)
	}
	replay, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, request)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.GetReplayed() || replay.GetStateVersion().GetPlayerSeq() != 2 ||
		!proto.Equal(replay.GetPlantResponse(), plant) {
		t.Fatalf("unexpected PLANT replay: %+v", replay)
	}

	if err := runtime.flushDirty(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 1 || store.expectedRevision[0] != 1 {
		t.Fatalf("unexpected batched checkpoint writes: count=%d expected=%v", len(store.saved), store.expectedRevision)
	}
	checkpoint := store.saved[0]
	if checkpoint.PlayerSeq != 2 || checkpoint.CheckpointRevision != 3 ||
		len(checkpoint.RecentResults) != 2 ||
		len(checkpoint.Plots) != int(InitialPlotCount) ||
		checkpoint.Plots[0].State != datav1.PlotRecordState_GROWING ||
		checkpoint.Plots[0].CropId != 2001 ||
		checkpoint.Plots[0].BaseGrowthRate.GetScaledValue() != 1_000_000 {
		t.Fatalf("unexpected PLANT checkpoint: %+v", checkpoint)
	}
	for _, untouched := range checkpoint.Plots[1:] {
		if untouched.State != datav1.PlotRecordState_EMPTY {
			t.Fatalf("PLANT modified secondary plot: %+v", untouched)
		}
	}
}

// TestPlantFreezesStealFieldsAndRoundTripsThroughCheckpoint verifies §1's
// freeze-at-plant contract: PLANT copies the CropConfig's steal_quantity/
// max_steal_times/protected_owner_yield onto the Plot exactly like
// base_yield, and those frozen values (fields 16-19 in PlotStateRecord)
// survive a checkpoint marshal/unmarshal round trip unchanged.
func TestPlantFreezesStealFieldsAndRoundTripsThroughCheckpoint(t *testing.T) {
	const playerID = uint64(42)
	fixedNow := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	state := NewDevelopmentState(playerID)
	state.CreatedAtMS = fixedNow.UnixMilli()
	state.UpdatedAtMS = fixedNow.UnixMilli()
	store := &recordingCheckpointStore{state: state}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return fixedNow }
	defer runtime.Close()

	response, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		buySeedsRequest(playerID, "00112233-4455-6677-8899-aabbccddee20", 1))
	if err != nil {
		t.Fatal(err)
	}
	if response.GetError() != nil {
		t.Fatalf("BUY_SEEDS: %+v", response)
	}
	response, err = runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		plantRequest(playerID, "00112233-4455-6677-8899-aabbccddee21", 1, 1001))
	if err != nil {
		t.Fatal(err)
	}
	if response.GetError() != nil {
		t.Fatalf("PLANT: %+v", response)
	}

	plot := response.GetPlantResponse().GetPatch().GetPlotUpserts()[0]
	if plot.GetCropId() != developmentCropID {
		t.Fatalf("PLANT response plot mismatch: %+v", plot)
	}

	if err := runtime.flushDirty(context.Background()); err != nil {
		t.Fatal(err)
	}
	checkpoint := store.saved[len(store.saved)-1]
	record := checkpoint.Plots[0]
	if record.StealQuantity != developmentStealQuantity ||
		record.MaxStealTimes != developmentMaxStealTimes ||
		record.ProtectedOwnerYield != developmentProtectedOwnerYield {
		t.Fatalf("checkpoint did not persist frozen steal fields: %+v", record)
	}

	body, digest, err := MarshalCheckpoint(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalCheckpoint(body, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	restored, err := StateFromCheckpoint(decoded)
	if err != nil {
		t.Fatal(err)
	}
	restoredPlot := restored.Plots[1]
	if restoredPlot.StealQuantity != developmentStealQuantity ||
		restoredPlot.MaxStealTimes != developmentMaxStealTimes ||
		restoredPlot.ProtectedOwnerYield != developmentProtectedOwnerYield {
		t.Fatalf("restored plot lost frozen steal fields: %+v", restoredPlot)
	}
}

func TestApplyFertilizerSettlesOldRateAndPersistsEffect(t *testing.T) {
	const playerID = uint64(42)
	fixedNow := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	state := NewDevelopmentState(playerID)
	state.CreatedAtMS = fixedNow.UnixMilli()
	state.UpdatedAtMS = fixedNow.UnixMilli()
	store := &recordingCheckpointStore{state: state}
	runtime := NewRuntime()
	runtime.store = store
	runtime.now = func() time.Time { return fixedNow }
	defer runtime.Close()

	if _, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		buySeedsRequest(playerID, "00112233-4455-6677-8899-aabbccddee11", 3)); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch,
		plantRequest(playerID, "00112233-4455-6677-8899-aabbccddee12", 1, 1001)); err != nil {
		t.Fatal(err)
	}
	request := fertilizerRequest(playerID, "00112233-4455-6677-8899-aabbccddee13", 1, 1)
	first, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, request)
	if err != nil {
		t.Fatal(err)
	}
	applied := first.GetApplyFertilizerResponse()
	plot := applied.GetPatch().GetPlotUpserts()[0]
	if first.GetError() != nil || first.GetStateVersion().GetPlayerSeq() != 3 ||
		applied.GetEffectInstanceId() == "" ||
		len(applied.GetPatch().GetInventoryRemovedItemIds()) != 1 ||
		plot.GetFertilizerEffect() == nil ||
		plot.GetEstimatedMatureAtMs() != fixedNow.Add(70*time.Second).UnixMilli() {
		t.Fatalf("unexpected APPLY_FERTILIZER response: %+v", first)
	}
	replay, err := runtime.Handle(context.Background(), playerID, LocalOwnerEpoch, request)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.GetReplayed() || replay.GetStateVersion().GetPlayerSeq() != 3 ||
		!proto.Equal(replay.GetApplyFertilizerResponse(), applied) {
		t.Fatalf("unexpected fertilizer replay: %+v", replay)
	}
	if err := runtime.flushDirty(context.Background()); err != nil {
		t.Fatal(err)
	}
	checkpoint := store.saved[0]
	if checkpoint.PlayerSeq != 3 || checkpoint.CheckpointRevision != 4 ||
		len(checkpoint.RecentResults) != 3 ||
		checkpoint.Plots[0].FertilizerEffect == nil ||
		checkpoint.Plots[0].EstimatedMatureAtMs == nil {
		t.Fatalf("unexpected fertilizer checkpoint: %+v", checkpoint)
	}
}
