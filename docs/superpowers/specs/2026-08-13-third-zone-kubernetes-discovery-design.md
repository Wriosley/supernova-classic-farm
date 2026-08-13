# Third Zone Kubernetes Discovery Design

## Status

Proposed for owner review. This design narrows Phase 04 to proving one newly
created candidate Zone can be discovered and registered without receiving any
Shard ownership.

## Goal

Keep `zone-a` and `zone-b` as the only Current owners, create
`zone-pool-0` as the third running Zone process, and make Coordinator discover,
authenticate, probe and publish it as HEALTHY. The 4096 durable routes, Fence
rows and migration progress must remain unchanged.

## Identity

Four concerns remain separate:

| Concern | Third Zone value | Lifetime |
|---|---|---|
| Pod/display name | `zone-pool-0` | current Kubernetes workload |
| logical Zone ID | deterministic UUIDv5 | stable for this pool ordinal |
| incarnation ID | random UUIDv4 | one process start |
| HMAC caller | `zone` | bounded service role |

The UUIDv5 name is exactly:

```text
classic-farm/zone/<cluster_id>/<namespace>/<statefulset_name>/<ordinal>
```

The implementation uses the RFC 9562 DNS namespace UUID
`6ba7b810-9dad-11d1-80b4-00c04fd430c8`. For the first candidate the name is:

```text
classic-farm/zone/classic-farm-local/classic-farm/zone-pool/0
```

`zone-a` and `zone-b` retain explicit legacy logical IDs while compatibility
ownership exists. Pod IP, Pod UID and endpoint never become logical identity.

## Components

### Zone identity and health

Zone creates one immutable process Identity during startup and exposes:

```text
GET /internal/v1/zone-identity
```

The response contains `logical_zone_id`, `incarnation_id` and the normalized
advertised endpoint. It contains no credentials or player data. `/livez`
checks only process/runtime liveness. `/readyz` reads an in-memory latch set
after configuration, stores and Coordinator SDK are ready; probes never query
Tcaplus repeatedly.

Zone-originated RPCs authenticate as role `zone`. Ownership-bearing request,
checkpoint, Fence and route fields continue carrying the logical Zone ID.
Legacy auth caller names remain accepted behind a compatibility switch until
the two existing owners have moved to role auth.

### Membership registry

Coordinator owns a transient copy-on-write registry keyed by logical Zone ID.
Each member records incarnation, endpoint, Pod identity/resourceVersion,
failure count and HEALTHY/SUSPECT/DEAD/DRAINING state. Registry changes advance
an independent `availability_version`; they never call RouteStore, Fence or
MigrationProgress.

One logical ID cannot have two simultaneously valid Pods. A newer verified
incarnation replaces the previous process. Stale Kubernetes events or probe
results cannot overwrite newer observations.

### Kubernetes discovery

Coordinator uses namespace-scoped client-go informers for Pods and
EndpointSlices selected by `zone-discovery`. Endpoint observations must point
to a matching Pod UID, expected labels and the named HTTP port. Initial cache
sync completes before Kubernetes membership mode is considered ready.

RBAC grants the Coordinator ServiceAccount only `get/list/watch` on Pods and
EndpointSlices in `classic-farm`. It grants no Secret, Node or cluster-wide
access.

### Identity and liveness probing

An EndpointSlice change schedules an immediate bounded probe. Periodic probes
run every 10 seconds with a 2-second timeout and a fixed worker pool.

```text
discover endpoint
-> derive expected UUIDv5 from StatefulSet topology
-> GET /internal/v1/zone-identity
-> verify logical ID, endpoint and incarnation
-> GET /livez
-> update membership
```

Success produces HEALTHY and resets failures. The first two failures produce
SUSPECT; the third produces DEAD. Terminal/deleted Pods become DEAD
immediately. Recovery before ownership changes returns to HEALTHY without
changing route epoch or version. Player request storage failures never enter
this counter.

### Availability publication

Publisher retains the latest complete AvailabilityBatch separately from route
history. A new subscriber receives an authoritative complete batch with
`previous_availability_version=0`. SDK treats that form as atomic replacement,
including after Coordinator restart resets transient versioning. Incremental
batches require contiguous availability versions and otherwise force resync.

SDK route resolution fails closed only when the Current owner has a known
non-HEALTHY availability. The candidate UUID is published but affects no
player route because no ShardRoute names it as owner.

## Kubernetes topology

Existing `zone-a` and `zone-b` Deployments and Services remain. Add:

```text
zone-pool StatefulSet replicas=1
zone-headless headless Service
zone-discovery selector Service
Coordinator ServiceAccount + namespace Role/RoleBinding
```

The StatefulSet manifest is designed to scale later, but this acceptance run
creates only `zone-pool-0`. Downward API supplies Pod name and namespace; the
advertised endpoint is its stable headless-Service DNS. The candidate starts
the same Zone binary, loads the Current authorization snapshot, owns zero
routes and must reject attempts to serve unowned Shards.

## Configuration and rollback

Coordinator provides `COORDINATOR_MEMBERSHIP_SOURCE=static|kubernetes` and
defaults to static until kind verification passes. Kubernetes mode also uses:

```text
CLUSTER_ID=classic-farm-local
POD_NAMESPACE=classic-farm
ZONE_DISCOVERY_SERVICE=zone-discovery
ZONE_LIVE_PROBE_INTERVAL=10s
ZONE_LIVE_PROBE_TIMEOUT=2s
ZONE_LIVE_FAILURE_THRESHOLD=3
ZONE_PROBE_WORKERS=8
```

Rollback sets membership source to `static` and scales `zone-pool` to zero.
It does not delete or recompute durable routes.

## Verification

Unit and race tests cover UUID golden vectors, restart identity semantics,
readiness transitions, registry ordering/concurrency, fake-client informer
events, bounded probes, failure/recovery transitions, authoritative
availability replacement and zero RouteStore mutations.

The kind acceptance gate captures route snapshots before and after and checks
all 4096 owner, endpoint, epoch, route version and map version fields are
identical. It then verifies:

1. `zone-pool-0` becomes Ready and HEALTHY;
2. membership diagnostics show `zone-a`, `zone-b` and the candidate;
3. Gate, Info and all Zone SDK sessions receive availability;
4. the candidate owns zero Shards;
5. restarting `zone-pool-0` preserves logical ID and changes incarnation;
6. Coordinator rediscovers the new process as HEALTHY;
7. existing dual-Zone owner/player regression remains green.

## Explicit exclusions

This design does not calculate Desired placement, enqueue migration, transfer
Fence, update Current, fail over a dead owner, elect a Coordinator leader or
prewarm Actors. Those remain later Phase 05+ work.
