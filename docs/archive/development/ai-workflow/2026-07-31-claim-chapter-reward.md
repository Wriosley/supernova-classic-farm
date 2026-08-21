# AI Work Record: CLAIM_CHAPTER_REWARD

## Goal

Continue from a `CLAIMABLE` first chapter through idempotent reward claim while
preserving the accepted warehouse-overflow and Outbox contract.

## Changes

- added immutable development chapter/reward configuration and a minimal
  chapter-two activation target;
- implemented claim validation, reward allocation, chapter transition,
  state patching and retained replay;
- added deterministic `CreateRewardMailV1` creation for item overflow;
- persisted pending Outbox records in the player checkpoint;
- added `player_outbox` DDL and atomic checkpoint/Outbox MySQL writer logic;
- extended unit tests and the four-process wrappers through `player_seq=7`.

The development chapter-two state is intentionally minimal and has no tasks.
This is an implementation convention, not a complete second-chapter product
design.

## Verification

- focused normal-claim, replay, failure-atomicity, full-warehouse,
  checkpoint-round-trip and mocked-SQL transaction tests pass;
- `go test ./...` and `go vet ./...` pass;
- a live in-memory four-process run reached 29 coins, one fertilizer, three
  next-chapter seeds and chapter two at `player_seq=7`.
- the owner-run MySQL extension completed the same flow, stopped all four
  services and recovered `player_id=9`, `player_seq=7`, 29 coins, two old
  seeds, one fertilizer, three next-chapter seeds, `NEED_CLEANUP` and chapter
  two from a fresh stack.

Observed output is in
`../../../archive/evidence/historical/2026-07-31-claim-chapter-reward-e2e.md`.

## Remaining uncertainty

The live normal reward fit inventory, so relational Outbox creation remains
covered by mocked SQL rather than a live overflow run. Outbox relay, Mail
Service, delivery reconciliation and `CLEAN_PLOT` remain unimplemented.
