---
date: 2026-07-31
ai: Cursor coding agent
task: MySQL registration and Player checkpoint slice
commit:
---

# AI Work Record: MySQL registration and checkpoint slice

## Goal

Add the smallest accepted durable path after the in-memory authenticated
snapshot proof: one local transaction for account, initial Player checkpoint
and first Session, followed by checkpoint-backed Actor activation.

## Context provided

- the accepted HTTP contract requires externally atomic registration;
- the accepted data-model contract requires a deterministic
  `PlayerCheckpointV1` blob plus a relational envelope;
- the current machine has no verified Docker/MySQL runtime.

## Boundaries and done criteria

- preserve the no-DSN in-memory first-stage path;
- select MySQL explicitly through `MYSQL_DSN`;
- never expose an account if checkpoint or Session creation fails;
- verify deterministic blob SHA-256 and envelope agreement before activation;
- do not claim live durability without a real MySQL restart test.

## Human corrections and decisions

The owner explicitly skipped teach-back and requested that implementation
continue directly.

## Changes made

- added account, HTTP Session and Player checkpoint migration DDL;
- added shared MySQL connection setup;
- added a MySQL-backed Login store for durable registration, login, Session
  lookup, refresh and revocation;
- made registration commit account, checkpoint and first Session in one
  serializable transaction;
- added deterministic initial checkpoint creation and validation;
- added MySQL checkpoint loading and Zone Actor activation;
- added the first `BUY_SEEDS` Actor write with retained idempotency success and
  terminal-failure results;
- added asynchronous Dirty checkpoint flushing with checkpoint-revision CAS
  and exact local database Fence checks;
- kept CSRF, WS Ticket, Coordinator route and online Actor state in memory;
- added optional MySQL selection to LoginSvr, ZoneSvr and the startup script.

## Verification

- complete `go test ./...` passed;
- `go vet ./...` passed;
- mocked SQL verifies registration commit and rollback on checkpoint failure;
- checkpoint tests verify deterministic round-trip, digest and envelope checks;
- Runtime tests verify loader-backed activation and no default-state fallback;
- PowerShell parser and `git diff --check` passed.
- after local MySQL 8.4.11 installation and migration, the owner ran the secure
  MySQL E2E wrapper; live registration, cross-process checkpoint activation,
  snapshot and Ticket replay rejection passed.
- the owner then ran a two-phase restart wrapper: the first fresh stack
  registered `player_id=2`, every process stopped, and a second fresh stack
  logged into the same account and restored the same initial checkpoint.
- after the Dirty slice was added, the owner ran the extended wrapper with
  `player_id=4`: `BUY_SEEDS` reached `player_seq=1`, 4 coins and three seed
  items, same-ID replay did not execute twice, and a fresh four-process stack
  restored that mutated checkpoint from MySQL.

## Remaining uncertainty

- Ticket/CSRF persistence remains outside this slice;
- the local Fence success path is exercised, but stale-owner rejection and
  abnormal termination inside the accepted Dirty-loss window are not live-tested;
- multi-Actor Dirty batching and throughput are not measured;
- no production cross-shard provisioning state machine is implemented.

## Related artifacts

- Plan: `../plans/2026-07-31-v3-first-stage-implementation-plan.md`
- Evidence: `../evidence/2026-07-31-mysql-registration-checkpoint-unit.md`
- Evidence: `../evidence/2026-07-31-mysql-authenticated-snapshot-e2e.md`
- Evidence: `../evidence/2026-07-31-mysql-restart-recovery-e2e.md`
- Commit:
