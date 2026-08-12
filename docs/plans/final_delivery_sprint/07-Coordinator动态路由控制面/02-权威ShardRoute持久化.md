# Durable Current ShardRoute Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:test-driven-development` while implementing this phase. Execute
> only after Phase 01 is complete and reviewed. Do not implement WatchRoutes or
> Kubernetes discovery in this task.

**Goal:** Make Tcaplus the restart authority for the complete Current ShardMap
and make durable Active ShardRoute—not the in-memory `Map.Activate` call—the
route commit point.

**Architecture:** Add one Tcaplus `ShardRoute` row per logical Shard plus one
`ShardMapMeta` row for global map-version allocation and algorithm metadata.
Introduce a `RouteStore` boundary with memory and Tcaplus implementations.
Bootstrap only an empty store; otherwise restore the exact Current snapshot.
Adapt the existing manual migration so PREPARING, Fence and ACTIVE transitions
are persisted before the in-memory map changes. Preserve HTTP lookup and the
static Zone list during this phase.

**Tech Stack:** Go 1.26, Tcaplus PB Generic tables, optimistic record-version
CAS, existing `routing.Map`, existing migration handler and tests.

## Global Constraints

- Read ADR-0012, `00-总路线图.md`, and Phase 01 before editing.
- Phase 01 generated types and error numbers are inputs; do not redesign them.
- Do not change Player→Shard or Rendezvous scoring.
- Tcaplus mode is the target. Preserve MySQL as a historical rollback adapter;
  do not add new MySQL ShardRoute tables in this phase.
- Static `zone-a/zone-b` configuration remains the bootstrap membership source.
- Do not implement Watch publication. “Published” still means visible through
  current HTTP route APIs after the in-memory map is updated.
- Do not change Gate/Info/Zone refresh behavior.
- Never update the in-memory Current to a new state before the matching durable
  ShardRoute state succeeds.
- Fence and ShardRoute are separate Tcaplus rows; do not claim a cross-row
  transaction. MigrationProgress and restart reconciliation close each crash
  window.
- Generated Tcaplus Go files come only from the deploy/tcaplus Buf command.

## Why `ShardMapMeta` Is Required

`map_version` orders changes across all 4096 Shards. Storing it only in
independent ShardRoute rows cannot safely allocate one monotonic global value
when two Shards change concurrently. Use one `ShardMapMeta` record as the CAS
serialization point for map versions and algorithm metadata.

This record does not replace ShardRoute or Fence:

```text
ShardMapMeta  -> global map_version and algorithm compatibility
ShardRoute    -> complete Current state of one Shard
ShardFence    -> owner_epoch allowed to persist Player data
MigrationProgress -> recoverable multi-row workflow
```

## Task 1: Add Tcaplus control table schemas

**Files:**

- Modify: `deploy/tcaplus/schema/classicfarm/v1/tcaplus/runtime_tables.proto`
- Generated: `server/gen/classicfarm/v1/tcaplus/runtime_tables.pb.go`
- Modify: `deploy/tcaplus/README.zh-CN.md`
- Modify: `server/internal/testtcaplus/client.go`
- Test: `server/internal/testtcaplus/client_test.go` if present; otherwise
  `server/internal/routing/tcaplus_route_store_test.go` in Task 3 covers it.

**Interfaces:**

Add these PB Generic messages. Tcaplus proto3 schemas must not use explicit
`optional`.

