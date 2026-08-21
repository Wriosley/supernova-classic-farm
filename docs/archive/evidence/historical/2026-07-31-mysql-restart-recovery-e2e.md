---
status: measured
date: 2026-07-31
scope: MySQL owner-loop restart recovery through CLEAN_PLOT
---

# MySQL restart recovery end-to-end evidence

## Claim boundary

This owner-run test proves that:

1. migrations created 4096 local `shard_fences` rows;
2. a first four-process stack registered a new account and activated its
   initial Player Actor from MySQL;
3. `BUY_SEEDS` changed the Actor from `player_seq=0`, 10 coins and no seed to
   `player_seq=1`, 4 coins and three units of item `1001`;
4. replaying the same request ID returned the stored result without applying
   the purchase twice;
5. `PLANT` consumed one seed, froze crop configuration, changed plot 1 from
   `EMPTY` to `GROWING` and advanced to `player_seq=2`;
6. replaying the Plant request did not consume another seed;
7. `APPLY_FERTILIZER` consumed the initial fertilizer, stored a deterministic
   effect instance and advanced to `player_seq=3`;
8. replaying the fertilizer request did not consume another item or replace
   the effect;
9. the online scheduler materialized the crop, emitted a `MATURED` Push and
   advanced to `player_seq=4`;
10. `HARVEST` added three crop items, changed the plot to `NEED_CLEANUP`,
    advanced to `player_seq=5`, and replayed without applying twice;
11. `SELL_CROP` removed all three crops, added 15 coins, completed the fifth
    task, changed the chapter to `CLAIMABLE`, advanced to `player_seq=6`, and
    replayed without applying twice;
12. `CLAIM_CHAPTER_REWARD` added 10 coins, one fertilizer and three
    next-chapter seeds, activated chapter two, advanced to `player_seq=7`, and
    replayed without granting twice;
13. `CLEAN_PLOT` cleared the harvested plot, advanced to `player_seq=8`, and
    replayed without applying twice;
14. the asynchronous Dirty flusher wrote the resulting complete checkpoint
   using exact local Fence
   ownership and checkpoint-revision CAS;
15. all four Go processes stopped and a fresh stack logged into the same
   `player_id`;
16. the fresh ZoneSvr loaded `player_seq=8`, 29 coins, two old seed items,
    one fertilizer, three next-chapter seeds, no crop, the `EMPTY` plot and
    chapter two `IN_PROGRESS` from MySQL.

It does not prove stale-owner Fence rejection, multi-Actor batch throughput,
live reward-overflow Outbox durability, survival of a write killed before the accepted Dirty
window elapses, WS Ticket survival across LoginSvr restart, availability or
capacity. The first stack stayed online through fertilizer expiry and maturity,
then restarted after harvest; restart while an effect or maturity deadline is
still in flight remains unverified.

## Reproduction

From the repository root:

```powershell
.\tests\e2e\run-mysql-restart-recovery.ps1
```

The script locates `mysql.exe` from PATH or the installed Windows MySQL
service, prompts securely for the application password, applies the idempotent
migrations and generates a unique test account. It runs the authenticated E2E
in `register` mode with `BUY_SEEDS`, `PLANT`, `APPLY_FERTILIZER`, online
maturity, `HARVEST`, `SELL_CROP`, `CLAIM_CHAPTER_REWARD`, `CLEAN_PLOT` and all seven command idempotency replays,
tears down every child process, then runs against a fresh stack in `login` mode
expecting the claimed checkpoint.

## Observed result

First stack:

