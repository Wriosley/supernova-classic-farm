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

## Live acceptance investigation

The deployment-before route snapshot was saved as
`/tmp/phase04-routes-before.json` with 4096 routes and map version 4117.
The first new Coordinator build failed closed before starting membership with:

```text
ACTIVE Current/Fence mismatch at shard 69
```

The serving old Coordinator reports Current shard 69 as:

```text
owner=zone-b epoch=2 route_version=3 state=ACTIVE map_version=4117
previous_owner=zone-a transition=80aa8194-40a8-4fca-9588-9f038e8e7fbc
```

Direct read-only Tcaplus inspection showed this was a validator defect rather
than corrupt data. Route and Fence have the same owner `zone-b`, epoch `2` and
transition ID. Fence retains the committed PREPARING `route_version=2`, while
ACTIVE Current correctly advanced to `route_version=3`. Phase 02 specifies
that ACTIVE cross-checks owner/epoch; activation intentionally increments the
route version after Fence advance. `validateCurrentFences` incorrectly also
required route-version equality. No Tcaplus repair is required.

The first failed gate was rolled back without writing ShardRoute, ShardFence or
MigrationProgress. After the validator correction the Deployment was resumed
with the new Coordinator image and the candidate was scaled back to one for
the successful acceptance below.

## Completed live acceptance

After correcting ACTIVE validation, Pod/EndpointSlice resource-version domain
handling, and discovery endpoint construction, the kind gate passed:

- Coordinator and `zone-pool-0` are both Ready;
- candidate logical identity is
  `d859cea1-ac5b-5524-bffa-4e542301cd95`;
- candidate restart preserved that logical ID and generated a new incarnation;
- availability version advanced from `1` to `3` across replacement (old
  incarnation DEAD, new incarnation HEALTHY);
- five SDK subscribers remained connected, with zero queue overflow/resync;
- all 4096 durable route fields matched the pre-deployment snapshot after
  excluding only runtime overlay `lease_expires_at_ms`/`routable`;
- map version remained `4117` and candidate-owned Shards remained `0`.

The discovery source now probes stable per-Pod headless DNS. EndpointSlice IP
is used to correlate the Pod target but never becomes the advertised Zone
identity. Stale informer/probe observations are ignored rather than stopping
the membership controller; unexpected controller exits are logged.