```proto
message ShardMapMeta {
  option (tcaplusservice.tcaplus_primary_key) = "map_id";

  uint32 map_id = 1; // always 1
  uint32 shard_count = 2;
  uint32 hash_algorithm_version = 3;
  uint32 assignment_algorithm_version = 4;
  uint64 map_version = 5;
  int64 updated_at_ms = 6;
  bool has_pending_commit = 7;
  uint32 pending_shard_id = 8;
  uint64 pending_map_version = 9;
  uint64 pending_route_version = 10;
  bytes pending_transition_id = 11;
  string pending_state = 12;
  string pending_owner_zone_id = 13;
  string pending_owner_endpoint = 14;
  uint64 pending_owner_epoch = 15;
  uint64 pending_lease_term = 16;
  bytes pending_lease_id = 17;
  int64 pending_lease_expires_at_ms = 18;
  string pending_previous_owner_zone_id = 19;
  int64 pending_updated_at_ms = 20;
}

message ShardRoute {
  option (tcaplusservice.tcaplus_primary_key) = "logical_shard_id";

  uint32 logical_shard_id = 1;
  string owner_zone_id = 2;
  string owner_endpoint = 3;
  uint64 owner_epoch = 4;
  uint64 route_version = 5;
  uint64 committed_map_version = 6;
  string state = 7;
  uint64 lease_term = 8;
  bytes lease_id = 9;
  int64 lease_expires_at_ms = 10;
  string previous_owner_zone_id = 11;
  bytes transition_id = 12;
  int64 updated_at_ms = 13;
}
```

`committed_map_version` is the map version at which that row last changed; it
need not equal the latest global map version for unchanged rows.

- [ ] **Step 1: Write a failing test using the future generated records**

Create `server/internal/routing/tcaplus_route_store_test.go` with a compile-time
test that inserts/loads `ShardMapMeta` and `ShardRoute` through
`testtcaplus.New()`.

Run:

```bash
cd server
go test ./internal/routing
```

Expected: FAIL because the generated types and fake-client keys do not exist.

- [ ] **Step 2: Add both schemas and fake-client key support**

In `recordKey`, add:

```go
case *tcaplusv1.ShardMapMeta:
    key = strconv.FormatUint(uint64(record.MapId), 10)
case *tcaplusv1.ShardRoute:
    key = strconv.FormatUint(uint64(record.LogicalShardId), 10)
```

- [ ] **Step 3: Update the Tcaplus runbook**

Add `ShardMapMeta` and `ShardRoute` to the required runtime table list and add:

```bash
export TCAPLUS_SHARD_MAP_META_TABLE='ShardMapMeta'
export TCAPLUS_SHARD_ROUTE_TABLE='ShardRoute'
```

State explicitly that an existing environment must create both tables before
enabling `COORDINATOR_ROUTE_STORE=tcaplus`.

- [ ] **Step 4: Regenerate Tcaplus types and run the compile test**

```bash
cd deploy/tcaplus
go run github.com/bufbuild/buf/cmd/buf@latest generate --template buf.gen.yaml
cd ../../server
go test ./internal/routing
```

Expected: the compile test advances to the missing Store implementation.

## Task 2: Define the RouteStore and validation model

**Files:**

- Create: `server/internal/coordinator/routestore/store.go`
- Create: `server/internal/coordinator/routestore/validate.go`
- Create: `server/internal/coordinator/routestore/memory.go`
- Create: `server/internal/coordinator/routestore/memory_test.go`
- Modify: `server/internal/routing/routing.go`
- Test: `server/internal/routing/routing_test.go`

**Interfaces:**

Use routing domain types at the boundary; generated Tcaplus messages stay
inside the Tcaplus adapter.

```go
type Metadata struct {
    ShardCount                 uint32
    HashAlgorithmVersion       uint32
    AssignmentAlgorithmVersion uint32
    MapVersion                 uint64
    UpdatedAt                  time.Time
}

type Snapshot struct {
    Metadata Metadata
    Entries  []routing.RouteEntry
}

type Store interface {
    Load(context.Context) (Snapshot, error)
    BootstrapIfEmpty(context.Context, Snapshot) (Snapshot, bool, error)
    CommitPreparing(context.Context, routing.RouteEntry, uint64) (Snapshot, error)
    CommitActive(context.Context, routing.RouteEntry, uint64) (Snapshot, error)
    RestoreSource(context.Context, routing.RouteEntry, uint64) (Snapshot, error)
}
```

