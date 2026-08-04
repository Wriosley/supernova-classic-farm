---
status: measured
date: 2026-07-31
scope: CLAIM_CHAPTER_REWARD and pending reward-mail Outbox
---

# CLAIM_CHAPTER_REWARD evidence

## Claim boundary

Automated tests prove that:

1. only the current `CLAIMABLE` chapter can be claimed;
2. the normal first-chapter reward credits 10 coins, one fertilizer and three
   next-chapter seed items, then activates chapter two;
3. same-ID replay returns the first receipt without granting twice;
4. an incomplete or wrong chapter fails without changing business state or
   `player_seq`;
5. warehouse allocation is deterministic and full-warehouse overflow creates
   exactly one `CreateRewardMailV1` pending event with sorted attachments;
6. the event ID, payload, payload hash and idempotency `outbox_ids` survive a
   checkpoint round trip;
7. the MySQL writer inserts the immutable relational Outbox row and updates the
   checkpoint CAS in one transaction.

A live in-memory four-process run proves the protocol path through normal
reward claim at `player_seq=7`. It does not exercise the overflow path because
the development reward fits the normal warehouse.

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
SELL_CROP ... player_seq=6 sold_quantity=3
  coins=19 chapter_status=CLAIMABLE replayed=true
CLAIM_CHAPTER_REWARD ... player_seq=7 coins=29
  fertilizer_item_1=1 next_seed_item_1003=3
  chapter_id=2 replayed=true
PASS TestAuthenticatedSnapshot (72.46s)
RESULT maturity_push_e2e=PASS
```

## Live MySQL restart result

`deploy/migrations/000004_player_outbox.up.sql` defines the relational table,
and mocked-SQL tests prove transaction ordering and arguments. The owner ran:

```powershell
.\tests\e2e\run-mysql-restart-recovery.ps1 `
  -HostName 127.0.0.1 -Port 3306 -User classicfarm
```

The first stack observed:

```text
CLAIM_CHAPTER_REWARD ... player_seq=7 coins=29
  fertilizer_item_1=1 next_seed_item_1003=3 chapter_id=2 replayed=true
RESULT authenticated_snapshot_e2e=PASS adapter=mysql-restart-register
```

After all four services stopped, the fresh stack observed:

```text
HTTP_AUTH mode=login player_id=9
SNAPSHOT ... player_seq=7 coins=29 seed_quantity=2
  plot_state=NEED_CLEANUP
RESULT authenticated_snapshot_e2e=PASS adapter=mysql-restart-login
RESULT mysql_restart_recovery_e2e=PASS
```

The snapshot assertion additionally verified one fertilizer, three item-1003
next-chapter seeds and chapter two `IN_PROGRESS`. This proves checkpoint
recovery for the normal no-overflow claim. It does not prove a live relational
Outbox row because the normal reward fit inventory. The Outbox relay, Mail
Service, delivered-event reconciliation, stale-owner rejection and abnormal
Dirty-window loss remain outside this evidence.
