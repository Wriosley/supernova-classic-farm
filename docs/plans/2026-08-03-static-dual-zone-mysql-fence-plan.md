---
status: completed
date: 2026-08-03
owner: project-owner
related:
  - 2026-08-03-static-dual-zone-routing-plan.md
  - 2026-08-03-manual-inactive-shard-migration-plan.md
  - ../contracts/data-model.md
  - ../decisions/ADR-0008-v3-quorum-shard-coordinator.md
---

# Static Dual-Zone MySQL Fence Alignment Plan

## Goal

Allow the static dual-Zone prototype to use the existing MySQL account,
checkpoint and Dirty-write path without claiming that MySQL-backed Shard
migration exists.

At startup, Coordinator must atomically align the 4096 original
`zone-local/epoch=1/route_version=1` bootstrap Fence rows with the committed
Rendezvous assignment for Zone A and Zone B. Registration and each Zone's
checkpoint writer must then use those per-Shard Fence owners.

## Safety boundary

This is bootstrap conversion, not ownership transfer:

- every Route and Fence must remain at `owner_epoch=1`;
- every Fence must remain at `route_version=1`;
- a stored Owner may be only `zone-local` or the exact target Owner;
- all 4096 Fence rows are locked, validated and changed in one transaction;
- explicit `DUAL_ZONE_FENCE_BOOTSTRAP=1` authorization is required;
- MySQL-backed manual migration remains unavailable until PREPARING-authorized
  Fence CAS, final Dirty flush and target checkpoint activation exist.

Any missing row, prior epoch advance, unexpected Owner, incompatible ShardMap
metadata or concurrent update aborts the whole reconciliation.

## Implementation

1. Add a routing-layer transactional reconciler for the initial static
   ShardMap and deterministic 16-byte bootstrap transition identities.
2. Run reconciliation before Coordinator becomes Ready and before Login starts
   in the provided launch scripts.
3. Make MySQL registration lock its player's Fence and copy the Fence epoch
   into the initial checkpoint instead of assuming `zone-local/epoch=1`.
4. Configure each MySQL checkpoint writer with its process `OWNER_ZONE_ID`.
5. Permit `start-servers.ps1 -DualZone` with `MYSQL_DSN`.
6. Add a five-process MySQL E2E that registers players owned by both Zones,
   executes one write on each and waits for both checkpoints to persist under
   the matching Fence Owner.

## Verification

- Reconciliation accepts an already aligned 4096-row map idempotently.
- Reconciliation rejects an unexpected existing Owner without committing.
- Registration uses the Fence's epoch in both envelope and blob.
- Zone B can flush under a Zone-B Fence; the default single-Zone writer remains
  compatible with `zone-local`.
- Existing in-memory dual-Zone routing/migration regression still passes.
- New dual-Zone MySQL E2E observes `player_seq=1` in both Zone-owned
  checkpoints and matching Fence owners.

## Non-goals

- MySQL-backed inactive or active Shard migration.
- Fence epoch advancement or PREPARING recovery.
- Actor mailbox drain and final Dirty flush.
- Target-owner readiness based on loaded checkpoints.
- Coordinator persistence, consensus or automatic rebalance.

## Stop conditions

Stop rather than rewrite Fence rows if the database contains any ownership
state beyond the original epoch-one static bootstrap. Such a database requires
the separately designed migration state machine, not bootstrap reconciliation.
