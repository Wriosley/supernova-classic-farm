# Asynchronous Friend Action Effects Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the synchronous multi-row friend-action Saga for steal, pest, catch, and clean with Owner-authoritative commits plus durable asynchronous Visitor effects.

**Architecture:** Owner Zone creates an immutable PREPARED PlayerOutbox row before entering the Owner Actor, then commits the Owner mutation and OWNER Receipt in one PlayerCheckpoint CAS and returns success. A bounded Zone-level dispatcher verifies the OWNER Receipt, routes the effect to the current Visitor Zone, where the Visitor Actor applies the effect and VISITOR Receipt in one CAS; FriendInteraction receives one terminal audit record only.

**Tech Stack:** Go, Protobuf, gRPC, TcaplusDB, Player Actor mailboxes, Coordinator routing SDK, Vue 3/Protobuf WebSocket, kind E2E.

## Global Constraints

- The design source is `docs/archive/development/superpowers/specs/2026-08-14-asynchronous-friend-action-effects-design.md` and remains `proposed` until the owner can explain its trade-offs and accepts it.
- Cover `STEAL_FRIEND_CROP`, `APPLY_PEST_TO_FRIEND`, `CATCH_PEST_FOR_FRIEND`, and `HELP_CLEAN_FRIEND_PLOT` through one infrastructure.
- Catch and clean grant exactly one coin; pest grants no item or coin; steal grants the frozen crop quantity and applies the frozen guard penalty.
- OWNER/VISITOR Receipt is authoritative for the corresponding Player mutation; PlayerOutbox is authoritative for unfinished delivery; FriendInteraction is terminal audit only.
- No unbounded goroutines, unbounded queues, or normal-operation full-table polling.
- Tcaplus field names must be at most 31 characters.
- This phase records one live response latency and one live Visitor-effect latency. p50/p95/p99 and capacity testing are a separate follow-up.
- Preserve the old Saga behind a default-off feature flag until the new E2E and rollback gate pass.
- Execute this plan in an isolated worktree because the current master workspace contains unrelated uncommitted work.

---

## File Structure

- `proto/classicfarm/v1/data/data_model.proto`: new outbox event/status enums.
- `proto/classicfarm/v1/event/event.proto`: immutable prepared-action payload.
- `proto/classicfarm/v1/rpc/runtime.proto`: Visitor-effect delivery RPC.
- `deploy/tcaplus/schema/classicfarm/v1/tcaplus/runtime_tables.proto`: durable outbox lifecycle fields; no new table.
- `server/internal/interaction/prepared_store.go`: PREPARED/READY/terminal PlayerOutbox state transitions.
- `server/internal/interaction/effect.go`: action-neutral effect model and payload validation.
- `server/internal/interaction/dispatcher.go`: Zone open-task index, ready queue, retry heap, workers, and overload snapshot.
- `server/internal/interaction/effect_client.go`: Coordinator-resolved Visitor Zone delivery.
- `server/internal/interaction/finalizer.go`: one terminal FriendInteraction insert.
- `server/internal/player/friend_effect.go`: Visitor mailbox effect application and VISITOR Receipt.
- `server/internal/player/receipt_index.go`: O(1) in-memory Receipt lookup rebuilt on activation.
- `server/cmd/zone/friend_rpc.go`: Owner prepare/commit boundary and Visitor-effect RPC wiring.
- `server/cmd/zone/main.go`: feature flag, dispatcher lifecycle, startup recovery, and shard-acquisition recovery.
- `web/src/App.vue`: treat friend-action response as Owner success and wait for real `PLAYER_STATE_CHANGED` for Visitor state.

---

### Task 1: Freeze Protocol and Tcaplus Lifecycle

**Files:**
- Modify: `proto/classicfarm/v1/data/data_model.proto`
- Modify: `proto/classicfarm/v1/event/event.proto`
- Modify: `proto/classicfarm/v1/rpc/runtime.proto`
- Modify: `deploy/tcaplus/schema/classicfarm/v1/tcaplus/runtime_tables.proto`
- Create: `server/internal/interaction/protocol_test.go`
- Regenerate: `server/gen/classicfarm/v1/**`, `web/src/gen/classicfarm/v1/**`

