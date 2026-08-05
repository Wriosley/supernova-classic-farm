---
status: proposed
date: 2026-08-04
owners:
  - project-owner
---

# Tcaplus-only minimum cluster prototype plan

## Goal

Before 2026-08-14, demonstrate a deliberately bounded Linux/Kubernetes
prototype:

```text
H5 -> Login -> Gate -> Coordinator -> 2..3 dynamically registered Zones
```

The existing single-player owner loop must remain intact. Player recovery
checkpoints move to TcaplusDB; a controlled `2 -> 3` Zone expansion moves a
limited number of Shards; and Zone scale-down drains, performs a final Dirty
flush, moves its Shards, then permits termination.

This is a 2--3 Zone controlled demonstration. It does not claim production
automatic scaling, Raft/Coordinator high availability, automatic failover, or
measured 30-million-DAU capacity.

## Accepted invariants that this work must preserve

- The online fact for an active player is the in-memory Player Actor. A normal
  game command changes Actor memory, marks Dirty, and replies without waiting
  for persistence.
- Same-player commands remain serialized by one Actor mailbox.
- `player_seq` is the client-visible business-state version. It must never be
  used as the persistence CAS token.
- `checkpoint_revision` is the private checkpoint-content CAS version. It can
  advance when `player_seq` does not.
- A replayed `request_id` returns its retained result and never double-spends
  resources or issues a second reward.
- Gate ordinary commands use only its committed RouteCache. A `NOT_OWNER`
  recovery may refresh a route; the normal command path must not query the
  Coordinator.
- `owner_epoch` must continue to reject an old Owner's delayed writes.
- No endpoint, App ID, credential, token, password, real internal address, or
  player data belongs in Git, logs, evidence, or this plan.

## Current factual baseline and blockers

Observed on 2026-08-04:

- Docker Engine and Docker Compose v2 are available; a local MySQL 8.4
  container can be started from `deploy/docker-compose.yml`.
- A local Kind cluster named `classic-farm` is Ready.
- The Linux host previously had Go 1.24.5 while `server/go.mod` requires Go
  1.26. The owner downloaded a Go 1.26 archive; installation and compilation
  remain verification steps.
- The current runnable backend orchestration is PowerShell-only
  (`start-servers.ps1`). There is no Linux shell equivalent.
- Current Login, Gate, Zone and Coordinator executables enforce loopback
  listener addresses, so they cannot be exposed through Kubernetes Services
  without a configuration change.
- No Tcaplus SDK module, allowed `TCAPLUS_*` configuration, test App/Zone, or
  confirmed table/CAS capability is currently available.

The Go/MySQL dual-Zone baseline must pass on Linux before Kubernetes automatic
scaling work starts. Tcaplus work stops at a single-record POC if a complete
owner loop cannot run against it.

## Scope and non-goals

### In scope

- Linux scripts for the current MySQL baseline.
- Tcaplus PB-table Player Checkpoint Load, CAS Save and restart recovery.
- A `CheckpointStore` boundary so Actor persistence does not depend directly
  on `*sql.DB`.
- Dynamic ready-Zone registration and a constrained rebalance planner.
- Kubernetes manifests, readiness, preStop Drain, PDB and bounded HPA.
- Controlled expansion from two to three Zones and controlled scale-down.
- Tests and reproducible evidence for owner-loop recovery, migration and
  stale-owner rejection.

### Explicitly out of scope

- Production Raft, majority Coordinator, automatic failover, or autonomous
  large-fleet scaling.
- A claim of atomic cross-record Tcaplus transactions.
- Friends, multiplayer gameplay, cross-player assets, a full Mail Service, or
  a full Outbox relay product.
- Deleting all MySQL code before the Tcaplus path has passed its owner-loop
  acceptance tests.

## Tcaplus table and consistency design

Use Tcaplus PB tables, not TDR tables. The repository already uses Protobuf and
PB keeps generated types and checkpoint serialization in one model.

### `PlayerCheckpoint`

Primary key: `player_id uint64`.

Required fields:

```text
player_id
logical_shard_id
owner_epoch
player_seq
checkpoint_revision
checkpoint_schema_version
checkpoint_blob
checkpoint_sha256
last_applied_config_version
created_at_ms
updated_at_ms
```

`checkpoint_blob` remains deterministic `PlayerCheckpointV1` bytes. Its
envelope fields and hash must agree with the decoded blob.

The write adapter must use both:

