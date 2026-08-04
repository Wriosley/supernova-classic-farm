---
status: completed
date: 2026-08-03
owner: project-owner
related:
  - 2026-08-03-active-shard-mysql-migration-plan.md
  - ../contracts/data-model.md
  - ../decisions/ADR-0008-v3-quorum-shard-coordinator.md
---

# Coordinator PREPARING Recovery Plan

## Goal

Persist MySQL migration progress and recover Coordinator restart while a Shard
is in `PREPARING`, without claiming a full durable ShardMap table.

## Decisions

- Persist only open/abandoned migration progress rows.
- Rebuild routable `ACTIVE` routes from `shard_fences` + Zone endpoints.
- Overlay open `PREPARING` from progress and stay fail-closed.
- Expose loopback inspect / continue / abandon controls.
- Refuse abandon after Fence advance; restore source `ACTIVE` before it and
  burn the abandoned prepared epoch.

## Implementation

1. Add `deploy/migrations/000005_shard_migration_progress.up.sql`.
2. Persist step boundaries during MySQL `move`.
3. Bootstrap virgin fences or hydrate advanced fences on startup.
4. Load open progress, restore PREPARING, do not auto-continue.
5. Continue resumes the same `transition_id`; abandon restores source routing.

## Verification

- Unit/component coverage for progress store, hydrate/restore, continue and
  abandon.
- Live dual-Zone MySQL E2E migrates a Shard, restarts Coordinator, and proves
  Fence hydration plus empty open-migration inspection.

## Non-goals

- Full ShardMap snapshot table or consensus log.
- Automatic continue on startup.
- Abandon after Fence advance.
- WS Ticket persistence and performance measurement.
