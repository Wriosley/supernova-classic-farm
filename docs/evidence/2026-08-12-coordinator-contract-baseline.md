---
status: verified
date: 2026-08-12
scope: final-delivery-sprint-07-phase-01
---

# Coordinator contract and static compatibility baseline

## Result

Phase 07/01 froze the implementation-facing Coordinator contract without
wiring it into the running Coordinator, Gate, Info or Zone processes.

- `ShardMapSnapshot.assignment_algorithm_version` is additive field 7.
- `ShardRouteEntry.owner_endpoint` is additive field 12.
- `CoordinatorService` generates unary route lookup, bidirectional
  `WatchRoutes` and failure-evidence reporting contracts for Go and TypeScript.
- `ZONE_MIGRATING`, `ZONE_UNAVAILABLE`, `ZONE_WARMING_UP` and
  `STORAGE_UNAVAILABLE` are frozen at numeric values 204 through 207.
- Existing field numbers, `ShardCount`, hashing, Rendezvous scoring, HTTP route
  endpoints and runtime error mapping were not changed.

## TDD evidence

The generated-type smoke tests were run before each Protobuf addition:

1. Route metadata failed to compile because
   `AssignmentAlgorithmVersion`/`OwnerEndpoint` and their accessors did not
   exist; it passed after fields 7 and 12 were generated.
2. Coordinator Watch round-trip failed because the coordinator generated
   package did not exist; it passed after `coordinator.proto` generation.
3. Lifecycle error-number assertions failed because all four generated enum
   constants were undefined; they passed after values 204–207 were generated.

## Verification

From the repository root:

```text
$ buf lint && buf generate
(no output; exit 0)
```

From `server/`, with `GOCACHE=/tmp/classic-farm-go-cache`:

```text
$ go test ./gen/smoke
ok github.com/Wriosley/supernova-classic-farm/server/gen/smoke

$ go test ./internal/routing ./internal/gateway ./cmd/coordinator
ok github.com/Wriosley/supernova-classic-farm/server/internal/routing
ok github.com/Wriosley/supernova-classic-farm/server/internal/gateway
ok github.com/Wriosley/supernova-classic-farm/server/cmd/coordinator

$ go test ./internal/routing ./internal/gateway ./cmd/coordinator ./cmd/zone
ok github.com/Wriosley/supernova-classic-farm/server/internal/routing
ok github.com/Wriosley/supernova-classic-farm/server/internal/gateway
ok github.com/Wriosley/supernova-classic-farm/server/cmd/coordinator
ok github.com/Wriosley/supernova-classic-farm/server/cmd/zone

$ go test ./...
all packages passed; exit 0

$ go test -count=1 ./...
all packages passed without cached test results; exit 0
```

From `web/`:

```text
$ npm run typecheck
> vue-tsc --noEmit -p tsconfig.app.json && tsc --noEmit -p tsconfig.node.json
(exit 0)
```

The compatibility assertions are held by:

- `TestStableHashAndShardAreVersionOneConstants`;
- `TestStaticMapUsesStableRendezvousPlacement`;
- `TestNewLocalMapInitializesAllShardsActive` and
  `TestRouteHTTPSnapshotIsCompleteAndCounted`;
- `TestCachedRouteResolverWarmKeepsCoordinatorOffHitPath`;
- `TestCachedRouteResolverConditionalInvalidationAndSingleflight`;
- `TestContinueResumesPersistedPreparingMigration`;
- `TestRouteHTTPReturnsDecimalStrings` and the complete-snapshot HTTP test.

## Limitations

- `CoordinatorService` and `WatchRoutes` are generated contracts only; there is
  no server, publisher, SDK consumer or runtime wiring.
- Current `ShardRoute` persistence is not implemented.
- The four new errors are documented contract values only; Gate and Zone do not
  emit or map them yet.
- No Kubernetes discovery, membership health, migration worker, failover,
  Leader Election, Tcaplus schema or Actor activation limiter was added.
- Verification is local unit/regression evidence only. No kind or live Tcaplus
  environment was used and no capacity claim is made.
