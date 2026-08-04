---
status: measured
date: 2026-07-31
scope: MySQL BUY_SEEDS, PLANT and APPLY_FERTILIZER restart recovery
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
9. the asynchronous Dirty flusher wrote the resulting complete checkpoint
   using exact local Fence
   ownership and checkpoint-revision CAS;
10. all four Go processes stopped and a fresh stack logged into the same
   `player_id`;
11. the fresh ZoneSvr loaded `player_seq=3`, 4 coins, two seed items, no
   fertilizer and the fertilized `GROWING` plot from MySQL.

It does not prove stale-owner Fence rejection, multi-Actor batch throughput,
Outbox durability, survival of a write killed before the accepted Dirty
window elapses, WS Ticket survival across LoginSvr restart, availability or
capacity. The restart completed before the 60-second fertilizer expiry and
70-second fertilized maturity time, so live expiry/maturity recovery is
unverified.

## Reproduction

From the repository root:

```powershell
.\tests\e2e\run-mysql-restart-recovery.ps1
```

The script locates `mysql.exe` from PATH or the installed Windows MySQL
service, prompts securely for the application password, applies the idempotent
migrations and generates a unique test account. It runs the authenticated E2E
in `register` mode with `BUY_SEEDS`, `PLANT`, `APPLY_FERTILIZER` and all three idempotency replays,
tears down every child process, then runs against a fresh stack in `login` mode
expecting the planted checkpoint.

## Observed result

First stack:

```text
MIGRATE file=000003_local_shard_fences.up.sql
PHASE mode=register account=restart_515b26d11077
HTTP_AUTH mode=register account=restart_515b26d11077 player_id=6 csrf_rotated=true
GET_SHOP ... config_version=1 shop_entry_id=5001 item_id=1001 unit_price=2 price_version=8
SNAPSHOT ... snapshot_player_id=6 owner_epoch=1 player_seq=0 coins=10 seed_quantity=0 plot_state=EMPTY
BUY_SEEDS ... player_seq=1 coins=4 seed_item_1001=3 replayed=true
PLANT ... player_seq=2 seed_item_1001=2 plot_id=1 plot_state=GROWING crop_id=2001 replayed=true
APPLY_FERTILIZER ... player_seq=3 fertilizer_item_1=0 effect_instance_id=483c483a-674a-51a3-8a05-9bd17106e344 replayed=true
RESULT authenticated_snapshot_e2e=PASS adapter=mysql-restart-register
```

All four service processes then stopped, followed by:

```text
RESTART boundary=fresh-four-process-stack
PHASE mode=login account=restart_515b26d11077
HTTP_AUTH mode=login account=restart_515b26d11077 player_id=6 csrf_rotated=true
SNAPSHOT ... snapshot_player_id=6 owner_epoch=1 player_seq=3 coins=4 seed_quantity=2 plot_state=GROWING
RESULT authenticated_snapshot_e2e=PASS adapter=mysql-restart-login
RESULT mysql_restart_recovery_e2e=PASS account=restart_515b26d11077
```

Both phases also passed Bootstrap/config digest validation, WebSocket AUTH,
PING correlation and one-time Ticket replay rejection.

## Interpretation

- The stable `player_id=6` demonstrates durable account identity.
- Successful second-phase login demonstrates that account credentials and
  Session-generation state were available to the fresh LoginSvr.
- The fresh ZoneSvr selected `state_adapter=mysql-checkpoint`; `player_seq=3`,
  4 coins, two seeds, absent fertilizer inventory and the asserted effect on
  the `GROWING` plot demonstrate recovery of all three Dirty mutations.
- The repeated `BUY_SEEDS` request returned `replayed=true` at the same
  `player_seq=1`, so the observed command did not deduct coins twice.
- The repeated `PLANT` request returned `replayed=true` at
  `player_seq=2`, so it did not consume another seed or plant twice.
- The repeated `APPLY_FERTILIZER` request returned `replayed=true` at
  `player_seq=3`, preserving the first effect identity.
- Unit tests prove that BUY, PLANT and APPLY_FERTILIZER can be coalesced into
  one checkpoint CAS while retaining all idempotency results, frozen plot
  fields and the timed effect.
- Unit tests separately verify the exact Fence-row read and checkpoint CAS SQL.
  This live run exercised their success path; it did not deliberately change
  the Fence row to prove stale-owner rejection.
