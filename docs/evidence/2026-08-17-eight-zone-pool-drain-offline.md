# Eight-Zone pool drain offline evidence (2026-08-17)

Implemented and verified offline:

- configured Drain overrides repeated healthy probes without masking
  SUSPECT/DEAD failures;
- Planner excludes DRAINING candidates and emits only DRAIN-priority work for
  their Current Shards;
- Planner writes no task before eight healthy candidates exist;
- drain status reports Current owner count, open/running task count and the
  exact removable gate;
- manifests render with eight `zone-pool` replicas, Planner/Worker enabled,
  A/B configured for Drain, and minimum healthy candidates set to eight.

Commands:

```text
go test ./internal/coordinator/membership -run 'TestController(ConfiguredDrain|Recovers|ThirdFailure|PodReplacement)'
PASS
go test ./internal/coordinator/placement
PASS
go test ./cmd/coordinator -run 'Test(MembershipConfig|PlannerConfig|DrainStatus)'
PASS
go test ./... -run '^$'
PASS
kubectl kustomize deploy/k8s
PASS (989 lines)
```

## First live attempt and safety pause

The first live attempt reached eight Ready pool Pods and began bounded DRAIN
work. Migrations then failed with `PROGRESS_CONFLICT`. The Coordinator Planner
and Worker were immediately set to disabled before deleting either legacy
Zone. A post-pause route snapshot retained all 4096 authoritative routes:

- `zone-a`: 2015 Shards;
- `zone-b`: 2080 Shards;
- pool: 1 Shard;
- durable `map_version`: 4127.

The failure was reproduced from persisted route contents. Existing ACTIVE
routes carry a historical `transition_id`, while `MigrationProgress` does not
persist the Source route's historical transition/previous-owner fields.
`sameProgressIdentity` incorrectly compared the complete in-memory
`RouteEntry`, so the first write/read/advance cycle rejected valid progress.

The comparison now covers exactly the fields persisted by
`MigrationProgress`. `TestProgressStoreIgnoresUnpersistedSourceRouteHistory`
guards the real legacy-route shape. Verification after the fix:

```text
go test -count=1 ./internal/coordinator/migration ./internal/routing
PASS
go test -count=1 ./cmd/coordinator
PASS (run with local-listener permission for httptest)
```

Live migration remains paused pending a rebuilt Coordinator and controlled
resume. No A/B manifest may be deleted before the drain endpoint reports both
Zones removable.

The Planner-only recovery attempt then indexed the existing 4096
`MigrationTask` keys successfully but stopped on Shard 0 with `migration task
conflict`. The existing PLANNED task and the new proposal had the same frozen
Source/Target intent; only `planned_from_map_version` and
`planned_from_availability_version` differed after restart. Those fields are
creation-time evidence, not task identity. Deduplication now preserves the
existing Task ID and original evidence when only the observation versions
change. Memory and Tcaplus-backed regression coverage passes in
`TestTaskStoreDeduplicatesSameIntentAcrossPlannerObservationVersions`.

The next Planner-only run advanced through Shard 1592 and then found Shard
1593's old task in `RUNNING`, with no MigrationProgress yet and Current still
on zone-b. This is the valid crash boundary after Task Claim and before the
first Progress write. Planner now preserves any open task whose frozen Source
still exactly matches Current and whose earlier Target remains HEALTHY; the
Worker, not Planner, resumes that task. A later reconcile can rebalance again
after ownership commits. `TestPlannerPreservesClaimedTaskBeforeFirstProgressBoundary`
covers this restart boundary.

The first Worker resume completed migrations and advanced durable Current from
map version 4127 to 4319, proving the Progress identity fixes worked. It also
exposed two additional boundaries before the run was stopped:

- eight executors concurrently called full-table `ShardRoute` Traverse through
  `RouteStore.Load`; the Tcaplus Go SDK permits only one active Traverser for a
  table, producing retriable `-8734` timeouts and `-11283` invalid-object-state
  errors;
- Shard 1593 restarted with durable PREPARING, Progress at
  `SOURCE_FLUSHED`, and the Fence still on Source. Startup validation only knew
  the superseded step names and incorrectly required the target Fence.

Executor now serializes only the RouteStore Load/commit/apply/publish critical
sections while Zone drain/load work remains bounded-concurrent. Fence startup
validation classifies `SOURCE_DRAINING`, `SOURCE_FLUSHED`, and
`ROUTE_PREPARING` as pre-Fence, and the later worker steps as post-Fence.
Focused migration/routestore/routing tests and the complete Coordinator package
pass. The live Coordinator was temporarily scaled to zero to stop the old
Worker before rebuilding this fix.

The fixed image then restarted read-only at durable map version 4380, loaded
1825 OPEN Progress rows, passed Current/Fence validation (including Shard
1593), and became Ready with both Planner and Worker disabled. This confirms
the persisted intermediate states are recoverable; the increase from 4319 was
committed by the old Worker before it fully terminated.

After A/B ownership reached zero, authority inspection found 3997 ACTIVE and
99 PREPARING pool routes at map version 12218. Shard 1593 demonstrated the
cause: OPEN `SOURCE_FLUSHED` Progress remained, while Planner had cancelled its
REBALANCE Task as `CURRENT_MATCHES_DESIRED` merely because PREPARING owner ID
already equalled Desired. The pool was therefore not yet fully routable and the
old drain endpoint incorrectly reported removable.

Recovery is now self-contained:

- Planner never cancels a Task on a PREPARING Current route;
- Worker startup reconstructs missing/terminal Tasks from complete OPEN
  Progress identity without changing Route, Fence, epoch, lease, or transition;
- active matching PLANNED/RUNNING tasks are retained idempotently;
- drain status counts every open Source task plus OPEN Progress and requires
  owner/task/progress counts all to reach zero.

`TestRecoverOpenProgressTasksRequeuesCancelledTask`,
`TestPlannerDoesNotCancelPreparingTaskWhenTargetMatchesDesired`, and
`TestDrainStatusBlocksRemovalWhileSourceProgressIsOpen` pass together with the
complete Coordinator package.

The next controlled resume made every authoritative route routable: at
`map_version=12317`, all 4096 routes were ACTIVE, zone-a/zone-b each owned zero
Shards, and the eight pool owners held 463/547/490/530/525/518/530/493 Shards.
The strict drain gate still found 13 OPEN Progress rows (4 sourced from zone-a,
9 from zone-b). Every row was at `TARGET_READY`; its Current route was the exact
target ACTIVE route, but its recovered Task had again been cancelled with
`CURRENT_MATCHES_DESIRED`.

The remaining race was an ACTIVE variant of the earlier PREPARING bug. Planner
now preserves a PLANNED Task when Current contains the complete committed-task
evidence: target owner/endpoint, source+1 epoch, source+2 route version,
previous owner and a non-empty transition. This is distinguishable from a
stale Task whose target merely happens to match Desired. Worker can therefore
idempotently advance `TARGET_READY -> ROUTE_ACTIVE`, complete Progress and
complete the Task. `TestPlannerPreservesCommittedActiveTaskForProgressTail`
passes with the migration/placement suites and the complete Coordinator
package. Live cleanup of the 13 rows requires one final Coordinator rebuild
and rollout; A/B remain protected until both drain entries report removable.

After the Kubernetes `deployment.apps/zone-a`, `deployment.apps/zone-b`,
`service/zone-a`, and `service/zone-b` resources were deleted, the coordinator
drain snapshot still reported one stale `zone-b` progress row for `shard
1623` at `TARGET_READY` with the task already cancelled. The pool itself
remained healthy with all 4096 routes ACTIVE on the eight pool owners.