**Interfaces:**
- Produces: `OutboxEventType_APPLY_FRIEND_ACTION_EFFECT`, `OutboxRelayStatus`, `ApplyFriendActionEffectV1`, and `VisitorZoneService.ApplyFriendActionEffect`.
- `ApplyFriendActionEffectV1` contains request facts only; mutable/frozen Owner results come from OWNER Receipt.

- [ ] **Step 1: Write descriptor tests that fail before schema changes**

```go
func TestFriendActionOutboxProtocol(t *testing.T) {
	if datav1.OutboxEventType_APPLY_FRIEND_ACTION_EFFECT == 0 { t.Fatal("event type missing") }
	fields := (&tcaplusv1.PlayerOutbox{}).ProtoReflect().Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		if len(string(fields.Get(i).Name())) > 31 { t.Fatalf("field too long: %s", fields.Get(i).Name()) }
	}
}
```

- [ ] **Step 2: Run the test and verify the missing generated symbols fail compilation**

Run: `cd server && go test ./internal/interaction -run TestFriendActionOutboxProtocol -count=1`

Expected: FAIL because `APPLY_FRIEND_ACTION_EFFECT` and lifecycle enum do not exist.

- [ ] **Step 3: Add exact protocol values without renumbering existing values**

```protobuf
enum OutboxEventType {
  OUTBOX_EVENT_TYPE_UNSPECIFIED = 0;
  CREATE_REWARD_MAIL = 1;
  CREATE_GIFT_MAIL = 2;
  APPLY_FRIEND_ACTION_EFFECT = 3;
}

enum OutboxRelayStatus {
  OUTBOX_RELAY_STATUS_UNSPECIFIED = 0;
  OUTBOX_RELAY_PENDING = 1;   // preserves existing rows
  OUTBOX_RELAY_CLAIMED = 2;
  OUTBOX_RELAY_DELIVERED = 3; // preserves existing rows
  OUTBOX_RELAY_PREPARED = 4;
  OUTBOX_RELAY_CANCELED = 5;
  OUTBOX_RELAY_CORRUPT = 6;
}
```

Add `ApplyFriendActionEffectV1` with interaction/request identity, owner/visitor IDs, owner shard, visit/plot/crop/pest/farm-view fields, and add request/response messages for the Visitor RPC. Keep every Tcaplus field name at most 31 characters.

- [ ] **Step 4: Generate Go/TypeScript code and verify checked-in output**

Run: `buf generate`

Expected: generated Go and TypeScript compile with the new symbols; no hand-edited generated files.

- [ ] **Step 5: Run protocol and descriptor tests**

Run: `cd server && go test ./internal/interaction ./cmd/zone -run 'Protocol|Descriptor|FieldName' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add proto deploy/tcaplus/schema server/gen web/src/gen server/internal/interaction/protocol_test.go
git commit -m "feat(friend): define async action effect protocol"
```

---

### Task 2: Add Idempotent PREPARED Outbox Store

**Files:**
- Create: `server/internal/interaction/prepared_store.go`
- Create: `server/internal/interaction/prepared_store_test.go`
- Modify: `server/internal/outbox/store.go`

**Interfaces:**
- Produces:

```go
type PreparedStore interface {
	EnsurePrepared(context.Context, *tcaplusv1.PlayerOutbox) (*tcaplusv1.PlayerOutbox, error)
	Get(context.Context, []byte) (*tcaplusv1.PlayerOutbox, error)
	MarkReady(context.Context, []byte, []byte) error
	Claim(context.Context, []byte, string, time.Time, time.Time) (*tcaplusv1.PlayerOutbox, error)
	Retry(context.Context, []byte, int64, string) error
	MarkDelivered(context.Context, []byte, int64) error
	MarkCanceled(context.Context, []byte, int64) error
	MarkCorrupt(context.Context, []byte, string) error
	RecoverOwned(context.Context, func(uint32) bool) ([]*tcaplusv1.PlayerOutbox, error)
}
```