```text
MIGRATE file=000003_local_shard_fences.up.sql
PHASE mode=register account=restart_7b4657278a20
HTTP_AUTH mode=register account=restart_7b4657278a20 player_id=8 csrf_rotated=true
GET_SHOP ... seed_entry_id=5001 seed_price=2 seed_price_version=8 crop_entry_id=5002 crop_price=5 crop_price_version=9
SNAPSHOT ... snapshot_player_id=8 owner_epoch=1 player_seq=0 coins=10 seed_quantity=0 plot_state=EMPTY
BUY_SEEDS ... player_seq=1 coins=4 seed_item_1001=3 replayed=true
PLANT ... player_seq=2 seed_item_1001=2 plot_id=1 plot_state=GROWING crop_id=2001 replayed=true
APPLY_FERTILIZER ... player_seq=3 fertilizer_item_1=0 effect_instance_id=aff5df6f-ddb6-5c0c-b955-62a1fd012558 replayed=true
PLAYER_STATE_CHANGED reason=MATURED player_seq=4 plot_id=1 plot_state=MATURE request_id_absent=true
HARVEST ... player_seq=5 crop_item_1002=3 plot_state=NEED_CLEANUP replayed=true
SELL_CROP ... player_seq=6 sold_quantity=3 coins=19 chapter_status=CLAIMABLE replayed=true
CLAIM_CHAPTER_REWARD ... player_seq=7 coins=29 fertilizer_item_1=1 next_seed_item_1003=3 chapter_id=2 replayed=true
CLEAN_PLOT ... player_seq=8 plot_id=1 plot_state=EMPTY replayed=true
RESULT authenticated_snapshot_e2e=PASS adapter=mysql-restart-register
```

All four service processes then stopped, followed by:

```text
RESTART boundary=fresh-four-process-stack
PHASE mode=login account=restart_f8ae8933cb0c
HTTP_AUTH mode=login account=restart_f8ae8933cb0c player_id=10 csrf_rotated=true
SNAPSHOT ... snapshot_player_id=10 owner_epoch=1 player_seq=8 coins=29 seed_quantity=2 plot_state=EMPTY
RESULT authenticated_snapshot_e2e=PASS adapter=mysql-restart-login
RESULT mysql_restart_recovery_e2e=PASS account=restart_f8ae8933cb0c
```

Both phases also passed Bootstrap/config digest validation, WebSocket AUTH,
PING correlation and one-time Ticket replay rejection.

## Interpretation

- The stable `player_id=10` demonstrates durable account identity.
- Successful second-phase login demonstrates that account credentials and
  Session-generation state were available to the fresh LoginSvr.
- The fresh ZoneSvr selected `state_adapter=mysql-checkpoint`; `player_seq=8`,
  29 coins, two old seeds, one fertilizer, three next-chapter seeds, absent
  crop inventory, the `EMPTY` plot and chapter two demonstrate recovery
  through the complete server owner loop.
- The repeated `BUY_SEEDS` request returned `replayed=true` at the same
  `player_seq=1`, so the observed command did not deduct coins twice.
- The repeated `PLANT` request returned `replayed=true` at
  `player_seq=2`, so it did not consume another seed or plant twice.
- The repeated `APPLY_FERTILIZER` request returned `replayed=true` at
  `player_seq=3`, preserving the first effect identity.
- The repeated `HARVEST` request returned `replayed=true` at `player_seq=5`,
  so it did not duplicate crop inventory or task progress.
- The repeated `SELL_CROP` request returned `replayed=true` at `player_seq=6`,
  so it did not duplicate coins or sell-task progress.
- The repeated `CLAIM_CHAPTER_REWARD` request returned `replayed=true` at
  `player_seq=7`, so it did not grant reward resources twice.
- The repeated `CLEAN_PLOT` request returned `replayed=true` at `player_seq=8`,
  so it did not mutate the plot or version twice.
- Unit tests prove the individual checkpoint and idempotency invariants through
  reward claim, including full-warehouse pending Outbox creation and
  chapter-two checkpoint round-trip.
- Unit tests separately verify the exact Fence-row read and checkpoint CAS SQL.
  This live run exercised their success path; it did not deliberately change
  the Fence row to prove stale-owner rejection.
