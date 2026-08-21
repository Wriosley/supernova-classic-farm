# Coordinator SDK and Active Route Publishing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:test-driven-development`. Execute only after Phases 01 and 02
> are complete and reviewed. Do not add Kubernetes discovery, rebalance or
> automatic failover in this task.

**Goal:** Push every durably committed Current Route change to embedded
Gate/Info/Zone SDK caches before the next normal request, while preserving HTTP
snapshot and `NOT_OWNER` fallback paths.

**Architecture:** A Coordinator Publisher consumes only snapshots returned by
successful RouteStore commits. It owns bounded subscriber sessions and exposes
the Phase 01 bidirectional `WatchRoutes` service. A shared `coordinatorclient`
SDK loads one full snapshot, maintains an immutable 4096-slot table, applies
strictly versioned batches and reconnects/resyncs on gaps. Gate and Info use
small adapters over the SDK; Zone atomically replaces its AuthorizationTable
from SDK snapshots. No business handler owns Watch or retry state.

**Tech Stack:** Go 1.26, gRPC bidirectional streaming over existing h2c,
Protobuf contract from Phase 01, durable RouteStore from Phase 02, HMAC internal
RPC metadata.

## Global Constraints

- Read ADR-0012, `00-总路线图.md`, Phases 01 and 02 before editing.
- Publish only after Fence/Active ShardRoute persistence and in-memory Current
  replacement succeed.
- Publisher and subscriber ACK never participate in the ownership commit.
- Ordinary player commands must resolve from local SDK/cache memory.
- Keep existing HTTP full snapshot, HTTP single-Shard lookup and `NOT_OWNER`
  retry operational.
- Do not add `client-go`, EndpointSlice Watch, Zone health decisions,
  Current/Desired planning, migration queues or Leader Election.
- Availability messages are implemented as transport/cache capability, but
  Phase 03 does not generate SUSPECT/DEAD transitions.
- Do not persist subscriber queues or ACKs.
- Default queue and timeout values are configurable assumptions, not measured
  capacity claims.
- Do not publish the legacy `renewOwnedLeases` mutations as route changes.
  Those mutations are memory-only, touch thousands of entries and would make
  SDK versions lead durable Current.

## Lease Compatibility Boundary

The current Coordinator increments RouteVersion while renewing every static
Zone's Shards every `leaseDuration/3`. That mechanism cannot be used by the
durable SDK path because it is neither based on Zone health nor persisted in
ShardRoute.

During Phase 03:

- `legacy-fence` + HTTP/poll mode keeps current per-Shard renewal unchanged;
- durable Current keeps Phase 02's HTTP runtime expiry overlay during rollout;
  the overlay binds to the complete durable route identity and changes expiry
  only, never RouteStore or route/map versions;
- durable Current + SDK mode disables the legacy `renewOwnedLeases`;
- ownership `route_version/map_version` change only through RouteStore;
- a healthy Watch connection supplies a transient `watch_fresh_until` renewed
  by valid Ping/Pong traffic;
- SDK Resolve uses `effective_expiry = watch_fresh_until` while connected;
- after disconnect it uses
  `min(last_watch_fresh_until, disconnected_at + DisconnectTTL)` and fails
  closed when that time passes;
- Zone SDK wiring applies the same effective expiry to its local authorization
  view without persisting or changing route versions;
- after Gate/Info/Zone have switched to SDK freshness, durable mode no longer
  depends on per-Shard HTTP lease refresh; HTTP remains a fallback path;
- Phase 04 replaces this static compatibility rule with membership/livez-driven
  availability. Do not claim Phase 03 has real Zone health leases.

## Target Runtime Flow

```text
RouteStore.CommitActive succeeds
-> Coordinator replaces in-memory Current
-> Publisher.Publish(previousSnapshot, currentSnapshot)
-> one RouteBatch enters each subscriber's bounded queue
-> SDK validates previous_map_version
-> SDK copy-on-write replaces changed slots
-> Gate/Info/Zone next request sees current owner
```

On any gap or queue overflow:

```text
ResyncRequired / stream close
-> SDK calls GetRouteSnapshot
-> atomically replaces all 4096 slots
-> opens a new Watch from the loaded versions
```

## Task 1: Add authenticated streaming RPC support

**Files:**

