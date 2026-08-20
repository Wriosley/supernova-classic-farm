# Friend Steal No-Await Baseline

Date: 2026-08-19  
Status: baseline complete; Await path intentionally not implemented

## Measurement definition

`friend_steal` prepares each Owner farm outside measurement, enters the farm, collects every public
`can_steal` plot, then sends one unique `STEAL_FRIEND_CROP` request per target. The measurement ends
when the finite successful workset is exhausted. Preparation, authentication, friendship creation,
farm entry and exit are excluded.

The scenario reports `attempt QPS` (all sent requests), `success QPS` (Owner Actor mutations),
business rejection count, and system error count separately. Successful latency percentiles contain
only successful steals.

## Result

Run: `/data/workspace/yace/raw/friend_steal_100pairs_base_01`  
Accounts: 100 pairs / concurrency 200  
Prepared targets: 610  
Attempt count: 610  
Successful steals: 610  
Business rejects: 0  
System errors: 0

| Attempt QPS | Success QPS | P50 success | P95 success | P99 success | Max success |
|---:|---:|---:|---:|---:|---:|
| 5,589.34 | 5,589.34 | 10.35ms | 43.69ms | 62.40ms | 69.22ms |

This is a finite burst baseline, not sustained 30-second capacity: the 610-target workset finished
in 109ms. Low resource usage therefore does not establish a Zone limit.

## Validation and next comparison

The 10-pair smoke produced 160/160 successful steals at 1,960.88 attempt/s with zero errors. Each
request passed Visitor validation, visit lease validation, Owner route fencing, Owner Actor
`CanSteal`/crop/visitor checks, Owner mutation and Visitor side-effect processing.

After Await is implemented, rerun this exact scenario with the same cohort and compare attempt QPS,
success QPS, successful latency, rejects, system errors, and Actor correctness. The setup records
the actual target count (development accounts yielded about six targets per Owner), rather than
assuming 16 plots.

