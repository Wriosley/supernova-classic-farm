# Classic Farm Documentation

This directory separates current project truth, design reasoning, executable contracts, work plans, and measured evidence.

## 面向负责人和评审

从 [`delivery/README.md`](../../delivery/README.md) 开始。该入口只组织当前有效架构、
实现能力、最终性能结果、接口合同和演示方法，不要求阅读开发期间的计划与流水记录。

## 面向开发与维护

1. Read `../AGENTS.md`.
2. Read `context/PROJECT.md` for stable project facts.
3. Read `context/CURRENT.md` for the actual current state and next actions.
4. Read `architecture/stateful-zone-v3-architecture.md` for the current target architecture.
5. For the first product slice, read `architecture/single-player-vertical-loop-business-architecture.md`.
6. Read only the requirement, module, contract, ADR, plan, or evidence files relevant to the current task.

## 当前架构

- Current production target: stateful Player Actor Zone V3 with asynchronous Dirty writeback.
- Accepted active decisions: ADR-0003 for Player Actors, ADR-0006 for Dirty persistence, ADR-0008 for majority-authorized Shard ownership, and ADR-0009 for current-chapter task ownership.
- Accepted first-slice business architecture: `architecture/single-player-vertical-loop-business-architecture.md`.
- Accepted contracts: `contracts/http-api.md`, `contracts/websocket-protocol.md`, `contracts/idempotency-and-errors.md`, `contracts/data-model.md`, `contracts/event-contracts.md`, and `contracts/internal-grpc.md`.
- Historical comparison only: synchronous-Journal V2 and stateless V1 are under
  `archive/architecture-v1-v2/`; ADR-0004, ADR-0005 and old plans remain design history.
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
- Friend phases 0–5 are complete: contracts are frozen, existing
  Gate→Zone/Zone→Gate game traffic uses HMAC-authenticated gRPC, FriendSvr
  plus authoritative friend relations/lists/task credit are live against
  real friend Tcaplus tables, and Gate/Zone route `ENTER`/`HEARTBEAT`/
  `EXIT_FRIEND_FARM` to a public farm snapshot with a minimal H5 friends
  panel. Public plot mutations (plant, fertilize, harvest, clean, natural
  maturity) now bump an independent `farm_view_seq` and broadcast an
  incremental `FarmViewPatch` to the owner and every online visitor through
  Gate; H5 replaces the full snapshot on an epoch change or seq gap. The
  cross-Actor `FriendInteraction` Saga is live for `STEAL_FRIEND_CROP`:
  frozen per-plot steal fields at plant, synchronous visitor
  reserve/owner-apply/visitor-commit/release steps, a Tcaplus (or in-memory
  dev) `FriendInteraction` store with CAS, and a 5-second reconciler ticker
  that recovers all three crash windows; ordinary single-player commands
  remain unaffected asynchronous Dirty writes. Phase 6 (pest/catch/help
  interactions, reusing the same Saga infrastructure) is the next boundary.
- Read `context/CURRENT.md` for the exact handoff and the dated files under `evidence/` for observed results and limitations.

## Directory roles

- `delivery/`: final delivery and defense reading entrance.
- `requirements/`: confirmed or proposed product and non-functional requirements.
- `architecture/`: current system topology and cross-cutting design.
- `modules/`: business ownership, capabilities, invariants, and module flows.
- `contracts/`: precise HTTP, WebSocket, event, data, error, and idempotency rules.
- `decisions/`: major alternatives, decisions, costs, and validation methods.
- `evidence/`: final-report evidence, performance topics and representative mechanism validation.
- `bugs/`: root-cause writeups for defects that locked players out or corrupted
  state (phenomenon, cause, investigation, fix). Reference only; current
  capability still lives in `context/CURRENT.md` and dated `evidence/`.
- `context/`: stable project context and mutable current handoff.
- `archive/`: superseded architecture, historical evidence, development plans,
  AI workflow records, study notes, generated implementation plans and tooling.

Internal development material is under `archive/development/`. It is preserved
for traceability and intentionally excluded from the delivery reading path.

## Source-of-truth rules

- Product scope comes from confirmed requirements.
- Architecture tradeoffs come from the current architecture and latest accepted ADR.
- Precise request, event, error, and storage formats come from contracts.
- Actual capability and performance claims require evidence.
- Plans and AI work records cannot override requirements, accepted decisions, contracts, or evidence.
- `ai-context`, UC notes, and chat history are reference material; copy only reviewed conclusions into this repository.

See `architecture/documentation-system.md` for the document lifecycle and migration rules.
