# Stable Zone Identity and Kubernetes Discovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:executing-plans` and `superpowers:test-driven-development`.
> Execute only after Phases 01–03 are
> complete and reviewed. Do not calculate Desired placement, create migration
> tasks, change Current owners, perform failover or add Leader Election here.

**Goal:** Give every dynamically started Zone a stable logical identity, let
Coordinator discover Zone processes from Kubernetes, verify process liveness,
and publish transient Zone availability to subscribed SDKs without changing
Shard ownership.

**Architecture:** A Zone separates its stable `logical_zone_id`, per-process
`incarnation_id`, network endpoint and HMAC caller role. Coordinator watches
Pod and EndpointSlice objects, validates each discovered Zone through an
identity endpoint and `/livez`, then maintains an in-memory membership
snapshot. Membership changes produce only `AvailabilityBatch` messages;
durable Current ShardRoute remains authoritative and unchanged.

**Tech Stack:** Go 1.26, Kubernetes Pod/EndpointSlice Watch, `client-go`,
Protobuf/gRPC Watch transport from Phase 03, HTTP health probes, kind.

## Global Constraints

- Read ADR-0012, `00-总路线图.md`, and Phases 01–03 before editing.
- Read the owner-approved narrowed design at
  `docs/superpowers/specs/2026-08-13-third-zone-kubernetes-discovery-design.md`.
- Keep `ShardCount=4096`, Player→Shard hashing and durable Current Route
  unchanged.
- Kubernetes discovery reports candidates and availability; it never grants
  Shard ownership.
- `DEAD` in this phase pauses requests through SDK availability only. It does
  not reassign the dead Zone's Shards.
- Do not create Desired placement, migration tasks, Fence changes, automatic
  failover or Coordinator Leader Election.
- Keep the existing `zone-a` and `zone-b` workloads and their Current ownership
  until a later migration phase moves those Shards safely.
- `/livez` means the Zone process and event loop are alive. `/readyz` reads an
  in-memory startup latch and must not query Tcaplus on every probe.
- Storage errors from player requests must not mark a Zone dead.
- Membership and availability are transient and are not persisted in Tcaplus.
- Use namespace-scoped least-privilege RBAC; Coordinator may only get/list/watch
  Pods and EndpointSlices required for Zone discovery.
- Probe interval `10s`, timeout `2s`, and failure threshold `3` are configurable
  initial assumptions, not measured production values.

## Identity Model

Do not use one string for four different concerns:

| Concern | Value | Lifetime |
|---|---|---|
| routing identity | `logical_zone_id` | stable across normal restart |
| process identity | `incarnation_id` | new UUID on every Zone process start |
| network location | `endpoint` | replaceable without changing logical ID |
| HMAC authorization | caller role `zone` | shared bounded service role |

For StatefulSet candidates, derive the stable logical ID as UUIDv5 from:

```text
classic-farm/zone/<cluster_id>/<namespace>/<statefulset_name>/<ordinal>
```

The UUIDv5 namespace constant is committed in code and covered by golden
vectors. A new ordinal gets a new logical ID; restarting the same ordinal keeps
the logical ID but creates a new `incarnation_id`. Never derive ownership from
Pod IP, Pod UID or endpoint.

## Compatibility Topology

```text
Current owners: zone-a + zone-b Deployments (unchanged)
Candidate pool: zone-pool StatefulSet (first acceptance run 1 replica, owns no Shards)
Discovery: Zone Pods -> zone-discovery Service -> EndpointSlices
Coordinator: Pod/EndpointSlice Watch + identity/livez probe
Publisher: AvailabilityBatch -> Gate/Info/Zone SDK
```

The first acceptance run creates only `zone-pool-0`. Deterministic identity
tests must still cover ordinals `0..7`, proving the manifest can be scaled
later without changing code. Do not claim eight live Ready Pods unless a later
kind exercise captures that exact run.

## Target Runtime Flow

```text
EndpointSlice add/update
-> derive expected logical_zone_id from Pod topology
-> GET /internal/v1/zone-identity
-> verify logical ID, incarnation and endpoint
-> GET /livez
-> membership HEALTHY
-> availability_version++
-> Publisher.PublishAvailability
-> SDK updates local Zone availability
```