- Modify: `server/internal/platform/rpcauth/auth.go`
- Modify: `server/internal/platform/rpcauth/auth_test.go`
- Modify: `docs/contracts/internal-grpc.md`

**Interfaces:**

Current HMAC interceptors authenticate unary messages and hash the Protobuf
body. Streaming RPCs have no request message available at stream creation.
Add dedicated stream interceptors:

```go
func NewClientStreamInterceptor(cfg ClientConfig) (grpc.StreamClientInterceptor, error)
func NewServerStreamInterceptor(cfg ServerConfig) (grpc.StreamServerInterceptor, error)
```

For stream establishment, sign the existing metadata tuple using SHA-256 of an
empty body:

```text
caller_service
full_method
timestamp_ms
nonce
sha256(empty)
```

The server verifies the method allowlist, clock window, nonce replay and
signature once when opening the stream. The Watch server separately requires
the first application message to be `Subscribe` and checks its subscriber ID
and kind. Do not treat application Ping/Pong as authentication.

- [ ] **Step 1: Write failing bufconn stream-auth tests**

Cover valid Gate/Info/Zone callers, missing metadata, wrong key, expired
timestamp, nonce replay and caller not allowed for `WatchRoutes`.

Run:

```bash
cd server
go test ./internal/platform/rpcauth
```

Expected: FAIL before stream interceptors exist.

- [ ] **Step 2: Refactor shared metadata verification carefully**

Reuse existing service-name validation, timestamp window, nonce cache and HMAC
helpers. Keep unary behavior and signatures byte-for-byte compatible.

- [ ] **Step 3: Implement and document stream authentication**

Document that stream authentication covers stream establishment, while
`WatchRoutes` validates Subscribe identity and message state. Do not claim
per-message HMAC.

- [ ] **Step 4: Run authentication regression**

```bash
cd server
go test ./internal/platform/rpcauth
```

Expected: PASS for existing unary and new stream tests.

## Task 2: Implement the Publisher and bounded subscriber sessions

**Files:**

- Create: `server/internal/coordinator/publisher/publisher.go`
- Create: `server/internal/coordinator/publisher/session.go`
- Create: `server/internal/coordinator/publisher/diff.go`
- Create: `server/internal/coordinator/publisher/publisher_test.go`
- Create: `server/internal/coordinator/publisher/session_test.go`

**Interfaces:**

```go
type SnapshotSource interface {
    Snapshot() routing.Snapshot
}

type Config struct {
    QueueCapacity int           // default 128 batches
    PingInterval  time.Duration // default 30s
    AckTimeout    time.Duration // diagnostics only; default 90s
    Now           func() time.Time
}

type Publisher struct { /* private state */ }

func New(source SnapshotSource, cfg Config) (*Publisher, error)
func (p *Publisher) Register(id string, kind coordinatorv1.SubscriberKind,
    lastMapVersion, lastAvailabilityVersion uint64) (*Session, error)
func (p *Publisher) PublishRoutes(previous, current routing.Snapshot) error
func (p *Publisher) PublishAvailability(batch *coordinatorv1.AvailabilityBatch) error
func (p *Publisher) Close()
```

`PublishRoutes` requirements:

- require `current.MapVersion > previous.MapVersion`;
- diff all 4096 ordered entries and include only changed Shards;
- encode `previous_map_version=previous.MapVersion` and
  `map_version=current.MapVersion`;
- if no entry changed while map version changed, return an invariant error;
- enqueue independently to every subscriber without waiting for ACK;
- if a session queue is full, mark it `ResyncRequired`, close only that session
  and increment a diagnostic counter;
- one slow subscriber cannot block another subscriber or RouteStore commit;
- no goroutine per published message and no unbounded goroutine creation.

`Register` rules:

- unique non-empty subscriber ID per live connection;
- unsupported kind rejected;
- if `last_map_version` equals current, begin with future batches;
- otherwise first deliver a full Snapshot, not a chain of retained deltas;
- Publisher retains no historical batches after enqueue.

- [ ] **Step 1: Write Publisher tests first**

Cover one-Shard diff, multiple ordered changes, invalid version, initial
Snapshot, current-version subscribe, duplicate subscriber replacement/reject
policy, queue overflow, slow vs fast subscribers, Close wakeup and concurrent
Publish/Register under `go test -race`.

