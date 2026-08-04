---
status: completed
date: 2026-08-03
---

# Coordinator PREPARING Recovery

## Human decisions

- Persist only migration progress, not a full 4096-row ShardMap table.
- Restart stays fail-closed; expose inspect / continue / abandon.
- Abandon is allowed only before Fence advance.

## AI-assisted work

- Added `shard_migration_progress` DDL and MySQL progress helpers.
- Hydrated ACTIVE routes from advanced fences; restored PREPARING overlays.
- Persisted MySQL move step boundaries and deleted progress on ACTIVE.
- Added loopback inspect/list/continue/abandon controls.
- Extended dual-Zone MySQL E2E with Coordinator restart hydration.

## Verification

```text
go test ./... -count=1                PASS
go vet ./...                          PASS
run-dual-zone-mysql.ps1               PASS
```

Live hydrate after Coordinator restart observed `advanced_fences=2` and
`dual-Zone MySQL routes hydrated from fences`.

## Remaining uncertainty

Live crash between drain and Fence still depends on Zone drain state surviving
Coordinator-only restart; component tests cover the continue path.