The final `uint64` argument is the expected current global `map_version`.
Successful commit allocates exactly `expected+1`, stores it in
`ShardMapMeta.map_version`, and stores it in the changed route's
`committed_map_version`. A stale expected version returns sentinel
`ErrRouteConflict`.

`BootstrapIfEmpty` returns `(loaded, created, error)`:

- both tables empty: insert metadata and exactly 4096 ordered routes;
- both complete: return the stored snapshot with `created=false`;
- only one table exists, wrong row count, duplicate/out-of-order Shard IDs, or
  incompatible algorithm metadata: return `ErrRouteStoreCorrupt`;
- concurrent bootstrap: loser reloads and validates the winner's snapshot.

- [ ] **Step 1: Write MemoryStore tests first**

Cover:

1. empty bootstrap creates 4096 routes once;
2. repeated bootstrap does not overwrite a migrated owner;
3. stale expected map version is rejected;
4. PREPARING→ACTIVE consumes two distinct map versions;
5. invalid/incomplete snapshot is rejected;
6. `Load` returns deep copies.

Run:

```bash
cd server
go test ./internal/coordinator/routestore
```

Expected: FAIL before implementation, then PASS.

- [ ] **Step 2: Add a safe Map restore constructor**

Add a constructor in `routing.go` equivalent to:

```go
func NewMapFromSnapshot(snapshot Snapshot) (*Map, error)
```

It must validate metadata, exactly 4096 ordered entries, positive versions,
known states, and `epochHighWater`. It must not generate new lease IDs, epochs
or route versions. Add tests for exact restoration and malformed rejection.

- [ ] **Step 3: Implement MemoryStore minimally**

Use one mutex and copy-on-read. Do not use it to emulate cross-process
guarantees; it exists for unit tests and development-only mode.

- [ ] **Step 4: Run focused tests**

```bash
cd server
go test ./internal/coordinator/routestore ./internal/routing
```

Expected: PASS.

## Task 3: Implement Tcaplus RouteStore with CAS

**Files:**

- Create: `server/internal/coordinator/routestore/tcaplus.go`
- Create: `server/internal/coordinator/routestore/tcaplus_test.go`
- Modify: `server/internal/platform/tcaplusdb/client.go` only if opening the two
  additional tables requires no behavior beyond the existing variadic table list.
- Reuse: `server/internal/testtcaplus/client.go`

**Interfaces:**

```go
type TcaplusClient interface {
    DoGet(proto.Message, *option.PBOpt, ...uint32) error
    DoInsert(proto.Message, *option.PBOpt, ...uint32) error
    DoUpdate(proto.Message, *option.PBOpt, ...uint32) error
    Traverse(proto.Message) ([]proto.Message, error)
}

func NewTcaplusStore(client TcaplusClient, zoneID uint32) (*TcaplusStore, error)
```

Use the Tcaplus record version returned in `PBOpt.Version` for CAS. Do not use
recursive unbounded retries. Retry bootstrap races a bounded number of times,
then return `ErrRouteConflict`.

Only one route commit may be pending globally. This deliberately serializes
control-plane commits in Phase 02; it does not serialize player commands.
The pending intent is self-contained: together, its fields encode the complete
target `ShardRoute`. `pending_map_version` is also the target route's
`committed_map_version`. RouteStore recovery does not read or depend on
`MigrationProgress`; that record continues to recover business migration
stages, while `ShardMapMeta.pending_*` recovers RouteStore's own cross-row
commit.

Commit algorithm for one route transition:

```text
1. Load ShardMapMeta and target ShardRoute with record versions.
2. Require stable meta.map_version == expected and no unrelated pending intent.
3. Validate the exact allowed source route/version/transition.
4. CAS ShardMapMeta to set one pending intent for shard, next map version,
   target route version, transition and target state; do not advance the stable
   map_version yet.
5. CAS update the target ShardRoute with committed_map_version=expected+1.
6. CAS finalize ShardMapMeta: map_version=expected+1 and clear pending fields.
7. Exact retries inspect the pending intent and route and resume steps 5/6;
   unrelated commits receive ErrRouteConflict until recovery completes.
```

Because Tcaplus does not provide a declared cross-row transaction here, a
crash can occur after pending-intent CAS, after Route CAS, or before Meta
finalize. `Load` applies these exact rules:

1. Without a pending intent, load the complete route set; any route whose
   `committed_map_version` is ahead of stable `meta.map_version` is corrupt and
   fails closed.
2. With a pending intent and the target route still at the legal old value,
   reconstruct the complete target route from Meta, CAS the route, then
   finalize Meta.
3. With a pending intent and a route already exactly equal to the complete
   pending route, skip the route write and finalize Meta.
4. With a pending intent and a route matching neither the legal old value nor
   the complete target, return `ErrRouteStoreCorrupt`; never guess, overwrite
   or recompute.
5. Finalize Meta with its record version: set `map_version` to
   `pending_map_version` and clear every pending field.

A route ahead of stable map_version without a matching pending intent is
corrupt and fails closed. Tests must exercise all crash windows. Do not hide
them in documentation.

> Implementation review checkpoint: if the real Tcaplus product supports a
> verified multi-record transaction suitable for these tables, stop and record
> evidence before changing this algorithm. Do not assume it does.

- [ ] **Step 1: Write failing Tcaplus Store tests**

Cover:

- empty bootstrap and exact reload;
- repeat bootstrap preserves changed routes;
- CAS conflict on stale metadata version;
- idempotent replay of exact PREPARING and ACTIVE commits;
- pending-intent success/route-write failure recovery;
- route-write success/meta-finalize failure recovery;
- finalize success followed by exact commit replay;
- pending target conflicting with the stored route;
- incomplete pending fields fail closed;
- unrelated commit rejected while a pending intent exists;
- incompatible algorithm version fails closed;
- missing one of 4096 rows fails closed.

- [ ] **Step 2: Implement record/domain conversion and validation**

Use existing UUID byte helpers only through exported, focused conversion
helpers or move generic UUID conversion into the new package. Do not duplicate
incompatible UUID formats.

- [ ] **Step 3: Implement Load, BootstrapIfEmpty and transition CAS**

Keep each method bounded and context-aware. Return typed errors so Coordinator
startup can distinguish empty, conflict, corrupt and storage unavailable.

- [ ] **Step 4: Run focused tests**

```bash
cd server
go test ./internal/coordinator/routestore ./internal/routing
```

Expected: PASS, including injected partial-write recovery.

## Task 4: Make durable Current authoritative at Coordinator startup

**Files:**

- Modify: `server/cmd/coordinator/main.go`
- Modify: `server/cmd/coordinator/main_test.go`
- Create: `server/cmd/coordinator/route_store_wiring.go`
- Create: `server/cmd/coordinator/route_store_wiring_test.go`
- Modify: `deploy/k8s/coordinator.yaml`
- Modify: `deploy/k8s/configmap.yaml` if shared non-secret table names live there.

**Interfaces and configuration:**

```text
COORDINATOR_ROUTE_STORE=legacy-fence | tcaplus
TCAPLUS_SHARD_MAP_META_TABLE=ShardMapMeta
TCAPLUS_SHARD_ROUTE_TABLE=ShardRoute
```

Default must remain `legacy-fence` until live Tcaplus tables and Phase 02 E2E
are verified. `tcaplus` mode requires `STORAGE_MODE=tcaplus` and both tables.

### Runtime lease expiry overlay

The existing `Map.RenewOwnedLeases` remains unchanged in `legacy-fence` mode:
it extends expiry and advances route/map versions. Durable mode must never call
it, because that would make in-memory Current lead RouteStore.

Durable mode maintains a separate runtime expiry overlay bound to each route's
`shard_id`, owner ID, owner epoch, route version, lease ID and lease term. On
startup, after RouteStore restore and Current/Fence validation but before
readiness, create overlays only for ACTIVE routes. Renewal changes only overlay
expiry: it does not modify the durable `RouteEntry`, allocate identities,
advance versions, call RouteStore or describe a RouteBatch change.

