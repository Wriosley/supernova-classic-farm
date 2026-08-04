# Classic Farm Documentation

This directory separates current project truth, design reasoning, executable contracts, work plans, and measured evidence.

## Start here

1. Read `../AGENTS.md`.
2. Read `context/PROJECT.md` for stable project facts.
3. Read `context/CURRENT.md` for the actual current state and next actions.
4. Read `architecture/stateful-zone-v3-architecture.md` for the current target architecture.
5. For the first product slice, read `architecture/single-player-vertical-loop-business-architecture.md`.
6. Read only the requirement, module, contract, ADR, plan, or evidence files relevant to the current task.

## Current architecture

- Current production target: stateful Player Actor Zone V3 with asynchronous Dirty writeback.
- Accepted active decisions: ADR-0003 for Player Actors, ADR-0006 for Dirty persistence, ADR-0008 for majority-authorized Shard ownership, and ADR-0009 for current-chapter task ownership.
- Accepted first-slice business architecture: `architecture/single-player-vertical-loop-business-architecture.md`.
- Accepted first-stage contracts: `contracts/http-api.md`, `contracts/websocket-protocol.md`, `contracts/idempotency-and-errors.md`, `contracts/data-model.md`, and `contracts/event-contracts.md`.
- Historical comparison only: synchronous-Journal V2, stateless V1, ADR-0004, ADR-0005, and their old plans.
- Target numbers and component choices remain planning assumptions until supported by evidence.

## Current implementation boundary

- The first runnable technical slice includes LoginSvr, GateSvr, ZoneSvr, a single-node Coordinator-compatible process, shared Go/TypeScript Protobuf and a Vue 3 snapshot/Push client.
- A multi-process protocol test and one owner-confirmed browser smoke reached the Player Actor snapshot.
- The default no-DSN run keeps accounts, Sessions, tickets, routes and Player Actors in process-local development memory.
- An optional MySQL code path commits account, first Session and initial Player checkpoint in one transaction, loads it on Actor activation, and asynchronously flushes Actor mutations under checkpoint CAS and a local database Fence. Mocked-SQL tests plus live idempotency replay and fresh-process `player_seq=3` fertilized-state recovery pass on MySQL 8.4.11; the extended harvested-state restart run is prepared but not yet owner-run.
- Zone has an immutable, atomically replaceable local configuration snapshot. `GET_SHOP` and `BUY_SEEDS` share its versioned quote; standalone ConfigSvr publication is not implemented.
- Exact base/effect interval growth, activation-time maturity and the local online maturity scan have automated tests. A live four-process run delivered natural maturity from Zone through Gate as an unsolicited `PLAYER_STATE_CHANGED` Push; Gate snapshot buffering and newer-version filtering have unit coverage.
- `HARVEST` enforces complete-yield warehouse capacity before mutation, advances the task and persists the `NEED_CLEANUP` plot. Unit/checkpoint tests and an in-memory four-process flow through `player_seq=5` pass.
- The local Push transport is loopback and non-durable; cross-Gate delivery, retry and production backpressure are not implemented. Tickets and CSRF remain process-local in both modes. Live MySQL maturity-boundary recovery, stale-owner Fence rejection, abnormal Dirty-window loss, the rest of the business loop and capacity evidence remain future work.
- Read `context/CURRENT.md` for the exact handoff and the dated files under `evidence/` for observed results and limitations.

## Directory roles

- `requirements/`: confirmed or proposed product and non-functional requirements.
- `architecture/`: current system topology and cross-cutting design.
- `modules/`: business ownership, capabilities, invariants, and module flows.
- `contracts/`: precise HTTP, WebSocket, event, data, error, and idempotency rules.
- `decisions/`: major alternatives, decisions, costs, and validation methods.
- `plans/`: unresolved-question boards and bounded execution plans.
- `evidence/`: reproducible tests, measurements, and limitations.
- `context/`: stable project context and mutable current handoff.
- `ai-workflow/`: concise AI collaboration records; never formal truth by itself.

## Source-of-truth rules

- Product scope comes from confirmed requirements.
- Architecture tradeoffs come from the current architecture and latest accepted ADR.
- Precise request, event, error, and storage formats come from contracts.
- Actual capability and performance claims require evidence.
- Plans and AI work records cannot override requirements, accepted decisions, contracts, or evidence.
- `ai-context`, UC notes, and chat history are reference material; copy only reviewed conclusions into this repository.

See `architecture/documentation-system.md` for the document lifecycle and migration rules.
