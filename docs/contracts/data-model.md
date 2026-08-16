---
status: accepted
version: 2
date: 2026-08-06
owners:
  - project-owner
related:
  - websocket-protocol.md
  - idempotency-and-errors.md
  - ../architecture/stateful-zone-v3-architecture.md
  - ../architecture/single-player-vertical-loop-business-architecture.md
  - ../decisions/ADR-0006-async-dirty-writeback.md
  - ../decisions/ADR-0008-v3-quorum-shard-coordinator.md
  - ../decisions/ADR-0009-player-actor-task-progress.md
---

# V3 Data-Model Contract V1

## 1. Scope and authority

This contract defines the complete first-stage logical data model for the V3 stateful Player Actor path. It is the normative English source. Normative words `MUST`, `MUST NOT`, `SHOULD` and `MAY` retain their usual requirements meaning.

It covers:

- the in-memory Player Actor aggregate;
- the client `PlayerSnapshot` projection;
- the serialized Player checkpoint;
- relational envelope, index, Outbox and fence data;
- committed ShardMap control-plane data;
- the in-memory Dirty queue and batch-write DTO;
- configuration references required to recover persisted state.

It does not define SQL DDL, generated Protobuf, RPC code, migrations, or the reward-mail event payload. The latter belongs to `event-contracts.md`.

V3 is authoritative. MySQL is the latest recovery checkpoint, not the online truth for an active Actor. There is no synchronous Journal, Kafka replay, database-per-command transaction path, local WAL, or claim that an acknowledged but unflushed ordinary write survives an abnormal Zone loss.

## 2. Representation choice and model boundaries

The first implementation MUST use:

1. a deterministic, versioned Protobuf `PlayerCheckpointV1` blob as the complete recoverable Player Actor aggregate;
2. minimal relational envelope columns for fencing, CAS, lookup, observability and migration;
3. immutable relational Outbox rows materialized in the same transaction as the checkpoint that first creates them;
4. separate Coordinator-owned ShardMap state and database-owned shard fences.

This choice keeps recovery to one aggregate decode, preserves unknown Protobuf fields, and avoids splitting same-player invariants across many mutable business tables. Relational columns MUST NOT become a second online business authority.

The following representations are distinct:

| Representation | Authority and purpose | Exclusions |
|---|---|---|
| In-memory Actor state | Online authority while the owning Actor is active; mutable only in its mailbox | DB relay status and Coordinator internals |
| Client `PlayerSnapshot` | Sanitized read projection defined by `websocket-protocol.md` | idempotency, Outbox, Dirty metadata, internal decimals, frozen task internals |
| `PlayerCheckpointV1` | Complete serialized recovery aggregate copied from Actor memory | passwords, Sessions, connections, subscriptions, mailbox, timers |
| Relational envelope/index | CAS/fence/version/lookup metadata around the blob | independently mutable coins, inventory, plots or tasks |
| Control-plane data | Committed routes, leases and shard fence authority | player business state |

The client projection MUST be produced from current Actor memory, never by treating a possibly stale database checkpoint as real-time state.

## 3. Common scalar, encoding and ordering rules

Unless this contract says otherwise, scalar meanings match `websocket-protocol.md`.

| Meaning | Logical type | Rule |
|---|---|---|
| Player/epoch/sequence/version | `uint64` | Non-negative; H5 never uses JavaScript `number` |
| Domain/config IDs | `uint32` | Zero is invalid unless explicitly identified as absent |
| Coin | `int64` | Integer coin units; balance MUST be `>= 0`; arithmetic MUST be checked for overflow |
| Quantity/progress | `uint32` | Checked arithmetic; inventory quantities are `1..300` |
| Time | `int64` | Unix milliseconds UTC from server time; `0` means absent only on fields marked optional |
| UUID | 16 raw bytes | RFC 4122 byte order; non-zero; text projections use canonical lowercase form |
| Hash | raw bytes | SHA-256 values are exactly 32 bytes |
| Opaque Protobuf | bytes | Deterministic serialization of the named accepted message version |

Repeated map-like collections MUST be unique and serialized in ascending key order:

- inventory by `item_id`;
- plots by `plot_id`;
- tasks by `task_id`;
- idempotency results by `(completed_at_ms, request_id bytes)`;
- pending Outbox records by `(created_at_ms, event_id bytes)`.

Unknown Protobuf fields MUST be preserved when a checkpoint is decoded and re-encoded without an explicit migration. Published field and enum numbers MUST never be reused.

