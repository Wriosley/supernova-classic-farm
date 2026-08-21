---
status: completed
date: 2026-08-03
owner: project-owner
related:
  - 2026-08-03-static-dual-zone-routing-plan.md
---

# Manual Inactive-Shard Migration Plan

## Goal

Extend the memory-only dual-Zone prototype with one controlled Shard handoff:

```text
ACTIVE(A, epoch=1)
-> block new commands on A
-> verify no active Player Actor exists
-> PREPARING(B, epoch=2)
-> ACTIVE(B, epoch=2)
-> refresh B ownership
-> Gate stale cache receives NOT_OWNER
-> same request reaches B
```

## Safety boundary

Without MySQL or state transfer, moving an active Actor would lose its current
memory state. This implementation therefore permits migration only when the
old Zone has no activated Player Actor in the Shard.

The old Zone acquires an exclusive per-Shard execution gate before marking the
Shard draining. Commands already in progress finish first. New commands then
observe `NOT_OWNER`. If an Actor exists, drain is cancelled and Coordinator
keeps the original committed Route unchanged.

## Implementation

1. Make Player Actor `owner_epoch` activation-scoped instead of hardcoded to
   one; responses, idempotency records, Outbox, checkpoints and maturity Push
   use the Actor's epoch.
2. Add per-Shard execution gates and Zone control endpoints for drain, resume
   and ownership-Snapshot refresh.
3. Add a loopback-only Coordinator move endpoint that checks target readiness,
   drains the old Owner, commits `PREPARING`, commits `ACTIVE`, then refreshes
   the target Zone.
4. Reuse Gate's conditional cache invalidation and same-body retry.
5. Extend the five-process E2E with one inactive Shard migration and one
   active-Shard rejection.

## Verification

- Epoch-two Actor activation produces epoch-two snapshots and checkpoints.
- Existing Actor rejects a request carrying another epoch.
- Drain and command execution cannot race through the ownership boundary.
- Active-Actor drain returns conflict and restores service.
- Successful move increments epoch and installs the target Owner.
- Gate starts with the old cached Route, receives `NOT_OWNER`, performs exactly
  one single-Shard Coordinator Resolve and receives an epoch-two snapshot.

## Non-goals

- Moving Shards with active Actors.
- Dirty flush and MySQL Fence CAS.
- Failure recovery after Coordinator commits `PREPARING`.
- Automatic migration, load balancing or failure detection.
- Majority-committed Coordinator.

## Next required step

Add MySQL-backed drain, Dirty flush, Fence CAS and checkpoint load before
removing the inactive-Shard restriction.
