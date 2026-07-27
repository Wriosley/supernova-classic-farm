---
status: active
updated: 2026-07-27
---

# Current Handoff

## Resume here

The overall design is in `docs/architecture/architecture.md`; module capabilities and cross-module flows are in `docs/architecture/module-design-and-flows.md`. The owner's inline review and the resulting resolutions are preserved in `docs/ai-workflow/2026-07-27-module-rules-review.md`.

At the next session:

1. Read `AGENTS.md`, `docs/context/PROJECT.md`, this file, and the two architecture documents.
2. Do not reopen decisions listed as confirmed unless new evidence appears.
3. Let the owner explain the chosen rule before adding implementation detail, so the design remains learnable and defensible.
4. Resolve the remaining small product constants, then write the first single-player vertical-slice implementation plan.
5. Do not start pet, collection, or mail implementation in the current phase.

## Work completed on 2026-07-27

### Overall design integration

- Reviewed the 2026-07-26 Obsidian drafts for requirements, architecture, module relationships, account, farm, inventory, shop, task, cross-module flows, technology options, and risks.
- Integrated system-level content into the overall design and added uniform summaries, capability catalogs, internal contracts, transaction ownership, and cross-module data flows.
- Kept Obsidian drafts as discussion material; only owner-confirmed conclusions enter the formal architecture.

### Accepted architecture decision

- The first version is a modular monolith: one Go application and one MySQL instance.
- Modules keep explicit ownership and independent tests, and split only after concurrency or performance evidence justifies it.
- Recorded as `docs/decisions/ADR-0001-modular-monolith-first.md`.

### Module-rules review

- Answered the owner's Session and registration-failure questions.
- Converted the owner's inline notes into confirmed design rules and preserved the original feedback in the AI workflow record.
- Removed the obsolete manual task-claim flow.
- Added the friend-steal flow and made Farm, rather than Realtime, the transaction owner.
- Kept three-client access as an acceptance baseline without introducing a product hard room limit.
- Marked pet, collection, and mail as deferred current-stage scope.

## Product code state

- No backend or frontend product code exists yet.
- No database schema or exact HTTP DTO has been accepted.
- No runtime, correctness, or performance claim has been verified.
- Tests and evidence do not yet exist.

## Confirmed project choices

- Repository name: `supernova-classic-farm`.
- Backend language: Go; minimal Vue 3 H5 client; local-machine demonstration.
- Project-owned account data instead of company authentication.
- First-version deployment: one Go application and one MySQL modular monolith.
- Asset module owns coins and items. Initial coins: 10. Inventory: 100 occupied item types, 300 units per type.
- Single-device login. Session Token has a fixed one-hour lifetime; a new login revokes the old Session.
- Registration creates account, player, wallet, farm, and initial plots in one transaction; any failure rolls everything back.
- Shop phase one buys seeds and sells mature crops, using the current server price at submission.
- Owner harvest clears the plot and preserves planting/harvest history.
- Tasks auto-complete and auto-grant coin rewards with a visible reward record in the triggering transaction; no manual claim.
- Invitation links are multi-use by different players for 30 minutes.
- A mature crop cycle can be stolen once. Demo yield 10 gives 3 to the friend and leaves 7 for the owner.
- Steal and owner harvest lock/recheck the same plot; Realtime only broadcasts committed state.
- No product hard room limit; three simultaneous clients are the initial acceptance baseline.
- Pet, collection, and mail remain final-scope records but are not developed in the current phase.

## High-priority design candidates

- Vue 3 H5, HTTP JSON, and MySQL details.
- Persisted `mature_at` with maturity derived from server time.
- Top-level `request_id` plus business unique keys for idempotency.
- MySQL transactions, conditional updates, row locks, and unique constraints for first-version concurrency correctness.

## Remaining questions before implementation

1. Initial plot count and whether registration grants seeds.
2. The first task list, target values, and coin reward amounts.
3. Rounding for 30% steal quantity when yield is not 10.
4. Whether deleting friends enters the first version.
5. `request_id` scope, retention, and cleanup.
6. Local visible-latency target and when deferred modules re-enter planning.

## Next three actions

1. Owner reads the confirmed rules in sections 3 and 6 of the module document and explains the purchase, harvest, automatic-task, and steal transaction boundaries in their own words.
2. Resolve initial plots/seeds, the first task list, and steal rounding; record an ADR for the crop-maturity mechanism if accepted.
3. Write the first single-player vertical-slice implementation plan before creating product code.

## Verification state

- Documentation sources and owner feedback: inspected.
- Architecture documents: updated to reflect confirmed rules; still design, not implementation evidence.
- Modular-monolith decision: accepted and recorded.
- Product behavior: not implemented.
- Tests and performance evidence: none.
