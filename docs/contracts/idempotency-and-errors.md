---
status: accepted
version: 2
date: 2026-08-06
owners:
  - project-owner
related:
  - websocket-protocol.md
  - ../architecture/single-player-vertical-loop-business-architecture.md
---

# Idempotency and Error Contract V1

## 1. Purpose

This contract makes retries safe and gives H5 stable behavior for failures. It distinguishes:

- a request that definitely did not execute;
- a request whose outcome is unknown to GateSvr;
- a terminal business result;
- a connection that can no longer carry trusted commands.

## 2. Error shape

A failed RESPONSE contains one `Error`:

| Field | Type | Meaning |
|---|---|---|
| `code` | `ErrorCode` | Stable machine-readable enum |
| `params` | repeated key/value string | Values for client localization; never trusted as commands |
| `retryable` | `bool` | Whether automatic retry with the same request is allowed |
| `retry_after_ms` | optional `uint32` | Minimum delay when retryable |
| `latest_shop_entry` | optional `ShopEntryView` | Present for a changed quote |
| `current_plot` | optional `PlotView` | Present for a plot-state conflict when available |
| `debug_message` | optional `string` | Development/logging only; H5 MUST NOT show directly to users |

User-facing text is selected by H5 from `code + params`. The server MUST NOT rely on changing human-readable text as protocol semantics.

Newly executed errors that reach a Player Actor include the Actor's current `state_version`. A replayed terminal Actor error returns the original stored `state_version` as defined in section 9; it does not substitute the Actor's later current version. Envelope, authentication and routing errors that never reach an Actor omit it.

## 3. Stable error codes

### 3.1 Protocol and connection

| Value | Code | Client behavior |
|---:|---|---|
| 0 | `ERROR_UNSPECIFIED` | Treat as client defect; log |
| 100 | `INVALID_ARGUMENT` | Fix request; do not retry automatically |
| 101 | `UNKNOWN_ACTION` | Client/server contract mismatch; keep connection |
| 102 | `UNSUPPORTED_PROTOCOL_VERSION` | Upgrade/reload client; connection closes |
| 103 | `UNAUTHENTICATED` | Obtain a new WS ticket; connection closes |
| 104 | `FORBIDDEN` | Do not retry automatically |
| 105 | `REQUEST_ID_CONFLICT` | Generate a new ID only after correcting intent |
| 106 | `RATE_LIMITED` | Retry same request after `retry_after_ms` if it was not admitted |

Malformed Protobuf, oversized frames and envelope combinations that cannot be correlated may close the connection without a RESPONSE.

### 3.2 Transient service errors

| Value | Code | Client behavior |
|---:|---|---|
| 200 | `SERVICE_UNAVAILABLE` | Same-ID retry with backoff |
| 201 | `SERVER_BUSY` | Same-ID retry after `retry_after_ms` |
| 202 | `REQUEST_OUTCOME_UNKNOWN` | Same-ID retry; never generate a new ID |
| 203 | `CONFIG_UNAVAILABLE` | Same-ID retry with backoff |
| 204 | `ZONE_MIGRATING` | Controlled Shard migration is in progress; retry the same request ID after delay |
| 205 | `ZONE_UNAVAILABLE` | Route is temporarily paused due to `SUSPECT`/`DEAD` handling; retry the same request ID after delay |
| 206 | `ZONE_WARMING_UP` | Actor activation admission queue is full or timed out; retry the same request ID after delay |
| 207 | `STORAGE_UNAVAILABLE` | Zone is alive but its recovery-store operation failed; retry the same request ID and never report the Zone dead from this code |

`REQUEST_OUTCOME_UNKNOWN` means GateSvr cannot prove whether the Actor executed the command. It is the strongest reason to preserve the original `request_id`.

All four routing lifecycle errors, `ZONE_MIGRATING`, `ZONE_UNAVAILABLE`,
`ZONE_WARMING_UP` and `STORAGE_UNAVAILABLE`, are retryable. When
`retry_after_ms` is present, the client MUST wait at least that long before
retrying. If command execution might have started, the retry MUST preserve the
original `request_id` and semantic payload.

