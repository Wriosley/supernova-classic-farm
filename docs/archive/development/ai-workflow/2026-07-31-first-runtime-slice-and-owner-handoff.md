---
date: 2026-07-31
ai: Cursor coding agents
task: first authenticated runtime slice and owner handoff
commit:
---

# AI Work Record: First authenticated runtime slice

## Goal

Convert the frozen first-stage contracts into the smallest runnable path from
H5 registration through an authenticated WebSocket to a Player Actor snapshot,
while preserving the distinction between prototype evidence and the V3 target.

## Context provided

- V3 stateful Zone architecture and active ADRs;
- accepted single-player business architecture;
- accepted HTTP, WebSocket, idempotency/error, data-model and event contracts;
- the 2026-08-02 minimum runnable-framework milestone.

## Boundaries and done criteria

- one correlated `GET_PLAYER_SNAPSHOT` command;
- no friends, multiplayer, full mail or complete farm business loop;
- a single-node Coordinator-compatible process only;
- local development adapters were allowed only when their limitations were
  explicit and recorded.

## Human's initial reasoning

The owner identified the request path and Zone scheduling as important
30-million-DAU design concerns beyond the business loop, and asked to divide
independent work among multiple AI conversations.

## AI proposal

The work was divided by exclusive file ownership into contract review,
Protobuf/tooling, platform infrastructure, Login, routing/Coordinator, Zone
Actor, Gate, H5 and E2E evidence.

## Human corrections and decisions

- The owner required a runnable view and asked how to inspect and debug it.
- A manual browser run exposed a real `CSRF_REJECTED` defect after successful
  registration.
- The owner then confirmed the corrected browser flow succeeded.
- The owner stated that implementation understanding is insufficient for the
  defense. Further feature expansion must proceed step by step. An owner-facing
  current request-chain walkthrough now exists at
  `D:\Knowledge\mine\classic-farm-note\05-答辩准备\当前认证快照请求链路讲解.md`.
- The owner then explicitly skipped teach-back and directed work to continue
  with MySQL registration and Player checkpoint persistence.

## Changes made

- reconciled three cross-contract version/rollback ambiguities;
- generated shared Go and TypeScript Protobuf;
- implemented platform helpers and local startup scripts;
- implemented development Login, Gate, Zone, Player Actor and single-node
  Coordinator-compatible processes;
- implemented the Vue H5 authenticated snapshot client;
- added multi-process E2E and protocol-toolchain evidence;
- fixed authenticated CSRF issuance and added a regression test;
- documented the exact current in-memory ownership and durability gap.

## Independent review

Independent agents reviewed authentication/WS consistency, data/event
consistency, the MT speaking outline, and the final repository verification.
The owner performed the decisive manual browser smoke.

## Verification

- shared Proto Go/TypeScript round-trip passed;
- complete Go tests and `go vet ./...` passed;
- Vue production build and WebCrypto hash tests passed;
- four-process protocol E2E passed registration, bootstrap/config digest,
  Ticket, AUTH, PING, snapshot and Ticket replay rejection;
- owner confirmed the corrected browser registration-to-snapshot path.

## Remaining uncertainty

- accounts, Sessions, tickets, routes and Player Actors are process-local;
- registration does not create a durable Player checkpoint;
- Zone lazily creates development Player state on first command;
- MySQL schema, checkpoint loading, Dirty writeback, fence enforcement,
  recovery, full business behavior and capacity remain unimplemented;
- browser evidence is manual rather than automated;
- owner understanding remains a defense risk, but it is not the current
  implementation task.

## Related artifacts

- ADR: ADR-0006, ADR-0008, ADR-0009
- Plan: `../plans/2026-07-31-v3-first-stage-implementation-plan.md`
- Evidence: `../../../archive/evidence/historical/2026-07-30-protobuf-toolchain.md`
- Evidence: `../../../archive/evidence/historical/2026-07-31-authenticated-snapshot-e2e.md`
- Evidence: `../../../archive/evidence/historical/2026-07-31-browser-manual-smoke.md`
- Commit:
