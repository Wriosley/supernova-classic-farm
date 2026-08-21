---
status: measured
date: 2026-07-31
scope: SELL_CROP and chapter completion
---

# SELL_CROP command evidence

## Claim boundary

Automated tests prove that:

1. development configuration publishes stable seed-buy and crop-sell quotes;
2. invalid or duplicate sell rules are rejected;
3. an explicit positive quantity sells only that quantity;
4. `sell_all` resolves the full stack once and same-ID replay returns the first
   resolved quantity;
5. stale `price_version`, insufficient inventory and non-sellable items fail
   without changing coins, inventory, tasks or `player_seq`;
6. successful sale removes or updates the stack, adds checked integer-price
   coins and advances the sell task by sold quantity;
7. completing the fifth task changes the chapter to `CLAIMABLE`;
8. `CLAIMABLE` survives checkpoint serialization and restoration.

Live in-memory and MySQL four-process runs prove the protocol flow from
registration through sell-all. The owner-run MySQL extension also proves
fresh-process recovery of the sold checkpoint and `CLAIMABLE` chapter.

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
GET_SHOP ... seed_entry_id=5001 seed_price=2 seed_price_version=8
  crop_entry_id=5002 crop_price=5 crop_price_version=9
HARVEST ... player_seq=5 crop_item_1002=3
SELL_CROP ... player_seq=6 sold_quantity=3
  coins=19 chapter_status=CLAIMABLE replayed=true
PASS TestAuthenticatedSnapshot (72.25s)
RESULT maturity_push_e2e=PASS
```

## Live MySQL restart result

```powershell
.\tests\e2e\run-mysql-restart-recovery.ps1 `
  -HostName 127.0.0.1 -Port 3306 -User classicfarm
```

Observed first stack:

```text
SELL_CROP ... player_seq=6 sold_quantity=3
  coins=19 chapter_status=CLAIMABLE replayed=true
RESULT authenticated_snapshot_e2e=PASS adapter=mysql-restart-register
```

After all four processes stopped, the fresh stack observed:

```text
HTTP_AUTH mode=login player_id=8
SNAPSHOT ... player_seq=6 coins=19 seed_quantity=2
  plot_state=NEED_CLEANUP
RESULT authenticated_snapshot_e2e=PASS adapter=mysql-restart-login
RESULT mysql_restart_recovery_e2e=PASS
```

The snapshot assertion also verified no crop stack and chapter status
`CLAIMABLE`. This run does not prove stale-owner rejection, abnormal loss
inside the accepted Dirty window, multi-Actor batching or capacity.
