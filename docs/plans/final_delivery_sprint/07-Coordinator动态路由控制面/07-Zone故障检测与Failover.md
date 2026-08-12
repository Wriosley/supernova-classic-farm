# Zone Failure Evidence and Failover Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:test-driven-development`.
> Execute after Phase 06. Reuse Phase 04 membership and Phase 06 worker; do not
> create a second failover migration implementation.

**Goal:** Combine Kubernetes/livez observations with subscriber failure
evidence, pause SUSPECT owners, and automatically reassign Shards only after a
Zone is confirmed DEAD.

## Global Constraints

- Refused/timeout/reset/gRPC UNAVAILABLE are evidence; business and storage
  errors are not.
- SUSPECT publishes unavailability and changes no epoch/owner.
- DEAD is Endpoint/Pod terminal evidence or three consecutive livez failures.
- DEAD migration skips Source Drain/manifest.
- Fence must advance before TargetReady and durable ACTIVE.
- If Tcaplus/Fence is unavailable, keep Shard unavailable.

## Task 1: Implement ReportZoneFailure aggregation

**Files:**
- Create: `server/internal/coordinator/membership/evidence.go`
- Create: `server/internal/coordinator/membership/evidence_test.go`
- Modify: Phase 03 Coordinator gRPC server
- Modify: `server/internal/coordinatorclient/client.go`

**Interfaces:**

```go
type FailureEvidence struct {
  ReporterID, LogicalZoneID, IncarnationID, Endpoint, Code string
  ObservedAt time.Time
}
func (c *Controller) Report(FailureEvidence) error
```

- SDK reports only transport failures for the exact cached route.
- Reject unknown reporter, stale incarnation, endpoint mismatch, future/old
  timestamps and unsupported codes.
- Deduplicate reporter/zone/code/time bucket.
- Evidence triggers immediate livez probe and SUSPECT, never direct DEAD.

- [ ] Test validation, dedup, evidence storm, storage-error exclusion and
  recovery.
- [ ] Run membership, SDK and gRPC tests with race detection.

## Task 2: Generate FAILOVER tasks for DEAD owners

**Files:**
- Create: `server/internal/coordinator/migration/failover_planner.go`
- Create: `server/internal/coordinator/migration/failover_planner_test.go`

- Scan durable Current for DEAD owner.
- Choose the highest Rendezvous-ranked HEALTHY candidate excluding the dead
  logical ID/incarnation.
- Upsert priority FAILOVER task with Current owner/epoch/version pinned.
- No healthy target means no task and continued unavailability.
- A Target that dies causes a new task/attempt with a higher future epoch.
- Recovery of the Source after DEAD does not cancel a running failover.

- [ ] Test 3-Zone next-candidate order, no target, repeated DEAD, target death,
  and no Current mutation.

## Task 3: Add failover path to the existing executor

**Files:**
- Modify: `server/internal/coordinator/migration/executor.go`
- Modify: `server/internal/coordinator/migration/executor_test.go`

FAILOVER path:

```text
PLANNED
-> ROUTE_PREPARING
-> Fence.Advance to target/new epoch
-> TARGET_LOADING
-> TargetReady(empty manifest)
-> RouteStore.CommitActive
-> in-memory Current
-> Publisher
-> COMPLETED
```

- Never call Source Drain/Restore.
- TargetReady validates Fence and initializes no Actors.
- Persist progress before/after every durable boundary.
- Retry a new target only with a new transition and higher epoch.
- Document accepted ADR-0006 loss: abnormal death may lose unflushed Dirty state.

- [ ] Failure-inject Fence, TargetReady, ACTIVE and publish boundaries.
- [ ] Assert stale Source writes/requests fail after Fence.
- [ ] Run migration package tests with race detection.

## Task 4: Fail closed in SDK consumers

**Files:**
- Modify: `server/internal/coordinatorclient/cache.go`
- Modify: Gate/Info/Zone adapters and tests

- SUSPECT/DEAD owner resolution returns retryable `ZONE_UNAVAILABLE`.
- PREPARING/migrating route returns `ZONE_MIGRATING`.
- Client retry preserves request ID and respects deadline/backoff.
- No consumer guesses another Zone or synchronously changes ownership.

- [ ] Test Gate request during SUSPECT, recovery without epoch change, DEAD
  pause, ACTIVE publish recovery and Info red-dot routing behavior.

## Task 5: Failure E2E

- Create `server/test/e2e/coordinator_failover_test.go`.
- Scenarios: two failed probes then recovery; third failure; Pod deletion;
  storage error; Source resurrection; Target dies during failover; Fence store
  outage.
- Prove SUSPECT does not write Route/Fence, DEAD does; ordinary cached request
  has no Coordinator lookup.
- Record `docs/evidence/2026-08-12-zone-failover.md` and update CURRENT.

## Completion Gate

False/transient evidence only pauses; confirmed DEAD uses durable failover;
control-store outage never causes memory-only ownership. Next:
`08-Coordinator三副本选主.md`.