### 3.3 Shop and configuration

| Value | Code | Meaning |
|---:|---|---|
| 300 | `SHOP_ENTRY_NOT_FOUND` | Unknown entry |
| 301 | `SHOP_ENTRY_DISABLED` | Entry no longer available |
| 302 | `PRICE_CHANGED` | Quote version changed; response carries latest quote |
| 303 | `CONFIG_ENTRY_DISABLED` | Seed, crop, fertilizer or sell rule is no longer available |

After `PRICE_CHANGED`, H5 updates the quote, asks the player to confirm, then sends a new business intent with a new `request_id`.

### 3.4 Assets and inventory

| Value | Code | Meaning |
|---:|---|---|
| 400 | `INSUFFICIENT_COINS` | Balance cannot cover total price |
| 401 | `INVENTORY_TYPE_LIMIT` | Adding a new item type would exceed 100 types |
| 402 | `INVENTORY_STACK_LIMIT` | Result would exceed 300 of one item |
| 403 | `ITEM_NOT_OWNED` | Required seed or fertilizer is absent |
| 404 | `INSUFFICIENT_ITEM_QUANTITY` | Sell quantity exceeds inventory |
| 405 | `ITEM_NOT_SELLABLE` | Item has no active sell rule |

Harvest uses the same inventory limit codes and remains wholly unapplied on failure.

### 3.5 Farm

| Value | Code | Meaning |
|---:|---|---|
| 500 | `PLOT_NOT_FOUND` | Unknown plot |
| 501 | `PLOT_STATE_CONFLICT` | Action is illegal for current state |
| 502 | `FERTILIZER_ALREADY_ACTIVE` | Existing fertilizer has not expired |
| 503 | `CROP_NOT_MATURE` | Harvest requested before maturity |

When safe, `PLOT_STATE_CONFLICT` includes the current `PlotView`, allowing H5 to repair that plot without a full snapshot.

### 3.6 Tasks

| Value | Code | Meaning |
|---:|---|---|
| 600 | `CHAPTER_NOT_FOUND` | Unknown chapter |
| 601 | `CHAPTER_NOT_CLAIMABLE` | Tasks are incomplete or chapter is inactive |
| 602 | `CHAPTER_REWARD_ALREADY_CLAIMED` | A different request tries to claim an already claimed chapter |

A retry of the original successful claim request returns the original successful result, not `CHAPTER_REWARD_ALREADY_CLAIMED`.

### 3.7 Friends, visits and interactions

| Value | Code | Meaning |
|---:|---|---|
| 700 | `FRIEND_CODE_NOT_FOUND` | No lookup record exists for the supplied code |
| 701 | `FRIEND_CODE_EXPIRED` | The code is no longer redeemable |
| 702 | `CANNOT_FRIEND_SELF` | Code owner and redeemer are the same player |
| 703 | `FRIEND_LIMIT_REACHED` | Either player's active plus reserved slots reached 100 |
| 704 | `NOT_MUTUAL_FRIEND` | No active authoritative relation exists |
| 705 | `VISIT_NOT_FOUND` | The visit ID is unknown or belongs to different identities |
| 706 | `VISIT_EXPIRED` | The visit lease has expired |
| 720 | `PLOT_NOT_ELIGIBLE` | Plot state cannot accept the requested friend action |
| 721 | `PEST_ALREADY_PRESENT` | A growing plot already has an active pest |
| 722 | `PEST_SOURCE_FORBIDDEN` | A player attempted to catch their own pest |
| 723 | `STEAL_NOT_AVAILABLE` | Frozen count or protected-yield rule rejects stealing |
| 724 | `INSUFFICIENT_ACTION_CHANCE` | Visitor has no chance for the requested action |
| 725 | `INVENTORY_CAPACITY_EXCEEDED` | Visitor cannot reserve the steal reward |
| 726 | `INTERACTION_OUTCOME_UNKNOWN` | Saga outcome must be recovered with the same request ID |

Codes 700–706 and 720–725 are terminal for the current intent. Code 726 is
retryable only with the same ID and semantic payload.

