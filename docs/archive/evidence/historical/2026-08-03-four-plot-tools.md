---
status: measured
date: 2026-08-03
scope: four authoritative plots and tool-driven H5 controls
---

# Four-plot tool interaction evidence

## Claim boundary

This change proves that newly created development Player state and MySQL
registration checkpoints contain four ordered authoritative plots
(`plot_id=1..4`). It also proves that the Vue H5:

- renders the four plots as a responsive 2x2 grid;
- selects seed, fertilizer, shovel or hand before targeting a plot;
- maps only a valid tool/state pair to the corresponding command;
- identifies a busy command by plot;
- selects a seed purchase quantity from 1 through 50;
- sends either an explicit crop-sale quantity or `sell_all`;
- uses four project-owned placeholder tool images in both the toolbar and
  desktop cursor rules.

The 50-item purchase cap is an H5 input rule. Coin balance, the 300-item stack
limit and all command preconditions remain server-authoritative.

## Static verification

```powershell
cd server
go test ./...
go vet ./...

cd ../web
npm run typecheck
npm run build
```

All commands passed. The web production build emitted approximately 184 KiB
of JavaScript and 12 KiB of CSS before gzip.

The deterministic tool generator produced `seed.png`, `fertilizer.png`,
`shovel.png` and `hand.png`. Their manifest count and 16x16 PNG dimensions
were checked with Node because this Windows environment did not have a usable
Python runtime for the existing full art validator.

## Browser observations

An in-memory four-process stack returned four empty plots to a newly
registered H5 player. Browser inspection observed:

```text
tool seed selected, inventory=0
plot 1..4=EMPTY
seed + EMPTY with no seed -> local "仓库里没有可用种子。" feedback
purchase quantity=50 -> total=100 coins and disabled for a 10-coin player
purchase quantity=1 -> total=2 coins and enabled
purchase quantity=3 -> server response credited exactly 3 seeds
seed + plot 2 -> plot 2=GROWING, plots 1/3/4 remained EMPTY
fertilizer + plot 2 -> fertilizer inventory reached 0
natural MATURED Push -> only plot 2 became harvestable
hand + plot 2 -> plot 2=NEED_CLEANUP and crop inventory reached 3
explicit sale quantity=1 -> one crop sold
sell_all -> the remaining two crops sold
chapter reward -> 29 coins, one fertilizer and three next-chapter seeds
shovel + plot 2 -> all four plots returned to EMPTY
```

The precondition failure above did not enter the WebSocket command path.
The final action notice was
`地块清理完成，服务端单玩家闭环已完成。`

The selected shovel produced a computed desktop cursor URL for
`runtime/tools/shovel.png` with hotspot `(8,8)`. A 320 CSS-pixel device check
returned:

```text
innerWidth=320
scrollWidth=320
horizontalOverflow=false
farmDashboardWidth=288
plotGridColumns=102.406px 102.406px
```

## Persistence compatibility

The authenticated snapshot E2E now asserts four ordered plots and requires
plots 2–4 to remain `EMPTY` while the existing command chain mutates plot 1.
Player/checkpoint tests also assert that planting the target plot leaves the
other three unchanged.

The owner ran the MySQL 8.4 restart script after applying migrations
`000001..000004`:

```text
account=restart_7cfd11131634
player_id=11
register stack: complete command loop through player_seq=8 PASS
restart boundary: fresh Login/Zone/Coordinator/Gate processes
login stack: recovered player_seq=8, coins=29 and plot 1=EMPTY PASS
secondary plot assertions: plot_id=2..4 remained EMPTY
RESULT mysql_restart_recovery_e2e=PASS
```

Old development checkpoints are not migrated online: local old data must be
explicitly reset and the player re-registered.

## Limitations

This work does not add plot-unlock progression or a production migration for
old one-plot checkpoints. Browser interaction uses the development in-memory
adapters; MySQL restart recovery remains a separate protocol E2E rather than
one combined browser run.