- Consumes: immutable payload/digest and lifecycle enum from Task 1.

- [ ] **Step 1: Write failing memory-store lifecycle tests**

Cover exact replay, immutable conflict, PREPARED claim refusal without Owner proof, expired claim recovery, retry scheduling, DELIVERED terminal retry, and ownership-filtered recovery.

```go
func TestEnsurePreparedRejectsImmutableConflict(t *testing.T) {
	store := NewMemoryPreparedStore()
	first := preparedFixture("event-1", []byte("digest-a"))
	_, _ = store.EnsurePrepared(context.Background(), first)
	conflict := preparedFixture("event-1", []byte("digest-b"))
	if _, err := store.EnsurePrepared(context.Background(), conflict); !errors.Is(err, ErrPreparedConflict) {
		t.Fatalf("err=%v", err)
	}
}
```

- [ ] **Step 2: Run and verify failure**

Run: `cd server && go test ./internal/interaction -run 'TestEnsurePrepared|TestPreparedStore' -count=1`

Expected: FAIL because store types do not exist.

- [ ] **Step 3: Implement memory and Tcaplus CAS transitions**

Use Tcaplus record version for every mutable transition. Never overwrite immutable event identity/payload/digest. Map already-exists with exact immutable equality to replay success.

- [ ] **Step 4: Run store tests including race**

Run: `cd server && go test -race ./internal/interaction ./internal/outbox -run 'Prepared|Outbox' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/interaction/prepared_store.go server/internal/interaction/prepared_store_test.go server/internal/outbox/store.go
git commit -m "feat(friend): add prepared action outbox store"
```

---

### Task 3: Build O(1) Receipt Index and Unified Visitor Effect

**Files:**
- Create: `server/internal/player/receipt_index.go`
- Create: `server/internal/player/receipt_index_test.go`
- Create: `server/internal/player/friend_effect.go`
- Create: `server/internal/player/friend_effect_test.go`
- Modify: `server/internal/player/state.go`
- Modify: `server/internal/player/runtime.go`
- Modify: `server/internal/player/checkpoint.go`

**Interfaces:**
- Produces:

```go
type FriendEffect struct {
	InteractionID []byte
	RequestDigest []byte
	Action datav1.FriendInteractionAction
	CropItemID, CropQuantity uint32
	CoinDelta int64
	GuardPenalty int64
	OwnerResultPayload []byte
}

func (r *Runtime) ApplyFriendEffect(context.Context, uint64, uint64, FriendEffect) (*wsv1.PlayerStatePatch, bool, error)
```

- [ ] **Step 1: Write failing tests for all four actions and replay**

Assert steal adds the frozen crop and penalty once; pest adds no item/coin but consumes chance/task once; catch and clean add exactly one coin; identical replay returns `alreadyApplied`; digest conflict returns a corrupt-conflict error.

- [ ] **Step 2: Run and verify failure**

Run: `cd server && go test ./internal/player -run 'TestApplyFriendEffect|TestReceiptIndex' -count=1`

Expected: FAIL because the unified effect API is absent.

- [ ] **Step 3: Build index during activation and checkpoint load**

```go
type receiptKey struct { id string; role datav1.FriendReceiptRole }

func rebuildReceiptIndex(receipts []*datav1.FriendInteractionReceipt) map[receiptKey]*datav1.FriendInteractionReceipt
```

Keep `FriendReceipts` as durable state; the map is a non-serialized index. Never remove a Receipt on a five-second cache expiry.

- [ ] **Step 4: Implement mailbox effect and one sync SaveCAS**

Inside the Visitor mailbox, check the index, apply action-specific changes, append VISITOR Receipt, update the index, increment player/checkpoint versions, and call the existing sync-persist boundary exactly once.

- [ ] **Step 5: Run focused Player tests with race**

Run: `cd server && go test -race ./internal/player -run 'TestApplyFriendEffect|TestReceiptIndex|FriendAction' -count=1`

Expected: PASS and a recording store observes one Visitor SaveCAS per first application and zero per replay.

