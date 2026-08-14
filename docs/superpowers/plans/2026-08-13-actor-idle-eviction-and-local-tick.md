# Actor Idle Eviction and Local Tick Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Runtime's one-second full Actor scan with Actor-owned deadlines and safely evict farms that have had no owner, visitor, or request activity for three minutes.

**Architecture:** Each Actor computes its next maturity deadline and returns Tick results; one Runtime min-heap only schedules mailbox work. A separate eviction sweep uses connection/visit presence interfaces, rechecks eligibility inside the mailbox, synchronously persists state, then removes exactly that Actor instance.

**Tech Stack:** Go 1.26, existing Actor mailbox, Tcaplus-neutral `CheckpointStore`, `container/heap`.

## Global Constraints

- Idle timeout is exactly `3 * time.Minute`; tests inject time.
- Runtime schedules but never reads plots or decides maturity.
- Do not create one ticker or goroutine per Actor.
- Dirty/Tcaplus failure keeps the Actor resident.
- Do not add Redis or QuerySvr in this plan.

---

### Task 1: Actor Tick Result and Deadline

**Files:** Create `server/internal/player/actor_tick.go`, `actor_tick_test.go`; modify `server/internal/player/runtime.go`.

**Interfaces:** Produces `runtimeActor.tick(now time.Time) (ActorTickResult, error)` and `runtimeActor.nextTickAt() (time.Time, bool)`.

- [ ] Write failing tests `TestRuntimeActorNextTickAtChoosesEarliestPlot` and `TestRuntimeActorTickMaterializesDuePlotsAndReturnsNextDeadline` proving earliest/no deadline and complete Tick results.
- [ ] Run `cd server && GOCACHE=/tmp/classic-farm-go-cache go test ./internal/player -run 'TestRuntimeActor(NextTickAt|Tick)'`; expect build failure for missing APIs.
- [ ] Implement:

```go
type ActorTickResult struct {
    DirtyRevision uint64
    MaturityEvents []MaturityEvent
    DomainChanges DomainChanges
    NextTickAt *time.Time
}
func (a *runtimeActor) nextTickAt() (time.Time, bool)
func (a *runtimeActor) tick(now time.Time) (ActorTickResult, error)
```

`tick` calls `materializeDueMaturities`, derives `DomainChangesFromPlotIDs`, and recomputes the earliest remaining `EstimatedMatureAtMS`.
- [ ] Run the targeted test and `go test ./internal/player`; expect PASS.
- [ ] Commit Task 1 files as `refactor: move maturity tick decisions into player actor`.

### Task 2: Shared Deadline Scheduler

**Files:** Create `server/internal/player/actor_scheduler.go`, `actor_scheduler_test.go`; modify `server/internal/player/runtime.go`.

**Interfaces:** Internal scheduler provides `schedule(playerID uint64, deadline time.Time, generation uint64)` and `cancel(playerID uint64)`; consumes Task 1 `tick`.

- [ ] Write failing deterministic tests: earliest fires first, reschedule invalidates old generation, cancellation prevents delivery, no deadline produces no delivery.
- [ ] Run `go test ./internal/player -run TestActorScheduler`; verify RED.
- [ ] Implement one `container/heap` scheduler with one goroutine and wake channel. Delivery submits to the mailbox and rechecks Actor identity/generation.
- [ ] Consume `ActorTickResult` outside business logic: mark Dirty, call existing maturity notification/farm-view dispatch, then reschedule `NextTickAt`.
- [ ] Remove `runMaturityScheduler`/`materializeOnlineMaturities`; register deadlines after activation and successful timing-changing commands.
- [ ] Run `go test ./internal/player` and `go test -race ./internal/player`; expect PASS.
- [ ] Commit as `refactor: schedule actor maturity by deadline`.

### Task 3: Eviction Eligibility Signals

**Files:** Modify Actor mailbox, connection registry, visit registry and their tests; modify `server/internal/player/runtime.go`.

**Interfaces:** `Mailbox.Idle() bool`; `connection.Registry.Has(playerID uint64) bool`; `visit.Registry.HasVisitors(ownerPlayerID uint64, now time.Time) bool`; Runtime depends on:

```go
type PlayerPresence interface { Has(playerID uint64) bool }
type FarmObservers interface { HasVisitors(ownerPlayerID uint64, now time.Time) bool }
```

- [ ] Write failing tests for queued/running mailbox work, connection presence, and expired/live visitors.
- [ ] Run package tests and verify RED.
- [ ] Implement thread-safe read methods without exposing maps; track mailbox queued/running state.
- [ ] Run `go test -race ./internal/actor ./internal/connection ./internal/visit ./internal/player`; expect PASS.
- [ ] Commit as `feat: expose actor eviction eligibility signals`.

### Task 4: Three-Minute Safe Eviction

**Files:** Create `server/internal/player/actor_eviction.go`, `actor_eviction_test.go`; modify `runtime.go`, `server/cmd/zone/main.go`.

**Interfaces:** `const actorIdleTimeout = 3 * time.Minute`; `Runtime.EvictIdleActors(ctx context.Context, now time.Time) error`.

- [ ] Write failing tests for each blocker, successful SaveCAS-before-delete, SaveCAS failure retention, and access racing eviction.
- [ ] Run `go test ./internal/player -run TestEvictIdleActor`; verify RED.
- [ ] Track `lastAccessAt` on Actor creation and every admitted external request/visit; scheduler Tick does not extend user activity.
- [ ] Implement eviction under Shard lock: mark Evicting, recheck in mailbox, tick, checkpoint, SaveCAS, cancel deadline, compare-and-delete same Actor, close mailbox. Restore Ready/deadline on failure.
- [ ] Wire a 10-second Zone sweep stopped by existing context.
- [ ] Run targeted test, `go test -race ./internal/player ./cmd/zone`, then `go test ./...`.
- [ ] Record `docs/evidence/2026-08-13-actor-idle-eviction-local-tick.md` and update CURRENT only after green verification.
- [ ] Commit as `feat: evict idle player actors safely`.

