# AI Workflow Records

This directory preserves concise, reviewable evidence of AI-assisted development for the final defense.

These files are a chronological collaboration log. Older records may describe V1 or V2 because they preserve what the project believed and changed at that time. They are not instructions to resume an obsolete architecture.

Do not copy full transcripts. For each meaningful task, record:

- Goal and boundaries.
- AI/tool used.
- Files and evidence provided as context.
- Initial proposal or plan.
- Human corrections and decisions.
- What the AI changed.
- How another AI or test independently reviewed it.
- Verification result.
- Remaining uncertainty.
- Related commit, ADR, or evidence.

Use `session-template.md`.

AI workflow records are traceability material, not the source of formal product, architecture, interface, or performance truth.

Before starting or handing off work, an AI must first read `../context/CURRENT.md`. If an older workflow record conflicts with it, preserve the old record and follow `CURRENT.md`; do not rewrite history.

The latest architecture handoff is `2026-07-30-v3-context-and-ai-handoff.md`.

The 2026-07-31 MT progress-sync speaking outline is `2026-07-31-mt-progress-sync.md`. It is a draft communication aid and must be updated from actual evidence before use.

The latest runtime and owner-handoff record is `2026-07-31-first-runtime-slice-and-owner-handoff.md`.

The MySQL registration/checkpoint implementation record is `2026-07-31-mysql-registration-checkpoint-slice.md`.

The minimum Zone configuration and shop-read record is `2026-07-31-zone-config-and-get-shop.md`.

The PLANT and Dirty restart-recovery record is `2026-07-31-plant-dirty-recovery.md`.

The fixed-point growth and maturity record is `2026-07-31-growth-and-maturity.md`.

The fertilizer interval and restart-recovery record is `2026-07-31-fertilizer-effect-recovery.md`.

The online maturity Push, Gate snapshot buffering and H5 gap-recovery record is `2026-07-31-maturity-push-and-gap-recovery.md`.

The all-or-nothing HARVEST and checkpoint record is `2026-07-31-harvest-command.md`.

The SELL_CROP, sell-all idempotency and chapter-completion record is `2026-07-31-sell-crop-command.md`.

The chapter reward, full-warehouse pending mail and atomic Outbox-write record is `2026-07-31-claim-chapter-reward.md`.

The CLEAN_PLOT and server-side owner-loop completion record is `2026-07-31-clean-plot.md`.

The responsive H5 farm interface and browser-completed owner-loop record is `2026-07-31-h5-farm-loop.md`.

The four authoritative plots, tool-first H5 controls and buy/sell quantity record is `2026-08-03-four-plot-tools.md`.

The static dual-Zone Rendezvous placement, Gate RouteCache and ownership
rejection record is `2026-08-03-dual-zone-routing.md`.

The manual inactive-Shard drain, epoch increment and stale Gate-cache recovery
record is `2026-08-03-manual-inactive-shard-migration.md`.

The staged and verified static dual-Zone MySQL Fence-alignment record is
`2026-08-03-static-dual-zone-mysql-fence.md`.

The verified active-Actor final-flush, Fence-CAS and target-preparation record
is `2026-08-03-active-shard-mysql-migration.md`.

The persistent migration-progress and Coordinator PREPARING recovery record is
`2026-08-03-coordinator-preparing-recovery.md`.

The short-lived WS Ticket/CSRF restart-loss boundary record is
`2026-08-03-ws-ticket-restart-boundary.md`.

The loopback local test platform catalog/runner record is
`2026-08-03-local-test-platform.md`.