1. the Tcaplus per-record version returned by the previous read; and
2. the logical `expected_checkpoint_revision`.

An equal logical revision is idempotent only when all immutable comparison
fields and the hash agree. A newer stored revision returns stale/conflict. A
different equal revision is corruption. The adapter returns typed
`APPLIED`, `ALREADY_APPLIED`, `STALE_COPY`, `FENCED`, `RETRYABLE_FAILURE`, or
`CORRUPT_CONFLICT` outcomes.

### Pure-Tcaplus control records

If the owner confirms the no-MySQL direction after the Checkpoint POC, add:

| Table | Primary key | Purpose |
| --- | --- | --- |
| `AccountByName` | `account_name` | unique account-name reservation and provisioning state |
| `AccountByPlayer` | `player_id` | account lookup and activation state |
| `Session` | `session_digest` | Session generation, expiry and revocation |
| `ShardFence` | `logical_shard_id` | current Zone, epoch, route and transition |
| `MigrationProgress` | `logical_shard_id` | durable PREPARING state and drain manifest |
| `PlayerOutbox` | `event_id` | immutable event payload and relay state |

All table definitions, primary keys, field tags, generated code, and table
names must be reviewed against the actual Tcaplus PB-table limits before
creation.

### Cross-record compensation boundary

Tcaplus does not supply a transaction across `ShardFence`,
`PlayerCheckpoint`, and `PlayerOutbox`. The design therefore uses explicit
state machines, deterministic IDs, CAS, and reconciliation:

- Registration creates a `PROVISIONING` account reservation, creates the
  initial checkpoint with put-if-absent, creates the Session, then CASes the
  account to `ACTIVE`. Failed steps are retryable or compensatable by the
  provisioning identifier.
- A Dirty checkpoint CAS persists `pending_outbox` records. Reconciliation
  creates each `PlayerOutbox` record by stable `event_id`; existing records
  must immutable-compare. Delivery stays at-least-once and consumers
  deduplicate by `event_id`.
- Migration persists `PREPARING`, drains and final-flushes known active
  Actors, advances the `ShardFence`, CASes target checkpoints to the new
  epoch, then publishes `ACTIVE`. A failure after fence advance remains
  fail-closed and can only continue; it cannot reactivate the old Owner.

This cannot be represented as a production-strength cross-record global
transaction. The demonstration must state the controlled assumptions:
the old Zone obeys Drain, all active Actors appear in the drain manifest,
and the target's checkpoint CAS completes before the route becomes `ACTIVE`.
The E2E test must prove a delayed old write with its old Tcaplus record
version/epoch is rejected.

## Implementation sequence

### Phase 0: Linux MySQL baseline

1. Verify Go 1.26 and `go test ./...`.
2. Add a Linux startup/migration script equivalent to the existing PowerShell
   path without logging a DSN or password.
3. Run a MySQL single-Zone and static dual-Zone owner-loop baseline.
4. Record actual commands, service readiness and failures under
   `docs/evidence/`.

Stop condition: do not start Kubernetes scaling if this baseline is not green.

### Phase 1: Storage boundary and Tcaplus POC

1. [x] Replace separate loader/writer injection with a `CheckpointStore`
   contract.
2. [x] Adapt the existing MySQL implementation to that contract without
   changing Actor command semantics.
3. [x] Add a fake store and unit tests for all typed CAS outcomes.
4. [x] Add a Tcaplus PB `PlayerCheckpoint` adapter and a standalone test
   program that performs create, Load, CAS success, stale CAS rejection and
   reload.
5. [x] Run restart recovery against the Tcaplus test environment.

Items 1–3 completed on 2026-08-04. Full Go regression and the live Linux
dual-Zone MySQL restart/migration/Fence E2E pass; Tcaplus SDK and table access
were still required at that checkpoint.

The Tcaplus POC implementation and live single-record verification completed
on 2026-08-04 using the official Go module `v0.2.3` (internal API 3.55). The
PB table schema, environment-only connection config, record-version plus
logical-revision CAS adapter, typed result tests and `cmd/tcaplus-poc` prove
Create, Load, CAS, duplicate retry, stale rejection and reload against a real
table. Item 5 completed on 2026-08-05 through the pure-Tcaplus owner loop,
active migration, complete process stop and post-migration restart.

Fallback: retain the MySQL Checkpoint implementation and publish only the
Tcaplus single-record POC when owner-loop recovery fails.

### Phase 2: Pure-Tcaplus control records

