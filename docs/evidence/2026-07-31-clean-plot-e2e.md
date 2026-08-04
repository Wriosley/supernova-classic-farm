---
status: measured
date: 2026-07-31
scope: CLEAN_PLOT and server-side owner-loop completion
---

# CLEAN_PLOT evidence

## Claim boundary

Automated tests prove that:

1. only a `NEED_CLEANUP` plot can be cleaned;
2. missing and incompatible plot states fail without changing resources or
   `player_seq`;
3. success changes the plot to `EMPTY` and clears all crop identity,
   configuration, growth, timestamp and effect fields;
4. cleanup consumes no item, grants no reward and advances no task;
5. same-ID replay returns the first response without applying twice;
6. the empty plot and retained idempotency result survive checkpoint
   serialization and Dirty writeback.

A live in-memory four-process run proves the full protocol path from
registration through cleanup at `player_seq=8`.

## Reproduction

```powershell
cd server
go test ./...
go vet ./...

cd ..
powershell -NoProfile -ExecutionPolicy Bypass `
  -File .\tests\e2e\run-maturity-push.ps1
```

## Observed result

```text
CLAIM_CHAPTER_REWARD ... player_seq=7 coins=29
  fertilizer_item_1=1 next_seed_item_1003=3 chapter_id=2 replayed=true
CLEAN_PLOT ... player_seq=8 plot_id=1 plot_state=EMPTY replayed=true
PASS TestAuthenticatedSnapshot (72.27s)
RESULT maturity_push_e2e=PASS
```

## Live MySQL restart result

The owner ran:

```powershell
.\tests\e2e\run-mysql-restart-recovery.ps1 `
  -HostName 127.0.0.1 -Port 3306 -User classicfarm
```

The first stack observed:

```text
CLEAN_PLOT ... player_seq=8 plot_id=1 plot_state=EMPTY replayed=true
RESULT authenticated_snapshot_e2e=PASS adapter=mysql-restart-register
```

After all four services stopped, the fresh stack observed:

```text
HTTP_AUTH mode=login player_id=10
SNAPSHOT ... player_seq=8 coins=29 seed_quantity=2 plot_state=EMPTY
RESULT authenticated_snapshot_e2e=PASS adapter=mysql-restart-login
RESULT mysql_restart_recovery_e2e=PASS
```

The snapshot assertion also verified one fertilizer, three next-chapter seeds
and chapter two `IN_PROGRESS`. Stale-owner rejection, abnormal Dirty-window
loss and multi-Actor batching remain outside this evidence.
