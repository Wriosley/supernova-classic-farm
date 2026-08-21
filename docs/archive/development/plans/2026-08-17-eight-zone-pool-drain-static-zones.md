---
status: live-paused-fix-verified-offline
date: 2026-08-17
---

# Eight-Zone pool and static Zone drain

## Goal

Expand `zone-pool` from four to eight replicas, move every Shard from legacy
`zone-a`/`zone-b` into the eight-member pool, and permit deletion of A/B only
after they own no Current routes and have no open DRAIN task.

## Accepted execution boundary

- Drain intent is deployment-persisted in `COORDINATOR_DRAIN_ZONE_IDS`; a
  Coordinator restart cannot silently return A/B to Desired candidates.
- `COORDINATOR_PLANNER_MIN_HEALTHY_ZONES=8` blocks planning while only a
  partial pool has passed liveness probes.
- DRAINING owners are removed from Desired and produce `DRAIN` priority tasks;
  HEALTHY ownership differences remain ordinary `REBALANCE` tasks.
- Existing Migration Worker limits remain global 8, per source 2, per target 2.
- `GET /internal/v1/zones/drain` is the deletion gate. A Zone is removable only
  when `owner_shards=0` and `open_tasks=0`.
- A/B manifests are not removed by this code change. Cluster deletion is a
  separate operator action after live convergence.

No Tcaplus table or schema change is required. MigrationTask and
MigrationProgress retain their existing responsibilities.

## Remaining live acceptance

The first live attempt exposed and then paused a `MigrationProgress` identity
bug; see the linked evidence. Resume from the current durable state as follows:

1. Build/load the fixed Coordinator image while keeping Planner and Worker
   disabled in the live Deployment.
2. Confirm eight pool Pods Ready and verify the Coordinator restored durable
   `map_version=4127` (or a later explicitly observed version).
3. Enable Planner, then Worker, and observe bounded progress without any new
   `PROGRESS_CONFLICT` before allowing the run to continue.
4. Wait for both A/B statuses to report `removable=true`.
5. Capture 4096-owner distribution and open-task count.
6. Only then remove A/B Deployment/Service manifests and re-verify after a
   Coordinator restart.
