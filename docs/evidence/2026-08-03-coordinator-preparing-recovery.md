---
status: verified
date: 2026-08-03
related:
  - ../plans/2026-08-03-coordinator-preparing-recovery-plan.md
  - ../ai-workflow/2026-08-03-coordinator-preparing-recovery.md
---

# Coordinator PREPARING Recovery Evidence

## Scope

Prove durable migration progress, fail-closed PREPARING overlay after
Coordinator restart, manual continue/abandon controls, and post-migration
Fence hydration without a full ShardMap table.

## Commands

```text
go test ./internal/routing ./cmd/coordinator -count=1
go test ./... -count=1
go vet ./...
# with MYSQL_DSN and migration 000005 applied:
tests/e2e/run-dual-zone-mysql.ps1
```

## Results

```text
go test ./... -count=1                               PASS
go vet ./...                                         PASS
run-dual-zone-mysql.ps1                              PASS
```

Unit/component coverage includes progress upsert/load/delete,
abandon-after-Fence refusal, Fence hydrate, epoch high-water,
continue from persisted `PREPARING_COMMITTED`, and abandon before Fence.

Live dual-Zone MySQL script:

1. Migrated `player_id=21/shard=3190` A→B at epoch 2 with
   `persisted_seq=2`; control B player stayed at `player_seq=1`.
2. Restarted Coordinator only; logs showed
   `dual-Zone MySQL routes hydrated from fences`.
3. Hydrate check observed `advanced_fences=2` matching Coordinator routes
   and an empty open-migration list.

## Limitations

- Live mid-`PREPARING` kill/continue is covered by Coordinator component tests
  with sqlmock and Zone httptest fixtures, not by crashing a live Coordinator
  between drain and Fence.
- Full durable ShardMap snapshots and automatic continue remain out of scope.
