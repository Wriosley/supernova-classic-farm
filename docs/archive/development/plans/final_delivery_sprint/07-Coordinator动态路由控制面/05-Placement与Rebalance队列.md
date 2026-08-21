# Placement and Rebalance Queue Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:executing-plans` and `superpowers:test-driven-development`.
> Execute only after Phases 01–04 pass. This phase calculates Desired and
> persists work; it must not drain Zones, advance Fence or mutate Current.

**Goal:** Deterministically calculate Desired owners from HEALTHY Zone
membership and persist a bounded, deduplicated migration queue for differences
between Desired and durable Current.

**Architecture:** A pure placement package reuses the existing Rendezvous
score. A planner compares immutable Current and membership snapshots and emits
proposals. A queue store materializes proposals as Tcaplus `MigrationTask`
rows. Calculation never grants ownership.

**Tech Stack:** Go 1.26, SHA-256 Rendezvous, Tcaplus CAS, existing routing and
membership packages.

## Global Constraints

- Keep 4096 Shards and `ShardForPlayer` unchanged.
- Use only HEALTHY Zones for new Desired calculation.
- Pure Rendezvous: no forced equal split and no load correction.
- Existing owner remains Current until Phase 06 completes migration.
- Priority order is `FAILOVER > DRAIN > REBALANCE`.
- No Source/Target RPC, Fence write, RouteStore commit or Publisher call here.
- Use local unit/race tests during Tasks 1–4. Build the Coordinator Docker
  image only for the final kind/Tcaplus gate.
- The live gate uses three `zone-pool` candidates plus `zone-a` and `zone-b`;
  the 8→9 transition remains an offline deterministic test.
- Do not ask the owner to create `MigrationTask` until Task 2 schema generation
  and fake-Tcaplus round-trip tests pass.

## Task 1: Extract pure placement calculation

**Files:**
- Create: `server/internal/coordinator/placement/placement.go`
- Create: `server/internal/coordinator/placement/placement_test.go`
- Modify: `server/internal/routing/routing.go` only to export the existing
  score helper without changing its bytes

**Interfaces:**

```go
type Candidate struct { LogicalZoneID, Endpoint string }
type DesiredEntry struct { ShardID uint32; OwnerZoneID, OwnerEndpoint string }
func Compute(shardCount uint32, assignmentVersion uint32,
    candidates []Candidate) ([]DesiredEntry, error)
```

- Sort/deduplicate candidates by logical ID before calculation.
- Score exact bytes already frozen by ADR-0012; highest score wins, equal score
  uses logical-ID byte order.
- Reject empty candidates, duplicate ID with conflicting endpoint, unsupported
  assignment version and shard count other than 4096.

- [x] Write golden-vector tests for 8 candidates, input-order independence,
  deterministic restart, duplicate rejection and statistical counts.
- [x] Add an 8→9 test asserting only Shards won by the ninth Zone change and
  old-to-old ownership never changes.
- [x] Run `cd server && go test ./internal/coordinator/placement ./internal/routing`;
  expected PASS without changing existing routing vectors.

## Task 2: Define persistent MigrationTask

**Files:**
- Modify: `deploy/tcaplus/schema/classicfarm/v1/tcaplus/runtime_tables.proto`
- Generated: `server/gen/classicfarm/v1/tcaplus/runtime_tables.pb.go`
- Modify: `server/internal/testtcaplus/client.go`
- Modify: `deploy/tcaplus/README.zh-CN.md`

**Schema:**

```proto
message MigrationTask {
  option (tcaplusservice.tcaplus_primary_key) = "logical_shard_id";
  uint32 logical_shard_id = 1;
  bytes task_id = 2;
  string reason = 3;
  string status = 4;
  uint32 priority = 5;
  string source_zone_id = 6;
  string source_endpoint = 7;
  uint64 source_owner_epoch = 8;
  uint64 source_route_version = 9;
  string target_zone_id = 10;
  string target_endpoint = 11;
  uint64 planned_from_map_version = 12;
  uint64 planned_availability_version = 13;
  uint32 attempt = 14;
  int64 retry_at_ms = 15;
  string last_error_code = 16;
  int64 created_at_ms = 17;
  int64 updated_at_ms = 18;
}
```

