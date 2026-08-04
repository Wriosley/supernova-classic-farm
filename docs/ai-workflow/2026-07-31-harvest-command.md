# AI Work Record: HARVEST command

## Goal

Continue the accepted owner loop after natural maturity by implementing
all-or-nothing harvest into the Player Actor warehouse.

## Changes

- added the `HARVEST` Runtime branch and request fingerprint;
- required a known `MATURE` plot and returned `CROP_NOT_MATURE` or
  `PLOT_STATE_CONFLICT` for invalid states;
- derived yield from frozen `base_yield - stolen_quantity`;
- validated the 100-type and 300-per-stack warehouse limits before mutation;
- added the complete crop yield, advanced task 4 and changed the plot to
  `NEED_CLEANUP`;
- cleared growth-only and timed-effect fields while retaining crop identity for
  cleanup projection and checkpoint validation;
- retained terminal success/failure results for idempotent replay;
- extended the protocol E2E and MySQL restart wrapper through harvest.

## Verification

- focused success, replay, checkpoint and atomic capacity-failure tests pass;
- `go test ./...` and `go vet ./...` pass;
- the live in-memory four-process flow reached natural maturity at
  `player_seq=4`, harvested three crop items, returned `NEED_CLEANUP` at
  `player_seq=5`, and replayed without applying twice.

Observed output is recorded in `../evidence/2026-07-31-harvest-e2e.md`.

## Remaining uncertainty

The password-prompted MySQL restart wrapper now expects the harvested
`player_seq=5` checkpoint, but this extension still needs an owner-run live
result. Sell, reward claim and cleanup remain unimplemented.
