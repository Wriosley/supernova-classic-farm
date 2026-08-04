---
status: completed
date: 2026-08-03
owner: project-owner
related:
  - 2026-08-03-manual-inactive-shard-migration-plan.md
  - 2026-08-03-static-dual-zone-mysql-fence-plan.md
  - ../contracts/data-model.md
  - ../decisions/ADR-0006-async-dirty-writeback.md
  - ../decisions/ADR-0008-v3-quorum-shard-coordinator.md
---

# Active-Shard MySQL Migration Plan

## Goal

Move a Shard containing active Player Actors from Zone A to Zone B without
losing acknowledged in-memory state:

```text
block A admission
-> PREPARING(B, epoch+1)
-> settle, final-flush and evict A Actors
-> CAS MySQL Fence
-> rewrite/validate drained checkpoints on B
-> ACTIVE(B)
```

## Safety boundary

- Commands, online maturity and background Dirty flush share per-Shard runtime
  lifecycle exclusion.
- Before `PREPARING`, failure resumes A.
- After `PREPARING`, the epoch is consumed and the Shard remains non-routable
  until the same in-process transition succeeds.
- A Fence advances only for the exact committed PREPARING owner, epoch, route
  version and transition ID; exact replay is idempotent.
- B never activates Actors before the Fence and drained checkpoints are ready.
- Coordinator restart recovery and persistent ShardMap are explicitly deferred.
  Restart after a migrated Fence fails closed.

## Implementation

1. Add final Shard drain to Player Runtime: settle, synchronous final CAS flush,
   ambiguous-commit reconciliation, all-player success barrier, manifest and
   Actor eviction.
2. Split Zone drain into admission barrier and idempotent completion; add target
   checkpoint preparation without premature Actor activation.
3. Add PREPARING-authorized MySQL Fence CAS and prevent lease renewal from
   mutating PREPARING routes.
4. Keep per-Shard Coordinator progress in memory so retries resume drain,
   Fence, target preparation, activation or final refresh.
5. Adopt a newer epoch lazily for inactive checkpoints by incrementing only
   `checkpoint_revision`, not `player_seq`.
6. Extend dual-Zone MySQL E2E through active migration and a second write on B.

## Verification

- Runtime drain persists the latest state and evicts only after all Actors
  succeed.
- Ambiguous final-flush commit is reconciled by loading and hashing the durable
  checkpoint with an independent timeout.
- Fence CAS applies once, exact replay succeeds and conflicting metadata fails.
- PREPARING route versions remain stable.
- Old Zone command and delayed old-Zone checkpoint write are rejected.
- Gate's stale epoch-one cache reaches B at epoch two.
- B persists a post-migration write.

## Non-goals

- Coordinator restart recovery or durable migration progress.
- Automatic rebalance/failover and majority consensus.
- Cross-database Fence transactions.
- Zero command downtime during PREPARING.