- [ ] **Step 2: Implement domain-to-Protobuf conversion in one place**

Create focused conversion helpers inside the publisher package. Preserve every
RouteEntry field frozen in Phase 01. Do not duplicate conversion logic in the
gRPC server and SDK.

- [ ] **Step 3: Implement bounded sessions and diagnostics**

Expose read-only counts for active subscribers, queue overflows, resyncs, last
published map version and ACK lag. Metrics are diagnostic observations, not
commit conditions.

- [ ] **Step 4: Run focused tests with race detection**

```bash
cd server
go test -race ./internal/coordinator/publisher
```

Expected: PASS.

## Task 3: Implement CoordinatorService and WatchRoutes server

**Files:**

- Create: `server/internal/coordinator/publisher/grpc_server.go`
- Create: `server/internal/coordinator/publisher/grpc_server_test.go`
- Create: `server/cmd/coordinator/grpc_wiring.go`
- Create: `server/cmd/coordinator/grpc_wiring_test.go`
- Modify: `server/cmd/coordinator/main.go`

**Interfaces:**

Implement the exact Phase 01 `CoordinatorService`:

- `GetRouteSnapshot`: returns current complete durable-backed in-memory
  snapshot;
- `GetShardRoute`: returns the entry and current map version; reject Shard IDs
  outside `[0,4096)`;
- `WatchRoutes`: bidirectional stream;
- `ReportZoneFailure`: return `UNIMPLEMENTED` in Phase 03 with a clear status;
  Phase 07 owns its behavior.

Watch state machine:

```text
OPEN
-> first client message must be Subscribe
-> register session
-> sender loop emits Snapshot/Batch/Ping/Resync
-> receiver loop accepts Ack/Pong only
-> EOF, protocol violation, context cancellation or session overflow
-> unregister and close
```

Protocol rules:

- reject Ack/Pong before Subscribe;
- reject a second Subscribe;
- Ack cannot move backwards or beyond the last sent map version;
- Pong must reference an outstanding ping ID;
- Ping every configured interval; 90 seconds without any valid Ack/Pong marks
  diagnostic timeout and closes the stream;
- send/receive errors close the session and release all goroutines;
- maximum full Snapshot size is 8 MiB; incremental messages remain bounded by
  at most 4096 entries;
- `WatchRoutes` never loads Tcaplus directly; it reads the committed Current
  source.

- [ ] **Step 1: Write bufconn server tests**

Test unary snapshot/shard lookup, authenticated subscribe, initial snapshot,
one committed batch, Ack, Ping/Pong, invalid message order, slow queue overflow
and disconnect cleanup.

- [ ] **Step 2: Wire Coordinator gRPC and HTTP on the same h2c server**

Use `rpcnet.H2CHandler`. Configure both unary and stream HMAC interceptors.
Allow `gate`, `info`, `zone-local`, `zone-a`, `zone-b` during the static phase.
Phase 04 will replace static Zone caller identities with stable generated IDs.

- [ ] **Step 3: Add a debug-only subscriber diagnostics endpoint**

Expose counts without subscriber secrets or endpoints, for example:

```text
GET /internal/v1/debug/route-subscribers
```

Return active counts by kind, overflow/resync counts and last map version. Keep
the existing internal-network restriction pattern.

- [ ] **Step 4: Run server tests**

```bash
cd server
go test -race ./internal/coordinator/publisher ./cmd/coordinator
```

Expected: PASS.

## Task 4: Publish only after durable Current commits

**Files:**

- Modify: `server/cmd/coordinator/migration.go`
- Modify: `server/cmd/coordinator/migration_test.go`
- Modify: `server/cmd/coordinator/migration_recovery_test.go`
- Modify: `server/cmd/coordinator/route_store_wiring.go`

**Interfaces:**

Inject a narrow callback rather than the full Publisher:

```go
type RouteChangePublisher interface {
    PublishRoutes(previous, current routing.Snapshot) error
}
```

Durable migration order from Phase 02 becomes:

```text
RouteStore.CommitPreparing
-> replace in-memory PREPARING
-> PublishRoutes(old ACTIVE, PREPARING)
...
RouteStore.CommitActive
-> replace in-memory ACTIVE
-> PublishRoutes(PREPARING, ACTIVE)
```

