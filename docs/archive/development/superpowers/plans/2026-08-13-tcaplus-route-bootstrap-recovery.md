# Tcaplus Route Bootstrap Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make first-time durable RouteStore bootstrap resumable and complete, while removing the Traverse-plus-N-reads restart path.

**Architecture:** Treat every Meta-absent route as uncommitted bootstrap debris; CAS-overwrite existing rows with one fresh candidate, insert missing rows, and publish Meta last. Use traversal records directly for normal snapshots and fetch only a CAS target row when its record version is needed. Give one bootstrap attempt a configurable 10-minute context budget.

**Tech Stack:** Go, protobuf, TcaplusDB Go SDK, in-memory fake Tcaplus tests, live Tcaplus verification.

## Global Constraints

- `ShardMapMeta` is inserted only after exactly 4096 candidate-equal `ShardRoute` rows exist.
- Once Meta exists, static bootstrap never repairs, overwrites or recomputes durable Current.
- Meta-absent partial rows may be overwritten; Meta-present durable Current
  never may.
- The default bootstrap timeout is 10 minutes; invalid or non-positive overrides fail startup.
- Durable mode remains disabled by default until live bootstrap and restart pass.
- Do not delete or overwrite the existing partial live table during verification.

---

### Task 1: Resumable Meta-Last Bootstrap

**Files:**
- Modify: `server/internal/coordinator/routestore/tcaplus.go`
- Test: `server/internal/coordinator/routestore/tcaplus_test.go`

**Interfaces:**
- Consumes: `TcaplusStore.BootstrapIfEmpty(context.Context, Snapshot)` and existing `routeRecord`/`metaRecord` conversions.
- Produces: bootstrap behavior that validates existing rows, inserts missing rows, and inserts Meta only after completeness.

- [ ] **Step 1: Write failing partial-bootstrap tests**

Add tests that pre-insert stale route records for Shards `0..1994` without Meta, call `BootstrapIfEmpty`, and assert the result contains the fresh candidate for all `routing.ShardCount` entries and Meta exists. Wrap the fake client to count Route inserts and updates; assert existing rows were updated, missing rows inserted, and Meta inserted once.

Add a second test that first completes Meta plus routes, then calls bootstrap with a different candidate and verifies committed Current is returned unchanged.

Add a third wrapper that cancels the context after a bounded number of route inserts. Assert the first call returns `context.Canceled` with no Meta, then a second call using a fresh context completes all 4096 rows and Meta.

- [ ] **Step 2: Run the focused tests and verify RED**

```bash
cd server
go test -count=1 ./internal/coordinator/routestore -run 'TestTcaplusStore(ResumesPartialBootstrap|RejectsConflictingPartialBootstrap|RetriesInterruptedBootstrap)$'
```

Expected: FAIL because current bootstrap either classifies partial routes without Meta as corrupt or refuses to replace stale uncommitted rows.

- [ ] **Step 3: Implement candidate-indexed partial validation and completion**

In `BootstrapIfEmpty`, load Meta first. If Meta exists, call `Load` and return committed Current. If Meta is absent, iterate the complete candidate.

For every candidate row, try `DoInsert`. On `AlreadyExists`, call `loadRoute` to obtain the current record version, then `DoUpdate` the fresh candidate through CAS. After all 4096 mutations succeed, insert `metaRecord(candidate.Metadata)`. If Meta already exists due to a race, call `Load` and return the winner with `created=false`.

- [ ] **Step 4: Run focused and package tests and verify GREEN**

```bash
cd server
go test -count=1 ./internal/coordinator/routestore -run 'TestTcaplusStore(ResumesPartialBootstrap|RejectsConflictingPartialBootstrap|RetriesInterruptedBootstrap)$'
go test -count=1 ./internal/coordinator/routestore
```

Expected: PASS.

- [ ] **Step 5: Commit the resumable bootstrap**

```bash
git add server/internal/coordinator/routestore/tcaplus.go server/internal/coordinator/routestore/tcaplus_test.go
git commit -m "fix(coordinator): resume partial route bootstrap"
```

### Task 2: Eliminate Load N+1 Reads Without Weakening Pending CAS

**Files:**
- Modify: `server/internal/coordinator/routestore/tcaplus.go`
- Test: `server/internal/coordinator/routestore/tcaplus_test.go`

**Interfaces:**
- Consumes: `loadRoutes(context.Context)` traversal and `loadRoute(context.Context, uint32)` versioned point read.
- Produces: normal `Load` with zero route `DoGet`s and pending recovery with one target-route `DoGet`.

- [ ] **Step 1: Write failing request-count tests**

Create a `countingClient` wrapper around `testtcaplus.Client` that counts `Traverse` and `DoGet`, separating Meta and Route message types. After bootstrap, reset counters and call `Load`; assert one route traversal, one Meta `DoGet`, and zero Route `DoGet`s. For pending-before-route recovery, inject the existing failure, reset counters, call `Load`, and assert recovery reads only the target Route.

- [ ] **Step 2: Run request-count tests and verify RED**

```bash
cd server
go test -count=1 ./internal/coordinator/routestore -run 'TestTcaplusStoreLoad(UsesTraversalRecords|ReadsOnlyPendingTarget)$'
```

Expected: FAIL because `loadRoutes` currently calls `loadRoute` for every traversed record.

- [ ] **Step 3: Use traversal records directly**

Change `loadRoutes` to clone and append each typed traversal record, sort by `LogicalShardId`, and stop collecting per-row versions:

```go
record, ok := message.(*tcaplusv1.ShardRoute)
if !ok {
    return nil, false, fmt.Errorf("%w: route traversal returned %T", ErrRouteStoreCorrupt, message)
}
routes = append(routes, proto.Clone(record).(*tcaplusv1.ShardRoute))
```

Change `recoverPending` to call `loadRoute(ctx, meta.PendingShardId)` once. Use that record and version for exact-target comparison, legal-old validation and the possible Route CAS. Keep the post-finalize reload.

- [ ] **Step 4: Run focused, package and race tests and verify GREEN**

```bash
cd server
go test -count=1 ./internal/coordinator/routestore -run 'TestTcaplusStoreLoad(UsesTraversalRecords|ReadsOnlyPendingTarget)$'
go test -count=1 ./internal/coordinator/routestore
go test -race -count=1 ./internal/coordinator/routestore
```

Expected: PASS.

- [ ] **Step 5: Commit the load-path fix**

```bash
git add server/internal/coordinator/routestore/tcaplus.go server/internal/coordinator/routestore/tcaplus_test.go
git commit -m "perf(coordinator): avoid per-route reloads"
```

### Task 3: Configurable Bootstrap Attempt Timeout

**Files:**
- Modify: `server/cmd/coordinator/main.go`
- Test: `server/cmd/coordinator/main_test.go`
- Modify: `docs/archive/development/plans/final_delivery_sprint/07-Coordinator动态路由控制面/02-权威ShardRoute持久化.md`

**Interfaces:**
- Consumes: environment variable `COORDINATOR_ROUTE_BOOTSTRAP_TIMEOUT`.
- Produces: `routeBootstrapTimeoutFromEnvironment() (time.Duration, error)` with a 10-minute default.

- [ ] **Step 1: Write the failing timeout parser test**

Add `TestRouteBootstrapTimeoutFromEnvironment` covering empty to `10*time.Minute`, `15m` to `15*time.Minute`, and `0s`, `-1s`, invalid text to errors.

- [ ] **Step 2: Run the parser test and verify RED**

```bash
cd server
go test -count=1 ./cmd/coordinator -run TestRouteBootstrapTimeoutFromEnvironment
```

Expected: FAIL because the parser and default constant do not exist.

- [ ] **Step 3: Implement and wire the timeout**

Add `defaultRouteBootstrapTimeout = 10 * time.Minute` and:

```go
func routeBootstrapTimeoutFromEnvironment() (time.Duration, error) {
    raw := strings.TrimSpace(os.Getenv("COORDINATOR_ROUTE_BOOTSTRAP_TIMEOUT"))
    if raw == "" {
        return defaultRouteBootstrapTimeout, nil
    }
    duration, err := time.ParseDuration(raw)
    if err != nil || duration <= 0 {
        return 0, fmt.Errorf("invalid COORDINATOR_ROUTE_BOOTSTRAP_TIMEOUT %q", raw)
    }
    return duration, nil
}
```

Parse it only when durable Tcaplus RouteStore mode is selected, and replace the fixed `60*time.Second` bootstrap context with it. Keep other startup timeouts unchanged. Update Phase 07/02 with the default, resumable initialization and Meta-last readiness rule.

- [ ] **Step 4: Run Coordinator and full offline regression**

```bash
cd server
gofmt -w cmd/coordinator/main.go cmd/coordinator/main_test.go internal/coordinator/routestore/tcaplus.go internal/coordinator/routestore/tcaplus_test.go
go test -count=1 ./cmd/coordinator ./internal/coordinator/routestore ./internal/routing
go test -count=1 ./...
```

Expected: PASS.

- [ ] **Step 5: Commit timeout wiring and plan update**

```bash
git add server/cmd/coordinator/main.go server/cmd/coordinator/main_test.go docs/archive/development/plans/final_delivery_sprint/07-Coordinator动态路由控制面/02-权威ShardRoute持久化.md
git commit -m "feat(coordinator): extend durable bootstrap budget"
```

### Task 4: Live Tcaplus Resume and Restart Evidence

**Files:**
- Modify: `docs/evidence/2026-08-12-durable-current-shard-route.md`
- Modify: `docs/context/CURRENT.md`

**Interfaces:**
- Consumes: existing `.env` Tcaplus credentials and partially populated `ShardRoute` with absent Meta.
- Produces: redacted evidence of completed row count, Meta creation, startup duration and exact restart recovery.

- [ ] **Step 1: Verify live state without mutation**

Start Coordinator in durable mode on a non-conflicting local HTTP port. Confirm successful table discovery without printing credentials or internal endpoints.

- [ ] **Step 2: Allow resumable bootstrap to finish**

Use `COORDINATOR_ROUTE_BOOTSTRAP_TIMEOUT=10m`. Wait for `durable Current ShardRoute ready`, then confirm Ready. Record elapsed time and redacted row count.

Expected: existing matching rows remain, missing rows are added, exactly 4096 routes exist, and Meta exists only after completion.

- [ ] **Step 3: Restart and verify exact durable recovery**

Stop Coordinator cleanly, restart with the same configuration, and confirm it loads durable Current without rewriting routes. Verify map version, route count and representative route identity remain unchanged.

- [ ] **Step 4: Update evidence and current handoff**

Change evidence status to the accurately observed live boundary. Record commands, redacted results, timings, row count, restart result and limitations. Update `CURRENT.md` because successful live recovery materially advances Phase 07/02.

- [ ] **Step 5: Check and commit evidence**

```bash
git diff --check
git add docs/evidence/2026-08-12-durable-current-shard-route.md docs/context/CURRENT.md
git commit -m "docs(coordinator): record live route recovery"
```

Expected: unrelated `core.1034525` remains untouched.