The owner selected immediate pure-Tcaplus implementation on 2026-08-05.
Existing MySQL adapters remain only as an explicit rollback path.

1. [x] Implement registration and Session provisioning Saga.
2. [x] Implement `ShardFence`, `MigrationProgress` and `PlayerOutbox` adapters.
3. [x] Add activation/outbox reconciliation.
4. [x] Prove request retry, Outbox immutable replay and old-epoch checkpoint
   rejection in tests.
5. [x] Create the seven new PB tables in the Tcaplus control plane and run the
   no-MySQL five-process restart/migration E2E.

Phase 2 completed on 2026-08-05. The live gate initialized all 4096 fences,
registered players through the Tcaplus Saga, routed players to both Zones,
persisted gameplay, migrated inactive and active Shards, and restarted all five
processes from advanced Fence state without `MYSQL_DSN`. See
`../evidence/2026-08-05-pure-tcaplus-runtime-gate.md`.

### Phase 3: Dynamic Zone membership

Skipped by owner decision on 2026-08-05. The minimum prototype keeps exactly
two static identities, `zone-a` and `zone-b`, configured by
`ZONE_A_*`/`ZONE_B_*`. It does not implement registration, discovery,
heartbeat-driven membership or automatic rebalancing.

### Phase 4: Kubernetes deployment

Created on 2026-08-05:

```text
deploy/k8s/namespace.yaml
deploy/k8s/configmap.yaml
deploy/k8s/secret.example.yaml
deploy/k8s/login.yaml
deploy/k8s/gate.yaml
deploy/k8s/coordinator.yaml
deploy/k8s/zone.yaml
deploy/k8s/services.yaml
deploy/k8s/kustomization.yaml
```

All Tcaplus values are `secretKeyRef` references only. The example Secret
contains placeholder keys, never real values.

The fixed two-Zone prototype has no HPA, PDB or Zone-level preStop Drain.
Those features require dynamic ownership evacuation and are explicitly outside
the selected scope. Zone process shutdown remains bounded but operators must
not voluntarily restart a Zone with active players.

### Phase 5: Fixed-cluster tests

1. [x] Start the five Deployments with exactly two static Zones.
2. [x] Verify Gate serves ordinary commands from its committed cache.
3. [x] Run inactive and active Shard migration through the Coordinator.
4. Restart recovery and old-owner delayed-write rejection remain explicit
   follow-up gates; replica scaling is not part of this prototype.

## File-level change inventory

Expected primary additions or changes:

```text
server/internal/player/checkpoint_store.go
server/internal/player/mysql_checkpoint_store.go
server/internal/player/tcaplus_checkpoint_store.go
server/internal/player/tcaplus_checkpoint_store_test.go
server/internal/player/outbox_reconciler.go
server/internal/auth/tcaplus_store.go
server/internal/auth/provisioning_saga.go
server/internal/routing/tcaplus_fence.go
server/internal/routing/tcaplus_migration_progress.go
server/internal/routing/zone_registry.go
server/internal/routing/rebalance.go
server/cmd/{login,gate,zone,coordinator}/
scripts/start-servers.sh
scripts/migrate-mysql.sh
deploy/k8s/
docs/evidence/
docs/context/CURRENT.md
```

The final names may change after inspecting the actual Tcaplus SDK/table
generation interfaces, but the boundaries must not.

## Environment prerequisites

The owner must provide a non-production Tcaplus App and Zone with PB-table
creation/read/write permission, a least-privilege credential, endpoint/network
access, the table option proto, and confirmation that the selected SDK supports
single-record versioned conditional update. Credentials are injected only as:

```text
TCAPLUS_ENDPOINT
TCAPLUS_APP_ID
TCAPLUS_ZONE
TCAPLUS_TABLE_*
TCAPLUS_CREDENTIAL_*
```

Before adding the SDK dependency, pin a tested official SDK release rather
than permanently using an unreviewed `@latest`.

## Evidence and acceptance

Each implementation phase records:

- files changed;
- exact sanitized commands and tests run;
- verified facts;
- remaining assumptions and risks;
- environmental or permission blockers.

Minimum final evidence:

1. full owner loop and Tcaplus restart recovery;
2. same `request_id` replay does not duplicate mutation/reward;
3. Tcaplus CAS rejects stale revision and old Owner epoch;
4. fixed two-Zone Kubernetes owner loop and controlled migration;
5. explicit statement that dynamic membership, autonomous scale and preStop
   ownership evacuation are not implemented;
6. explicit statement of all non-production limits.
