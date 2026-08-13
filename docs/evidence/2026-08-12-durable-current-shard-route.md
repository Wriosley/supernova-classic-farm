---
status: verified-live
date: 2026-08-13
scope: final-delivery-sprint-07-phase-02
---

# Durable Current ShardRoute

## Result

Phase 07/02 adds an opt-in `COORDINATOR_ROUTE_STORE=tcaplus` path in which
`ShardMapMeta` and 4096 `ShardRoute` rows are restart authority for Current.
The Kubernetes default remains `legacy-fence`; the opt-in mode now passes live
Tcaplus initialization, readiness and exact restart recovery.

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

## Live Tcaplus verification

The two new tables were created by the owner and discovered successfully by the
Tcaplus SDK. The first live attempt exposed two real latency defects: serial
bootstrap exceeded the old fixed 60-second context after about 1995 rows, and
restart used one traversal plus one `DoGet` per returned route. The repair:

- gives one attempt a configurable `COORDINATOR_ROUTE_BOOTSTRAP_TIMEOUT`, with
  a `10m` default;
- uses traversal records directly for normal Load and reads only a pending CAS
  target separately;
- treats Meta-absent Route rows as uncommitted initialization debris, replaces
  existing rows through record-version CAS, inserts missing rows, and writes
  Meta only after all 4096 rows succeed;
- constructs the first durable candidate from the existing authoritative
  ShardFence set, preserving historical owner/epoch/route-version advances
  made under legacy-fence before durable Current existed.

On 2026-08-13, a fresh Fence-aligned initialization replaced all 4096 existing
uncommitted/test Route rows and committed one Meta row in approximately six
minutes. Tcaplus reported no response timeouts, route failures or request
failures during the run. The committed snapshot had `map_version=4097`, because
the Fence hydration restored all 4096 active entries into the initial in-memory
candidate before its single durable bootstrap commit.

Coordinator then passed Current/Fence/Progress validation, served:

```text
GET /livez  -> alive
GET /readyz -> ready, shard_map=ready
```

Representative Shard 145 returned ACTIVE `zone-b`, owner epoch 2, route version
3 and map version 4097. After a clean stop, restart reached listening state in
about two seconds with `bootstrapped=false` and the same owner, epoch, route
version, map version, lease ID, transition ID and durable update timestamp.
Only the runtime expiry overlay moved forward, as required.

## Limitations

- `COORDINATOR_ROUTE_STORE` remains `legacy-fence` in kind pending an explicit
  deployment-mode change; the local opt-in live path is verified.
- Static zone-a/zone-b membership remains the empty-store bootstrap source.
- HTTP polling remains the compatibility transport. Watch/SDK publication,
  Kubernetes membership, rebalance, failover and Leader Election remain
  unimplemented.
- Runtime lease renewal assumes the configured static Zones are alive; it is
  not health evidence. Phase 03/04 replace this transitional rule.
- No production capacity or concurrency claim is made.
- The 2026-08-13 full `go test -count=1 ./...` rerun reached an unrelated,
  reproducible `internal/player.TestApplyMailRewardAllOrNothingAndReplay`
  failure: its fixed 2026-08-12 runtime is earlier than the state creation date
  on 2026-08-13. Coordinator, RouteStore and routing target suites pass; this
  phase did not change that Player test.
- `buf lint` for the complete `deploy/tcaplus` module is currently blocked by
  pre-existing `MailClaimSagaStatus` enum-value prefix findings in
  `mail_tables.proto`. The phase-required `buf generate --template
  buf.gen.yaml` succeeds, generated code compiles, and the full Go suite passes;
  this task did not rename the frozen mail enum contract.