The HTTP snapshot and per-Shard compatibility APIs expose overlay expiry as
the effective lease only when every binding field still matches Current and
the overlay is unexpired. Missing, expired or mismatched overlays fail closed.
PREPARING and UNASSIGNED routes never receive a routable overlay.

Zone `AuthorizationTable.Replace` accepts a same-map-version snapshot only
when all 4096 ownership/state/version/identity fields are unchanged and every
lease expiry is unchanged or extended. It then atomically installs the
lease-only refresh. Any other same-version change fails closed.

Startup in `tcaplus` route-store mode:

```text
read static bootstrap candidates
-> build candidate initial Snapshot in memory
-> RouteStore.BootstrapIfEmpty(candidate)
-> validate/load returned durable Current
-> routing.NewMapFromSnapshot(Current)
-> load/reconcile MigrationProgress against Current and Fence
-> build runtime lease expiry overlays for ACTIVE Current routes
-> become ready
```

Never call `HydrateActiveRoutesFromFences` as the source of Active routes in
this mode. Fence becomes a cross-check:

- ACTIVE Current owner/epoch must match Fence;
- PREPARING before Fence advance may still match Source Fence;
- PREPARING after Fence advance must match Target Fence and Progress step;
- any unexplained mismatch fails startup closed.

- [ ] **Step 1: Write wiring tests before changing `run()`**

Test legacy selection, Tcaplus selection, invalid combinations, empty
bootstrap, restart load and Current/Fence mismatch rejection using injected
stores. Avoid environment-global parallel tests.

- [ ] **Step 2: Extract route-store construction from `run()`**

Keep `main.go` responsible for configuration and wiring only. Open the Tcaplus
client with Fence, MigrationProgress, ShardMapMeta and ShardRoute table names.

- [ ] **Step 3: Build `routing.Map` only from the returned durable snapshot**

After this point, restarting with a different static list must not reassign
Current. Unknown owner endpoint is read from ShardRoute, not static config.
The static list remains only the empty-store bootstrap input in this phase.

- [ ] **Step 4: Render Kubernetes manifests**

Add table-name variables but leave `COORDINATOR_ROUTE_STORE=legacy-fence` until
the live table rollout task succeeds.

```bash
kubectl kustomize deploy/k8s >/tmp/classic-farm-rendered.yaml
kubectl apply --dry-run=client -f /tmp/classic-farm-rendered.yaml
```

Expected: PASS.

## Task 5: Move manual migration's commit point to RouteStore

**Files:**

- Modify: `server/cmd/coordinator/migration.go`
- Modify: `server/cmd/coordinator/migration_test.go`
- Modify: `server/cmd/coordinator/migration_recovery_test.go`
- Modify: `server/internal/routing/tcaplus_control_store.go` only for explicit
  Current/Fence reconciliation helpers; do not merge RouteStore into it.

**Interfaces:**

Inject `routestore.Store` into `migrationHandler`. In durable mode use this
ordering:

```text
Source Drain and flush
-> persist MigrationProgress=Drained
-> RouteStore.CommitPreparing
-> replace in-memory Map with returned PREPARING snapshot
-> persist Progress=PreparingCommitted
-> Fence.AdvanceFence
-> persist Progress=FenceAdvanced
-> Target prepare / ShardReady
-> persist Progress=TargetPrepared
-> RouteStore.CommitActive
-> replace in-memory Map with returned ACTIVE snapshot
-> best-effort target ownership refresh
-> mark/delete completed Progress
```

The in-memory Map must never lead durable Current. If target refresh fails
after durable ACTIVE, return a retryable control error but do not roll ownership
back. A retry must recognize the exact ACTIVE transition and finish refresh/
cleanup idempotently.

- [ ] **Step 1: Add failure-injection tests for each durable boundary**

