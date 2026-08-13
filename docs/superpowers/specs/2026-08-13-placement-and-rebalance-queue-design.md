# Placement and Rebalance Queue Design

## Status

Proposed for owner review on 2026-08-13.

## Goal

Phase 05 deterministically calculates Desired Shard owners from HEALTHY Zone
membership and persists bounded, deduplicated migration work. It does not
change Current ownership. The first live validation uses three `zone-pool`
candidates plus the existing `zone-a` and `zone-b`; offline vectors cover the
larger 8-to-9 candidate transition.

## Safety boundary

The phase has three separate facts:

- Current is the durable 4096-row `ShardRoute` snapshot and remains the only
  routing authority.
- Desired is a pure calculation from one pinned membership snapshot.
- `MigrationTask` records the difference between Current and Desired but grants
  no ownership.

No Phase 05 component may call Zone Drain/Prepare, advance ShardFence, commit
RouteStore, write MigrationProgress, or publish a route batch. A newly
discovered candidate therefore remains empty until Phase 06 executes a task.
The planner is disabled by default behind `COORDINATOR_PLANNER_ENABLED=0|1`.

## Placement

The placement package reuses the exact existing SHA-256 Rendezvous score bytes
and assignment algorithm version. It sorts candidates by logical Zone ID,
deduplicates exact repeats, rejects conflicting endpoints for one ID, and
requires 4096 Shards plus at least one HEALTHY candidate.

For each Shard, the greatest score wins; a score tie uses logical-ID byte
order. Candidate input order cannot affect output. Adding a candidate changes
only Shards won by that candidate: an old owner can never change directly to a
different old owner. Pure Rendezvous intentionally does not force equal counts
or use runtime load metrics.

## Persistent task model

Add one Tcaplus protobuf table named `MigrationTask`, keyed by
`logical_shard_id`. One Shard has at most one open task. A task freezes source
owner/endpoint/epoch/route version, target owner/endpoint, Current map version,
membership availability version, priority, retry state and immutable UUID
task ID.

Statuses are `PLANNED`, `RUNNING`, `COMPLETED`, and `CANCELLED`. Reasons are
`REBALANCE`, `DRAIN`, and `FAILOVER`, with priority
`FAILOVER > DRAIN > REBALANCE`. Phase 05 creates only REBALANCE tasks; the
other reasons define queue replacement semantics for later phases.

`MigrationTask` and `MigrationProgress` are deliberately separate. Task is
scheduling intent; Progress is Phase 06's crash-recoverable execution state.
The Tcaplus table is created only after protobuf generation and fake-client
round-trip tests confirm the final schema.

## Queue semantics

Exact proposal replay returns the existing task without changing its ID or
timestamps. A different proposal cannot replace a RUNNING task. A higher
priority proposal may replace a lower-priority PLANNED task. Updates use
record-version CAS with bounded retries. Open tasks load in deterministic
priority-descending, creation-time, then Shard-ID order.

A stale PLANNED task is cancelled only when pinned Current now equals Desired.
RUNNING tasks are never cancelled by the planner. Completed and cancelled rows
remain terminal evidence and may be replaced by a later proposal for the same
Shard through CAS.

## Planner flow

Each reconcile run pins exactly one durable Current snapshot and one immutable
membership snapshot. It selects HEALTHY members, computes Desired, and proposes
REBALANCE tasks only where owners differ. If a Current owner is known SUSPECT
or DEAD, Phase 05 does not create a REBALANCE task; Phase 07 owns failover.

Immediately before every task upsert, the planner rechecks Current map version.
Any version change aborts the run; a later coalesced reconcile recomputes from
fresh snapshots. Membership changes trigger reconciliation, while a
configurable 30-second ticker provides convergence. Bursts coalesce into one
run.

## Configuration and deployment

The final runtime configuration adds:

```text
TCAPLUS_MIGRATION_TASK_TABLE=MigrationTask
COORDINATOR_PLANNER_ENABLED=0
COORDINATOR_PLANNER_INTERVAL=30s
```

Local development uses unit and race tests for the implementation loop. Docker
is built only at the final integration gate, not after every task. The live
gate scales `zone-pool` to three, enables the planner explicitly, and verifies
that tasks survive Coordinator restart and deduplicate.

## Verification

Automated tests cover placement golden vectors, input-order independence,
8-to-9 minimal movement, task CAS/dedup/replacement/restart ordering, stale
Current abort, unhealthy-owner exclusion, burst coalescing and zero ownership
mutation interfaces.

The kind/Tcaplus gate records the five-Zone Desired distribution and task
count, restarts Coordinator, and proves:

- the same tasks and immutable IDs reload;
- all 4096 durable Current owner/endpoint/epoch/route/state/lease identity
  fields remain unchanged;
- ShardFence and MigrationProgress receive no Phase 05 writes;
- Gate/Info/Zone SDK routing remains on Current;
- scaling candidates or disabling the planner does not directly reroute a
  player.

## Deferred work

Phase 06 claims and executes tasks through Drain, Fence and durable Current
activation. Phase 07 creates FAILOVER tasks. Leader election, automatic
capacity correction, load-aware ordering, HPA and prewarming remain outside
Phase 05.