- [ ] **Step 6: Commit**

```bash
git add server/internal/player/receipt_index.go server/internal/player/receipt_index_test.go server/internal/player/friend_effect.go server/internal/player/friend_effect_test.go server/internal/player/state.go server/internal/player/runtime.go server/internal/player/checkpoint.go
git commit -m "feat(friend): apply visitor effects idempotently"
```

---

### Task 4: Move PREPARED Creation into Owner RPC Boundary

**Files:**
- Modify: `server/cmd/zone/friend_rpc.go`
- Modify: `server/cmd/zone/friend_rpc_steal_test.go`
- Create: `server/cmd/zone/friend_rpc_async_test.go`
- Modify: `server/internal/player/steal_crop.go`
- Modify: `server/internal/player/apply_pest.go`
- Modify: `server/internal/player/catch_pest.go`
- Modify: `server/internal/player/help_clean_plot.go`

**Interfaces:**
- Consumes: `PreparedStore.EnsurePrepared` and existing Owner `ApplyVisitorAction` actor methods.
- Produces: Owner RPC behavior `ensure PREPARED -> Owner mailbox SaveCAS -> dispatcher.Register`.

- [ ] **Step 1: Write failure-window tests**

Test Outbox insert failure causes no Owner mutation; Owner rejection leaves PREPARED non-deliverable; Owner SaveCAS success writes an OWNER Receipt whose result digest matches the RPC response; exact replay does not mutate the plot twice.

- [ ] **Step 2: Run and verify current RPC violates the new order**

Run: `cd server && go test ./cmd/zone -run 'TestAsyncOwner|TestPreparedBeforeOwner' -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement immutable prepared payload construction**

Build payload from request facts before Owner evaluation. Put frozen guard outcome and final Owner result only in OWNER Receipt/result payload; do not mutate Outbox immutable payload.

- [ ] **Step 4: Implement action-specific OWNER Receipt linkage**

Add the Outbox event ID/request digest to the result payload or receipt validation material so the dispatcher can prove PREPARED belongs to the committed Owner result.

- [ ] **Step 5: Run Owner tests**

Run: `cd server && go test -race ./cmd/zone ./internal/player -run 'AsyncOwner|PreparedBeforeOwner|ApplySteal|ApplyPest|CatchPest|HelpClean' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server/cmd/zone/friend_rpc.go server/cmd/zone/friend_rpc_steal_test.go server/cmd/zone/friend_rpc_async_test.go server/internal/player/steal_crop.go server/internal/player/apply_pest.go server/internal/player/catch_pest.go server/internal/player/help_clean_plot.go
git commit -m "feat(friend): commit owner actions behind prepared outbox"
```

---

### Task 5: Add Visitor Effect RPC and Coordinator-Routed Client

**Files:**
- Create: `server/internal/interaction/effect_client.go`
- Create: `server/internal/interaction/effect_client_test.go`
- Modify: `server/cmd/zone/friend_rpc.go`
- Modify: `server/cmd/zone/grpc_server.go`
- Modify: `server/cmd/zone/grpc_server_test.go`

**Interfaces:**
- Produces:

```go
type EffectClient interface {
	Apply(context.Context, *tcaplusv1.PlayerOutbox, []byte) (EffectResult, error)
}

