# Coordinator Contract and Compatibility Baseline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:test-driven-development` while implementing this phase. Do not
> begin Phase 02 in the same task.

**Goal:** Freeze one implementation-facing route/Watch/error contract while
preserving the current static dual-Zone runtime behavior.

**Architecture:** Extend the existing data-model route messages instead of
creating duplicate route types. Put Coordinator subscription traffic in its
own internal gRPC service. Generate code and add compatibility tests, but do
not wire the service, persist ShardRoute, add Kubernetes dependencies, or
change Gate/Zone runtime behavior in this phase.

**Tech Stack:** Protobuf, Buf, Go 1.26, gRPC generated types, existing routing
and Gate unit tests.

## Global Constraints

- Read ADR-0012 and `00-总路线图.md` before editing.
- Do not change field numbers or meanings of existing Protobuf fields.
- Do not modify `ShardCount`, `StableHash64`, `ShardForPlayer`, or the
  Rendezvous score domain.
- Existing HTTP route endpoints remain available and unchanged.
- Existing `SERVICE_UNAVAILABLE` behavior remains unchanged in this phase;
  new error codes are contract-only until their owning phases wire them.
- Do not add `client-go`, Tcaplus ShardRoute storage, Watch server wiring,
  membership discovery, migration workers, or Actor limiters.
- Generated Go/TypeScript files must come from `buf generate`; never edit them
  manually.

## Current Contract Facts

- `proto/classicfarm/v1/data/data_model.proto` already contains
  `ShardMapSnapshot` and `ShardRouteEntry`.
- `ShardMapSnapshot` lacks `assignment_algorithm_version`.
- `ShardRouteEntry` lacks `owner_endpoint` and an incarnation identifier.
- `proto/classicfarm/v1/rpc/runtime.proto` already has a small
  request-facing `CommittedRoute`; keep it because Zone RPCs do not need the
  complete control-plane record.
- `proto/classicfarm/v1/ws/ws.proto` currently has generic retryable
  `SERVICE_UNAVAILABLE`/`SERVER_BUSY`, but no routing lifecycle codes.
- `server/internal/routing/http.go` and Gate HTTP adapters already expose and
  consume endpoint plus assignment version outside Protobuf.

## Task 1: Extend the existing route data model

**Files:**

- Modify: `proto/classicfarm/v1/data/data_model.proto`
- Generated: `server/gen/classicfarm/v1/data/data_model.pb.go`
- Generated: `web/src/gen/classicfarm/v1/data/data_model_pb.ts`
- Test: `server/gen/smoke/roundtrip_test.go`

**Interfaces:**

- Preserve every existing field number in `ShardMapSnapshot` and
  `ShardRouteEntry`.
- Add `assignment_algorithm_version` as field 7 of `ShardMapSnapshot`.
- Add `owner_endpoint` as field 12 of `ShardRouteEntry`.
- Do **not** add `incarnation_id` to durable `ShardRouteEntry`; incarnation is
  runtime membership evidence and is defined in Task 2's `ZoneIdentity`.

- [ ] **Step 1: Add a failing generated-type smoke test**

Extend `server/gen/smoke/roundtrip_test.go` with a test that constructs a
`ShardMapSnapshot` containing one `ShardRouteEntry`, marshals/unmarshals it,
and asserts:

```go
if got.GetAssignmentAlgorithmVersion() != 1 {
    t.Fatalf("assignment_algorithm_version=%d want 1", got.GetAssignmentAlgorithmVersion())
}
if got.GetEntries()[0].GetOwnerEndpoint() != "http://zone-a:8082" {
    t.Fatalf("owner_endpoint=%q", got.GetEntries()[0].GetOwnerEndpoint())
}
```

- [ ] **Step 2: Run the narrow test and confirm it fails to compile**

```bash
cd server
go test ./gen/smoke
```

Expected: FAIL because the two generated accessors do not exist.