## 4. Which failures close the connection

Close after:

- invalid/expired authentication;
- unsupported protocol version;
- Session revoked by a newer login;
- oversized message;
- malformed envelope that prevents safe parsing;
- persistent invalid traffic or abuse.

Keep open after every normal business error, transient service error that can be answered safely, unknown action and invalid business argument.

## 5. Request ID rules

- H5 generates a canonical lowercase UUID before first send.
- Every REQUEST has an ID for correlation.
- Only write actions use the Player Actor idempotency window.
- A retry MUST preserve `request_id`, `action`, `target_player_id` and all semantic payload fields.
- A changed intent MUST use a new ID.
- GateSvr MUST preserve the ID across `NOT_OWNER` rerouting.
- Services MUST log the request ID, but MUST NOT use it as a secret or authentication token.

Idempotency scope is:

```text
(caller_player_id, request_id)
```

The stored fingerprint additionally covers action, target and semantic payload.

## 6. Canonical payload fingerprint

The Actor computes the fingerprint after Protobuf validation from:

```text
fingerprint_schema_version
+ protocol_version
+ action enum
+ target_player_id
+ action-specific known semantic fields in documented order
```

Examples:

```text
BUY_SEEDS:
shop_entry_id, quantity, expected_price_version

SELL_CROP:
crop_item_id, expected_price_version, amount branch, quantity when present

REDEEM_FRIEND_CODE:
normalized code

ENTER/HEARTBEAT/EXIT_FRIEND_FARM:
owner_player_id, visit_id when present

FRIEND PLOT ACTION:
owner_player_id, visit_id, plot_id, pest_id when present
```

The fingerprint is not based on client-supplied hash text. Unknown compatible Protobuf fields do not change V1 semantics and are excluded by the V1 fingerprint schema.

## 7. Actor execution order

For a write request:

```text
validate envelope and authentication at Gate
→ route to target Actor
→ look up (caller_player_id, request_id)
→ existing + same fingerprint: return stored result with replayed=true
→ existing + different fingerprint: REQUEST_ID_CONFLICT
→ new request: pin server time and one config snapshot
→ validate business preconditions
→ execute or produce terminal business failure
→ store terminal result
→ respond
```

Successful execution:

```text
apply all business changes atomically
→ update task progress
→ player_seq++
→ store response, fingerprint and Outbox
→ checkpoint_revision++
→ mark Dirty
```

Terminal Actor business failure:

```text
do not change coins, inventory, plots, tasks or player_seq
→ store failure result in idempotency metadata
→ checkpoint_revision++
→ mark Actor Dirty
```

Caching terminal failures prevents a lost failure response from later becoming a success under the same ID after player state changes. The player may retry a corrected/new intent only with a new ID.

`checkpoint_revision` is the persistence CAS version defined by `data-model.md`. It is not part of client `state_version`. Any checkpoint-content mutation increments it even when `player_seq` remains unchanged.

Failures before Actor admission—malformed message, authentication failure, rate rejection, unavailable route—are not stored in the Player Actor.

## 8. Stored result

Each retained write result stores:

| Field | Meaning |
|---|---|
| `request_id` | Idempotency key component |
| `action` | Original action |
| `payload_fingerprint` | Canonical semantic fingerprint |
| `completed_at_ms` | Server completion time |
| `success` | Terminal outcome |
| `state_version` | Original result version |
| `response_payload` | Compact original typed receipt and patch |
| `error` | Original terminal business error when failed |
| `outbox_ids` | IDs created by this command, if any |

Retention is at most the newest 100 write results per player and at most 24 hours. Entries older than 24 hours are removed; if more than 100 remain, oldest entries are removed.

The retained response MUST stay below the protocol message limit. Full Player Snapshots are query responses and are never stored in the idempotency window.

## 9. Replay behavior

Same ID and same fingerprint:

- returns the first terminal success or failure;
- sets envelope `replayed = true`;
- returns the original `state_version`, receipt, patch and error;
- does not execute validation, deduct assets, advance tasks, create Outbox or increment `player_seq` again.