PREPARING must be published so SDK consumers immediately stop routing the
Shard. ACTIVE is published only after its durable commit. Publisher enqueue
failure or a slow subscriber must be logged/observed but must not roll back a
successful durable route. A functioning subscriber will recover from a missed
batch through version gap/resync.

- [ ] **Step 1: Add ordering tests**

Use recording fakes to prove:

1. no publish before RouteStore success;
2. PREPARING is published after durable PREPARING;
3. ACTIVE is published after durable ACTIVE;
4. Publisher failure does not roll back Current;
5. restart recovery publishes/serves the restored Snapshot to new subscribers,
   not a fabricated transition batch.

- [ ] **Step 2: Add the callback at the exact durable boundaries**

Do not call Publisher from `routing.Map`, Store adapters or business handlers.
The Coordinator application orchestration layer owns the ordering.

- [ ] **Step 3: Run migration regression**

```bash
cd server
go test ./cmd/coordinator ./internal/coordinator/publisher
```

Expected: PASS.

## Task 5: Implement the embedded coordinatorclient SDK

**Files:**

- Create: `server/internal/coordinatorclient/client.go`
- Create: `server/internal/coordinatorclient/cache.go`
- Create: `server/internal/coordinatorclient/watch.go`
- Create: `server/internal/coordinatorclient/backoff.go`
- Create: `server/internal/coordinatorclient/client_test.go`
- Create: `server/internal/coordinatorclient/cache_test.go`
- Create: `server/internal/coordinatorclient/watch_test.go`

**Interfaces:**

```go
type Config struct {
    Endpoint       string
    SubscriberID   string
    Kind           coordinatorv1.SubscriberKind
    HMACKey        []byte
    DisconnectTTL  time.Duration // default 90s
    MinBackoff     time.Duration // default 100ms
    MaxBackoff     time.Duration // default 5s
    Now            func() time.Time
}

type Client struct { /* private */ }

func New(cfg Config) (*Client, error)
func (c *Client) Start(context.Context) error
func (c *Client) ResolvePlayer(playerID uint64) (routing.RouteEntry, error)
func (c *Client) ResolveShard(shardID uint32) (routing.RouteEntry, error)
func (c *Client) Snapshot() routing.Snapshot
func (c *Client) Close() error
```

Cache behavior:

- immutable fixed `[4096]` slots behind `atomic.Pointer`;
- initial full Snapshot must validate all metadata and ordered entries;
- RouteBatch applies only when `previous_map_version == local.map_version` and
  `map_version > previous`;
- duplicate/old batches are ignored only when their final version is not newer;
- a forward gap, malformed entry or version regression triggers Resync;
- PREPARING/UNASSIGNED entries remain cached but `Resolve*` returns a typed
  retryable route-unavailable error;
- ACTIVE route usability in SDK mode follows the Phase 03
  `watch_fresh_until` compatibility boundary above; persisted legacy
  `lease_expires_at` is retained for HTTP/poll rollback but does not generate
  SDK RouteBatch churn;
- reconnect obtains a full unary Snapshot before opening/continuing Watch;
- one reconnect loop with bounded exponential backoff and jitter; no retry per
  business request;
- `Close` wakes all goroutines promptly.

Availability behavior:

- maintain a separate immutable logical Zone availability map with its own
  `availability_version`;
- a cached ACTIVE route whose owner is SUSPECT/DEAD/DRAINING is unavailable;
- Phase 03 starts with no availability entries, interpreted as no additional
  pause; Phase 07 will publish them.

- [ ] **Step 1: Write cache state-machine tests**

Cover valid snapshot, contiguous batch, duplicate, gap, malformed entry,
PREPARING pause, lease expiry, disconnect TTL, availability version and
concurrent Resolve/apply under `-race`.

- [ ] **Step 2: Write bufconn Watch/reconnect tests**

Cover initial sync, Ping/Pong, Ack, server close, ResyncRequired, full resync,
backoff cancellation and Close with no goroutine leak.

- [ ] **Step 3: Implement SDK without importing gateway/info/zone packages**

The SDK may import `routing` and generated Coordinator types. It must not import
business service packages. Adapters in Tasks 6–8 depend on SDK, never reverse.

- [ ] **Step 4: Run focused race tests**

```bash
cd server
go test -race ./internal/coordinatorclient
```

Expected: PASS.

## Task 6: Integrate Gate while preserving HTTP fallback

**Files:**

- Create: `server/internal/gateway/coordinator_routes.go`
- Create: `server/internal/gateway/coordinator_routes_test.go`
- Modify: `server/cmd/gate/main.go`
- Modify: `server/cmd/gate/main_test.go`
- Preserve: `server/internal/gateway/http_adapters.go`
- Preserve: `server/internal/gateway/route_cache.go`

**Configuration:**

```text
GATE_ROUTE_SOURCE=http | coordinator-sdk
COORDINATOR_RPC_URL=http://coordinator:8083
```

Default remains `http` until Gate SDK E2E passes.

The adapter implements existing `gateway.RouteResolver` and
`RouteInvalidator`:

- normal Resolve converts SDK `routing.RouteEntry` to `gateway.Route`;
- `InvalidateIfVersion` asks the SDK to force one full resync or marks the slot
  unavailable; it must not perform synchronous Coordinator I/O itself;
- the existing Gateway `NOT_OWNER` retry may use the preserved HTTP resolver as
  a one-Shard fallback while SDK reconnect/resync occurs;
- ordinary cache hits generate no Coordinator HTTP or gRPC unary lookup.

- [ ] **Step 1: Write adapter and wiring tests**

Prove conversion, PREPARING pause, NOT_OWNER fallback, default HTTP selection,
SDK selection, startup failure and graceful Close.

- [ ] **Step 2: Wire the SDK behind the configuration switch**

Use `GATEWAY_ID` as subscriber ID and `SUBSCRIBER_KIND_GATE`. Start and fully
warm the SDK before Gate becomes Ready.

- [ ] **Step 3: Run Gate tests**

```bash
cd server
go test -race ./internal/gateway ./cmd/gate
```

Expected: PASS.

## Task 7: Integrate InfoSvr using the same SDK

**Files:**

- Create: `server/internal/info/coordinator_routes.go`
- Create: `server/internal/info/coordinator_routes_test.go`
- Modify: `server/cmd/info/main.go`
- Modify: `server/cmd/info/main_test.go` if it exists; otherwise create it.

**Configuration:**

```text
INFO_ROUTE_SOURCE=http | coordinator-sdk
INFO_INSTANCE_ID=info-local
```

Default remains `http` until E2E passes. Use the same SDK package; do not reuse
Gate's concrete cache implementation through the `gateway` package. Keep the
existing `info.Service` route-facing interface stable or move the shared route
interface to a neutral package if required to break dependency direction.

- [ ] **Step 1: Write Info adapter/wiring tests**

Prove player→Shard resolution, paused route rejection, default selection,
subscriber identity and shutdown.

- [ ] **Step 2: Wire SDK behind the switch**

Start/warm before Info becomes Ready. Existing red-dot delivery behavior and
Zone client remain unchanged.

- [ ] **Step 3: Run Info tests**

```bash
cd server
go test -race ./internal/info ./cmd/info
```

Expected: PASS.

## Task 8: Integrate Zone authorization and remove fast polling only in SDK mode

**Files:**

- Modify: `server/internal/routing/authorization.go`
- Modify: `server/internal/routing/authorization_test.go`
- Create: `server/cmd/zone/coordinator_routes.go`
- Create: `server/cmd/zone/coordinator_routes_test.go`
- Modify: `server/cmd/zone/main.go`
- Modify: `server/cmd/zone/main_test.go` if present; otherwise use focused
  wiring tests.

**Configuration:**

```text
ZONE_ROUTE_SOURCE=http-poll | coordinator-sdk
ZONE_INSTANCE_ID=<current owner zone ID in Phase 03>
```

Default remains `http-poll`. In SDK mode:

- initial SDK Snapshot atomically fills AuthorizationTable;
- each contiguous RouteBatch results in one atomic updated authorization
  snapshot;
- effective authorization expiry is derived from SDK Watch freshness and does
  not mutate owner epoch, route version or durable ShardRoute;
- remove the 5-second full Snapshot loop;
- retain a configurable low-frequency full verification, default 5 minutes,
  that compares map version/checksum and triggers SDK resync on mismatch;
