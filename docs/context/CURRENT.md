---
status: active
updated: 2026-07-24
---

# Current Handoff

## Resume here

The project is paused immediately before the owner's first independent design answer for the crop growth and maturity model.

At the next session:

1. Read `AGENTS.md`, `docs/context/PROJECT.md`, and this file.
2. Ask the owner to explain their initial crop-maturity design before presenting solutions.
3. Challenge the design with offline growth, process restart, large data volume, correctness, and validation.
4. Let the owner revise and summarize the decision.
5. Only then draft the first project ADR.

Suggested opening prompt:

```text
Continue the crop maturity design discussion. Do not give me the complete
solution first. Ask me to propose how a crop grows while the player is offline
and the server may restart, then challenge my assumptions.
```

## Today's completed work

### Project and delivery boundaries

- Confirmed that the real topic is the Classic Farm H5 game.
- UC backend, xRPC, Actor, Proxyless, and pre-opening notes are reference material only.
- Confirmed solo development, Go backend, beginner H5 experience, and local-machine demonstration.
- Confirmed final defense on 2026-08-21 and material freeze by 2026-08-18.
- Confirmed the 30-million-DAU target is a design and validation concern.
- Confirmed that one farm will later target at most three simultaneous users.

### Delivery sequence

- Phase 1 is the farm owner's single-player vertical slice.
- Friendship comes after the single-player loop works.
- Realtime multiplayer comes after friendship and will not block the first slice.

Planned single-player loop:

```text
register/login
→ enter own farm
→ buy seeds
→ plant
→ grow while offline
→ harvest
→ store/sell
→ claim a basic task reward
```

### Documentation and AI workflow

- Established the project documentation and cross-AI handoff skeleton.
- Added `AGENTS.md` as the canonical shared instruction file for Codex and CodeBuddy.
- Added `CLAUDE.md` to import `AGENTS.md` for Claude.
- Added project context, current handoff, architecture, ADR, evidence, plans, and AI-workflow directories.
- Established the rule that the owner proposes first, AI challenges second, and an ADR is accepted only after the owner can explain it.
- Established that full chat transcripts are not project memory.

### Git

- Repository: `Wriosley/supernova-classic-farm`
- Branch: `main`
- Remote tracking was clean before this progress update.

## Product code state

- No backend or frontend product code exists yet.
- No database schema has been accepted.
- No runtime or performance claim has been verified.

## Confirmed operational choices

- Repository name: `supernova-classic-farm`.
- Backend language: Go.
- Minimal Vue 3 H5 client is the current working baseline.
- Demonstration target: local machine.
- Account login will use the project's own data store rather than company authentication.
- Single-player behavior will be implemented before friends and multiplayer.

These are working project choices. Significant technical choices still require ADR reasoning before they are treated as accepted architecture.

## Proposed, not accepted

- Modular monolith.
- MySQL as the first persistent store.
- HTTP for the single-player API.
- WebSocket for the later multiplayer phase.
- Server-side session tokens for local account login.
- Timestamp-based crop maturity.
- Per-farm serialized command processing for later multiplayer.

## Active design discussion

### Crop growth and maturity

The owner has not yet written or stated an initial solution.

Problem constraints:

- Crops must grow while the player is offline.
- Process restart must not lose the correct state.
- The target design must account for a large number of growing crops.
- The local demonstration needs short, observable growth times.

No crop-maturity solution has been accepted.

## Open questions

- Crop maturity mechanism.
- Exact single-player data model and transactional invariants.
- Go HTTP library and data-access approach.
- Account session mechanism.
- Minimal Vue page structure.
- Exact acceptance depth for pets, collection, and mail.
- Whether the private Obsidian project notes are synchronized to the home machine.
- Whether company policy permits `ai-context` internal learning material on personal GitHub or home devices.

## Next three actions

1. Complete the owner's initial crop-maturity reasoning.
2. Challenge, revise, and create the first accepted ADR.
3. Discuss the single-player data model and atomic business operations.

## Verification state

- Documentation structure: created and reviewed.
- Git repository: initialized and connected to a remote.
- Product behavior: not implemented.
- Tests: none.
- Performance evidence: none.
