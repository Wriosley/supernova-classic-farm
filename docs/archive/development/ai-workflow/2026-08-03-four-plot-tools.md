# AI Work Record: Four-plot tools

## Goal and boundary

Replace the single-plot button controls with four server-authoritative plots,
a tool-first interaction model and explicit buy/sell quantities. Multi-Zone
Gate routing and old-checkpoint online migration remain outside this change.

## Human decisions

- The first H5 version starts with four plots.
- Plant, fertilizer, harvest and cleanup are selected from a toolbar and then
  applied by clicking a plot.
- Seed purchase quantity is limited to 1–50 by the H5.
- Crop sale supports both a chosen quantity and all inventory.
- Existing local development data is reset/re-registered rather than migrated.

## Changes

- centralized four initial plot construction for in-memory and registration
  checkpoints;
- extended checkpoint and E2E assertions for ordered plot IDs and untouched
  secondary plots;
- added four deterministic project-owned tool placeholders and source records;
- rebuilt the farm dashboard around a 2x2 plot grid, tool selection,
  tool-specific cursors, invalid-target feedback and per-plot busy state;
- added quantity controls, price previews and an explicit-quantity
  `SELL_CROP` WebSocket client method;
- kept response patches merged by `plot_id`, preserving server authority.

## Verification

- `go test ./...`: pass;
- `go vet ./...`: pass;
- `npm run typecheck`: pass;
- `npm run build`: pass;
- four tool PNGs and their manifest dimensions: pass;
- browser: four empty plots, local invalid-target rejection, 1/50 purchase
  boundaries, isolated plot-2 planting/fertilizing/maturity/harvest/cleanup,
  explicit quantity sale, sell-all and chapter reward observed;
- browser 320 CSS-pixel check: no horizontal overflow.
- owner-run MySQL 8.4 fresh-process recovery: pass at `player_seq=8`, with
  all four ordered plots validated by the updated E2E.

Measured details are in
`../../../archive/evidence/historical/2026-08-03-four-plot-tools.md`.

## Remaining uncertainty

Old one-plot checkpoints require an explicit local reset. This change does not
claim production migration, final art, multi-Zone routing or browser-driven
MySQL recovery.
