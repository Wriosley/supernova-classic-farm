---
date: 2026-07-31
ai: Cursor coding agent
task: fixed-point crop growth and maturity materialization
commit:
---

# AI Work Record: growth and maturity

## Goal

Build the authoritative base-rate growth primitive required before fertilizer:
checked fixed-point settlement, offline maturity during Actor activation and
the local online maturity scan.

## Changes

- added exact millisecond `RateDecimal6 -> GrowthDecimal9` settlement with
  arbitrary-width multiplication intermediates;
- enforced monotonic effective time under clock rollback;
- added deterministic estimated-maturity calculation;
- materialized plots in stable ID order and advanced both versions once per
  plot;
- settled overdue plots before first Actor response and before later requests;
- added a one-second runtime scan for online Actors;
- retained Dirty checkpoint CAS against the pre-activation persisted revision;
- pinned one server time per request for maturity plus command execution.

## Correction found during testing

The first activation implementation assigned the post-maturity in-memory
revision as the persisted CAS base. A test exposed that this would attempt CAS
`expected=4` against a database row still at revision `3`. The runtime now
captures the loaded revision before activation settlement and flushes the new
revision `4` with `expected=3`.

## Verification

- exact half-growth and maturity tests pass;
- rollback and very-large-elapsed tests pass;
- activation-time maturity returns `MATURE`, increments to `player_seq=3` and
  produces the correct Dirty CAS;
- online scan materialization passes;
- `go test ./...` and `go vet ./...` pass.

## Remaining uncertainty

- effect-aware interval splitting is not implemented;
- no maturity Push reaches Gate/H5;
- wall-clock scheduler latency and load are not measured;
- no live MySQL run waited through the 100-second development maturity time.

## Evidence

- `../evidence/2026-07-31-growth-and-maturity-tests.md`
