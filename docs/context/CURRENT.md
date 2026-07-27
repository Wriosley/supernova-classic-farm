---
status: active
updated: 2026-07-27
---

# Current Handoff

## Resume here

The project now has an integrated overall-design draft at `docs/architecture/architecture.md` and a module/interface companion at `docs/architecture/module-design-and-flows.md`.

At the next session:

1. Read `AGENTS.md`, `docs/context/PROJECT.md`, this file, and the overall design.
2. Ask the owner to perform the first-pass review described in section 17 of the overall design.
3. Focus on five topics: module boundaries, data ownership and transactions, crop maturity, idempotency and concurrency, and target-scale evolution. Do not ask the owner to approve every field.
4. Let the owner state an initial view before presenting a complete answer.
5. Accept a decision only after the owner can explain the problem, alternatives, rationale, costs, and validation method.

## Work completed on 2026-07-27

### Overall design integration

- Reviewed the 2026-07-26 Obsidian drafts for requirements, architecture, module relationships, account, farm, inventory, shop, task, cross-module flows, technology options, and risks.
- Confirmed that Obsidian module documents are discussion material, not accepted architecture.
- Integrated their system-level content into `docs/architecture/architecture.md` version 0.1.
- Added explicit labels for confirmed, candidate, unverified, unresolved, and later-stage content.
- Added a guided reading method so the owner can learn the design without reading every draft field first.
- After the owner's first review found later modules too implicit, added uniform summaries for all ten modules.
- Added external capability catalogs, internal module contracts, transaction ownership, and eight cross-module data flows.

### Accepted architecture decision

- The owner initially proposed separate services for future scalability and module testing.
- The discussion tested that idea against purchase, planting, harvest, reward, and mail-attachment transactions.
- The owner concluded that the first version should keep modules in one Go application, get the basic flow working, preserve code/test boundaries, and split only after concurrency or performance evidence justifies it.
- Recorded this as `docs/decisions/ADR-0001-modular-monolith-first.md`.

## Product code state

- No backend or frontend product code exists yet.
- No database schema or API contract has been accepted.
- No runtime, correctness, or performance claim has been verified.
- Tests and evidence do not yet exist.

## Confirmed project choices

- Repository name: `supernova-classic-farm`.
- Backend language: Go.
- Minimal Vue 3 H5 client is the current baseline.
- Demonstration target: local machine.
- Project-owned account data will be used instead of company authentication.
- Single-player behavior comes before friends and multiplayer.
- First-version deployment is a modular monolith: one Go application and one MySQL instance, with explicit module boundaries and tests.

## Proposed, not accepted

- MySQL details and data-access approach.
- HTTP JSON details for the single-player API.
- Server-side Session Token and exact single-device/session-expiry rules.
- Warehouse/asset ownership of coins and items versus a broader economy module.
- Timestamp-based crop maturity using persisted `mature_at`.
- Top-level `request_id` plus business unique keys for idempotency.
- Synchronous task progress in the core business transaction.
- WebSocket snapshots, versions, and room broadcasts for later multiplayer.

## Highest-priority open questions

1. Final data ownership for wallet, items, and shop orchestration.
2. Crop maturity teach-back and ADR acceptance.
3. Single-account multi-device behavior and Session lifetime.
4. Initial coins, plots, seeds, and new-player initialization failure semantics.
5. Whether task progress failure rolls back the core action.
6. Friend access permissions and invitation-link rules.
7. Minimum acceptance rules for pet, collection, and mail.
8. `request_id` scope, retention, and cleanup.

## Next three actions

1. Owner reads the first-pass sections of the overall design and marks only unclear or disagreeable statements.
2. Resolve data ownership and crop maturity, then create the next ADR where appropriate.
3. Use accepted design to write the first single-player vertical-slice implementation plan before creating product code.

## Verification state

- Documentation sources: inspected.
- Overall design: drafted, not yet owner-reviewed as a whole.
- Modular monolith decision: accepted and recorded.
- Product behavior: not implemented.
- Tests: none.
- Performance evidence: none.
