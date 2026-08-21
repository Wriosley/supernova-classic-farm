---
status: measured
date: 2026-08-03
scope: direct plot cleanup and basic-fertilizer shop purchase
---

# Fertilizer shop and cleanup evidence

## Claim boundary

- H5 permits the shovel on every authoritative `NEED_CLEANUP` plot without
  checking whether the chapter-one reward was claimed.
- `GET_SHOP` exposes basic fertilizer (`item_id=1`) as a 2-coin buy quote.
- `BUY_FERTILIZER` accepts quantities 1–50, deducts the pinned quote total,
  adds to the 300-item stack, persists an idempotency record and returns a
  state patch. It does not advance the seed-purchase task.

## Automated verification

```powershell
cd proto
.\scripts\generate.ps1

cd ..\server
go test ./internal/player ./internal/gateway

cd ..\web
npm run build
```

All commands passed on 2026-08-03. The Player runtime test covers a
two-fertilizer purchase, same-request replay, a quantity of 51 rejection, and
unchanged seed-task progress. Configuration tests assert the three stable
development shop quotes.

## Limitations

This evidence does not claim a new browser or MySQL restart run. The existing
checkpoint model persists inventory, coins, task state and idempotency records;
the complete repository regression and MySQL restart scenario should be rerun
before presenting the new command as end-to-end durability evidence.
