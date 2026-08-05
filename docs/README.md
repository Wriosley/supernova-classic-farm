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

- The runnable product includes Login, Gate, Coordinator, two static Zone
  Owners, shared Go/TypeScript Protobuf and the Vue H5.
- The complete single-player loop is implemented through `player_seq=8`:
  buy, plant, fertilize, mature, harvest, sell, claim and clean.
- The current persistence target is pure Tcaplus. It stores accounts, Sessions,
  Player checkpoints, Shard fences, migration progress and Outbox records.
  MySQL remains a tested historical baseline and rollback adapter.
- Static dual-Zone routing uses a committed 4096-entry ShardMap, immutable Gate
  RouteCache, Zone authorization snapshots and epoch fencing. Inactive and
  active migration, stale-owner rejection and post-migration restart pass live
  Tcaplus E2E.
- A local kind cluster runs Coordinator, Login, Gate, `zone-a` and `zone-b` as
  five Ready Deployments. It uses fixed membership and does not implement
  dynamic discovery, HPA, PDB, Ingress/TLS or Zone-level preStop Drain.
- Default startup without a storage option remains development-only in-memory
  mode. Tickets and CSRF nonce records remain process-local by ADR-0010.
- Outbox relay, Mail Service, production Push delivery, abnormal Dirty-window
  guarantees and production capacity evidence remain outside the prototype.
- Friend functionality is design-only. The reviewed source is
  `plans/friend_design_plan/`; implementation resumes at phase 0 in
  `plans/friend_design_plan/06-分阶段实施方案.md`.
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