type EffectResult struct {
	AlreadyApplied bool
	ResultPayload []byte
	ResultDigest []byte
}
```

- [ ] **Step 1: Write failing authorization, route-refresh, and receipt-replay tests**

Require internal HMAC caller `zone`; reject wrong Visitor Shard/epoch; map FailedPrecondition to route invalidation and one re-resolve; assert same-Zone adapter still enters `Runtime.ApplyFriendEffect`.

- [ ] **Step 2: Run and verify failure**

Run: `cd server && go test ./internal/interaction ./cmd/zone -run 'EffectClient|ApplyFriendActionEffect' -count=1`

Expected: FAIL because RPC/client is not wired.

- [ ] **Step 3: Implement server and client**

Resolve Visitor route for every delivery, carry route identity, validate authorization, and call the Visitor Actor API. Do not write Visitor Tcaplus directly from the dispatcher.

- [ ] **Step 4: Run focused gRPC tests**

Run: `cd server && go test -race ./internal/interaction ./cmd/zone -run 'EffectClient|ApplyFriendActionEffect|GRPC' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/interaction/effect_client.go server/internal/interaction/effect_client_test.go server/cmd/zone/friend_rpc.go server/cmd/zone/grpc_server.go server/cmd/zone/grpc_server_test.go
git commit -m "feat(friend): route async effects to visitor actors"
```

---

### Task 6: Implement Bounded Zone Dispatcher and Terminal Finalizer

**Files:**
- Create: `server/internal/interaction/dispatcher.go`
- Create: `server/internal/interaction/dispatcher_test.go`
- Create: `server/internal/interaction/finalizer.go`
- Create: `server/internal/interaction/finalizer_test.go`
- Modify: `server/internal/interaction/store.go`

**Interfaces:**
- Produces:

```go
type Dispatcher interface {
	Register([]byte) error
	Recover(context.Context, func(uint32) bool) error
	Snapshot() BacklogSnapshot
	Run(context.Context)
}

