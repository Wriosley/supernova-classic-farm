---
status: measured
date: 2026-08-20
---

# Zone drain migration concurrency adjustment

## Scope

To prepare the physical single-Zone load-test topology, seven `zone-pool`
owners are being drained into `zone-pool-0`. The Coordinator migration limits
were previously fixed at global 8, per source 2 and per target 2. Because all
tasks share one target, effective concurrency was limited to two.

## Change and validation

- Added positive-integer environment overrides while preserving defaults:
  `COORDINATOR_MIGRATION_GLOBAL_LIMIT`,
  `COORDINATOR_MIGRATION_PER_SOURCE_LIMIT`, and
  `COORDINATOR_MIGRATION_PER_TARGET_LIMIT`.
- Focused Coordinator tests passed with default, override and invalid-value
  coverage: `go test ./cmd/coordinator`.
- The local load-test Coordinator was deployed with `8/2/4`; its startup log
  reported `global_limit=8`, `per_source_limit=2`, `per_target_limit=4`.
- After the Tcaplus task store rebuilt its 4096-key in-process open-task index,
  Shards 1675 through 1694 showed continuous successful completion. In the
  observed steady window, completions were approximately 0.7--0.9 seconds
  apart, with no new migration failure or storage-timeout log.

## Operational limitation

This is a local drain acceleration setting, not a production capacity claim.
The single-node Coordinator must be restarted with old replica termination
before the new replica starts; overlapping active replicas can observe an
in-flight Fence/route intermediate state and fail closed. Physical Zone
scale-down remains forbidden until every draining Zone reports zero owner
Shards, tasks and progress and `removable=true`.

## Higher-concurrency trial

The load-test cluster was subsequently raised to global 16, per source 4 and
per target 16. After the restart-time task index rebuild, 50 migrations
completed in the first observed five-minute window (which includes the index
delay), with steady completions resuming from Shard 2093 onward. Coordinator
was approximately 151m CPU/42Mi and the target Zone 46m CPU/45Mi. No new
Tcaplus timeout, placement conflict or CAS error was observed. Two pre-existing
problem Shards continued their independent retries (`FINAL_FLUSH_FAILED` and
`NOT_OWNER`); increasing concurrency does not repair those state errors.