- keep `/internal/v1/ownership/refresh` as a compatibility endpoint that asks
  the SDK to resync instead of starting a second independent route fetch;
- Drain markers keep their current behavior across snapshot replacement.

- [ ] **Step 1: Add AuthorizationTable apply tests**

Either reuse full `Replace` after every SDK batch or add an atomic
`ApplySnapshot`. Prove version rollback rejection, Drain preservation and
PREPARING→ACTIVE drain clearing rules.

- [ ] **Step 2: Write Zone wiring tests**

Prove default polling remains, SDK mode disables the 5-second loop, initial
warm is required, compatibility refresh triggers resync and shutdown closes
the SDK.

- [ ] **Step 3: Run Zone tests**

```bash
cd server
go test -race ./internal/routing ./cmd/zone
```

Expected: PASS.

## Task 9: Roll out and verify active delivery

**Files:**

- Modify: `deploy/k8s/configmap.yaml`
- Modify: `deploy/k8s/coordinator.yaml`
- Modify: `deploy/k8s/gate.yaml`
- Modify: `deploy/k8s/info.yaml`
- Modify: `deploy/k8s/zone.yaml`
- Modify: `deploy/k8s/README.zh-CN.md`
- Modify: `server/test/e2e/dual_zone_routing_test.go`
- Create: `docs/evidence/2026-08-12-coordinator-sdk-route-publish.md`
- Modify after verification: `docs/context/CURRENT.md`

**Rollout sequence:**

1. deploy Coordinator with gRPC Watch while all consumers still use legacy
   HTTP/poll;
2. enable one Gate SDK subscriber and verify fallback;
3. enable Info SDK;
4. enable zone-a SDK, then zone-b SDK;
5. only after all pass, make SDK mode the kind default while retaining env
   rollback switches.

- [x] **Step 1: Render manifests**

```bash
kubectl kustomize deploy/k8s >/tmp/classic-farm-rendered.yaml
kubectl apply --dry-run=client -f /tmp/classic-farm-rendered.yaml
```

- [x] **Step 2: Run offline regression**

```bash
cd server
go test -race ./internal/platform/rpcauth \
  ./internal/coordinator/publisher ./internal/coordinatorclient \
  ./internal/gateway ./internal/info ./internal/routing \
  ./cmd/coordinator ./cmd/gate ./cmd/info ./cmd/zone
go test ./...
```

- [x] **Step 3: Run kind migration observation**

Observe before/after map versions and debug counters:

```text
all four subscribers connected
-> migrate one Shard
-> PREPARING arrives without a player request
-> ACTIVE arrives after durable commit
-> next Gate route uses Target
-> Coordinator single-Shard HTTP lookup counter does not increase on normal hit
```

Also inject one disconnected subscriber and one deliberately slow test
subscriber; healthy subscribers must continue, and the affected SDK must full
resync.

- [x] **Step 4: Record evidence and update CURRENT**

Evidence must include exact commands/output, observed delivery versions,
subscriber diagnostics, fallback behavior and explicit limitations:

- static zone-a/zone-b membership remains;
- no availability transitions are generated;
- no automatic rebalance/failover or Leader Election exists;
- queue/time defaults are unmeasured configuration assumptions.

## Done Criteria

- CoordinatorService unary and Watch RPCs are authenticated and wired.
- Publisher emits only durable committed PREPARING/ACTIVE changes.
- Slow subscribers cannot block RouteStore or other subscribers.
- SDK validates versions, resyncs on gaps and bounds disconnected cache use.
- Gate, Info and Zone use the same neutral SDK behind rollback switches.
- Ordinary route hits perform no Coordinator lookup.
- `NOT_OWNER`, HTTP snapshot and single-Shard lookup remain functional.
- Zone's 5-second polling is disabled only in verified SDK mode.
- SDK mode does not broadcast or persist legacy bulk per-Shard lease renewals;
  Watch freshness temporarily provides fail-closed control-connection expiry.
- Static membership, automatic migration, health decisions and HA remain out of
  scope and are not claimed.

## Next Phase Boundary

Only after SDK rollout evidence is complete, execute
`04-Zone身份与Kubernetes发现.md`. Phase 04 supplies real membership and
availability observations to the already verified transport; it must not
redesign the SDK or Watch protocol.