```text
livez failure #1/#2 -> SUSPECT -> publish, Current Route unchanged
livez recovery     -> HEALTHY -> publish, Current Route unchanged
livez failure #3 or Pod terminal/deleted
                   -> DEAD -> publish, Current Route unchanged
```

## Target File Map

| File | Responsibility |
|---|---|
| `server/internal/zoneidentity/identity.go` | derive/validate stable logical identity and create incarnation |
| `server/internal/zoneidentity/identity_test.go` | UUIDv5 vectors and restart semantics |
| `server/internal/coordinator/membership/types.go` | member and snapshot domain types |
| `server/internal/coordinator/membership/registry.go` | copy-on-write membership state and availability versions |
| `server/internal/coordinator/membership/kubernetes.go` | Pod/EndpointSlice informer adapter only |
| `server/internal/coordinator/membership/prober.go` | identity and `/livez` HTTP probes |
| `server/internal/coordinator/membership/controller.go` | reconcile observations into availability state |
| `server/cmd/zone/identity_handler.go` | internal identity HTTP endpoint |
| `server/cmd/zone/readiness.go` | in-memory readiness latch |
| `server/cmd/coordinator/membership_wiring.go` | config and lifecycle wiring |
| `deploy/k8s/zone-pool.yaml` | headless/discovery Services and candidate StatefulSet |
| `deploy/k8s/coordinator-rbac.yaml` | namespace-scoped discovery permissions |

## Task 1: Separate Zone logical, process, endpoint and HMAC identities

**Files:**

- Create: `server/internal/zoneidentity/identity.go`
- Create: `server/internal/zoneidentity/identity_test.go`
- Create: `server/cmd/zone/identity_handler.go`
- Create: `server/cmd/zone/identity_handler_test.go`
- Modify: `server/cmd/zone/main.go`
- Modify: `server/internal/platform/rpcauth/auth.go`
- Modify: `server/internal/platform/rpcauth/auth_test.go`
- Modify: Zone-originated gRPC client constructors currently using
  `OWNER_ZONE_ID` as `rpcauth.ClientConfig.Service`
- Modify: Zone-serving HMAC method allowlists currently listing
  `zone-local`, `zone-a`, `zone-b`
- Modify: `docs/contracts/internal-grpc.md`

**Interfaces:**

```go
package zoneidentity

type Config struct {
    ClusterID       string
    Namespace       string
    StatefulSetName string
    PodName         string
    LogicalOverride string
    Endpoint        string
}

type Identity struct {
    LogicalZoneID string
    IncarnationID string
    Endpoint      string
}

func New(cfg Config) (Identity, error)
func DeriveLogicalID(clusterID, namespace, statefulSetName string,
    ordinal int) (string, error)
func ParseOrdinal(podName, statefulSetName string) (int, error)
```

`New` rules:

- use `LogicalOverride` only for the compatibility workloads `zone-a` and
  `zone-b`;
- otherwise require non-empty cluster, namespace and StatefulSet names and a
  Pod name ending in `-<ordinal>`;
- derive a lowercase canonical UUIDv5 logical ID;
- generate a UUIDv4 `incarnation_id` on every call/process start;
- validate `Endpoint` with the existing internal-network URL policy;
- reject malformed or ambiguous Pod names instead of silently using a default.

Expose:

```text
GET /internal/v1/zone-identity
```

with JSON:

```json
{
  "logical_zone_id": "canonical-uuid-or-legacy-zone-a",
  "incarnation_id": "per-process-uuid",
  "endpoint": "http://zone-pool-0.zone-headless.classic-farm.svc.cluster.local:8082"
}
```

This endpoint is internal-only and contains no credentials or player data.

HMAC rules:

- Zone processes sign internal calls with caller service `zone`;
- the request's route/actor fields continue to carry `logical_zone_id` where
  ownership identity is required;