### 3.1 Decimal and growth arithmetic

Authoritative rate values and rate modifiers use signed fixed-point `RateDecimal6`; maturity and accumulated growth use signed fixed-point `GrowthDecimal9`:

```text
RateDecimal6.scaled_value = real rate × 1,000,000
GrowthDecimal9.scaled_value = real growth × 1,000,000,000
```

Both types are logically `int64`; floating-point values MUST NOT enter authoritative calculations. Rates are growth units per second. The intuitive V1 values are represented exactly:

```text
maturity 100       = GrowthDecimal9(100,000,000,000)
base rate 1        = RateDecimal6(1,000,000)
fertilizer +0.5    = RateDecimal6(500,000)
pest -0.3          = RateDecimal6(-300,000)
```

Settlement is:

```text
effective_rate_scaled6 =
    base_rate_scaled6 + active_modifier_scaled6...

delta_growth_scaled9 =
    elapsed_ms × effective_rate_scaled6

new_growth_scaled9 =
    min(maturity_scaled9, old_growth_scaled9 + delta_growth_scaled9)
```

The scale relationship makes millisecond settlement exact: `milliseconds × rate-scaled-1e6` directly yields growth-scaled-1e9. Therefore repeated settlements equal one long settlement without rounding or a persisted remainder. Intermediates MUST use checked 128-bit-or-wider integer arithmetic before range-checking into `int64`. Growth is clamped to `[0, maturity_value]`; the effective rate MUST remain positive for a growing plot. `effective_now = max(server_now, last_settled_at_ms)`. Buff intervals are `[start_at_ms, end_at_ms)`.

## 4. In-memory Player Actor aggregate

One Actor owns exactly one `player_id` and logically contains:

```text
PlayerActorState {
  player_id
  logical_shard_id
  owner_zone_id
  owner_epoch
  player_seq
  checkpoint_revision
  coin_balance
  inventory
  plots
  current_chapter
  recent_results
  pending_outbox
  last_applied_config_version
  dirty_metadata
}
```

`owner_zone_id`, Dirty timestamps, in-flight flush tokens, mailbox and timers are runtime-only. Every other field above is represented in the checkpoint or its envelope.

Invariants:

1. `logical_shard_id = stable_hash64(player_id) % 4096`.
2. `owner_epoch` is the committed ownership epoch and MUST match the database fence before a critical write or flush.
3. `player_seq` is monotonic along the transactionally committed checkpoint lineage. Each successful business mutation or each independently materialized maturity transition increments it exactly once. A controlled epoch change loads the latest checkpoint and continues the sequence. After abnormal loss, the new epoch starts from the latest committed value, which can be lower than the lost Owner's acknowledged in-memory value and can therefore reuse an uncommitted sequence number; `(owner_epoch, player_seq)` keeps those histories distinct.
4. `checkpoint_revision` is monotonic along the transactionally committed checkpoint lineage. It increments once for every mutation of checkpoint content, including a terminal idempotency failure that does not change `player_seq`, retention pruning, and Outbox reconciliation. After abnormal loss, uncommitted in-memory revisions disappear and the new Owner continues from the latest committed revision.
5. One successful business command that changes business state increments both sequences once. A terminal Actor business failure increments only `checkpoint_revision`.
6. All command effects across coin, inventory, plots, chapter, idempotency and new Outbox records are applied atomically in Actor memory before the result is exposed.
7. Actor activation completes checkpoint validation, Outbox reconciliation and offline maturity settlement before serving the first snapshot or write.

`checkpoint_revision` is required because the accepted idempotency contract stores terminal failures while explicitly leaving `player_seq` unchanged. A Dirty CAS keyed only by `player_seq` cannot persist two distinct checkpoint contents at the same client state version. This is a cross-contract clarification; `player_seq` and WebSocket ordering semantics are unchanged.

## 5. Serialized `PlayerCheckpointV1`

The blob has logical message name `PlayerCheckpointV1` and these exact fields:

