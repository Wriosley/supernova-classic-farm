# Immediate Gift Mail Relay Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver a newly persisted friend-gift Outbox row immediately by event ID while retaining the two-second full-scan ticker for recovery.

**Architecture:** Extend the Outbox store with primary-key lookup and make Relay.Run serialize targeted wakeups with periodic recovery scans. Wire a successful SEND_FRIEND_GIFT response to a non-blocking Relay notification only after Runtime persistence has completed.

**Tech Stack:** Go, TcaplusDB, PlayerOutbox, Player Runtime, Zone handler, gRPC Mail client, kind E2E.

## Global Constraints

- PlayerOutbox remains the durable source of truth; Notify is an optional low-latency hint.
- The immediate path must use event-ID Get and must not Traverse PlayerOutbox.
- The existing two-second recovery ticker remains unchanged.
- No new Tcaplus table, field, or external protocol.
- This task records one live response/red-dot latency sample and makes no percentile claim.
- Implement in an isolated worktree because master contains unrelated uncommitted changes.

---

### Task 1: Add Primary-Key Outbox Lookup

**Files:**
- Modify: `server/internal/outbox/relay.go`
- Modify: `server/internal/outbox/store.go`
- Modify: `server/internal/outbox/relay_test.go`

**Interfaces:**
- Produces: `Store.Get(context.Context, []byte) (*tcaplusv1.PlayerOutbox, error)`.

- [ ] Write a failing test whose fake Store records Get and Traverse calls.
- [ ] Run `cd server && go test ./internal/outbox -run TestRelayTargeted -count=1`; expect compile/test failure.
- [ ] Implement memory/Tcaplus Get with NotFound mapping and no Traverse.
- [ ] Run `cd server && go test -race ./internal/outbox -count=1`; expect PASS.
- [ ] Commit with `git commit -m "feat(mail): load outbox events by id"`.

### Task 2: Add Serialized Immediate Relay Wakeup

**Files:**
- Modify: `server/internal/outbox/relay.go`
- Modify: `server/internal/outbox/relay_test.go`

**Interfaces:**
- Produces: `func (r *Relay) Notify(eventID []byte)` and `func (r *Relay) RelayOne(context.Context, []byte) error`.

- [ ] Write failing tests for immediate delivery, duplicate notification, dropped/full wake fallback, retry_at, ownership, failure retention, and Run shutdown.
- [ ] Run `cd server && go test ./internal/outbox -run 'TestRelayNotify|TestRelayOne' -count=1`; expect failure.
- [ ] Implement one Relay.Run select loop over ticker/wake/context; Notify copies ID and never blocks.
- [ ] Keep Mail source-event dedup and MarkDelivered behavior unchanged.
- [ ] Run `cd server && go test -race ./internal/outbox -count=1`; expect PASS.
- [ ] Commit with `git commit -m "feat(mail): wake gift relay immediately"`.

### Task 3: Notify After Durable Gift Success

**Files:**
- Modify: `server/cmd/zone/handler.go`
- Modify: `server/cmd/zone/main.go`
- Modify: `server/cmd/zone/handler_test.go`
- Modify: `server/cmd/zone/gift_wiring.go`

**Interfaces:**
- Consumes: `Relay.Notify` from Task 2.
- Produces: Zone command hook that notifies only successful, non-empty SEND_FRIEND_GIFT Outbox event IDs.

- [ ] Write failing tests for success notification, failure/no-ID/no-relay suppression, and exact replay safety.
- [ ] Run `cd server && go test ./cmd/zone -run TestGiftRelayNotify -count=1`; expect failure.
- [ ] Inject a narrow `GiftOutboxNotifier` into the handler and call it after `runtime.Handle` returns a successful persisted response.
- [ ] Wire the same Relay instance used by `Relay.Run`; do not create a second Relay.
- [ ] Run `cd server && go test -race ./cmd/zone ./internal/outbox ./internal/player -run 'Gift|Outbox|Relay' -count=1`; expect PASS.
- [ ] Commit with `git commit -m "feat(mail): notify relay after gift commit"`.

### Task 4: Verify Complete Chain Once

**Files:**
- Modify: `server/test/e2e/friend_interaction_test.go`
- Create: `docs/evidence/2026-08-14-immediate-gift-mail-relay.md`
- Modify: `docs/context/CURRENT.md`

**Interfaces:**
- Produces: one exact SEND_FRIEND_GIFT response duration and one red-dot push duration.

- [ ] Run focused offline regression: `cd server && go test -race ./internal/outbox ./internal/player ./internal/mail ./internal/info ./internal/gateway ./cmd/zone -count=1`.
- [ ] Build/load/roll the Zone image and verify all relevant Pods Ready.
- [ ] Run the existing gift red-dot latency E2E once against kind/Tcaplus.
- [ ] Record exact commands, commit/image/config, single-sample timings, and limitations; do not state p50/p95/p99.
- [ ] Update CURRENT only if the deployed project state materially changed.
- [ ] Commit with `git commit -m "test(mail): verify immediate gift relay"`.