- method allowlists authorize the bounded role `zone`, not arbitrary UUIDs;
- do not add wildcard or prefix matching to `rpcauth`;
- keep `zone-a`, `zone-b`, and `zone-local` accepted only behind the existing
  legacy compatibility mode until all callers have moved to role `zone`.

- [ ] **Step 1: Write identity and HMAC regression tests**

Cover fixed UUIDv5 vectors for ordinals 0 and 7, same ordinal after restart,
different incarnation per `New`, different ordinal, malformed Pod name,
missing topology, invalid endpoint, legacy override, caller role `zone`, and
legacy compatibility on/off.

Run:

```bash
cd server
go test ./internal/zoneidentity ./internal/platform/rpcauth ./cmd/zone
```

Expected: FAIL before the identity package and role split exist.

- [ ] **Step 2: Implement identity derivation and the identity endpoint**

Keep UUID namespace and derivation input explicit constants. Wire one Identity
instance at Zone startup and pass it to runtime authorization, endpoint
handler and Coordinator SDK subscriber configuration.

- [ ] **Step 3: Replace Zone-originated HMAC caller strings**

Search with:

```bash
rg -n 'OWNER_ZONE_ID|zone-local|zone-a|zone-b|ClientConfig.*Service' \
  server/cmd/zone server/internal
```

Change only authentication identity to role `zone`; do not replace route owner
IDs stored in checkpoints, Fence, ShardRoute or interaction records.

- [ ] **Step 4: Run focused tests and repository regression**

```bash
cd server
go test ./internal/zoneidentity ./internal/platform/rpcauth ./cmd/zone
go test ./...
```

Expected: PASS; legacy static dual-Zone behavior remains green.

## Task 2: Make Zone readiness an in-memory startup state

**Files:**

- Create: `server/cmd/zone/readiness.go`
- Create: `server/cmd/zone/readiness_test.go`
- Modify: `server/cmd/zone/main.go`
- Modify if reusable state is chosen: `server/internal/platform/health/health.go`
- Modify if reusable state is chosen: `server/internal/platform/health/health_test.go`

**Interfaces:**

```go
type readinessState struct {
    // atomic state; no network or storage call in Ready
}

func newReadinessState() *readinessState
func (s *readinessState) SetReady()
func (s *readinessState) SetNotReady(reason string)
func (s *readinessState) Check(context.Context) error
```

State transitions:

```text
process starts                            -> not ready
configuration + stores + route SDK ready -> ready
graceful shutdown begins                 -> not ready
```

`/livez` remains `200` while the process can serve its event loop.
`/readyz` calls only the in-memory check. Database connectivity is validated
during startup where already required; no health probe performs a repeated
Tcaplus query. A later request-time storage error returns the normal service
error and is not translated into Zone death.

- [ ] **Step 1: Write handler/state transition tests**

Assert initial `/readyz=503`, `/livez=200`, ready transition to `200`, shutdown
transition to `503`, and zero invocation of a fake storage function across
repeated probes.

- [ ] **Step 2: Implement and wire the readiness latch**

Set ready only after all startup dependencies used by requests are initialized.
Set not-ready before stopping gRPC/HTTP acceptance during graceful shutdown.

- [ ] **Step 3: Run focused tests**

```bash
cd server
go test ./internal/platform/health ./cmd/zone
```

Expected: PASS.

## Task 3: Add the membership domain registry

**Files:**

- Create: `server/internal/coordinator/membership/types.go`
- Create: `server/internal/coordinator/membership/registry.go`
- Create: `server/internal/coordinator/membership/registry_test.go`

**Interfaces:**

```go
type State uint8

const (
    StateUnknown State = iota
    StateHealthy
    StateSuspect
    StateDead
    StateDraining
)

type Member struct {
    LogicalZoneID     string
    IncarnationID     string
    Endpoint          string
    Namespace         string
    PodName           string
    PodUID            string
    ResourceVersion   string
    State             State
    ConsecutiveFailures int
    ObservedAt        time.Time
}

type Snapshot struct {
    AvailabilityVersion uint64
    Members             []Member
}

type Registry struct { /* private copy-on-write state */ }

func NewRegistry(now func() time.Time) *Registry
func (r *Registry) Apply(observation Observation) (Snapshot, bool, error)
func (r *Registry) Snapshot() Snapshot
```