At minimum:

1. PREPARING store failure leaves Source Current in memory;
2. Fence failure leaves durable/in-memory PREPARING and resumable Progress;
3. Target prepare failure after Fence remains resumable and cannot abandon;
4. ACTIVE store failure does not expose Target as ACTIVE in memory;
5. ACTIVE success plus refresh failure remains Target authority after restart;
6. exact retry is idempotent;
7. completed Progress cleanup failure cannot roll route back.

- [ ] **Step 2: Implement durable-first map replacement**

Prefer replacing the complete in-memory map from the returned store Snapshot,
or add an exact `ApplyCommittedSnapshot` method that rejects version rollback.
Do not call `Map.Prepare/Activate` before store success in durable mode.

- [ ] **Step 3: Preserve legacy path behind its switch**

Existing memory/MySQL historical tests may use the legacy handler path. Do not
claim the legacy path has durable Current semantics.

- [ ] **Step 4: Run migration tests**

```bash
cd server
go test ./cmd/coordinator ./internal/coordinator/routestore ./internal/routing
```

Expected: PASS.

## Task 6: Verify restart authority and record evidence

**Files:**

- Modify: `server/test/e2e/dual_zone_routing_test.go`
- Create: `docs/evidence/2026-08-12-durable-current-shard-route.md`
- Modify after verification: `docs/context/CURRENT.md`

- [ ] **Step 1: Add a process-level restart scenario**

The test must prove:

```text
bootstrap static zone-a/zone-b
-> migrate one Shard to its non-initial owner
-> stop Coordinator
-> restart with static candidate order reversed
-> load Current from RouteStore
-> same owner/epoch/route_version/map_version
-> old owner write rejected
```

The in-memory fake may validate process wiring. Live Tcaplus validation is a
separate owner-run step because table creation is external.

- [ ] **Step 2: Run offline regression**

```bash
cd deploy/tcaplus
go run github.com/bufbuild/buf/cmd/buf@latest generate --template buf.gen.yaml
cd ../../server
go test ./internal/coordinator/routestore ./internal/routing ./cmd/coordinator
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Run live Tcaplus verification only when tables exist**

Create `ShardMapMeta` and `ShardRoute` from the schema, set the two table env
variables and `COORDINATOR_ROUTE_STORE=tcaplus`, then execute the existing
dual-Zone migration/restart workflow. Capture commands and redacted output.

If tables or credentials are unavailable, mark live verification as blocked;
do not enable the mode in kind and do not claim it passed.

- [ ] **Step 4: Update evidence and CURRENT**

Evidence must state:

- offline unit/process results;
- whether live Tcaplus ran;
- bootstrap row count and observed restart result;
- injected partial-write windows;
- that HTTP consumers still pull and Watch remains unimplemented;
- that static members are still the empty-store bootstrap source;
- that global/default concurrency values remain assumptions.

Only after live Tcaplus succeeds may kind set
`COORDINATOR_ROUTE_STORE=tcaplus`. Update CURRENT with the actual verified
boundary, not the full ADR target.

## Done Criteria

- Tcaplus schemas contain `ShardMapMeta` and `ShardRoute`.
- RouteStore has validated memory and Tcaplus implementations.
- Empty bootstrap is idempotent; non-empty startup never recomputes Current.
- `routing.Map` restores exact durable versions without minting identities.
- Manual durable migration persists PREPARING/Fence/ACTIVE before exposing
  each matching in-memory state.
- Restart preserves owner, epoch, route version and map version.
- Fence/Route/Progress mismatches fail closed or reconcile only documented
  idempotent crash windows.
- Existing HTTP route APIs, Gate cache and Zone polling still work.
- Watch, Kubernetes membership, automatic rebalance and failover remain absent.

## Next Phase Boundary

Only after durable Current is verified, execute
`03-Coordinator-SDK与主动发布.md`. Phase 03 may publish only snapshots and
RouteBatches produced after successful RouteStore commits.