type BacklogSnapshot struct { Pending int; OldestAge time.Duration; BusyWorkers int }
```

- [ ] **Step 1: Write deterministic dispatcher tests with fake clock**

Cover immediate Register, event-ID dedup, bounded concurrent deliveries, retry heap ordering, expired claims, queue saturation without task loss, fair Visitor-Shard scheduling, terminal FriendInteraction insert, and DELIVERED ordering.

- [ ] **Step 2: Run and verify failure**

Run: `cd server && go test ./internal/interaction -run 'TestDispatcher|TestFinalizer' -count=1`

Expected: FAIL because dispatcher/finalizer do not exist.

- [ ] **Step 3: Implement the minimal bounded scheduler**

Use one dispatcher goroutine to own `openTasks` and retry heap; use a configured fixed worker count. Worker sequence is: load/claim, validate OWNER Receipt, effect RPC, terminal finalizer, MarkDelivered. Persist Retry before re-inserting into retry heap.

- [ ] **Step 4: Add conservative overload decision**

```go
func (s BacklogSnapshot) Reject(limit int, maxAge time.Duration) bool {
	return s.Pending >= limit || s.OldestAge >= maxAge
}
```

The caller checks this before PREPARED creation/Owner mutation and returns retryable `SYSTEM_BUSY`.

- [ ] **Step 5: Run race and leak-sensitive tests**

Run: `cd server && go test -race ./internal/interaction -run 'Dispatcher|Finalizer|Backlog' -count=1`

Expected: PASS with worker concurrency never exceeding the configured limit.

- [ ] **Step 6: Commit**

```bash
git add server/internal/interaction/dispatcher.go server/internal/interaction/dispatcher_test.go server/internal/interaction/finalizer.go server/internal/interaction/finalizer_test.go server/internal/interaction/store.go
git commit -m "feat(friend): dispatch durable visitor effects"
```

---

### Task 7: Wire Lifecycle, Startup Recovery, and Shard Acquisition

**Files:**
- Modify: `server/cmd/zone/main.go`
- Modify: `server/cmd/zone/lifecycle.go`
- Modify: `server/cmd/zone/lifecycle_test.go`
- Create: `server/cmd/zone/interaction_dispatcher_test.go`
- Modify: `deploy/k8s/configmap.yaml`

**Interfaces:**
- Consumes: Dispatcher from Task 6 and current routing ownership callbacks/gates.
- Produces: `FRIEND_ACTION_ASYNC_ENABLED`, worker/queue/backlog settings, startup recovery, and ACTIVE-Shard acquisition recovery.

- [ ] **Step 1: Write failing lifecycle tests**

Assert default flag is off; enabled Zone starts workers before accepting async actions; startup rebuilds pending/retry state; acquiring an ACTIVE Shard triggers recovery for that Shard; losing ownership stops new claims; shutdown drains or releases claims without losing durable tasks.

- [ ] **Step 2: Run and verify failure**

Run: `cd server && go test ./cmd/zone -run 'InteractionDispatcher|ShardAcquisition|Lifecycle' -count=1`

Expected: FAIL.

- [ ] **Step 3: Wire conservative defaults and readiness**

Start dispatcher with a small fixed worker count and bounded scheduling settings. Recovery discovery must complete before the async path is Ready, but historical task execution itself must not block Zone readiness indefinitely.

- [ ] **Step 4: Run lifecycle tests with race**

Run: `cd server && go test -race ./cmd/zone -run 'InteractionDispatcher|ShardAcquisition|Lifecycle|Readiness' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/cmd/zone/main.go server/cmd/zone/lifecycle.go server/cmd/zone/lifecycle_test.go server/cmd/zone/interaction_dispatcher_test.go deploy/k8s/configmap.yaml
git commit -m "feat(friend): wire async dispatcher lifecycle"
```

---

### Task 8: Cut Over Four Actions and Preserve Rollback

**Files:**
- Modify: `server/cmd/zone/friend_rpc.go`
- Modify: `server/internal/interaction/saga.go`
- Modify: `server/internal/interaction/action_saga.go`
- Modify: `server/internal/interaction/reconciler.go`
- Modify: `server/internal/gateway/grpc_visitor.go`
- Modify: `web/src/App.vue`
- Modify: `web/src/__tests__/friend-farm-pet.spec.ts`
- Create: `server/cmd/zone/friend_action_cutover_test.go`

**Interfaces:**
- Produces: feature-flag selection in which one interaction uses exactly one path; async response means Owner committed and carries no speculative Visitor patch.

- [ ] **Step 1: Write failing cutover tests**

For each action assert enabled mode does not call reservation/CommitSteal/CommitActionChance before response; disabled mode retains old Saga; same interaction cannot be processed by both paths; frontend displays success without applying a speculative Visitor patch.

- [ ] **Step 2: Run and verify failure**

Run: `cd server && go test ./cmd/zone ./internal/interaction -run 'Cutover|LegacySaga' -count=1`

Run: `cd web && npm test -- --run src/__tests__/friend-farm-pet.spec.ts`

Expected: at least the async cutover expectations FAIL.

- [ ] **Step 3: Implement feature-flag branch and terminal-only semantics**

Keep legacy types/tests for rollback. In async mode return Owner result immediately, remove response dependence on Visitor patch/state version, and stop registering new records with the old periodic FriendInteraction Reconciler.

- [ ] **Step 4: Run backend and frontend focused tests**

Run: `cd server && go test -race ./cmd/zone ./internal/interaction ./internal/gateway -count=1`

Run: `cd web && npm run typecheck && npm test -- --run src/__tests__/friend-farm-pet.spec.ts`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/cmd/zone/friend_rpc.go server/cmd/zone/friend_action_cutover_test.go server/internal/interaction server/internal/gateway/grpc_visitor.go web/src/App.vue web/src/__tests__/friend-farm-pet.spec.ts
git commit -m "feat(friend): cut over actions to async effects"
```

---

### Task 9: Cover Every Crash Boundary and Migration Handoff

**Files:**
- Create: `server/internal/interaction/async_recovery_test.go`
- Create: `server/cmd/zone/friend_action_recovery_test.go`
- Modify: `server/test/e2e/friend_interaction_saga_recovery_test.go`
- Modify: `tests/e2e/run-friend-restart-recovery.sh`

**Interfaces:**
- Consumes: feature flag, prepared store, dispatcher, effect RPC, Receipts, and finalizer.
- Produces: deterministic evidence that every durable boundary converges.

- [ ] **Step 1: Add table-driven offline fault injection**

Inject after PREPARED, after OWNER Receipt, after response, before Visitor SaveCAS, after Visitor SaveCAS, after terminal FriendInteraction, and before DELIVERED. Assert exact Owner mutation once, exact Visitor effect once, and terminal convergence.

- [ ] **Step 2: Add route and ownership tests**

