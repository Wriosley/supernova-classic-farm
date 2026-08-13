---
status: partial
date: 2026-08-13
---

# Normal migration worker evidence

## Scope

Phase 06 Tasks 1–5, one normal live Tcaplus migration/restart gate, and one
real persisted-boundary process-crash recovery are verified. The remaining
per-step Coordinator crash matrix is deliberately deferred, so the Kubernetes
worker switch remains disabled by default.

## Offline verification

```text
go test -race -count=1 ./cmd/coordinator ./cmd/zone \
  ./internal/coordinator/migration ./internal/coordinator/routestore \
  ./internal/routing

go test -race -count=1 ./internal/player \
  -run 'TestRuntime(DrainShard|PrepareShard|ReportsActiveActors|ActivatesFromCheckpointLoaderOnce)'
```

Observed: all listed packages and focused Player migration tests passed.
The executor suite injects failures at every persisted Progress boundary,
Fence and RouteStore commits. Scheduler tests cover global/source/target
limits, one-Shard exclusion, retry, restart and bounded cancellation.

`kubectl kustomize deploy/k8s` followed by client-side dry-run also passed.

## Live Tcaplus / kind gate

Environment: `kind-classic-farm`, pure Tcaplus, durable ShardRoute, Kubernetes
membership, Planner disabled.

1. Coordinator and Zone images were rebuilt and loaded once.
2. With Worker disabled, all Coordinator/zone-a/zone-b/zone-pool workloads
   became Ready. Coordinator restored `map_version=4117` with
   `bootstrapped=false`.
3. Worker was enabled explicitly and a manual request created a DRAIN task for
   Shard 1 from zone-a to zone-b.
4. The first gate exposed that the live `MigrationTask` table's Traverse API
   returned zero rows even when exact primary-key `DoGet` found the task. The
   Store was changed to recover the fixed 4096-key space once at startup and
   maintain its open-task index from the single Leader's writes thereafter.
5. After the fix, the durable worker committed Shard 1 as:

```text
owner: zone-a -> zone-b
owner_epoch: 1 -> 2
route_version: 1 -> 3
map_version: 4117 -> 4119
state: ACTIVE
```

6. MigrationProgress was absent after completion. After Coordinator restart,
   the exact same ACTIVE route and versions were restored with
   `bootstrapped=false`; the completed task did not execute again.
7. Worker was returned to `0` after the gate.

## Real process-crash smoke gate

A development/E2E-only `AfterPersist` hook was added for all nine durable
Progress/Task boundaries. Configuration rejects unknown boundaries and is
forbidden when `APP_ENV=production`. With injection set to
`SOURCE_DRAINING` for Shard 10, the Coordinator exited with code 86 after the
Progress row was created. Kubernetes restarted the same container once; the
worker resumed from durable Progress without recreating it and completed:

```text
Shard: 10
owner: zone-b -> zone-a
owner_epoch: 1 -> 2
route_version: 1 -> 3
state: ACTIVE
Coordinator restartCount: 0 -> 1
```

The reusable matrix runner is
`tests/e2e/coordinator-migration-crash-matrix.sh`. A first full-matrix attempt
was interrupted by the execution window while the migration POST connection
was intentionally lost during the injected crash; this is a test-orchestration
limitation, not a failed recovery assertion. The other eight live boundaries
remain unverified.

## Limitations and next gate

- The live test covered successful migration plus post-completion Coordinator
  restart and a real crash after `SOURCE_DRAINING`. It did not kill Coordinator
  at every other persisted step.
- Old-epoch rejection, no-double-owner and target lazy activation are covered
  offline but still need to be repeated in the final live recovery matrix.
- The root `internal/player` full package currently includes an unrelated
  mail-reward test fixture whose fixed clock predates `NewDevelopmentState`;
  the Phase 06 Player migration subset is green.
