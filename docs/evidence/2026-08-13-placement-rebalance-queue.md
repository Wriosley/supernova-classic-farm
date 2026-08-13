---
status: partial
updated: 2026-08-13
---

# Placement and Rebalance Queue Evidence

## Scope

This evidence covers the Phase 05 offline implementation gate. The live
Tcaplus/kind gate remains pending creation of the `MigrationTask` PB Generic
table. Planner configuration defaults to disabled, so loading the new image
does not change Current ownership.

## Deterministic placement

The frozen eight-candidate vector over 4096 Shards produced:

```text
zone-0 479
zone-1 520
zone-2 517
zone-3 556
zone-4 517
zone-5 515
zone-6 505
zone-7 487
```

Adding `zone-8` changed exactly 442 Shards. Every changed Shard moved from its
old owner to `zone-8`; no old owner changed directly to another old owner.
Candidate ordering, exact duplicate input and repeated process-local
calculation produced identical Desired entries.

## Schema compatibility correction

The first console upload rejected `planned_from_availability_version` because
it contains 33 characters and Tcaplus limits field names to 31. Field 13 was
renamed to `planned_availability_version` (28 characters) before table
creation. A protobuf-descriptor regression test now rejects any
`MigrationTask` field longer than 31 characters.

## Automated verification

Passed:

```text
GOCACHE=/tmp/classic-farm-go-cache go test -race -count=1 \
  ./internal/coordinator/placement \
  ./internal/coordinator/migration \
  ./internal/coordinator/membership \
  ./cmd/coordinator

go test ./internal/testtcaplus ./internal/routing

kubectl kustomize deploy/k8s >/tmp/classic-farm-phase05-rendered.yaml
kubectl apply --dry-run=client -f /tmp/classic-farm-phase05-rendered.yaml
```

The queue tests cover exact replay deduplication, immutable task IDs, restart
load, priority replacement, RUNNING conflict, task-ID checked cancellation,
deterministic ordering and bounded stale-CAS retry. Planner tests cover no-op,
8-to-9 differences, stale Current abort, unhealthy Current-owner exclusion,
stale PLANNED cancellation and coalesced membership triggers.

`go test ./...` was also run. All Coordinator and routing packages passed, but
the unrelated existing `internal/player` test
`TestApplyMailRewardAllOrNothingAndReplay` failed reproducibly because it fixes
the runtime clock at 2026-08-12 while `NewDevelopmentState` now creates a
checkpoint on 2026-08-13, producing an invalid backwards timestamp. Phase 05
does not modify Player code.

## Build

The final implementation-loop image was built once and loaded into kind:

```text
classic-farm/coordinator:dev
sha256:c3878a9ca1207a50ac176cdddda2b0f2d27df4a9e6a696382597cf9eb9eb74a5
```

## Remaining live gate

After the owner creates `MigrationTask` from the corrected schema:

1. scale `zone-pool` to three candidates;
2. enable the planner explicitly;
3. record the five-Zone Desired/task distribution;
4. restart Coordinator and prove task IDs deduplicate;
5. compare all 4096 durable Current rows, ShardFence rows and SDK caches before
   and after planning.

No live claim is made yet.
