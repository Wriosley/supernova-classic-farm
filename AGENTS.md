# Classic Farm Agent Guide

## Project goal

Build a demonstrable H5 classic farm game with a Go backend. The implementation must support the required product flow, explain a target design for 30 million DAU, preserve AI-assisted development evidence, and remain understandable to the owner for the final defense.

## Source of truth

Before doing project work, read:

1. `docs/context/PROJECT.md`
2. `docs/context/CURRENT.md`
3. Only the plan, ADR, requirement, or architecture files relevant to the current task

Do not treat chat history, `ai-context`, UC backend notes, or pre-opening predictions as decisions adopted by this project. They are reference material only.

## Decision discipline

- Important choices start as `proposed`, not `accepted`.
- For design discussions, let the user state an initial idea before giving a complete solution.
- Challenge the idea with constraints, failure cases, scale, and alternatives.
- A decision is accepted only when the user can explain the problem, alternatives, rationale, costs, and validation method.
- Record accepted architecture decisions as ADRs under `docs/decisions/`.
- Accepted ADRs are immutable. A later change creates a new ADR that supersedes the old one.
- Never turn a theoretical benefit into a measured claim.

## Current delivery strategy

- The first vertical slice is the farm owner's single-player loop.
- Friends and multiplayer synchronization come after that loop is correct.
- Prefer a modular monolith until evidence shows a need for distribution.
- The current technical baseline is Go for the backend and a minimal Vue 3 H5 client.
- The final demonstration only needs to run locally.

## Working method

For each task:

1. Restate the goal, relevant context, constraints, and done criteria.
2. Inspect before editing.
3. Plan before implementing when the task is ambiguous or architectural.
4. Keep changes scoped and reversible.
5. Verify behavior with the smallest relevant tests.
6. Report what was verified and what remains an assumption.
7. Update `docs/context/CURRENT.md` only when project state materially changes.
8. Create or update evidence under `docs/evidence/` for measurements and tests used in reports.

## Documentation boundaries

- `docs/context/PROJECT.md`: stable facts and project boundaries.
- `docs/context/CURRENT.md`: mutable handoff, current state, and next actions.
- `docs/architecture/architecture.md`: current effective architecture.
- `docs/decisions/`: significant decisions and tradeoffs.
- `docs/plans/`: execution plans.
- `docs/evidence/`: reproducible tests, measurements, and result summaries.
- `docs/ai-workflow/`: concise AI work records suitable for the defense.

Do not copy full chat transcripts into project documentation. Record only decisions, reasoning changes, evidence, unresolved questions, and links or IDs needed for traceability.

## Security and confidentiality

- Never commit credentials, tokens, cookies, internal service addresses, player data, or company-confidential source material.
- Do not copy UC backend implementation details into this repository unless explicitly approved and safe to share.
- Do not connect company authentication or internal infrastructure without an explicit requirement and authorization.

## Completion standard

A task is complete only when:

- The requested behavior or document exists.
- Relevant validation has run.
- Results and limitations are stated.
- Project context or ADRs are updated when the task changed accepted project state.
- The owner can explain the design in their own words.