| Field | Type | Rule |
|---|---|---|
| `schema_version` | `uint32` | Required; V1 is `1` |
| `player_id` | `uint64` | Required; equals relational key |
| `logical_shard_id` | `uint32` | Required; `0..4095` and hash-derived |
| `owner_epoch` | `uint64` | Required; equals envelope epoch |
| `player_seq` | `uint64` | Required; equals envelope sequence |
| `checkpoint_revision` | `uint64` | Required; equals envelope revision |
| `coin_balance` | `int64` | Required and non-negative |
| `inventory` | repeated `InventoryStack` | Non-zero unique stacks, sorted |
| `plots` | repeated `PlotStateRecord` | Every owned plot, sorted |
| `current_chapter` | `ChapterStateRecord` | Exactly one active/current chapter record |
| `recent_results` | repeated `IdempotencyResultRecord` | Pruned to section 7 |
| `pending_outbox` | repeated `PendingOutboxRecord` | Created by this aggregate and not known delivered |
| `last_applied_config_version` | `uint64` | Snapshot pinned by the last checkpoint mutation |
| `created_at_ms` | `int64` | Player initialization time; immutable |
| `updated_at_ms` | `int64` | Time of the mutation producing this revision |

The blob MUST decode to no more than 4 MiB in V1. Exceeding the limit is an invariant violation and blocks writes/eviction; it MUST NOT be silently truncated.

### 5.1 Inventory and coin

`InventoryStack` contains `item_id uint32` and `quantity uint32`.

- At most 100 stacks are present.
- Each quantity is `1..300`.
- A zero quantity removes the stack.
- Item IDs are stable configuration identities; disabled items remain decodable and usable according to the accepted business rules.
- Coin changes and inventory changes from one command are atomic.

### 5.2 Plots and persisted effects

`PlotStateRecord` contains:

| Field | Type | Presence/rule |
|---|---|---|
| `plot_id` | `uint32` | Required, unique |
| `state` | enum | `EMPTY=1`, `GROWING=2`, `MATURE=3`, `NEED_CLEANUP=4`; zero invalid |
| `crop_id` | `uint32` | Non-EMPTY |
| `crop_item_id` | `uint32` | Non-EMPTY; harvested inventory item identity |
| `crop_config_version` | `uint64` | Non-EMPTY; frozen planting config |
| `planted_at_ms` | `int64` | Non-EMPTY |
| `maturity_value` | `GrowthDecimal9` | Non-EMPTY, `> 0`, frozen |
| `base_growth_rate` | `RateDecimal6` | Non-EMPTY, `> 0`, frozen |
| `base_yield` | `uint32` | Non-EMPTY, `> 0`, frozen |
| `stolen_quantity` | `uint32` | Non-EMPTY; `<= base_yield` |
| `settled_growth_value` | `GrowthDecimal9` | GROWING/MATURE; within bounds |
| `last_settled_at_ms` | `int64` | GROWING/MATURE |
| `estimated_mature_at_ms` | optional `int64` | GROWING cache; rebuildable |
| `fertilizer_effect` | optional `TimedEffectRecord` | GROWING only |
| `pest_effect` | optional `TimedEffectRecord` | GROWING only |

`TimedEffectRecord` contains `effect_instance_id UUID`, `effect_kind`, `effect_item_or_pest_id uint32`, optional `source_player_id uint64`, `config_version uint64`, `modifier RateDecimal6`, `start_at_ms int64`, and `end_at_ms int64`.

State invariants:

- `EMPTY` has none of the crop, settlement or effect fields.
- `GROWING` has all growth fields and at most one effect of each kind.
- `MATURE` has growth clamped to maturity, no active effects and no estimated maturity.
- `NEED_CLEANUP` retains crop identity/config/plant time for display/audit but has no growth or effect fields.
- Expired effects remain until their historical interval has been settled; after settlement they are removed.
- `estimated_mature_at_ms` is non-authoritative and MUST be recomputed when missing or inconsistent.

### 5.3 Current chapter

`ChapterStateRecord` contains:

| Field | Type | Rule |
|---|---|---|
| `chapter_id` | `uint32` | Required |
| `chapter_config_version` | `uint64` | Configuration used to activate/freeze this chapter |
| `status` | enum | `IN_PROGRESS=1`, `CLAIMABLE=2`, `CLAIMED=3`; zero invalid |
| `activated_at_ms` | `int64` | Required |
| `claimed_at_ms` | optional `int64` | Present only when claimed |
| `tasks` | repeated `TaskStateRecord` | Unique, sorted |
| `next_chapter_id` | optional `uint32` | Frozen transition target when configured |

`TaskStateRecord` contains `task_id uint32`, `task_config_version uint64`, `metric enum`, `current_value uint32`, `target_value uint32`, and `completed bool`.

