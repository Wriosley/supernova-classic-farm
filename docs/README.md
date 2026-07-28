# Classic Farm Documentation

This directory separates current project truth, design reasoning, executable contracts, work plans, and measured evidence.

## Start here

1. Read `../AGENTS.md`.
2. Read `context/PROJECT.md` for stable project facts.
3. Read `context/CURRENT.md` for the actual current state and next actions.
4. Read `architecture/stateful-zone-v2-architecture.md` for the current target architecture.
5. Read only the requirement, module, contract, ADR, plan, or evidence files relevant to the current task.

## Current architecture

- Current production target: stateful Player Actor Zone V2.
- Accepted decisions: `decisions/ADR-0003-stateful-player-actor-zone.md` and `decisions/ADR-0004-shard-placement-and-control-plane-consensus.md`.
- Historical comparison only: stateless V1 in `architecture/target-30m-dau-architecture.md` and its old open-question plan.
- Target numbers and component choices remain planning assumptions until supported by evidence.

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