One Shard has at most one open task. `task_id` is immutable UUID; status is
`PLANNED/RUNNING/COMPLETED/CANCELLED`. Do not overload existing
`MigrationProgress`; Task schedules work, Progress records execution steps.

- [x] Write fake-Tcaplus record-key and round-trip tests first.
- [x] Add schema, regenerate with the existing Buf command, and document
  `TCAPLUS_MIGRATION_TASK_TABLE=MigrationTask`.
- [x] Run `cd server && go test ./internal/testtcaplus ./internal/routing`.

## Task 3: Implement queue Store

**Files:**
- Create: `server/internal/coordinator/migration/task_store.go`
- Create: `server/internal/coordinator/migration/memory_task_store.go`
- Create: `server/internal/coordinator/migration/tcaplus_task_store.go`
- Create: `server/internal/coordinator/migration/task_store_test.go`

**Interfaces:**

```go
type TaskStore interface {
  UpsertPlanned(context.Context, Task) (Task, bool, error)
  LoadOpen(context.Context) ([]Task, error)
  Get(context.Context, uint32) (Task, bool, error)
  Cancel(context.Context, uint32, []byte, string) error
}
```

- Exact proposal replay returns the existing task.
- Different proposal for a Shard with RUNNING task returns conflict.
- A PLANNED REBALANCE may be replaced by higher-priority FAILOVER/DRAIN.
- Ordering is priority descending, then `created_at_ms`, then `shard_id`.
- All updates use record-version CAS and bounded retries.

- [x] Test deduplication, replacement, stale CAS, restart load and stable order.
- [x] Implement memory then Tcaplus adapters.
- [x] Run `cd server && go test -race ./internal/coordinator/migration`.

## Task 4: Implement Current/Desired planner

**Files:**
- Create: `server/internal/coordinator/placement/planner.go`
- Create: `server/internal/coordinator/placement/planner_test.go`
- Create: `server/cmd/coordinator/planner_wiring.go`
- Modify: `server/cmd/coordinator/main.go`

**Interfaces:**

```go
type Planner struct { /* Current, Membership, TaskStore */ }
func (p *Planner) Reconcile(ctx context.Context) (Result, error)
```

- Pin one Current snapshot and one membership snapshot per run.
- For HEALTHY members compute Desired and enqueue only
  `Current.owner_zone_id != Desired.owner_zone_id`.
- If Current owner is SUSPECT/DEAD, do not create REBALANCE; Phase 07 owns it.
- Recheck Current map version immediately before each Upsert; stale plans abort
  and recompute.
- Do not cancel RUNNING tasks. Cancel stale PLANNED tasks whose Current already
  equals Desired.
- Trigger on membership change plus configurable 30s reconciliation; coalesce
  bursts into one run.

- [x] Test no-op, 8→9 diff, member recovery, stale Current, existing task and
  zero calls to RouteStore/Fence/Publisher.
- [x] Wire behind `COORDINATOR_PLANNER_ENABLED=0|1`, default 0.
- [x] Run `cd server && go test -race ./internal/coordinator/placement
  ./internal/coordinator/migration ./cmd/coordinator && go test ./...`.

## Task 5: Verify and document

- Render schema/config changes.
- Record 8-Zone counts and exact 8→9 changed-Shard count under
  `docs/evidence/2026-08-12-placement-rebalance-queue.md`.
- Prove all 4096 Current rows, Fence rows and SDK route caches are unchanged.
- Update `docs/context/CURRENT.md` only after verification.

## Completion Gate

Same membership always yields the same Desired; 8→9 changes only Rendezvous
winners; proposals survive restart and deduplicate; no code in this phase can
grant ownership. Next: `06-正常迁移状态机.md`.
