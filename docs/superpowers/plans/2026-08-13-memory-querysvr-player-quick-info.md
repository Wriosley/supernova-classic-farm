# Memory QuerySvr Player Quick Info Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Provide memory-only Actor residency, player presence, and stealable-farm summaries and use them to seed friend red dots.

**Architecture:** Zones publish versioned replacements. QuerySvr returns `found=false` after restart; friend-list reads seed H5 indicators while Actor remains authoritative.

**Tech Stack:** Go 1.26, Protobuf gRPC/HMAC, existing friend/H5 flows.

## Global Constraints

- Memory-only; restart loss is accepted.
- Unknown is `found=false`, not an all-false record.
- Stale state-version/incarnation updates cannot overwrite newer data.
- Query results never authorize stealing.

---

### Task 1: Contract and Memory Store

**Files:** Create `proto/classicfarm/v1/query/query.proto`, generated clients, `server/internal/query/store.go`, `store_test.go`.

- [ ] Define `UpsertPlayerQuickInfo` and `BatchGetPlayerQuickInfo` with player/residency/online/stealable/Zone/incarnation/version/time fields and per-item `found`.
- [ ] Write failing tests for unknown, ordering, version rejection, incarnation replacement.
- [ ] Generate clients, implement locked store, run unit/race tests; commit `feat: add in-memory player quick info store`.

### Task 2: QuerySvr Process

**Files:** Create `server/cmd/query/main.go` and tests, `deploy/k8s/query.yaml`; modify service/Kustomize/config.

- [ ] Write failing RPC validation/auth tests.
- [ ] Implement HMAC service with batch limit 256 and no persistence path.
- [ ] Deploy and verify health plus empty-after-restart behavior; commit `feat: run memory query service`.

### Task 3: Zone Publishers

**Files:** Create `server/internal/player/quick_info.go` and tests; modify lifecycle/crop paths and Zone connection RPC/wiring.

- [ ] Write failing tests for activation/eviction, final connection online/offline, maturity true, harvest/steal exhaustion/clean false, stale/failing publish.
- [ ] Compute `has_stealable_crop` inside Actor with `CanSteal`; publish versioned replacements after mailbox commit.
- [ ] Publish connection/lifecycle changes fail-open.
- [ ] Run player/zone/query race tests; commit `feat: publish player quick info changes`.

### Task 4: Friend Indicator Recovery

**Files:** Modify friend-list response path/proto if required, `web/src/App.vue`, `FriendsPanel.vue`, backend/frontend tests.

- [ ] Write failing tests for total red dot and only matching friend buttons.
- [ ] Batch-query after listing friends; `found=false` leaves unmarked.
- [ ] Merge results with existing realtime InfoSvr red-dot Set; do not replace push behavior.
- [ ] Run Go tests, `npm run typecheck`, frontend tests, and QuerySvr-restart E2E.
- [ ] Record evidence/update CURRENT and commit `feat: recover friend farm indicators from query service`.

