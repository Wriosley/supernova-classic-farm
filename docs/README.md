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

- The runnable slice includes LoginSvr, GateSvr, ZoneSvr, a single-node Coordinator-compatible process, shared Go/TypeScript Protobuf and a Vue 3 farm client.
- An explicit dual-Zone mode uses versioned Rendezvous bootstrap placement, a
  committed 4096-entry ShardMap, an immutable Gate RouteCache and Zone-side
  ownership Snapshots. Its memory-only five-process E2E proves routing to both
  Zones, wrong-Owner rejection and one inactive-Shard epoch-incrementing
  handoff. Static epoch-one MySQL Fence alignment and one persisted write in
  each Zone are verified. MySQL-backed active migration now final-flushes the
  old Actors, advances the PREPARING Fence, prepares target checkpoints and
  persists a post-migration write at epoch two. Coordinator restart rebuilds
  routes from fences, overlays open PREPARING fail-closed, and exposes
  inspect/continue/abandon controls backed by durable migration progress.
- The H5 exposes the complete owner loop through a responsive shop, plot, inventory and chapter interface. A browser-driven in-memory run reached `player_seq=8`, consumed one maturity Push and completed without version-gap recovery; type-check, production build and a 320-pixel no-overflow check pass.
- The default no-DSN run keeps accounts, Sessions, tickets, routes and Player Actors in process-local development memory.
- An optional MySQL code path commits account, first Session and initial Player checkpoint in one transaction, loads it on Actor activation, and asynchronously flushes Actor mutations under checkpoint CAS and a database Fence. The single-Zone path has live fresh-process full-loop `player_seq=8` recovery; the static dual-Zone path has live Zone-A/Zone-B `player_seq=1` persistence evidence.
- Zone has an immutable, atomically replaceable local configuration snapshot. `GET_SHOP`, `BUY_SEEDS` and `SELL_CROP` share its versioned buy/sell quotes; standalone ConfigSvr publication is not implemented.
- Exact base/effect interval growth, activation-time maturity and the local online maturity scan have automated tests. A live four-process run delivered natural maturity from Zone through Gate as an unsolicited `PLAYER_STATE_CHANGED` Push; Gate snapshot buffering and newer-version filtering have unit coverage.
- `HARVEST` enforces complete-yield warehouse capacity before mutation, advances the task and persists the `NEED_CLEANUP` plot. Unit/checkpoint tests plus in-memory and MySQL four-process flows through `player_seq=5` pass.
- `SELL_CROP` supports quantity and sell-all semantics, checked integer pricing, retained idempotency and chapter transition to `CLAIMABLE`. Unit/checkpoint tests plus in-memory and MySQL four-process flows through fresh-process `player_seq=6` recovery pass.
- `CLAIM_CHAPTER_REWARD` grants the accepted first-chapter reward, activates development chapter two and retains an idempotent receipt. Warehouse overflow creates one deterministic pending reward-mail Outbox record; checkpoint CAS and relational Outbox insertion share one MySQL transaction. Unit tests, mocked SQL and live in-memory/MySQL flows through fresh-process `player_seq=7` recovery pass.
- `CLEAN_PLOT` requires `NEED_CLEANUP`, clears all frozen crop fields and returns the plot to `EMPTY` without resources or task progress. Unit/checkpoint tests and live in-memory/MySQL full owner loops through fresh-process `player_seq=8` recovery pass.
- The Outbox relay, Mail Service, delivery reconciliation and mail UI are not implemented, so `items_pending_mail` means only that Actor state recorded a pending event.
- The local Push transport is loopback and non-durable; cross-Gate delivery, retry and production backpressure are not implemented. Tickets and CSRF remain process-local in both modes. A combined MySQL-backed browser run, abnormal Dirty-window loss and capacity evidence remain future work.
- Coordinator route and migration progress remain process-local. Restart after
  a durable migrated Fence intentionally fails closed; persistent PREPARING
  recovery is not yet implemented.
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