- [ ] **Step 3: Extend the messages additively**

Apply exactly these additions:

```proto
message ShardMapSnapshot {
  // existing fields 1..6 remain unchanged
  uint32 assignment_algorithm_version = 7;
}

message ShardRouteEntry {
  // existing fields 1..11 remain unchanged
  string owner_endpoint = 12;
}
```

- [ ] **Step 4: Regenerate and run the smoke test**

```bash
buf lint
buf generate
cd server
go test ./gen/smoke
```

Expected: PASS. Confirm generated changes are limited to the expected Go and
TypeScript Protobuf outputs.

## Task 2: Define the Coordinator control-plane gRPC contract

**Files:**

- Create: `proto/classicfarm/v1/coordinator/coordinator.proto`
- Generated: `server/gen/classicfarm/v1/coordinator/coordinator.pb.go`
- Generated: `server/gen/classicfarm/v1/coordinator/coordinator_grpc.pb.go`
- Generated: `web/src/gen/classicfarm/v1/coordinator/coordinator_pb.ts`
- Test: `server/gen/smoke/roundtrip_test.go`

**Interfaces:**

The package is `classicfarm.coordinator.v1`, with Go package:

```proto
option go_package = "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/coordinator;coordinatorv1";
```

The Phase 03 server/client must consume these exact RPCs and messages; Phase 01
only generates and round-trips them.

- [ ] **Step 1: Write the failing smoke test against the future package**

Add a test that imports `coordinatorv1`, creates a Subscribe request and a
RouteBatch carrying one existing `datav1.ShardRouteEntry`, then verifies a
Protobuf round trip preserves subscriber ID, versions, shard ID and endpoint.

Run:

```bash
cd server
go test ./gen/smoke
```

Expected: FAIL because the package does not exist.

- [ ] **Step 2: Create `coordinator.proto` with the frozen service**

Use this service shape:

```proto
syntax = "proto3";

package classicfarm.coordinator.v1;

import "classicfarm/v1/data/data_model.proto";

option go_package = "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/coordinator;coordinatorv1";

service CoordinatorService {
  rpc GetRouteSnapshot(GetRouteSnapshotRequest) returns (GetRouteSnapshotResponse);
  rpc GetShardRoute(GetShardRouteRequest) returns (GetShardRouteResponse);
  rpc WatchRoutes(stream WatchRoutesRequest) returns (stream WatchRoutesResponse);
  rpc ReportZoneFailure(ReportZoneFailureRequest) returns (ReportZoneFailureResponse);
}
```

Define these enums:

```proto
enum SubscriberKind {
  SUBSCRIBER_KIND_UNSPECIFIED = 0;
  SUBSCRIBER_KIND_GATE = 1;
  SUBSCRIBER_KIND_INFO = 2;
  SUBSCRIBER_KIND_ZONE = 3;
  SUBSCRIBER_KIND_OTHER = 4;
}

enum ZoneAvailability {
  ZONE_AVAILABILITY_UNSPECIFIED = 0;
  ZONE_AVAILABILITY_HEALTHY = 1;
  ZONE_AVAILABILITY_SUSPECT = 2;
  ZONE_AVAILABILITY_DEAD = 3;
  ZONE_AVAILABILITY_DRAINING = 4;
}

enum ZoneFailureKind {
  ZONE_FAILURE_KIND_UNSPECIFIED = 0;
  ZONE_FAILURE_KIND_CONNECTION_REFUSED = 1;
  ZONE_FAILURE_KIND_TIMEOUT = 2;
  ZONE_FAILURE_KIND_CONNECTION_RESET = 3;
  ZONE_FAILURE_KIND_GRPC_UNAVAILABLE = 4;
}
```

Define runtime membership identity:

```proto
message ZoneIdentity {
  string logical_zone_id = 1;
  string incarnation_id = 2;
  string endpoint = 3;
}
```

Define unary lookup messages:

```proto
message GetRouteSnapshotRequest {}
message GetRouteSnapshotResponse {
  classicfarm.data.v1.ShardMapSnapshot snapshot = 1;
}
message GetShardRouteRequest { uint32 shard_id = 1; }
message GetShardRouteResponse {
  classicfarm.data.v1.ShardRouteEntry route = 1;
  uint64 map_version = 2;
}
```

Define the bidirectional Watch request using `oneof`:

```proto
message WatchRoutesRequest {
  oneof payload {
    Subscribe subscribe = 1;
    RouteAck ack = 2;
    WatchPong pong = 3;
  }
}

message Subscribe {
  string subscriber_id = 1;
  SubscriberKind kind = 2;
  uint64 last_map_version = 3;
  uint64 last_availability_version = 4;
}

message RouteAck { uint64 map_version = 1; }
message WatchPong { uint64 ping_id = 1; }
```

Define the Watch response:

```proto
message WatchRoutesResponse {
  oneof payload {
    classicfarm.data.v1.ShardMapSnapshot snapshot = 1;
    RouteBatch route_batch = 2;
    AvailabilityBatch availability_batch = 3;
    WatchPing ping = 4;
    ResyncRequired resync_required = 5;
  }
}

message RouteBatch {
  uint64 previous_map_version = 1;
  uint64 map_version = 2;
  repeated classicfarm.data.v1.ShardRouteEntry routes = 3;
}

message ZoneAvailabilityEntry {
  string logical_zone_id = 1;
  ZoneAvailability availability = 2;
  string incarnation_id = 3;
  int64 observed_at_ms = 4;
}

message AvailabilityBatch {
  uint64 previous_availability_version = 1;
  uint64 availability_version = 2;
  repeated ZoneAvailabilityEntry zones = 3;
}

message WatchPing { uint64 ping_id = 1; }
message ResyncRequired { string reason = 1; }
```

Define failure reporting as evidence, not a death decision:

```proto
message ReportZoneFailureRequest {
  string reporter_id = 1;
  SubscriberKind reporter_kind = 2;
  uint64 player_id = 3;
  uint32 shard_id = 4;
  string logical_zone_id = 5;
  string incarnation_id = 6;
  ZoneFailureKind failure_kind = 7;
  int64 observed_at_ms = 8;
}

message ReportZoneFailureResponse {
  ZoneAvailability observed_availability = 1;
}
```

- [ ] **Step 3: Generate code and pass contract tests**

```bash
buf lint
buf generate
cd server
go test ./gen/smoke
```

Expected: PASS. Do not create a CoordinatorService implementation yet.

## Task 3: Freeze routing lifecycle error codes

**Files:**

- Modify: `proto/classicfarm/v1/ws/ws.proto`
- Modify: `docs/contracts/idempotency-and-errors.md`
- Generated: `server/gen/classicfarm/v1/ws/ws.pb.go`
- Generated: `web/src/gen/classicfarm/v1/ws/ws_pb.ts`
- Test: `server/gen/smoke/roundtrip_test.go`

**Interfaces:**

Add these values without renumbering existing errors:

```proto
ZONE_MIGRATING = 204;
ZONE_UNAVAILABLE = 205;
ZONE_WARMING_UP = 206;
STORAGE_UNAVAILABLE = 207;
```

All four are retryable. A retry of a command with uncertain execution keeps the
same `request_id`; the client must honor `retry_after_ms` when supplied.

- [ ] **Step 1: Add a failing enum-number test**

Assert the four generated enum constants have exactly 204–207. Run
`go test ./gen/smoke` and confirm compilation fails before generation.

- [ ] **Step 2: Extend `ErrorCode` and the error contract**

Document exact meanings:

| Code | Meaning | Retry rule |
|---|---|---|
| `ZONE_MIGRATING` | Controlled Shard migration is in progress | Retry same request ID after delay |
| `ZONE_UNAVAILABLE` | Route is temporarily paused due to SUSPECT/DEAD handling | Retry same request ID after delay |
| `ZONE_WARMING_UP` | Actor activation admission queue is full or timed out | Retry same request ID after delay |
| `STORAGE_UNAVAILABLE` | Zone is alive but its recovery store operation failed | Retry same request ID; never report Zone dead from this code |

Do not modify Gate/Zone error mapping in this task.

- [ ] **Step 3: Regenerate and validate**

```bash
buf lint
buf generate
cd server
go test ./gen/smoke
```

Expected: PASS.

## Task 4: Lock the existing static compatibility baseline

**Files:**

- Modify only if assertions are missing:
  `server/internal/routing/routing_test.go`
- Modify only if assertions are missing:
  `server/internal/routing/http_test.go`
- Modify only if assertions are missing:
  `server/internal/gateway/route_cache_test.go`
- Modify only if assertions are missing:
  `server/cmd/coordinator/migration_recovery_test.go`
- Create evidence after successful verification:
  `docs/archive/evidence/historical/2026-08-12-coordinator-contract-baseline.md`
- Modify after successful verification: `docs/context/CURRENT.md`

**Required assertions:**

- `ShardForPlayer` remains deterministic and always `<4096`;
- Rendezvous output is independent of candidate input ordering;
- the existing static two-Zone snapshot contains 4096 ordered ACTIVE routes;
- Gate Warm performs one full snapshot load and cache hits perform zero
  per-Shard lookups;
- `InvalidateIfVersion` removes only the matching stale version;
- an open PREPARING migration is restored from MigrationProgress;
- the HTTP route JSON remains backward compatible.

- [ ] **Step 1: Run the narrow baseline before editing tests**

```bash
cd server
go test ./internal/routing ./internal/gateway ./cmd/coordinator
```

If all required assertions already exist, do not duplicate them. Record the
existing test names in evidence. Add only genuinely missing assertions.

- [ ] **Step 2: Run generated and server regression**

```bash
buf lint
buf generate
cd server
go test ./gen/smoke
go test ./internal/routing ./internal/gateway ./cmd/coordinator ./cmd/zone
go test ./...
```

Expected: PASS. This phase must not require kind, Tcaplus or network access.

- [ ] **Step 3: Inspect the scoped diff**

Expected changed source scope:

```text
proto/classicfarm/v1/data/data_model.proto
proto/classicfarm/v1/coordinator/coordinator.proto
proto/classicfarm/v1/ws/ws.proto
generated Go/TypeScript Protobuf files
docs/contracts/idempotency-and-errors.md
targeted tests only when coverage was missing
```

Reject unrelated changes to Coordinator runtime, Gate routing, Zone polling,
Kubernetes manifests, Tcaplus schemas or Player Runtime.

- [ ] **Step 4: Record evidence and handoff**

The evidence file must include:

- exact commands and outputs;
- changed contract fields and enum numbers;
- confirmation that no service wiring/runtime behavior changed;
- confirmation that static dual-Zone compatibility tests passed;
- limitations: Watch, persistence and new errors are not yet wired.

Update `docs/context/CURRENT.md` with one concise completed-phase bullet only
after all commands pass.

## Done Criteria

- Existing route messages are extended rather than duplicated.
- CoordinatorService and Watch messages generate for Go and TypeScript.
- Routing lifecycle errors have stable numeric codes and documented retry
  semantics.
- Existing static routing/migration behavior is unchanged and green.
- No Kubernetes dependency, ShardRoute store or Watch implementation exists.
- Evidence and CURRENT accurately distinguish generated contract from runtime
  capability.

## Next Phase Boundary

Only after this phase is reviewed, execute `02-权威ShardRoute持久化.md`.
Phase 02 may use `ShardRouteEntry.owner_endpoint` and the frozen version fields,
but must not yet wire `WatchRoutes` consumers.