The client displays the receipt but applies its state patch only under normal version rules:

- original seq is older/equal to local: ignore patch;
- exactly local + 1: apply;
- gap: request full snapshot;
- different epoch: replace with full snapshot.

If an ID has expired from the retention window, the server cannot recognize it. Clients MUST NOT automatically retry write intents older than 24 hours; they must refresh state and ask the player to initiate a new intent.

## 10. Retry policy

### Automatic same-ID retry

Allowed only for:

- connection loss before a correlated response;
- client timeout;
- `SERVICE_UNAVAILABLE`;
- `SERVER_BUSY`;
- `REQUEST_OUTCOME_UNKNOWN`;
- `CONFIG_UNAVAILABLE`;
- `ZONE_MIGRATING`;
- `ZONE_UNAVAILABLE`;
- `ZONE_WARMING_UP`;
- `STORAGE_UNAVAILABLE`;
- `RATE_LIMITED` when the response states the request was not admitted.

Honor `retry_after_ms` when supplied; otherwise use bounded exponential backoff
with jitter. Stop when the HTTP Session expires or the intent is older than 24
hours.

### Refresh and create a new ID

Required for:

- `PRICE_CHANGED` after player confirms latest price;
- a corrected invalid quantity;
- a new action after `PLOT_STATE_CONFLICT`;
- any changed payload;
- retry after the idempotency retention window.

### Never automatic

Do not automatically repeat:

- insufficient coins or items;
- inventory limits;
- active fertilizer;
- immature crop;
- task not claimable;
- forbidden action.

## 11. Dirty recovery consequence

The idempotency window is part of the same Dirty checkpoint as player state and Outbox.

Dirty persistence orders checkpoint contents by `checkpoint_revision`; WebSocket state ordering remains `(owner_epoch, player_seq)`.

After abnormal Zone loss, both the latest business mutation and its idempotency result may roll back together to the last MySQL checkpoint. A new `owner_epoch` forces the client to accept a full snapshot.

V3 does not claim durable exactly-once execution across an unflushed abnormal failure. It provides:

- exactly-once behavior within the active Actor and retained checkpoint;
- atomic recovery of player state, retained result and pending Outbox;
- an explicit bounded-loss window accepted by ADR-0006.

Cross-player friend interactions are the exception to asynchronous Dirty
acknowledgement. The visitor reservation is synchronously checkpointed before
the owner mutation; the owner receipt is synchronously checkpointed with that
mutation; and the visitor completion receipt is synchronously checkpointed
with resource consumption, reward and task credit. `FriendInteraction` records
orchestrate these checkpoints but never replace them as resource authority.

The WebSocket request UUID, raw-byte `interaction_id`, Tcaplus Saga key and both
Player receipts identify one intent. A same-ID/different-fingerprint request is
`REQUEST_ID_CONFLICT`. A timeout after any durable step is
`INTERACTION_OUTCOME_UNKNOWN`; the client must keep the ID until reconciliation
returns the stored terminal result.

`FriendInteraction` persists every input needed to resume execution, including
`action`, `plot_id` and `pest_id` when present; the request digest detects any
changed retry. Owner-enter calls also forward the original request ID so a
timeout cannot create duplicate visit leases or presence notifications.

## 12. Required tests

1. same ID and payload executes each write once;
2. same ID with a changed field returns `REQUEST_ID_CONFLICT`;
3. successful replay returns the original version and receipt;
4. failed business result is replayed even after Actor state later changes;
5. query request IDs are correlated but not retained;
6. `NOT_OWNER` reroute preserves ID and does not duplicate execution;
7. price refresh requires a new ID after player confirmation;
8. claim reward replay creates no duplicate inventory or mail Outbox;
9. retention enforces both 100-result and 24-hour bounds;
10. epoch recovery restores player state, idempotency records and Outbox from one checkpoint;
11. a same-ID friend action cannot consume two chances or mutate the owner twice;
12. recovery from every interaction Saga boundary converges to one committed result or a released reservation;
13. `INTERACTION_OUTCOME_UNKNOWN` retries preserve ID and reject changed payloads.
