---
status: completed
date: 2026-08-03
claim_scope: active-Shard MySQL handoff
---

# Active-Shard MySQL Migration Evidence

## Claim boundary

The implementation is intended to preserve active Actor state across one
controlled A-to-B Shard migration in a live five-process MySQL stack.

It does not claim Coordinator restart recovery, automatic migration,
availability during PREPARING, consensus or repeatable reuse of a database
after the process-local Coordinator loses a migrated ShardMap.

## Added coverage

- Shard lifecycle exclusion for commands, maturity and Dirty flush.
- Final Actor checkpoint flush, ambiguous-commit reconciliation and eviction.
- Lazy epoch adoption without changing `player_seq`.
- PREPARING-bound idempotent Fence CAS and conflict rejection.
- Stable PREPARING route during lease renewal.
- Admission barrier, final drain manifest and target checkpoint preparation.
- In-process migration continuation through final target refresh.
- E2E assertions for state continuity, epoch-two Gate recovery, post-migration
  persistence and stale old-Owner Fence rejection.

## Required commands

```text
go test ./...
go vet ./...
tests/e2e/run-authenticated-snapshot.ps1
tests/e2e/run-dual-zone-routing.ps1
tests/e2e/run-dual-zone-mysql.ps1
```

## Observed results

The owner ran:

```text
go test ./...                                      PASS
go vet ./...                                       PASS
run-authenticated-snapshot.ps1                    PASS
run-dual-zone-routing.ps1                         PASS
run-dual-zone-mysql.ps1                           PASS
```

Active MySQL migration observed:

```text
source_player=14
logical_shard=3371
source_zone=zone-a
target_zone=zone-b
target_epoch=2
post_migration_persisted_player_seq=2
zone_b_control_player=19
zone_b_control_persisted_player_seq=1
```

The run mutated player 14 on A, moved its active Shard, recovered through the
stale Gate route at epoch two, performed another write on B and observed the
new state in MySQL. It also exercised direct old-Zone command rejection and a
stale Zone-A checkpoint writer rejected by the epoch-two Zone-B Fence.

The active migration MySQL E2E ran last against an epoch-one static dual-Zone
database. Successful migration durably advanced one Fence while Coordinator
state remains process-local by the selected scope; the same database must not
be used to claim restart recovery or rerun the bootstrap E2E.