Cover Visitor NotOwner re-resolution, Owner Shard handoff, old Zone claim refusal, new Zone recovery, simultaneous duplicate workers, corrupt digest, and PREPARED without OWNER Receipt cancellation.

- [ ] **Step 3: Run focused recovery suite**

Run: `cd server && go test -race ./internal/interaction ./internal/player ./cmd/zone -run 'AsyncRecovery|FriendActionRecovery|Handoff' -count=1`

Expected: PASS.

- [ ] **Step 4: Run live restart recovery once**

Run: `tests/e2e/run-friend-restart-recovery.sh`

Expected: all four actions converge after the scripted restart, with no duplicate crop/coin/task/penalty.

- [ ] **Step 5: Commit**

```bash
git add server/internal/interaction/async_recovery_test.go server/cmd/zone/friend_action_recovery_test.go server/test/e2e/friend_interaction_saga_recovery_test.go tests/e2e/run-friend-restart-recovery.sh
git commit -m "test(friend): cover async effect recovery"
```

---

### Task 10: Run One Live Chain, Record Timing, and Update Handoff

**Files:**
- Modify: `server/test/e2e/friend_interaction_test.go`
- Modify: `tests/e2e/run-friend-interaction.sh`
- Create: `docs/evidence/2026-08-14-asynchronous-friend-action-effects.md`
- Modify: `docs/context/CURRENT.md`
- Modify: `docs/archive/development/superpowers/specs/2026-08-14-asynchronous-friend-action-effects-design.md`

**Interfaces:**
- Produces: one measured Owner-response duration and one Visitor-effect duration for each action that has a Visitor effect; no percentile claim.

- [ ] **Step 1: Add E2E timestamps around observable boundaries**

Record request write, Owner-success response, matching `PLAYER_STATE_CHANGED`, and final snapshot verification. For pest, verify task/chance effect without an item/coin reward; for catch/clean verify exactly one coin.

- [ ] **Step 2: Build and deploy changed Zone image with the feature flag enabled**

Run the repository's existing Zone image build/load and Kubernetes rollout commands, then verify all Zone Pods Ready and the intended flag value.

- [ ] **Step 3: Run the complete friend interaction E2E once**

Run: `tests/e2e/run-friend-interaction.sh`

Expected: PASS for steal, pest, catch, and clean; output includes single-sample response/effect durations.

- [ ] **Step 4: Run rollback smoke**

Disable `FRIEND_ACTION_ASYNC_ENABLED`, roll Zone, and run the focused legacy interaction smoke. Expected: old Saga remains functional and no async task is created.

- [ ] **Step 5: Record evidence without percentile claims**

Document exact commit/image/config, commands, single-sample timings, crash recovery result, limitations, and the explicit follow-up performance plan for p50/p95/p99/capacity.

- [ ] **Step 6: Mark design accepted only after owner explanation gate**

Update design status from `proposed` only if the owner can explain PREPARED/OWNER Receipt/VISITOR Receipt/Outbox recovery and accepts the cost of two synchronous durable writes. Otherwise leave it proposed and record the unresolved item.

- [ ] **Step 7: Run final offline regression**

Run: `cd server && go test -race ./internal/interaction ./internal/outbox ./internal/player ./internal/gateway ./cmd/zone -count=1`

Run: `cd web && npm run typecheck && npm run build`

Expected: PASS, with any unrelated pre-existing failure reported rather than hidden.

- [ ] **Step 8: Commit**

```bash
git add server/test/e2e/friend_interaction_test.go tests/e2e/run-friend-interaction.sh docs/evidence/2026-08-14-asynchronous-friend-action-effects.md docs/context/CURRENT.md docs/archive/development/superpowers/specs/2026-08-14-asynchronous-friend-action-effects-design.md
git commit -m "test(friend): verify async action effects"
```

---

## Deferred Performance Plan

After this functional plan passes, create a separate plan and evidence run for response/effect p50/p95/p99, sustained throughput, burst absorption, Worker count, queue capacity, overload thresholds, Tcaplus operation counts, and Shard-distributed recovery cost. None of those percentile or capacity claims may be inferred from the single live chain in Task 10.
