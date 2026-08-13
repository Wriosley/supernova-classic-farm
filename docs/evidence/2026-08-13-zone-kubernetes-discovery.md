# Phase 04 Zone Kubernetes discovery evidence

Date: 2026-08-13

## Verified in code

- Stable UUIDv5 Zone logical identity and per-process UUIDv4 incarnation.
- Zone HMAC role is `zone`; legacy logical caller names remain compatibility-only.
- Zone `/readyz` uses an in-memory startup latch; `/livez` remains process liveness.
- Copy-on-write membership registry, Kubernetes Pod/EndpointSlice source,
  bounded identity/liveness prober and HEALTHY/SUSPECT/DEAD controller.
- Publisher authoritative availability replay and SDK replacement when
  `previous_availability_version=0`.
- `COORDINATOR_MEMBERSHIP_SOURCE=static|kubernetes`, default `static`.
- Candidate `zone-pool` StatefulSet has one replica in the committed manifest;
  it has no route-placement or ownership mutation path.

Focused race verification passed:

```text
go test -race -count=1 ./cmd/coordinator ./internal/coordinator/membership \
  ./internal/coordinator/publisher ./internal/coordinatorclient
go test -race -count=1 ./internal/platform/health ./cmd/zone
```

The final `go test -count=1 ./...` run passed every package except the existing
`internal/player.TestApplyMailRewardAllOrNothingAndReplay`; its fixed historical
checkpoint timestamp now violates timestamp validation on 2026-08-13. Phase 04
packages and the complete Coordinator/Zone commands passed independently.

Manifest render and client dry-run passed against Kubernetes server v1.36.1.
The pinned libraries are `k8s.io/api`, `apimachinery`, and `client-go` v0.36.1.
Namespace-scoped RBAC returned:

```text
get pods: yes
watch endpointslices.discovery.k8s.io: yes
get secrets: no
```

## Live acceptance blocker

The deployment-before route snapshot was saved as
`/tmp/phase04-routes-before.json` with 4096 routes and map version 4117.
The new Coordinator failed closed before starting membership because durable
Current and ShardFence already disagree at shard 69:

```text
ACTIVE Current/Fence mismatch at shard 69
```

The serving old Coordinator reports Current shard 69 as:

```text
owner=zone-b epoch=2 route_version=3 state=ACTIVE map_version=4117
previous_owner=zone-a transition=80aa8194-40a8-4fca-9588-9f038e8e7fbc
```

Its migration inspection reports no open MigrationProgress. Therefore the
Phase 02 fail-closed rule forbids guessing or overwriting the Fence. Because
the old Coordinator does not yet accept HMAC caller role `zone`, `zone-pool-0`
could not finish its Coordinator SDK startup and was not registered.

The live gate was rolled back: the old healthy Coordinator remains the only
serving Coordinator Pod, `zone-a` and `zone-b` remain Ready, and `zone-pool`
was scaled to zero. No membership code wrote ShardRoute, ShardFence or
MigrationProgress. The Coordinator Deployment is paused while pinned to the
surviving old revision; repair shard 69 before resuming/applying the new image.

## Remaining acceptance

After an explicit, evidence-backed shard 69 repair:

1. resume/apply the new Coordinator image and set membership source to
   `kubernetes`;
2. scale `zone-pool` to one;
3. verify candidate identity `d859cea1-ac5b-5524-bffa-4e542301cd95` becomes
   HEALTHY and owns zero Shards;
4. restart the candidate and verify stable logical ID/new incarnation;
5. compare all 4096 Current routes to the saved before snapshot.