Registry invariants:

- one active incarnation per logical Zone ID;
- one logical identity cannot resolve to two live Pods/endpoints;
- an endpoint change for the same incarnation is allowed and versioned;
- a new valid incarnation atomically replaces the old incarnation;
- stale Kubernetes `resourceVersion` or stale probe results cannot overwrite a
  newer observation;
- `availability_version` increments only when externally visible identity,
  endpoint or state changes;
- snapshots are sorted by `logical_zone_id` and cannot be mutated by callers;
- registry never reads or writes ShardRoute, Fence or MigrationProgress.

- [ ] **Step 1: Write table and concurrency tests**

Cover first HEALTHY observation, duplicate no-op, HEALTHY→SUSPECT→HEALTHY,
third failure to DEAD, new incarnation, stale observation rejection,
conflicting Pod rejection, endpoint replacement, sorted immutable snapshots
and concurrent Apply/Snapshot under `go test -race`.

- [ ] **Step 2: Implement the minimal copy-on-write registry**

Keep failure-threshold policy outside the map mutation primitive where
possible; the controller supplies the intended next state and counters.

- [ ] **Step 3: Run race tests**

```bash
cd server
go test -race ./internal/coordinator/membership
```

Expected: PASS.

## Task 4: Implement Kubernetes Pod and EndpointSlice discovery

**Files:**

- Create: `server/internal/coordinator/membership/kubernetes.go`
- Create: `server/internal/coordinator/membership/kubernetes_test.go`
- Modify: `server/go.mod`
- Modify: `server/go.sum`

**Interfaces:**

```go
type EndpointObservation struct {
    Namespace       string
    PodName         string
    PodUID          string
    ResourceVersion string
    ClusterID       string
    StatefulSetName string
    Ordinal         int
    Endpoint        string
    EndpointReady   bool
    PodPhase        string
    Deleting        bool
}

type ObservationSink interface {
    UpsertEndpoint(EndpointObservation)
    DeletePod(namespace, name, uid, resourceVersion string)
}

type KubernetesSource struct { /* informer lifecycle */ }

func NewKubernetesSource(client kubernetes.Interface, namespace,
    serviceName, clusterID string, sink ObservationSink) (*KubernetesSource, error)
func (s *KubernetesSource) Run(ctx context.Context) error
```

Dependency rule: before editing `go.mod`, run `kubectl version -o yaml` and pin
`k8s.io/client-go`, `k8s.io/api` and `k8s.io/apimachinery` to one exact mutually
compatible minor supported by that cluster. Record the selected versions in
the phase evidence; never leave them as floating `latest` dependencies.

Watch rules:

- namespace is required and defaults from `POD_NAMESPACE`, not all namespaces;
- EndpointSlices must carry
  `kubernetes.io/service-name=zone-discovery`;
- accept only the named HTTP port used by Zone;
- require Endpoint `targetRef.kind=Pod`, Pod UID match and expected Zone labels;
- derive StatefulSet name and ordinal from the Pod owner/name, not Pod IP;
- use Endpoint condition `ready`; an unready endpoint is an observation, not
  proof of ownership loss;
- Pod `Failed`, `Succeeded` or deletion produces a terminal observation;
- informer resync/relist must converge without duplicate version increments;
- initial informer cache sync must complete before Coordinator reports its
  membership source ready;
- no handler may call Tcaplus or mutate routing state.

- [ ] **Step 1: Write fake-client informer tests**

Use Kubernetes fake clients and real informer callbacks. Cover add/update/
delete, unrelated service, wrong namespace, wrong port, missing Pod target,
not-ready endpoint, Pod UID replacement, terminal Pod and relist duplicates.

- [ ] **Step 2: Pin Kubernetes libraries and implement the adapter**

Keep Kubernetes objects inside this file. Convert immediately to the domain
`EndpointObservation` so registry/controller tests do not depend on
Kubernetes packages.

- [ ] **Step 3: Run focused tests**

```bash
cd server
go test -race ./internal/coordinator/membership
```

Expected: PASS.

## Task 5: Probe identity and liveness, then drive membership states

**Files:**

- Create: `server/internal/coordinator/membership/prober.go`
- Create: `server/internal/coordinator/membership/prober_test.go`
- Create: `server/internal/coordinator/membership/controller.go`
- Create: `server/internal/coordinator/membership/controller_test.go`

**Interfaces:**

```go
type ProbeResult struct {
    LogicalZoneID string
    IncarnationID string
    Endpoint      string
    Live          bool
    ObservedAt    time.Time
    Err           error
}

type Prober interface {
    Probe(ctx context.Context, endpoint string) ProbeResult
}

type ControllerConfig struct {
    ProbeInterval    time.Duration // 10s
    ProbeTimeout     time.Duration // 2s
    FailureThreshold int           // 3
}

func NewController(source Source, registry *Registry, prober Prober,
    publisher AvailabilityPublisher, cfg ControllerConfig) (*Controller, error)
func (c *Controller) Run(ctx context.Context) error
```

Probe behavior:

1. request `/internal/v1/zone-identity`;
2. verify response logical ID equals the topology-derived expected ID, except
   explicit legacy `zone-a/zone-b` overrides;
3. validate canonical UUIDs and `incarnation_id`;
4. verify the advertised endpoint equals the discovered endpoint after URL
   normalization;
5. request `/livez` with a separate `2s` timeout;
6. close response bodies, reject redirects and cap identity response to 4 KiB;
7. never call `/readyz` for ongoing liveness.

State policy:

- valid identity plus successful `/livez` -> HEALTHY and failures reset to 0;
- first or second liveness failure -> SUSPECT;
- third consecutive liveness failure -> DEAD;
- Pod terminal/deleted -> DEAD immediately;
- Endpoint temporarily unready -> SUSPECT, then active probe decides recovery;
- identity mismatch/conflicting live incarnation -> SUSPECT plus diagnostic,
  never HEALTHY;
- request-time Tcaplus failures are outside this controller and cannot enter
  the failure counter.

Every visible registry change converts the complete current membership
snapshot to a Phase 03 `AvailabilityBatch` and calls
`Publisher.PublishAvailability`. Ownership/map version fields remain
unchanged. On each new subscriber stream, Publisher sends a full authoritative
availability batch with `previous_availability_version=0`; the SDK replaces
its availability table even after Coordinator restart resets transient
availability versioning.

- [ ] **Step 1: Write HTTP probe security and state-machine tests**

Cover valid identity/live, oversized body, redirect, identity mismatch,
endpoint mismatch, timeout, two failures then recovery, three failures to
DEAD, terminal Pod immediate DEAD and cancellation without goroutine leaks.

- [ ] **Step 2: Implement bounded probing and controller scheduling**

Use one bounded worker pool sized by configuration; do not create an unbounded
goroutine per Zone per tick. An EndpointSlice event schedules an immediate
probe, while the ticker supplies periodic confirmation.

- [ ] **Step 3: Verify race and leak-sensitive tests**

```bash
cd server
go test -race ./internal/coordinator/membership \
  ./internal/coordinator/publisher ./internal/coordinatorclient
```

Expected: PASS.

## Task 6: Wire membership into Coordinator with a rollback switch

**Files:**

- Create: `server/cmd/coordinator/membership_wiring.go`
- Create: `server/cmd/coordinator/membership_wiring_test.go`
- Modify: `server/cmd/coordinator/main.go`
- Modify: `server/internal/coordinator/publisher/publisher.go`
- Modify: `server/internal/coordinator/publisher/publisher_test.go`

**Configuration:**

```text
COORDINATOR_MEMBERSHIP_SOURCE=static|kubernetes
CLUSTER_ID=classic-farm-local
POD_NAMESPACE=classic-farm
ZONE_DISCOVERY_SERVICE=zone-discovery
ZONE_LIVE_PROBE_INTERVAL=10s
ZONE_LIVE_PROBE_TIMEOUT=2s
ZONE_LIVE_FAILURE_THRESHOLD=3
ZONE_PROBE_WORKERS=8
```

Rules:

- default remains `static` until the Kubernetes mode passes its phase gate;
- static mode reports configured `zone-a/zone-b` through the same membership
  registry/controller interfaces so Publisher behavior is shared;
- Kubernetes mode builds in-cluster config, starts informers, waits for cache
  sync and then starts probes;
- membership readiness failure must not fabricate a new routing map;
- Coordinator shutdown cancels informers, probe workers and Publisher cleanly;
- expose diagnostics for discovered/healthy/suspect/dead counts, last Watch
  resource version, last successful probe and availability version;
- do not add a route mutation callback to membership wiring.

- [ ] **Step 1: Write wiring tests with fake source/prober/publisher**

Verify static default, Kubernetes config validation, initial cache-sync gate,
publication, cancellation and the invariant that RouteStore commit count is
zero for all membership events.

- [ ] **Step 2: Wire both sources and full availability snapshots**

Ensure Phase 03 SDK treats `previous_availability_version=0` immediately after
Subscribe as authoritative replacement, including after Coordinator restart.

- [ ] **Step 3: Run Coordinator and SDK regression**

```bash
cd server
go test -race ./cmd/coordinator ./internal/coordinator/... \
  ./internal/coordinatorclient
go test ./...
```

Expected: PASS.

## Task 7: Add Kubernetes candidate Zone pool and least-privilege RBAC

**Files:**

- Create: `deploy/k8s/zone-pool.yaml`
- Create: `deploy/k8s/coordinator-rbac.yaml`
- Modify: `deploy/k8s/zone.yaml`
- Modify: `deploy/k8s/services.yaml`
- Modify: `deploy/k8s/coordinator.yaml`
- Modify: `deploy/k8s/kustomization.yaml`
- Modify: `deploy/k8s/configmap.yaml`

**Manifest requirements:**

- preserve existing `zone-a` and `zone-b` Deployments and Services;
- add `zone-pool` StatefulSet with `replicas: 1` and
  `serviceName: zone-headless`;
- add headless `zone-headless` Service and selector-based `zone-discovery`
  Service so Kubernetes creates EndpointSlices;
- inject `POD_NAME` and `POD_NAMESPACE` with the Downward API;
- set `ZONE_STATEFULSET_NAME=zone-pool`, `CLUSTER_ID=classic-farm-local`,
  `INTERNAL_RPC_CALLER_SERVICE=zone`, and advertise stable per-Pod DNS;
- candidate pods run the same Zone image but start with zero Current Shards;
  they must not synthesize ownership from their ordinal;
- apply readiness `/readyz` and liveness `/livez` probes with independent
  thresholds;
- give Coordinator a dedicated ServiceAccount, Role and RoleBinding;
- RBAC resources are namespace-scoped and allow only `get/list/watch` on
  `pods` and `discovery.k8s.io/endpointslices`;
- do not grant access to Secrets, ConfigMaps, Nodes or all namespaces;
- Coordinator uses `COORDINATOR_MEMBERSHIP_SOURCE=kubernetes` only in the
  explicit validation overlay/switch, not as an unverified universal default.

- [ ] **Step 1: Render and inspect manifests before applying**

```bash
kubectl kustomize deploy/k8s >/tmp/classic-farm-rendered.yaml
kubectl apply --dry-run=client -f /tmp/classic-farm-rendered.yaml
rg -n 'kind: StatefulSet|replicas: 1|zone-discovery|EndpointSlice|pods' \
  /tmp/classic-farm-rendered.yaml
```

Expected: render and dry-run PASS; old owner workloads and new candidate pool
are both present.

- [ ] **Step 2: Add RBAC permission assertions**

After applying in kind:

```bash
kubectl auth can-i get pods \
  --as=system:serviceaccount:classic-farm:coordinator -n classic-farm
kubectl auth can-i watch endpointslices.discovery.k8s.io \
  --as=system:serviceaccount:classic-farm:coordinator -n classic-farm
kubectl auth can-i get secrets \
  --as=system:serviceaccount:classic-farm:coordinator -n classic-farm
```

Expected: `yes`, `yes`, `no`.

