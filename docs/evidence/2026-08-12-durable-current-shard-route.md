---
status: verified-offline
date: 2026-08-12
scope: final-delivery-sprint-07-phase-02
---

# Durable Current ShardRoute

## Result

Phase 07/02 adds an opt-in `COORDINATOR_ROUTE_STORE=tcaplus` path in which
`ShardMapMeta` and 4096 `ShardRoute` rows are restart authority for Current.
The Kubernetes default remains `legacy-fence`; live mode was not enabled.

`ShardMapMeta.pending_*` was expanded during implementation to contain the
complete target route. This was necessary because a crash after Meta pending
CAS but before Route CAS cannot be recovered from shard/version/transition
alone. RouteStore recovery remains independent of `MigrationProgress`:
pending Meta recovers the cross-row route commit, while MigrationProgress
recovers the business migration stage.

## Verified offline behavior

- Empty fake Tcaplus bootstrap creates one metadata row and exactly 4096
  ordered route rows; repeat and concurrent bootstrap load the winner without
  overwriting Current.
- Stored Current survives restart and changed/reordered static bootstrap
  candidates with the same owner, endpoint, epoch, route version and map
  version.
- PREPARING and ACTIVE commits consume distinct global map versions through
  the metadata CAS serialization point.
- Stale/unrelated commits conflict; incomplete tables, incompatible algorithms,
  incomplete pending intent and unexplained route/meta mismatches fail closed.
- Injected crash after pending Meta and before Route write recovers the exact
  target Route, then finalizes Meta.
- Injected crash after Route write and before Meta finalize skips duplicate
  Route write and finalizes Meta.
- Exact retry after finalize is idempotent.
- Durable migration applies PREPARING and ACTIVE to the in-memory Map only
  after the corresponding RouteStore commit. Store failure leaves memory at
  the last durable state.
- Injected Fence failure leaves durable/in-memory PREPARING; Target prepare
  failure remains at `FENCE_ADVANCED`; post-ACTIVE target refresh and Progress
  cleanup failures retain the target as durable/in-memory authority.
- Current/Fence/Progress reconciliation rejects unexplained mismatches.

## Runtime lease compatibility

Durable mode does not call legacy `Map.RenewOwnedLeases`. It keeps a separate
runtime expiry overlay bound to shard ID, owner ID, owner epoch, route version,
lease ID and lease term. Renewal changes expiry only: no durable RouteEntry,
identity, route version, map version or RouteStore write changes.

HTTP snapshot/single-Shard responses use effective overlay expiry only when
the binding matches Current and is unexpired. Zone AuthorizationTable accepts
same-map-version refresh only when all durable route fields are identical and
expiry only stays equal or moves forward. An offline compatibility test
verified Gate/Zone-style HTTP refresh remains authorized 45 seconds after a
stored 30-second expiry.

## Commands and observed results

```text
$ cd deploy/tcaplus
$ buf generate --template buf.gen.yaml
(exit 0)

$ cd ../../server
$ go test ./internal/coordinator/routestore ./internal/routing ./cmd/coordinator
all packages passed

$ go test -count=1 ./...
all packages passed; exit 0

$ go test -race -count=1 ./internal/coordinator/routestore ./internal/routing ./cmd/coordinator
all three packages passed; exit 0

$ kubectl kustomize deploy/k8s > /tmp/classic-farm-rendered.yaml
(exit 0)

$ kubectl apply --dry-run=client -f /tmp/classic-farm-rendered.yaml
namespace/configmap/services/deployments configured (dry run); exit 0
```

## Limitations

- Live Tcaplus validation did not run because creation/availability of the new
  `ShardMapMeta` and `ShardRoute` tables and credentials was not confirmed in
  this task. No live row count or live process restart is claimed.
- `COORDINATOR_ROUTE_STORE` therefore remains `legacy-fence` in kind.
- Static zone-a/zone-b membership remains the empty-store bootstrap source.
- HTTP polling remains the compatibility transport. Watch/SDK publication,
  Kubernetes membership, rebalance, failover and Leader Election remain
  unimplemented.
- Runtime lease renewal assumes the configured static Zones are alive; it is
  not health evidence. Phase 03/04 replace this transitional rule.
- No production capacity or concurrency claim is made.
- `buf lint` for the complete `deploy/tcaplus` module is currently blocked by
  pre-existing `MailClaimSagaStatus` enum-value prefix findings in
  `mail_tables.proto`. The phase-required `buf generate --template
  buf.gen.yaml` succeeds, generated code compiles, and the full Go suite passes;
  this task did not rename the frozen mail enum contract.
