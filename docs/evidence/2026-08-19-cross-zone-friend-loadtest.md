# Cross-Zone Friend Visit Load Test

Date: 2026-08-19  
Environment: local single-node kind cluster  
Scope: friend visit lifecycle (`ENTER_FRIEND_FARM -> FARM_HEARTBEAT -> EXIT_FRIEND_FARM`)

## Claim boundary

This evidence measures the visit lifecycle, not friend creation and not steal/help/pest actions.
One reported QPS unit is one successful three-command lifecycle; approximate successful command
throughput is therefore `3 * lifecycle QPS`. These results are a local prototype baseline, not a
production capacity claim.

## Workload and setup

- 800 pre-created accounts form 400 adjacent visitor/owner pairs.
- Every pair crosses Zone ownership; the cohort generator interleaves four Zone-pair groups so the
  first 50 and 100 pairs are distributed across all eight Zone owners.
- Accounts are sticky-hashed over four Login endpoints and three direct per-Pod Gate NodePorts.
- The measured section excludes authentication, friendship establishment, and the initial enter.
- Each closed-loop worker repeatedly enters, heartbeats, and exits the same friend's farm.
- `concurrency=100` means 50 pairs; `concurrency=200` means 100 pairs.

The benchrunner was extended to report per-action success/latency, emit explicit setup/warmup phase
markers, and persist lifecycle samples in `latency.csv`. Focused `go test ./cmd/benchrunner` and build
passed. No Gate, Zone, Friend, or Coordinator business code was deployed for this test.

## Results

| Run | Pairs | Duration | Lifecycle QPS | Approx command/s | Cycle P50 | Cycle P95 | Cycle P99 | Errors |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| balanced-50 | 50 | 30s | 227.07 | 681.22 | 205.47ms | 305.95ms | 388.59ms | 0 |
| calibration-100 | 100 | 30s | 228.82 | 686.47 | 407.70ms | 696.97ms | 810.19ms | 0 |
| profile-02 | 100 | 60s | 227.19 | 681.57 | 410.96ms | 696.29ms | 806.90ms | 0 |
| profile-03 | 100 | 45s | 223.20 | 669.60 | 438.02ms | 707.87ms | 900.98ms | 0 |

At 100 pairs, profile-02 action latency was:

| Action | Success | P50 | P95 | P99 | Max |
|---|---:|---:|---:|---:|---:|
| enter | 13,701 | 198.82ms | 318.83ms | 476.29ms | 977.44ms |
| heartbeat | 13,701 | 104.98ms | 289.03ms | 388.73ms | 703.45ms |
| exit | 13,701 | 100.44ms | 284.07ms | 389.70ms | 846.68ms |

Doubling pairs from 50 to 100 increased lifecycle throughput by only 0.8%, while cycle P50 nearly
doubled and P99 more than doubled. This is a saturation/queueing signature, despite zero errors.

## Bottleneck evidence

The 100-pair resource series records these peak CPU values:

| Component | Observed peak | CPU limit |
|---|---:|---:|
| Coordinator | 499m | 500m |
| busiest Gate | 188m | 3000m |
| busiest Zone | 249m | 2000m |
| busiest Friend | 103m | 500m |

Coordinator rose from about 43--52m during setup to 188m and then remained at 499m for six
consecutive samples during the load interval. Gate, Friend, and every Zone retained substantial CPU
headroom. The two sampled Zones each accumulated only about 0.39--0.40 CPU-seconds in a 15-second
profile; their CPU samples were dominated by runtime wait/scheduling paths rather than Actor
business functions.

The code path explains the control-plane load:

1. Gate routes the visitor command to the visitor's Zone.
2. `visit.Service` synchronously calls `ZoneOwnerFarmClient` for the owner's Zone.
3. Every enter, heartbeat, and exit invokes `resolveRoute`.
4. `resolveRoute` performs a fresh Coordinator HTTP
   `GET /internal/v1/routes/{shard}` before the owner-Zone gRPC call.

Thus the measured plateau of roughly 670--686 commands/s also produces roughly the same order of
Coordinator route GETs/s. Enter is slower because it additionally performs a synchronous mutual-
friend check, builds an owner snapshot through the Actor runtime, and publishes presence.

Conclusion: for this workload and deployment, the first demonstrated bottleneck is the
Coordinator route lookup on the friend-visit data path, not Gate CPU, FriendSvr CPU, or Zone Actor
CPU. Coordinator pprof was unavailable because its HTTP server returned 404 for the pprof route;
the conclusion instead rests on the sustained CPU-limit observation plus the exact synchronous
code path. It does not prove which internal Coordinator function is hottest.

## Next isolating experiment

Update: this experiment was completed later on 2026-08-19. See
`2026-08-19-zone-friend-sdk-routing.md`; the SDK path removed Coordinator saturation and improved
the directly comparable 50-pair lifecycle throughput from 227.07/s to 1227.26/s.

Do not increase pair count yet. First compare, with otherwise identical 50/100-pair runs:

1. current per-command Coordinator HTTP lookup;
2. a proposed bounded route cache/watch-snapshot lookup in `ZoneOwnerFarmClient`, retaining epoch
   fencing and retry-on-stale behavior;
3. optionally a temporary Coordinator CPU-limit increase as a diagnostic control, not as the fix.

Acceptance evidence must show that Coordinator CPU and route GET rate fall, lifecycle throughput
increases or latency falls, and stale-route/epoch tests remain green. Only after this isolation
should action scenarios such as steal/help/pest be added.

## Artifacts

- Cohort: `/data/workspace/yace/cohorts/friend-cross-zone-400-pairs.csv`
- Results: `/data/workspace/yace/raw/friend_cross_zone_*`
- Resource series: `/data/workspace/yace/monitor/friend_cross_zone_100pairs_cal_01-pods.csv`
- Profiles: `/data/workspace/yace/profiles/friend_cross_zone_100pairs_profile_02` and `_03`
