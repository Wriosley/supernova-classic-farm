# Friend steal experimental Await benchmark

## Scope

This is a performance-only experiment for the Actor `AwaitFriendOwnerCall` path. The change releases the visitor Actor mailbox while the cross-zone Owner RPC is in flight. It is not the production consistency design: there is no durable pending receipt, UNKNOWN reconciliation, eviction/migration gate, or atomic cross-Actor commit.

## Environment and method

- Zone image: experimental Await image `sha256:c72656b4f22c9192126fc3b5ae9a9bece55671ab68507d857bf04f93e96acb87`.
- 8 Zone Pods, 3 Gate Pods, 4 Login Pod port-forwards.
- Scenario: `friend_steal`, closed mode, same `friend-cross-zone-400-pairs.csv` cohort, concurrency 200, warmup 0, duration 30s, timeout 15s.
- Setup (reset, plant, authenticate, friendship, farm preparation) is excluded from the measured interval.
- One request per prepared `can_steal` target. Attempt QPS includes successes and business rejects; this run had no rejects/errors. Successful latency percentiles are reported separately by the runner.

## Result

| run | attempts | successes | rejects/errors | measured burst | attempt/success QPS | success P50/P95/P99 |
|---|---:|---:|---:|---:|---:|---|
| no Await baseline (`fs100base01`) | 610 | 610 | 0/0 | 109 ms | 5589.34 | 10.352 / 43.692 / 62.396 ms |
| experimental Await (`fs100await02`) | 1600 | 1600 | 0/0 | 264 ms | 6057.59 | 11.793 / 36.292 / 51.716 ms |

The prepared target counts differ (610 vs 1600), so this is not a controlled throughput win/loss claim. The Await run is a successful smoke/formal validation of the path and shows lower P95/P99 in this finite burst, but a sustained, equal-target repeated test is required before drawing a performance conclusion.

## Operational issue and recovery

The first two Await attempts did not reach measurement because Gate retained connections to an old Zone Pod IP after the Zone rollout (`10.244.0.65:8082`), producing `SERVICE_UNAVAILABLE` and `DeadlineExceeded`. Rolling the `gate` StatefulSet restored initialization; the subsequent 10-pair smoke and 100-pair run completed with zero errors.

## Validation and limitations

`go test ./...` passed except for the pre-existing/flaky `internal/platform/rpcnet/TestRoundRobinDistributesAndRemovesBackend` assertion (`one=0 two=12`); all Await/player/zone/benchrunner tests passed. `git diff --check` was clean.

Do not deploy this Await implementation as a correctness change without adding durable pending interaction state, timeout/UNKNOWN handling, duplicate protection, and migration/eviction coordination.