- `target_value` and `metric` are frozen at chapter activation so a later config publication cannot reinterpret persisted progress.
- `current_value` is saturated at `target_value`; `completed` equals `current_value >= target_value`.
- `CLAIMABLE` means all tasks completed.
- Only successful server-side actions advance progress.
- Claiming atomically records the reward effects and marks the chapter claimed. A configured next chapter replaces it as the current chapter; a terminal chapter remains in the checkpoint as `CLAIMED`. Earlier chapter history and analytics are outside V1 and may only be inferred by the client from the current chapter transition order.

## 6. Configuration snapshot references

ConfigSvr remains authority. A Zone atomically replaces complete immutable `ConfigSnapshot(version)`, and a command pins one snapshot.

Persisted references are:

- top-level `last_applied_config_version`;
- `crop_config_version` plus frozen maturity, growth and yield values;
- each effect `config_version` plus frozen modifier and interval;
- `chapter_config_version` and each task's config version, metric and target;
- stable item/crop/effect/chapter/task IDs.

Shop `price_version` is an input equality token and is stored in the idempotency fingerprint and original receipt, not as current player state. Config entries referenced by persisted state MUST be disable-only, not physically deleted. Recovery MUST succeed without the historical config by using frozen fields; current actions that require missing current config fail with `CONFIG_UNAVAILABLE`.

## 7. Idempotency result record

`IdempotencyResultRecord` has:

| Field | Type | Rule |
|---|---|---|
| `caller_player_id` | `uint64` | V1 equals checkpoint player |
| `request_id` | UUID | Key with caller |
| `fingerprint_schema_version` | `uint32` | V1 is `1` |
| `protocol_version` | `uint32` | Original protocol |
| `action` | `uint32` | Original stable enum number |
| `target_player_id` | `uint64` | Original target |
| `payload_fingerprint_sha256` | 32 bytes | Canonical fingerprint |
| `completed_at_ms` | `int64` | Server completion time |
| `success` | `bool` | Terminal outcome |
| `result_owner_epoch` | `uint64` | Original response version |
| `result_player_seq` | `uint64` | Original response version |
| `response_payload_type` | `uint32` | Stable action/result discriminator |
| `response_payload` | bytes | Deterministic compact typed receipt/patch |
| `error_payload` | optional bytes | Deterministic V1 `Error` for terminal failure |
| `outbox_ids` | repeated UUID | Unique, sorted |

Exactly one record exists per `(caller_player_id, request_id)`. The retained encoded response MUST fit the 64 KiB WebSocket limit. Full snapshots MUST NOT be retained.

Pruning occurs before every insertion and SHOULD occur during activation:

1. remove records with `completed_at_ms <= server_now - 24h`;
2. insert/retain the new terminal record;
3. if more than 100 remain, remove oldest by `(completed_at_ms, request_id)`.

Thus the window contains at most 100 results and no result older than 24 hours. Pruning changes checkpoint content and increments `checkpoint_revision`, but not `player_seq`. Abnormal loss may roll back a result together with its associated business mutation, as defined by ADR-0006.

## 8. Pending Outbox record and relational relay state

`PendingOutboxRecord` in the checkpoint contains:

| Field | Type | Rule |
|---|---|---|
| `event_id` | UUID | Globally unique; stable across retries |
| `event_type` | enum | V1 includes `CREATE_REWARD_MAIL=1` |
| `event_contract_version` | `uint32` | Accepted event payload version |
| `aggregate_player_id` | `uint64` | Checkpoint player |
| `caused_by_request_id` | UUID | Originating write |
| `created_owner_epoch` | `uint64` | Epoch at creation |
| `created_player_seq` | `uint64` | Business version after creation |
| `created_at_ms` | `int64` | Server time |
| `payload` | bytes | Deterministic event payload defined by `event-contracts.md` |
| `payload_sha256` | 32 bytes | Hash of exact payload bytes |

This contract deliberately does not define reward-mail payload fields.

The relay uses one logical `player_outbox` row per event:

