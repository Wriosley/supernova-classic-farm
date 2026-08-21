# Actor Cold-Load Limiter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:test-driven-development`.
> Preserve register-before-load: one player has one Loading Actor and one mailbox.

**Goal:** Bound Tcaplus checkpoint Load concurrency and queue growth after Zone
restart/failover without Actor prewarming.

**Architecture:** A runtime-level activation limiter grants both global and
per-Shard permits before the first mailbox Load task calls Store.Load.
Concurrent requests for the same player still share the already-registered
Loading Actor and do not consume extra permits.

## Global Constraints

- Initial assumptions: global active 64, per-Shard active 4, global queue 1024,
  per-Shard queue 64, queue timeout 2s, Load timeout 3s.
- Do not prewarm or enumerate players.
- Business handlers do not retry storage.
- Queue full/timeout -> retryable `ZONE_WARMING_UP`.
- Load timeout/storage failure -> `STORAGE_UNAVAILABLE`.
- Drain cancels queued activation and handles already-running Load safely.

## Task 1: Implement limiter primitive

**Files:**
- Create: `server/internal/player/activation/limiter.go`
- Create: `server/internal/player/activation/limiter_test.go`

```go
type Config struct {
  GlobalActive, PerShardActive int
  GlobalQueue, PerShardQueue int
  QueueTimeout, LoadTimeout time.Duration
}
type Permit interface { LoadContext(context.Context) (context.Context, context.CancelFunc); Release() }
func (l *Limiter) Acquire(context.Context, uint32) (Permit, error)
func (l *Limiter) CancelShard(uint32)
```

- FIFO within a Shard; bounded global/per-Shard queues.
- Permit release exactly once, including panic/cancellation.
- Avoid holding limiter locks while waiting or loading.
- Expose active/queued/rejected/timeout observations.

- [ ] Test exact limits, fairness, cancellation, timeout, double release and
  race behavior.

## Task 2: Integrate after Actor registration and before Load

**Files:**
- Modify: `server/internal/player/runtime.go`
- Modify: `server/internal/player/runtime_activation_test.go`
- Modify: `server/internal/player/checkpoint_store.go`

Required order:

```text
lock runtime
-> find or create Loading Actor + mailbox
-> unlock
-> first mailbox task waits for activation permit
-> Store.Load with load timeout
-> publish Ready or activation error
-> release permit
```

- Same-player 100 requests see one Actor, one queue entry and one Load.
- A failed activation removes only the same Actor instance and permits retry.
- Existing Ready Actor bypasses limiter.
- New-player CreateInitial uses the same permit/load timeout.

- [ ] Extend cold-activation tests for same player, many Shards and timeout.
- [ ] Run `go test -race ./internal/player/... ./cmd/zone`.

## Task 3: Integrate Shard Drain and errors

**Files:**
- Modify: `server/internal/player/runtime.go`
- Modify: `server/internal/routing/authorization.go`
- Modify: Gate/Zone error mapping and contracts if Phase 01 lacks exact mapping

- Drain marks authorization first, cancels queued permits for the Shard and
  waits for already-started Load before flush/evict.
- Queued cancellation returns `ZONE_MIGRATING`, not warming-up.
- Client retry preserves request ID; no business command is partially applied.

- [ ] Race Drain against queued and active Load; assert no Actor becomes Ready
  under revoked ownership.

## Task 4: Wire configuration and diagnostics

**Files:**
- Create: `server/cmd/zone/activation_wiring.go`
- Modify: `server/cmd/zone/main.go`
- Modify: `deploy/k8s/configmap.yaml`

Variables:

```text
ACTIVATION_GLOBAL_ACTIVE=64
ACTIVATION_PER_SHARD_ACTIVE=4
ACTIVATION_GLOBAL_QUEUE=1024
ACTIVATION_PER_SHARD_QUEUE=64
ACTIVATION_QUEUE_TIMEOUT=2s
ACTIVATION_LOAD_TIMEOUT=3s
```

Reject invalid/zero production limits; memory/local tests may inject explicit
unlimited mode. Log aggregated counters, never player IDs.

## Task 5: Measure cold-load behavior

- Create load scenario under `server/test/load/` using fake delayed Store,
  then run live Tcaplus only if available.
- Measure max concurrent Load, queue latency, rejection count, Tcaplus QPS and
  p50/p95/p99.
- Compare unbounded baseline only if actually measured.
- Record `docs/evidence/2026-08-12-actor-cold-load-limiter.md`; update CURRENT.

## Completion Gate

Concurrency never exceeds configured limits, same-player requests Load once,
Drain is safe, and evidence labels configured assumptions versus measurements.
Next: `10-扩缩容与平滑更新演练.md`.

