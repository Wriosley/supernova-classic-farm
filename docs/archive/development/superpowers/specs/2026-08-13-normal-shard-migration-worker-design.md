# Recoverable Normal Shard Migration Worker Design

## Status

Approved by the owner on 2026-08-13.

## Goal

Phase 06 consumes durable `MigrationTask` rows and moves one logical Shard from
its current Source Zone to its planned Target Zone through a crash-recoverable
state machine. It handles planned `REBALANCE` and operator-requested `DRAIN`
work only. Abrupt `DEAD` failover remains Phase 07.

## User-visible behavior

The existing administrator endpoint remains available. It no longer performs
the complete migration inside one HTTP request. Instead, it validates the
requested Source and Target, creates a high-priority `DRAIN` task, and returns
`202 Accepted` with the immutable task ID. Automatic Placement creates ordinary
`REBALANCE` tasks. Both enter the same Scheduler and Worker.

Migration pauses only the affected Shard. The Source Zone continues serving
all unrelated Shards. Requests for the affected Shard fail with retryable
`ZONE_MIGRATING` from the moment Source Drain begins until a durable ACTIVE
route is published.

## Authority and safety boundary

Four durable records have separate roles:

- `MigrationTask` schedules work, priority, attempts and retry timing.
- `MigrationProgress` records the exact execution step and frozen migration
  evidence.
- `ShardFence` grants the only valid write epoch.
- `ShardRoute` is Current routing authority.

The in-memory routing map and Publisher never lead durable `ShardRoute`.
Committing PREPARING or ACTIVE follows `RouteStore commit -> replace in-memory
Current -> publish`. Before Fence advances, recovery may restore the Source.
After Fence advances, recovery must never return to the old Source epoch and
can only continue toward the Target.

## State machine

The persisted path is:

```text
PLANNED
-> SOURCE_DRAINING
-> SOURCE_FLUSHED
-> ROUTE_PREPARING
-> FENCE_ADVANCED
-> TARGET_LOADING
-> TARGET_READY
-> ROUTE_ACTIVE
-> COMPLETED
```

Every external side effect is preceded or followed by enough durable evidence
to identify an exact replay. Transition ID binds Source Drain, Progress,
PREPARING Route, Fence and Target Prepare. Source owner epoch/route version and
Target prepared epoch/route version/lease identity are frozen once and never
recomputed during recovery.

## Source Drain

Source Drain is Shard-scoped and idempotent by transition ID. It atomically
blocks new requests and new/loading Actor activations for that Shard, waits for
already accepted mailbox work, flushes every Dirty Actor, records a manifest,
and evicts those Actors. A different transition for an already draining Shard
is rejected.

If Drain or flush fails before Fence advances, the worker may call Source
Restore and return its authorization to ACTIVE. Once Fence advances, Source
Restore is forbidden even if later Target work fails.

## Target Prepare and activation

Target Prepare validates the transition, manifest and durable Fence. It grants
Shard authorization but does not enumerate players or prewarm Actors. Player
Actors remain lazy and load their checkpoint only when traffic resumes.

The worker commits ACTIVE to durable `ShardRoute` before replacing in-memory
Current or publishing. Best-effort Target refresh follows the durable ACTIVE
commit and cannot roll ownership back. Only after ACTIVE is committed does the
worker complete Progress and Task.

## Scheduler

The Coordinator owns one Scheduler. It loads open tasks in priority-descending,
creation-time and Shard-ID order. Initial concurrency limits are eight global,
two per Source and two per Target. Permits are acquired before a task moves to
RUNNING; one Shard cannot execute twice.

Transient failures persist attempt count, bounded exponential retry time and
the last error code. Corrupt Progress, mismatched Task/Current/Fence, illegal
step transitions and ownership contradictions fail that task closed and are
surfaced as diagnostics rather than guessed or overwritten.

Shutdown stops claiming new work, waits for a bounded worker grace period and
then cancels remaining calls. Restart reloads Task and Progress and resumes
from the last validated durable boundary.

## Compatibility and rollout

`COORDINATOR_MIGRATION_WORKER_ENABLED=0|1` defaults to `0`. The existing
inspection, continue and abandon endpoints remain temporarily for diagnostics,
but they operate through the same durable worker state and cannot run a second
synchronous migration path. No new Tcaplus table is required in Phase 06;
`MigrationTask`, `MigrationProgress`, `ShardFence`, `ShardMapMeta` and
`ShardRoute` are reused.

The Worker is enabled in kind only after unit/race recovery tests pass. The
live gate migrates a bounded Shard, restarts Coordinator at persisted steps,
and proves no dual owner, old-epoch rejection, lazy Target Actor load and SDK
publication only after durable ACTIVE.

## Verification

Tests inject failure after every state boundary and verify exact replay,
pre-Fence Source restoration, post-Fence forward-only recovery, no ACTIVE
publication after Fence/Route failure, scheduler limits, priority, retry,
shutdown and race safety. Zone tests cover concurrent commands versus Drain,
new/loading Actor rejection, Dirty flush failure, repeated lifecycle calls and
stale transitions.

The completion gate requires every normal-migration crash window to resume,
all concurrency limits to hold, no Actor prewarming, and no path that restores
the Source after Fence advancement.
