# Redis Offline Maturity Wakeup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist the next maturity deadline for evicted Actors in Redis and wake the current Owner Zone with at-least-once delivery.

**Architecture:** TimerSvr leases due ZSet entries, resolves Coordinator routing, and calls a Zone wake RPC. Resident Actors return `ALREADY_RESIDENT`; absent Actors reuse `actorFor`, settle in the mailbox, reschedule, and ACK.

**Tech Stack:** Go 1.26, Redis ZSet/Lua, gRPC/HMAC, Coordinator client, Kubernetes.

## Global Constraints

- Redis is an index; Tcaplus remains farm authority.
- At-least-once delivery with generation and processing lease.
- Growing Actor cannot be evicted if scheduling fails.
- No per-player timers.

---

### Task 1: Schedule Contract

**Files:** Create `server/internal/maturityschedule/store.go`, `memory_store.go`, `store_test.go`.

```go
type Task struct { PlayerID, Generation uint64; ExpectedMatureAtMS int64 }
type Store interface {
    Schedule(context.Context, Task) error
    Cancel(context.Context, uint64, uint64) error
    ClaimDue(context.Context, time.Time, time.Duration, int) ([]Task, error)
    Ack(context.Context, Task) error
    RetryExpired(context.Context, time.Time, int) error
}
```

- [ ] Write failing tests for new-generation overwrite, stale cancel, claim lease, ACK, expired retry.
- [ ] Verify RED, implement locked MemoryStore, then run unit/race tests.
- [ ] Commit as `feat: define offline maturity schedule contract`.

### Task 2: Redis Adapter

**Files:** Modify `server/go.mod`; create Redis adapter/tests under `server/internal/maturityschedule`.

- [ ] Add an explicit Redis Go client and integration-test fixture.
- [ ] Write failing real-Lua tests for atomic scheduled→processing claim, replacement, ACK, retry.
- [ ] Implement partitioned scheduled/processing ZSets plus payload storage without full-player traversal.
- [ ] Run targeted/race tests and commit `feat: add redis maturity schedule index`.

### Task 3: Zone Wake RPC

**Files:** Modify `proto/classicfarm/v1/rpc/runtime.proto`; regenerate clients; create `server/cmd/zone/maturity_rpc.go` and tests; modify Runtime.

- [ ] Write failing tests for resident ACK, absent activation/settlement, Evicting retry, wrong owner, duplicate generation.
- [ ] Define `WakePlayerForMaturity`; results are `WOKEN`, `ALREADY_RESIDENT`, `RETRY_LATER`; ownership failure is gRPC `FailedPrecondition`.
- [ ] Make resident lookup precede `actorFor`; absent activation settles in mailbox and emits changes once.
- [ ] Make `activateActor` Load/init only so it cannot swallow maturity events.
- [ ] Run protocol generation check and `go test -race ./internal/player ./cmd/zone`; commit `feat: wake evicted actors for crop maturity`.

### Task 4: TimerSvr and Deployment

**Files:** Create `server/cmd/timer/main.go`, `server/internal/maturitytimer/service.go` and tests, `deploy/k8s/timer.yaml`; modify Kustomize/config/docker-compose.

- [ ] Write failing service tests for route refresh, both ACK results, transient retry, and expired-lease recovery.
- [ ] Implement bounded polling workers with no farm rules.
- [ ] Wire Redis through Secret/config references without credentials in Git.
- [ ] Make Actor eviction schedule before delete and activation cancel matching generation.
- [ ] Run package/full Go tests and local E2E: plant → disconnect → eviction → maturity → Actor reload → notification.
- [ ] Record evidence/update CURRENT and commit `feat: schedule offline crop maturity with redis`.