- [ ] **Step 3: Apply and observe without changing Current ownership**

```bash
kubectl apply -k deploy/k8s
kubectl rollout status deployment/coordinator -n classic-farm
kubectl rollout status statefulset/zone-pool -n classic-farm
kubectl get pods,endpointslices -n classic-farm -o wide
```

Before and after the rollout, save Coordinator route snapshots and compare all
4096 `owner_zone_id`, `owner_epoch`, `route_version` and `map_version` values.
Expected: byte-equivalent ownership/version fields.

## Task 8: Verify availability publication and compatibility

**Files:**

- Create: `server/test/e2e/coordinator_membership_test.go`
- Modify: `server/test/e2e/dual_zone_routing_test.go` only if setup must expose
  the new identity endpoint
- Create after successful execution:
  `docs/evidence/2026-08-13-zone-identity-k8s-discovery.md`
- Modify after successful execution: `docs/context/CURRENT.md`

**Required checks:**

1. UUIDv5 vectors produce eight unique, restart-stable logical IDs in unit
   tests; the first live run creates only ordinal 0.
2. Candidate Zone process restart preserves logical ID and changes incarnation.
3. EndpointSlice add reaches HEALTHY and is visible in Gate/Info/Zone SDKs.
4. One and two forced `/livez` failures publish SUSPECT.
5. Recovery before the third failure publishes HEALTHY without changing epoch.
6. Three failures publish DEAD, and requests for Shards whose Current owner is
   DEAD fail closed with the Phase 01 retryable availability error.
7. DEAD causes zero RouteStore/Fence/MigrationProgress writes in this phase.
8. Coordinator restart rebuilds membership and SDKs accept a full transient
   availability replacement.
9. Existing `zone-a/zone-b` single-player, friend visit, broadcast and steal
   flows still work.
10. No storage failure is counted as a liveness failure.

- [ ] **Step 1: Run focused and full Go tests**

```bash
cd server
go test -race ./internal/zoneidentity ./internal/coordinator/membership \
  ./internal/coordinator/publisher ./internal/coordinatorclient \
  ./cmd/coordinator ./cmd/zone
go test ./...
```

Expected: PASS.

- [ ] **Step 2: Run kind discovery scenarios**

Capture commands, timestamps, Pod/EndpointSlice snapshots, Coordinator logs,
SDK availability observations, route snapshots and exact limitations. If the
machine cannot run eight candidates, record the smaller measured count and do
not convert the manifest target into a measured claim.

- [ ] **Step 3: Write evidence and update handoff**

Evidence must distinguish:

- measured: tests and exact live Pod count actually run;
- derived: UUID stability and version invariants demonstrated by tests;
- assumed: probe timing and worker-pool sizes;
- not implemented: Desired planning, migration, failover and leader election.

Update `docs/context/CURRENT.md` only after verification. State explicitly that
discovery changes availability only and Current ownership is still unchanged.

## Phase Completion Gate

Phase 04 is complete only when:

- stable logical identity and per-process incarnation are separate and tested;
- dynamic Zones authenticate with role `zone` without wildcard HMAC callers;
- `/livez` and in-memory `/readyz` have distinct semantics;
- Coordinator discovers expected Pods/EndpointSlices with scoped RBAC;
- identity/liveness probes produce versioned HEALTHY/SUSPECT/DEAD updates;
- Gate/Info/Zone SDKs receive initial and incremental availability;
- all membership transitions produce zero Current Route/Fence/migration writes;
- old `zone-a/zone-b` ownership and game regressions remain green;
- evidence and `CURRENT.md` state the tested boundary honestly.

## Rollback

Set `COORDINATOR_MEMBERSHIP_SOURCE=static`, scale `zone-pool` to zero, and keep
the existing `zone-a/zone-b` Deployments. Do not delete or recompute durable
ShardRoute rows. The Phase 03 SDK/HTTP fallback remains available.

## Next Phase

Only after this gate passes, execute `05-Placement与Rebalance队列.md` to compute
deterministic Desired placement from HEALTHY membership and create bounded
rebalance work. Phase 05 still must not make a new owner Active by calculation
alone.
