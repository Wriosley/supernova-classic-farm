# Zone Friend Routing Uses Coordinator SDK Snapshot

Date: 2026-08-19  
Status: code, automated tests, live deployment and A/B benchmark complete

## Problem

The Zone process already subscribed to Coordinator route snapshots/batches through
`coordinatorclient`, but `visit.ZoneOwnerFarmClient` independently issued
`GET /internal/v1/routes/{shard}` for every enter, heartbeat, exit, and friend action. In the
100-pair cross-Zone friend benchmark this placed Coordinator at its 500m CPU limit while Gate and
Zone retained CPU headroom.

## Change

- `ZoneOwnerFarmClient` now accepts the Zone's existing `gateway.RouteResolver` rather than a
  Coordinator HTTP URL/client.
- In `ZONE_ROUTE_SOURCE=coordinator-sdk`, ordinary friend calls resolve from the SDK's versioned
  local snapshot.
- In `ZONE_ROUTE_SOURCE=http-poll`, calls reuse the already-warmed local cached resolver.
- Every Owner RPC continues to carry `logical_shard_id`, `owner_zone_id`, `owner_epoch`, and
  `route_version` for Owner-side fencing.
- An Owner `FailedPrecondition` invalidates only the route version used, requests SDK resync, then
  performs at most one re-resolution/retry. The SDK resolver has an HTTP fallback for this stale
  route case; normal calls do not use it.
- Non-dual local mode retains the legacy HTTP resolver as a compatibility fallback.

No HTTP/Protobuf contract or stored schema changed.

## Verification

- Added a focused test proving `ZoneOwnerFarmClient` uses the injected resolver, preserves route
  versions in `CommittedRoute`, and forwards version-specific invalidation.
- Passed `go test ./...` for the complete Go workspace.
- Passed `git diff --check`.

## Live deployment

Only the Zone image was rebuilt and loaded into kind. The eight `zone-pool` Pods rolled from image
ID `sha256:9ade2755...aab92` to `sha256:4ba510d8...8f0ea`. All eight became Ready with zero restarts;
Gate, Coordinator, Friend, Info, Login, and Mail were not redeployed.

## A/B benchmark

The workload is the same balanced cross-Zone friend lifecycle used in
`2026-08-19-cross-zone-friend-loadtest.md`: one cycle is enter, heartbeat, and exit. Authentication,
friendship setup, and the initial enter are excluded from measurement.

| Version/run | Pairs | Lifecycle QPS | Approx command/s | Cycle P50 | Cycle P99 | Errors | Coordinator peak |
|---|---:|---:|---:|---:|---:|---:|---:|
| before, balanced-50 | 50 | 227.07 | 681 | 205.47ms | 388.59ms | 0 | not isolated |
| before, calibration-100 | 100 | 228.82 | 686 | 407.70ms | 810.19ms | 0 | 499m/500m |
| after, sdk-50 | 50 | 1227.26 | 3682 | 40.16ms | 52.65ms | 0 | 56m/500m |
| after, sdk-100 | 100 | 1881.51 | 5645 | 52.11ms | 77.43ms | 0 | 54m/500m |
| after, sdk-200 | 200 | 2528.24 | 7585 | 77.43ms | 124.93ms | 0 | 54m/500m |

At the directly comparable 50-pair point, lifecycle throughput improved 5.4x and P99 fell 86.4%.
At 100 pairs, throughput improved 8.2x and P99 fell 90.4%. Coordinator stayed near its idle
42--56m rather than saturating at 499m. This verifies that ordinary friend commands no longer place
route lookup traffic on Coordinator.

The bottleneck moved rather than disappeared. At 200 pairs, peak CPU was:

| Component | Peak | Limit |
|---|---:|---:|
| Coordinator | 54m | 500m |
| Info | 501m | 500m |
| busiest Zone | 822m | 1000m |
| busiest Gate | 1033m | 3000m |
| busiest Friend | 367m | 500m |

Every successful enter starts a best-effort asynchronous `RecordOfflineFarmVisit`; Info is now the
first component at its CPU limit. Therefore 2528 lifecycle/s is not a demonstrated Zone ceiling.
The next isolation should disable/batch/sample that non-critical observation path or give Info
diagnostic headroom, then repeat 100/200/400 pairs before profiling the hottest Zone.

## Artifacts

- `/data/workspace/yace/raw/friend_cross_zone_50pairs_sdk_01`
- `/data/workspace/yace/raw/friend_cross_zone_100pairs_sdk_01`
- `/data/workspace/yace/raw/friend_cross_zone_200pairs_sdk_01`
- corresponding CSV files under `/data/workspace/yace/monitor/`
