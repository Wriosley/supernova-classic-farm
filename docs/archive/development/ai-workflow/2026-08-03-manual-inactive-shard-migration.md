---
status: completed
date: 2026-08-03
---

# Manual Inactive-Shard Migration

## Goal

Continue after static dual-Zone routing with the smallest safe manual Owner
handoff, while keeping MySQL Fence work separate.

## Human decision

The owner chose manual single-Shard migration before dual-Zone MySQL fencing.

## Safety correction

An in-memory active Actor cannot be reconstructed on the new Zone. The
implementation therefore rejects migration when the old Zone has any active
Actor in that Shard instead of pretending that route movement preserves state.

## Changes

- Parameterized Actor epoch throughout responses and stored runtime records.
- Added per-Shard execution exclusion, drain/resume and ownership refresh.
- Added loopback Coordinator migration orchestration.
- Extended the dual-Zone E2E with stale Gate cache recovery to epoch two.
- Added active-Shard refusal tests.

## Verification

- Targeted routing, Player, Zone, Coordinator, Gateway and E2E packages passed.
- The five-process migration E2E passed.
- Full Go tests, vet, the original single-Zone E2E and the migration-enabled
  dual-Zone E2E passed.

## Remaining uncertainty

- Active Actor migration still requires Dirty flush, Fence CAS and checkpoint
  load.
- PREPARING failure recovery is not yet implemented.
- No performance or availability claim follows from this functional proof.

## Related

- Plan: `../plans/2026-08-03-manual-inactive-shard-migration-plan.md`
- Evidence: `../../../archive/evidence/historical/2026-08-03-manual-inactive-shard-migration.md`
