---
status: completed
date: 2026-08-03
owner: project-owner
---

# Static Dual-Zone Routing and RouteCache Plan

## Goal

Extend the single-node V3 prototype from one local Zone to two real Zone
processes and prove:

```text
player_id
-> stable logical shard
-> committed ShardMap
-> Gate RouteCache
-> the authorized Zone process
-> Player Actor
```

The result must keep Coordinator out of the ordinary command path and recover
from one explicit `NOT_OWNER` response without changing `request_id`.

## Scope

- Preserve 4096 stable logical player Shards.
- Add an explicit, memory-only `static-dual-zone` launch mode.
- Run `zone-a` on port 8082 and `zone-b` on port 8084.
- Use versioned Rendezvous Hashing to propose initial equal-capacity placement.
- Materialize all proposals into the single-node Coordinator's committed
  `ACTIVE` ShardMap; Gate and Zone never treat local hashing as authority.
- Publish a complete versioned route Snapshot and retain single-Shard Resolve.
- Warm an immutable Gate RouteCache before serving.
- Refresh an expired/missing route with per-Shard request coalescing.
- Conditionally invalidate a failed cached version on `NOT_OWNER` and retry
  once with the original Protobuf body and `request_id`.
- Give each Zone an atomically replaced read-only ownership Snapshot and reject
  wrong Shard, Zone or epoch before Actor activation.
- Keep the existing single-Zone mode compatible.

## Placement strategy

`player_id -> shard_id` keeps the accepted FNV-1a V1 contract.

For `shard_id -> candidate Zone`, assignment algorithm V1 hashes:

```text
farm-shard-assignment-v1
+ shard_id encoded as big-endian uint32
+ stable zone_id
```

with SHA-256 and chooses the highest 64-bit score. Endpoint does not
participate, so changing a physical address does not remap Shards.

Rendezvous output is only a placement proposal. The committed Route remains
the authorization:

```text
shard_id
+ owner_zone_id
+ owner_endpoint
+ owner_epoch
+ route_version
+ state
+ lease_expires_at
+ map_version
```

Equal-capacity Rendezvous placement is statistically balanced; this plan does
not promise exactly 2048 Shards per Zone. Future capacity weights, Actor load,
CPU, Dirty backlog and hotspot correction must produce explicit migration
plans rather than changing Gate-side hashing.

## Implementation

1. Extend `server/internal/routing/` with candidate validation, deterministic
   Rendezvous placement, full Snapshot HTTP publication, lookup counters and a
   Zone authorization table.
2. Configure `server/cmd/coordinator/` for local or static dual-Zone maps and
   renew every configured Owner's routes.
3. Extend `server/internal/gateway/` with route metadata, Snapshot loading,
   immutable cache publication, per-Shard miss collapse and conditional
   invalidation.
4. Forward trusted Shard, Zone, epoch and route-version headers from Gate.
5. Parameterize `server/cmd/zone/` identity/address, load Coordinator ownership
   Snapshots and reject unauthorized commands before Runtime handling.
6. Add `-DualZone` to `start-servers.ps1` and a dedicated five-process E2E.

## Verification

- Rendezvous result is independent of candidate input order.
- Adding a Zone only moves Shards won by the new Zone; removing a Zone only
  moves Shards owned by it.
- Snapshot is complete, ordered and version-compatible.
- Cache hits cause no single-Shard Coordinator lookup.
- Concurrent misses collapse into one Resolve.
- `NOT_OWNER` invalidates only the failed version and retries once with the
  same request.
- Two players mapped to different Zones both authenticate and load through one
  Gate.
- A command sent directly to the wrong Zone returns HTTP 409 `NOT_OWNER`.
- A write in Zone A does not mutate a player in Zone B.
- Existing single-Zone tests and E2E remain valid.

## Non-goals

- Majority/Raft Coordinator.
- Dynamic load-aware rebalance.
- Automatic failure detection or Owner migration.
- `PREPARING -> Drain -> Fence CAS -> ACTIVE` handoff.
- Multi-owner MySQL `shard_fences`.
- Hot-state replication or seamless migration.
- Production scale or availability claims.

## Stop conditions

- Dual-Zone mode must refuse `MYSQL_DSN` until database Fence ownership is
  aligned with the committed dual-Zone map.
- An invalid or expired ownership Snapshot must fail closed.
- Failure to load the initial Gate or Zone Snapshot must prevent readiness
  rather than silently route from a local guess.