| Column | Type | Rule |
|---|---|---|
| `event_id` | UUID | Primary key |
| `db_shard_id` | `uint32` | Physical placement |
| `aggregate_player_id` | `uint64` | Indexed with status |
| `logical_shard_id` | `uint32` | Hash-derived |
| `event_type` | `uint32` | Immutable |
| `event_contract_version` | `uint32` | Immutable |
| `caused_by_request_id` | UUID | Immutable |
| `created_owner_epoch` | `uint64` | Immutable |
| `created_player_seq` | `uint64` | Immutable |
| `created_at_ms` | `int64` | Immutable |
| `payload` | bytes | Immutable |
| `payload_sha256` | 32 bytes | Immutable |
| `relay_status` | enum | `PENDING=1`, `IN_FLIGHT=2`, `DELIVERED=3` |
| `attempt_count` | `uint32` | Starts at zero |
| `next_attempt_at_ms` | `int64` | Relay scheduling time |
| `claim_owner` | optional string | Relay worker identity |
| `claim_until_ms` | optional `int64` | Expiring claim |
| `last_error_code` | optional string | Bounded diagnostics |
| `delivered_at_ms` | optional `int64` | Present only when delivered |

Creation atomicity:

- the checkpoint blob containing a new pending record and the corresponding `PENDING` row MUST commit in the same database transaction;
- insertion is idempotent by `event_id`;
- an existing row is acceptable only if every immutable field and payload hash matches; otherwise the flush fails as corruption;
- checkpoint CAS MUST NOT reset an existing row's relay status or attempts.

Relay delivery is at-least-once. The downstream consumer MUST deduplicate by `event_id`. A worker atomically claims an eligible `PENDING` row (or expired `IN_FLIGHT` row), increments attempts, publishes, then marks `DELIVERED`; on failure it returns the row to `PENDING` with bounded exponential backoff and jitter.

On Actor activation and before eviction, Outbox reconciliation reads statuses for checkpoint `pending_outbox` IDs. `DELIVERED` records are removed from Actor memory; this increments `checkpoint_revision` and becomes Dirty. A stale pending copy in the blob can therefore only cause an idempotent status lookup, never a second logical event. Delivered rows MUST be retained for at least 24 hours and until no checkpoint can still reference them; exact longer archival retention is operational policy.

## 9. Relational Player checkpoint envelope

One logical `player_checkpoints` row exists per player:

| Column | Type | Rule/index purpose |
|---|---|---|
| `player_id` | `uint64` | Primary key |
| `db_shard_id` | `uint32` | Physical database routing |
| `logical_shard_id` | `uint32` | Indexed; hash-derived |
| `owner_epoch` | `uint64` | Last accepted flush epoch |
| `player_seq` | `uint64` | Client business version |
| `checkpoint_revision` | `uint64` | CAS persistence version |
| `checkpoint_schema_version` | `uint32` | V1 is `1` |
| `checkpoint_blob` | bytes | Deterministic `PlayerCheckpointV1` |
| `checkpoint_sha256` | 32 bytes | Exact blob hash |
| `last_applied_config_version` | `uint64` | Diagnostics/migration index |
| `created_at_ms` | `int64` | Immutable |
| `updated_at_ms` | `int64` | Last successful flush |

Envelope values and blob values MUST match. A mismatch is corruption and MUST stop Actor activation or flush; the system MUST NOT choose one silently.

Coins, inventory, plots, chapter and recent results have no separate mutable relational tables in V1. Operational projections MAY be built asynchronously but are non-authoritative.

## 10. ShardMap committed data

The production Coordinator's majority-committed log/state store is authoritative. The single-node prototype exposes the same logical schema without claiming consensus.

`ShardMapSnapshot` contains:

| Field | Type | Rule |
|---|---|---|
| `shard_count` | `uint32` | V1 exactly `4096` |
| `hash_algorithm_version` | `uint32` | V1 exactly `1` |
| `map_version` | `uint64` | Increments for every committed map change |
| `committed_term` | `uint64` | Consensus term; prototype still persists it |
| `committed_index` | `uint64` | Monotonic commit index |
| `entries` | 4096 `ShardRouteEntry` | One per `shard_id`, sorted |

`ShardRouteEntry` contains:

| Field | Type | Rule |
|---|---|---|
| `shard_id` | `uint32` | `0..4095` |
| `owner_zone_id` | optional string | Required except `UNASSIGNED` |
| `owner_epoch` | `uint64` | Increases on every ownership grant; never reused |
| `route_version` | `uint64` | Increases on every committed change to this entry |
| `state` | enum | `UNASSIGNED=1`, `PREPARING=2`, `ACTIVE=3` |
| `lease_term` | `uint64` | Leader term that granted the lease |
| `lease_id` | UUID | Unique grant identity |
| `lease_expires_at_ms` | `int64` | Coordinator server time |
| `previous_owner_zone_id` | optional string | Migration/failure handoff |
| `transition_id` | optional UUID | Required in `PREPARING` |
| `updated_at_ms` | `int64` | Commit time |

