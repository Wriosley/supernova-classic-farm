# AI Work Record: CLEAN_PLOT

## Goal

Complete the accepted server-side single-player owner loop by returning the
harvested plot from `NEED_CLEANUP` to `EMPTY`.

## Changes

- implemented `CLEAN_PLOT` in the Player Actor mailbox;
- validated plot identity and the cleanup-only state transition;
- reset the complete persisted plot record while preserving `plot_id`;
- retained success and deterministic failure results for idempotent replay;
- extended the end-to-end and MySQL restart wrappers through `player_seq=8`.

## Verification

- focused success, replay, failure-atomicity and checkpoint tests pass;
- `go test ./...` and `go vet ./...` pass;
- a live in-memory four-process run completed the full server owner loop with
  an `EMPTY` plot at `player_seq=8`.
- the owner-run MySQL extension completed the same loop, stopped all four
  services and recovered `player_id=10`, `player_seq=8`, 29 coins, the expected
  inventory, chapter two and the `EMPTY` plot from a fresh stack.

Observed output is in `../../../archive/evidence/historical/2026-07-31-clean-plot-e2e.md`.

## Remaining uncertainty

The H5 does not yet expose the full sequence of business interactions.
