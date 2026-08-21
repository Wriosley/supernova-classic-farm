---
status: completed
date: 2026-08-03
claim_scope: memory-only inactive-Shard handoff
---

# Manual Inactive-Shard Migration Evidence

## Claim boundary

The local prototype can safely move an inactive logical Shard from Zone A to
Zone B, increment `owner_epoch`, reject the stale cached Route and complete the
original request through Zone B.

It refuses to move a Shard that has an active Player Actor. It does not prove
active-state transfer, Dirty flush, MySQL Fence CAS, failure recovery or
automatic rebalance.

## Automated coverage

Unit and component tests cover:

- dynamic Actor epoch in snapshots, checkpoints, idempotency, Outbox and Push;
- existing-Actor stale-epoch rejection;
- per-Shard command/drain exclusion;
- drain cancellation when the Shard contains an active Actor;
- Coordinator old-Owner drain, epoch increment and target refresh;
- unchanged committed Route when drain is rejected.

Full regression:

```text
go test ./...
go vet ./...
tests/e2e/run-authenticated-snapshot.ps1
tests/e2e/run-dual-zone-routing.ps1
```

All passed. The first E2E preserves the original single-Zone epoch-one path;
the second exercises the migration path below.

## Five-process result

Command:

```text
tests/e2e/run-dual-zone-routing.ps1
```

Observed:

```text
zone_a_player=2 shard=1631
zone_b_player=1 shard=2066
migrated_player=7 migrated_shard=3552 migrated_epoch=2
snapshot_lookups=5 shard_lookups=1
RESULT dual_zone_routing_e2e=PASS
```

Gate had warmed the original epoch-one Route before migration. The first
snapshot request for player 7 reached the drained old Zone, refreshed exactly
one Shard from Coordinator with the same request, then succeeded on Zone B
with `owner_epoch=2`.

The same run attempted to move the already activated Zone-A player Shard.
Coordinator returned conflict because old Zone reported an active Actor; the
Route remained on its original Owner.

## Limitations

- Only inactive Shards can move.
- The single-node Coordinator commits `PREPARING` and `ACTIVE` in memory.
- If target Snapshot refresh fails after `ACTIVE`, safety is retained but the
  Shard can remain temporarily unavailable until periodic refresh succeeds.
- There is no automated rollback from a committed `PREPARING` state.