Only committed `ACTIVE` entries with a currently valid lease are routable for writes. `PREPARING` is never routable. A transition commits `PREPARING(epoch=N+1)`, waits for old-owner stop or lease expiry, advances the database fence, loads checkpoints and reaches Ready, then commits `ACTIVE` without changing that prepared epoch. Coordinator restart MUST resume or abandon the same `transition_id` idempotently; an abandoned transition does not reuse its epoch.

## 11. Database shard fence

Each player database shard stores one logical `shard_fences` row per logical shard placed there:

| Column | Type | Rule |
|---|---|---|
| `logical_shard_id` | `uint32` | Primary key within DB shard |
| `owner_zone_id` | string | Current database write owner |
| `owner_epoch` | `uint64` | Current fenced epoch |
| `route_version` | `uint64` | Authorizing committed route |
| `transition_id` | UUID | Authorizing handoff |
| `fenced_at_ms` | `int64` | Database time of CAS |

Fence advance is a database CAS:

```text
accept only a committed PREPARING entry
and requested owner_epoch > stored owner_epoch
and transition_id/owner/route match that entry
→ atomically replace fence
```

Replaying the exact already-applied transition succeeds idempotently. Same/lower epoch with different metadata is rejected. The new Owner MUST NOT become `ACTIVE` if any required database-shard fence cannot advance.

Every checkpoint flush transaction MUST read/lock or equivalently condition on the fence row and require exact `(logical_shard_id, owner_zone_id, owner_epoch)`. A lease check at the Zone and a database fence check solve different failure modes; both are required.

## 12. Dirty queue and batch-write DTO

The Dirty queue is Zone memory, not durable recovery data. It holds at most one coalesced entry per active player:

`DirtyQueueEntry`:

| Field | Type | Rule |
|---|---|---|
| `player_id` | `uint64` | Queue key |
| `db_shard_id` | `uint32` | Batch grouping key |
| `logical_shard_id` | `uint32` | Hash-derived |
| `owner_zone_id` | string | Current owner |
| `owner_epoch` | `uint64` | Captured ownership |
| `latest_checkpoint_revision` | `uint64` | Latest known dirty revision |
| `first_dirty_at_ms` | `int64` | Preserved across coalescing/retries |
| `last_dirty_at_ms` | `int64` | Latest mutation |
| `next_attempt_at_ms` | `int64` | Backoff scheduling |
| `attempt_count` | `uint32` | Consecutive failures |
| `in_flight_revision` | optional `uint64` | At most one flush copy per player |

Marking Dirty updates `latest_checkpoint_revision` and `last_dirty_at_ms` but preserves `first_dirty_at_ms`. It MUST NOT enqueue duplicate per-Actor timers.

The flusher copies immutable DTOs under Actor serialization:

`DirtyBatchWriteRequest` contains `batch_id UUID`, `db_shard_id`, `owner_zone_id`, `created_at_ms`, and repeated `PlayerCheckpointWrite` entries.

`PlayerCheckpointWrite` contains all relational envelope values, deterministic `checkpoint_blob`, `checkpoint_sha256`, and repeated Outbox rows required by that copied checkpoint. Entries are unique and sorted by `player_id`. One database transaction is used per player, so a failed player does not roll back unrelated players in the batch.

`PlayerCheckpointWriteResult` returns `player_id`, `copied_checkpoint_revision`, and one status:

- `APPLIED`;
- `ALREADY_APPLIED` (same revision and hash);
- `STALE_COPY` (database has a higher revision);
- `FENCED` (owner/epoch mismatch);
- `RETRYABLE_FAILURE`;
- `CORRUPT_CONFLICT` (same revision but different hash or Outbox immutable mismatch).

## 13. Flush CAS, retry and stale-write rejection

For each copied player DTO, the writer MUST execute:

1. begin a database transaction;
2. verify the current shard fence exactly matches the DTO's logical shard, owner Zone and epoch;
3. lock/read the Player envelope, if present;
4. verify blob/envelope identities, versions and SHA-256;
5. reject if stored `checkpoint_revision > incoming`;
6. if revisions are equal, return `ALREADY_APPLIED` only when epoch, player sequence and hash all match; otherwise return `CORRUPT_CONFLICT`;
7. require incoming `checkpoint_revision > stored`, incoming `player_seq >= stored.player_seq`, and incoming `owner_epoch >= stored.owner_epoch`;
8. insert/verify all new immutable Outbox rows without changing existing relay state;
9. replace the envelope and blob;
10. commit.

