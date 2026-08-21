---
status: completed
date: 2026-08-03
---

# Active-Shard MySQL Migration

## Human decisions

- Implement active Actor migration directly rather than first limiting MySQL
  migration to inactive Shards.
- Keep migration retryable while Coordinator remains alive.
- Defer persistent PREPARING recovery and fail closed after Coordinator restart.

## AI-assisted work

- Mapped command, maturity and Dirty-flush race windows.
- Added runtime Shard exclusion, final flush, durable manifest and eviction.
- Added PREPARING-bound MySQL Fence CAS.
- Split old-Owner admission blocking from final drain completion.
- Added target checkpoint epoch preparation before ACTIVE.
- Made Coordinator continue an in-process PREPARING transition idempotently.
- Extended unit, component and live MySQL E2E coverage.

## Review corrections

Independent read-only review found and drove fixes for:

- the target Actor's loaded checkpoint CAS baseline;
- drain-manifest reuse across later ownership epochs;
- retry after final target-refresh failure;
- ambiguous final-flush commits;
- canceled-context drain rollback;
- direct stale old-Zone database writer rejection.

## Verification

The owner manually ran verification because the command runner did not report
exit status:

```text
go test ./...                                    PASS
go vet ./...                                     PASS
run-authenticated-snapshot.ps1                  PASS
run-dual-zone-routing.ps1                       PASS
run-dual-zone-mysql.ps1                         PASS
```

The live migration moved `player_id=14/shard=3371` from Zone A epoch one to
Zone B epoch two, preserved the first mutation and persisted a second mutation
on B at `player_seq=2`.

## Remaining uncertainty

Coordinator ShardMap and migration progress remain process-local. After a
successful durable Fence advance, restart intentionally fails closed until the
next phase adds persistent recovery.
