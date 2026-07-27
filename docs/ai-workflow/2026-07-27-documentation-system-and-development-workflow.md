---
date: 2026-07-27
ai: Codex
task: Design the documentation system for architecture hardening and later AI implementation
status: recorded
---

# AI Work Record: Documentation System and Development Workflow

## Trigger

The target-scale architecture discussion produced a large open-question plan covering correctness, capacity, sharding, events, realtime, multi-AZ, security, and evidence. The owner recognized that this workflow plan is useful for tracking discussion but is not yet the final detailed design needed for implementation or defense.

## Owner intent

- Preserve the workflow-based plan as the list of unresolved and in-progress architecture work.
- Turn confirmed conclusions into structured formal design documents.
- Eventually provide AI workers with concrete data flow, interface, storage, failure, and test contracts sufficient for implementation.
- Keep the documentation understandable enough for the owner to explain during the defense.

## Chosen documentation direction

Use a layered documentation system rather than one giant document or final documents organized by discussion workflow:

- `requirements` records product and non-functional requirements;
- `architecture` records system-wide topology and cross-cutting design;
- `modules` records business ownership, capabilities, invariants, and module flows;
- `contracts` records precise HTTP, WebSocket, event, data, error, and idempotency rules;
- `decisions` records major tradeoffs;
- `plans` tracks open questions and implementation order;
- `evidence` records actual tests and measurements;
- `ai-workflow` records concise collaboration history, not formal truth.

The detailed proposed structure and migration safeguards are recorded in `docs/architecture/documentation-system.md`.

## Work-item lifecycle

An unresolved item remains in the architecture strategy plan until discussion produces a confirmed design. The result then moves to the relevant architecture or module document; exact formats move to contracts; major choices receive an ADR; implementation receives a phase-specific plan; executed tests and measurements move to evidence. The workflow checkbox links these outputs instead of duplicating them.

## AI implementation handoff

Future AI work should receive a task-scoped packet consisting of project context, relevant architecture, module design, contracts, decisions, implementation plan, and acceptance evidence. Chat history, UC learning notes, and AI workflow records remain reference material and cannot silently override formal documents.

## Current repository state and safeguards

At the time of this record:

- `docs/architecture/target-30m-dau-architecture.md` contains uncommitted sequence-diagram additions from another conversation;
- `docs/plans/2026-07-27-30m-dau-architecture-strategy-and-open-questions-plan.md` is an untracked owner-created plan;
- `docs/.obsidian/` remains untracked user state.

The documentation-system design must not overwrite, stage, or commit those items. Actual directory migration waits for owner review of the written organization design and will use small reversible commits with link validation.

## Next step

The owner reviews `docs/architecture/documentation-system.md`. After approval, create a detailed migration plan, then establish indexes and target folders before moving content topic by topic.