The first checkpoint is accepted only under the current exact fence. Relative to the latest stored checkpoint, a higher epoch does not permit a lower `player_seq` or `checkpoint_revision`; new Owners load that checkpoint and continue both committed-lineage sequences. This does not compare against unflushed values lost with an abnormal old Owner.

After commit, the flusher reports the copied revision to the Actor:

- if Actor `checkpoint_revision == copied`, clear Dirty and the in-flight token;
- if Actor revision is higher, clear only the in-flight token and retain Dirty;
- `STALE_COPY` triggers a fresh copy and is never allowed to overwrite;
- `FENCED` immediately stops writes for that Actor, reports `NOT_OWNER`, and does not retry under the old epoch;
- retryable failures retain Dirty and use bounded exponential backoff with jitter;
- corruption stops Actor writes and pages an operator; it is never auto-repaired by choosing one copy.

Default flush cadence is one second. `oldest_dirty_age > 3s` alerts and starts admission limiting; near 5 seconds the Zone stops new critical writes. Reads may continue. These are health targets, not durability guarantees.

## 14. Lifecycle and recovery guarantees

### 14.1 Normal shutdown, eviction and controlled migration

The Zone MUST:

```text
stop admitting new writes
→ drain the Actor mailbox
→ settle due transitions
→ reconcile Outbox statuses
→ flush through the Actor's final checkpoint_revision
→ verify no Dirty or in-flight write remains
→ release/advance ownership
```

An Actor eligible for normal eviction must also have no connection, subscriber, mailbox work or migration and must satisfy the accepted three-minute idle rule. Flush failure cancels eviction and keeps the Actor resident for retry. A normal shutdown MUST report failure rather than claim success while Dirty state remains.

### 14.2 Abnormal Zone loss

Recovery loads only the latest committed MySQL checkpoint under the new fenced epoch. No Journal is replayed. All state after that checkpoint—including coins, inventory, plots, chapter progress, idempotency results and not-yet-persisted Outbox events—may roll back together. Already committed Outbox rows retain relay progress. A higher `owner_epoch` forces a full client snapshot.

The rollback boundary is exactly the highest transactionally committed `checkpoint_revision`, not the time a command response was sent. V3 does not provide durable exactly-once behavior for unflushed acknowledgements.

## 15. Compatibility and migration

- `schema_version` selects the blob decoder. V1 writers write version `1`.
- Readers MUST reject unknown future schema versions unless a registered migrator exists.
- Compatible additions use new optional Protobuf fields with safe defaults; old field/enum numbers remain reserved forever.
- A semantic change that cannot be represented by optional fields requires a new checkpoint schema version and an explicit deterministic migrator.
- Migration runs after fence acquisition and before Actor admission, preserves unknown fields where meaningful, increments `checkpoint_revision`, updates the envelope/blob atomically, and does not increment `player_seq` unless client-visible business state changes.
- Migration MUST be retryable and idempotent from the same source blob.
- Rolling deployment MUST follow expand-read, then write-new, then contract-old. A writer MUST NOT emit a version that any possible recovery reader cannot decode.
- Shard hash algorithm or shard count changes require a separately accepted migration contract; V1 fixes algorithm version 1 and 4096 shards.
- Event payload compatibility is governed by `event_contract_version` and `event-contracts.md`; relay code treats payload bytes as immutable.

## 16. Acceptance and failure tests

Implementation evidence MUST prove:

1. deterministic checkpoint encode/decode preserves all fields and unknown compatible fields;
2. envelope/blob ID, epoch, sequence, revision, config version and hash mismatches are rejected;
3. inventory enforces 100 types, 300 per stack, no zero stacks and checked coin arithmetic;
4. plot state/presence invariants and fixed-point growth are identical across differently partitioned settlement intervals;
5. recovery works from frozen crop, effect and task fields when historical config entries are disabled;
6. one successful command increments `player_seq` and `checkpoint_revision` once despite changing multiple aggregate parts;
7. terminal business failures persist and replay while leaving `player_seq` unchanged;
8. retention enforces both newest 100 and 24-hour limits deterministically;
9. checkpoint and newly created Outbox row commit or roll back together;
10. relay retries and expired claims may duplicate delivery attempts but consumer deduplication by `event_id` creates one logical mail;
11. replayed reward claim creates neither a second Outbox ID nor a second inventory grant;
12. delivered Outbox reconciliation is safe when the checkpoint still contains a stale pending copy;
13. one-second coalescing writes the latest copied revision and preserves Dirty when the Actor advances during flush;
14. same revision/same hash is idempotent, while same revision/different hash is a corruption failure;
15. old epoch, wrong Zone and stale fence writes are rejected even when their player sequence is higher;
16. a prepared route is not writable, and `ACTIVE` is impossible before successful fence advance and Owner Ready;
17. Coordinator restart resumes or abandons a `PREPARING` transition without epoch reuse;
18. normal shutdown, eviction and controlled migration flush the final revision before ownership release;
19. forced Zone termination restores exactly the latest committed checkpoint and demonstrates the accepted rollback;
20. database delay retains Dirty, raises age metrics, applies limiting, stops critical writes near the threshold and never adds a Journal/WAL fallback;
21. checkpoint migration is deterministic, idempotent and safe across a rolling reader/writer deployment;
22. client snapshots contain no idempotency, Outbox, internal decimal, Dirty or control-plane fields.

## 17. Cross-contract consistency

Design reconciliation found that persisting terminal failures without incrementing `player_seq` requires a separate persistence CAS version. The V3 architecture and idempotency contract now use `checkpoint_revision` for that purpose and keep `(owner_epoch, player_seq)` unchanged as the client state version.

## 18. Friend extension and checkpoint schema V2

Friend gameplay extends the Player aggregate; it does not create a second mutable
authority for player resources. A checkpoint that contains the extension writes
`schema_version = 2`. During activation, a V1 checkpoint is deterministically
migrated by creating `FriendActionState` with 100 apply-pest, 100 catch-pest and
100 help-clean chances, empty reservations and empty receipts. The migration
increments `checkpoint_revision` but not `player_seq`.

`PlayerCheckpointV1` appends:

- `friend_actions`: the three non-negative remaining action-chance counters;
- `friend_reservations`: at most one live record per `interaction_id`;
- `friend_receipts`: at most one record per `(interaction_id, role)`;
- `friend_task_credit_receipts`: at most one record per `relation_id`.

Reservations freeze the action chance or inventory capacity before the remote
owner mutation. A reservation is consumed or released exactly once. Receipts
contain the deterministic result bytes and SHA-256 digest used to answer a
same-ID retry. Interaction collections are serialized by raw `interaction_id`,
then role; task-credit receipts are serialized by raw `relation_id`. Terminal
interaction receipts are retained for at least the 24-hour interaction
retention window.

`FriendTaskCreditReceipt` is part of the credited player's checkpoint and makes
`ApplyFriendTaskCredit(relation_id)` durable across retries and Actor restarts.
It is written atomically with the `TASK_ADD_FRIEND` increment and retained while
the relation exists.

Each planted plot freezes `steal_quantity`, `max_steal_times` and
`protected_owner_yield` together with `base_yield`. `steal_count` and
`stolen_quantity` start at zero. A steal is allowed only when:

```text
steal_count < max_steal_times
base_yield - stolen_quantity - steal_quantity >= protected_owner_yield
```

The frozen values are never recomputed from newer configuration. A successful
steal atomically increments `steal_count` and `stolen_quantity` in the owner
checkpoint.

Task metrics 6, 7 and 8 are respectively `TASK_ADD_FRIEND`,
`TASK_STEAL_CROP` and `TASK_APPLY_PEST_TO_FRIEND`. Friend-link task credit is
deduplicated by `relation_id`; interaction task credit is part of the visitor's
committed receipt.

The Tcaplus social records use these authoritative keys:

- `FriendCodeCurrent(owner_player_id)` and `FriendCodeLookup(code)`;
- `FriendRelation(player_low_id, player_high_id)`, where IDs are sorted;
- `FriendList(player_id)`, which is a repairable projection with persisted slot
  reservations and a hard `active_count + reserved_count <= 100` invariant;
- `FriendLinkSaga(link_id)` and `FriendInteraction(interaction_id)`.

Only an `ACTIVE FriendRelation` authorizes a visit. `FriendList` disagreement
never grants access. All state-machine advances and count changes use Tcaplus
single-record CAS; cross-record completion is recovered by the persisted Saga
and Player checkpoint receipts.
