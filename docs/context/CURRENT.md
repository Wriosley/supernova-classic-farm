---
status: active
updated: 2026-07-24
---

# Current Handoff

## Current milestone

Establish the project documentation and AI handoff skeleton, then design the farm owner's single-player core loop.

## Implemented

- No product code yet.
- Documentation skeleton created.

## Confirmed decisions

- Repository name: `supernova-classic-farm`.
- Go backend.
- Minimal Vue 3 H5 client baseline.
- Local-machine demonstration.
- Single-player farm-owner loop before friends and multiplayer.

These decisions should receive ADRs when their alternatives, rationale, and consequences have been discussed and understood.

## Active design discussion

- Crop growth and maturity model.

The user must first record an initial idea in the Obsidian design discussion note before an AI provides a complete solution.

## Proposed, not accepted

- Modular monolith.
- MySQL as the first persistent store.
- HTTP for the single-player API.
- WebSocket for the later multiplayer phase.
- Server-side session tokens for local account login.

## Open questions

- Crop maturity mechanism.
- Exact single-player data model.
- Go HTTP and data-access library choices.
- Minimal Vue page structure.
- Depth required for pets, collection, and mail.

## Next three actions

1. Complete the crop maturity design discussion.
2. Create the first accepted ADR from the user's own reasoning.
3. Discuss the single-player data model and transactional invariants.

## Verification state

No code or performance claims have been verified.
