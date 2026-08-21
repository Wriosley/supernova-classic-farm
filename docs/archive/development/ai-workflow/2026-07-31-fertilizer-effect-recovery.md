---
date: 2026-07-31
ai: Cursor coding agent
task: fertilizer effect, interval growth and restart recovery
commit:
---

# AI Work Record: fertilizer effect recovery

## Goal

Implement the next accepted business command after planting: settle growth under
the old rate, apply one frozen fertilizer interval, preserve idempotency and
recover the effect from MySQL.

## Changes

- added versioned fertilizer configuration;
- implemented exact interval splitting across fertilizer and future pest
  boundaries;
- implemented `APPLY_FERTILIZER` state/config/inventory/active-slot validation;
- generated deterministic RFC-4122-formatted effect IDs from player and request
  identity;
- froze modifier, config version and `[start_at_ms, end_at_ms)` in the
  checkpoint;
- recomputed estimated maturity across the boosted interval and later base
  interval;
- advanced the fertilizer task, stored terminal idempotency results and marked
  Dirty;
- extended the four-process and MySQL restart E2Es.

## Verification

- unit tests prove `+0.5` for 60 seconds yields growth 75 at 50 seconds and
  maturity at 70 seconds for the development crop;
- same-request replay preserves the first effect ID and does not consume a
  second item;
- owner-run MySQL 8.4.11 recovery restored `player_id=6`, `player_seq=3`, two
  seeds, zero fertilizer and the timed effect after all four services restarted;
- complete Go tests and static analysis pass.

## Remaining uncertainty

- the live restart completed before effect expiry and crop maturity;
- maturity Push is not connected to Gate/H5;
- pest command creation and cross-player rules remain unimplemented;
- fertilizer duration and item values are local development conventions.

## Evidence

- `../../../archive/evidence/historical/2026-07-31-mysql-restart-recovery-e2e.md`
- `../../../archive/evidence/historical/2026-07-31-growth-and-maturity-tests.md`
