---
status: completed
date: 2026-08-03
---

# Dual-Zone Routing and ShardMap

## Goal and boundary

Implement the first two steps from the reviewed routing study:

1. static dual-Zone placement and ownership rejection;
2. Gate RouteCache with stale-route invalidation and one same-request retry.

Owner migration, multi-Zone database fencing and production consensus stayed
out of scope.

## Human decisions

- Close the completed first-stage single-player plan.
- Start the multi-Zone routing work with two local Zone processes.
- Use the conclusions under
  `docs/archive/development/study/tasks/17-双Zone路由与ShardMap调研/`.
- Implement both phase A and phase B rather than stopping after direct routing.
- Copy the approved implementation plan into the project `docs/` repository.

## AI-assisted changes

- Replaced the earlier contiguous half-range proposal with versioned
  Rendezvous candidate placement materialized by Coordinator.
- Added complete route Snapshot publication and lookup diagnostics.
- Added an immutable Gate cache, miss coalescing and conditional invalidation.
- Added trusted ownership forwarding fields and Zone-side authorization.
- Parameterized Zone identity and HTTP address.
- Added an explicit memory-only dual-Zone launcher and E2E.
- Added the project plan and evidence record.

## Verification

- Targeted Go package tests passed.
- The five-process E2E passed with one player routed to each Zone.
- Coordinator recorded zero single-Shard lookups during ordinary commands.
- A direct request to the wrong Zone returned `NOT_OWNER`.
- Cache retry tests retained the same `request_id`.
- The full Go test and vet passes completed, and both the original single-Zone
  E2E and new dual-Zone E2E passed.

## Remaining uncertainty

- Owner handoff, Lease acquisition and MySQL Fence CAS remain future work.
- No throughput or availability claim follows from this functional E2E.

## Related records

- Plan: `../plans/2026-08-03-static-dual-zone-routing-plan.md`
- Evidence: `../../../archive/evidence/historical/2026-08-03-dual-zone-routing.md`
- Current handoff: `../../../context/CURRENT.md`
