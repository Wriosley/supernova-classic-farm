---
status: accepted
version: 1
date: 2026-07-30
owners:
  - project-owner
related:
  - data-model.md
  - websocket-protocol.md
  - idempotency-and-errors.md
  - ../architecture/stateful-zone-v3-architecture.md
  - ../architecture/single-player-vertical-loop-business-architecture.md
---

# Reward-Mail Event Contract V1

## 1. Scope and authority

This is the normative English source for the minimum first-stage Outbox event contract. Normative words `MUST`, `MUST NOT`, `SHOULD`, and `MAY` retain their usual requirements meaning. The Chinese file is a complete reading mirror; this English file wins if they differ.

V1 defines exactly one event:

```text
CREATE_REWARD_MAIL = 1
```

The Player Actor creates it only while successfully claiming a chapter reward when one or more reward item quantities cannot fully fit inventory. Coin and every item quantity that fits remain in the Player Actor transaction. Only overflow item quantities become mail attachments. The event MUST contain at least one attachment and MUST NOT contain coin.

This contract defines the logical Protobuf event envelope and payload, immutable delivery identity, ordering, relay and consumer behavior, limits, privacy, observability, and acceptance tests. It does not define generated `.proto`, code, SQL, migrations, broker deployment, mail read/claim/expiry APIs, mail UI, notification UX, or a general mail product.

V3 remains authoritative. The event bus is an asynchronous side-effect path, not a Journal and not a player-recovery source. No consumer result may mutate or reconstruct Player Actor state.

## 2. Relationship to the data model

`data-model.md` owns `PendingOutboxRecord`, the immutable relational `player_outbox` fields, and relay-mutated columns. This contract does not add or rename Outbox columns.

The Actor stores only the deterministic `CreateRewardMailV1` bytes in `PendingOutboxRecord.payload`. At flush time, the corresponding immutable relational row receives the same payload bytes and hash. The relay constructs `EventEnvelopeV1` from that row without changing any immutable value.

The mappings are exact:

| Envelope field | Outbox source |
|---|---|
| `event_id` | `event_id` |
| `event_type` | `event_type` |
| `event_contract_version` | `event_contract_version` |
| `aggregate_player_id` | `aggregate_player_id` |
| `caused_by_request_id` | `caused_by_request_id` |
| `created_owner_epoch` | `created_owner_epoch` |
| `created_player_seq` | `created_player_seq` |
| `created_at_ms` | `created_at_ms` |
| `payload` | `payload` |
| `payload_sha256` | `payload_sha256` |

`relay_status`, `attempt_count`, `next_attempt_at_ms`, `claim_owner`, `claim_until_ms`, `last_error_code`, and `delivered_at_ms` are relay state and MUST NOT appear in the published event.

## 3. Scalar and compatibility rules

| Meaning | Logical Protobuf type | Rule |
|---|---|---|
| UUID | `bytes` | Exactly 16 non-zero bytes in RFC 4122 byte order |
| Player ID / owner epoch / player sequence / configuration version | `uint64` | Non-zero where required; checked without signed conversion |
| Event contract/schema version | `uint32` | Non-zero supported version; V1 is `1` |
| Domain/config ID | `uint32` | Zero is invalid |
| Quantity | `uint32` | Greater than zero; checked addition |
| Time | `int64` | Unix milliseconds UTC from server time; greater than zero |
| Hash | `bytes` | Exactly 32 raw SHA-256 bytes |
| Localization key | `string` | Lowercase ASCII dot-separated key, 1–96 bytes |

Published field tags and enum numbers MUST never be reused. Removed numbers remain reserved. A compatible V1 addition may use a new optional field whose absence has a safe meaning. A semantic change that changes required validation, attachment meaning, recipient authority, or deduplication requires `event_contract_version = 2` and a new payload message.

V1 writers MUST emit only fields defined here. Consumers MUST ignore unknown compatible fields and preserve them if they relay or store the original event bytes. Consumers MUST reject an unsupported `event_contract_version`; they MUST NOT guess a decoder.

## 4. Stable enums

### 4.1 `EventType`

| Value | Name | Rule |
|---:|---|---|
| 0 | `EVENT_TYPE_UNSPECIFIED` | Invalid |
| 1 | `CREATE_REWARD_MAIL` | V1 reward overflow event |

Values 2–99 are reserved for future accepted event types.

### 4.2 `ConsumeResultCode`

| Value | Name | Meaning |
|---:|---|---|
| 0 | `CONSUME_RESULT_UNSPECIFIED` | Invalid |
| 1 | `APPLIED` | Mail and attachments were committed |
| 2 | `ALREADY_APPLIED` | Same `event_id` and immutable fingerprint already committed |
| 3 | `RETRYABLE_FAILURE` | No consumer transaction committed; retry is allowed |
| 4 | `CORRUPT_CONFLICT` | Same `event_id` exists with different immutable content |
| 5 | `INVALID_EVENT` | Deterministically malformed, unsupported, or policy-invalid event |

`APPLIED` and `ALREADY_APPLIED` are successful consumption. `CORRUPT_CONFLICT` and `INVALID_EVENT` are poison results, not successful business application.

## 5. Logical `EventEnvelopeV1`

The published Protobuf message is logically:

| Tag | Field | Type | Rule |
|---:|---|---|---|
| 1 | `event_contract_version` | `uint32` | Required; exactly `1` |
| 2 | `event_id` | `bytes` | Required UUID; global immutable delivery identity |
| 3 | `event_type` | `EventType` | Required; exactly `CREATE_REWARD_MAIL` |
| 4 | `aggregate_player_id` | `uint64` | Required; Player Actor and recipient identity |
| 5 | `caused_by_request_id` | `bytes` | Required UUID of `CLAIM_CHAPTER_REWARD` |
| 6 | `created_owner_epoch` | `uint64` | Required; Actor epoch at creation |
| 7 | `created_player_seq` | `uint64` | Required; business version after reward claim |
| 8 | `created_at_ms` | `int64` | Required server creation time |
| 9 | `payload` | `bytes` | Deterministic `CreateRewardMailV1` bytes |
| 10 | `payload_sha256` | `bytes` | SHA-256 of the exact bytes in tag 9 |

Tags 11–19 are reserved for compatible common metadata. Tags 20–99 are reserved for future accepted envelope expansion.

The envelope tuple MUST agree with the immutable Outbox row. The relay MUST verify `SHA-256(payload) == payload_sha256` and validate the supported payload before publication. A mismatch is corruption, never a retryable broker error.

## 6. Logical `CreateRewardMailV1`

The payload has these exact fields:

| Tag | Field | Type | Rule |
|---:|---|---|---|
| 1 | `recipient_player_id` | `uint64` | Required; equals envelope `aggregate_player_id` |
| 2 | `attachments` | repeated `RewardMailAttachmentV1` | Required; 1–100 entries, unique and sorted by `item_id` |
| 3 | `subject_text_key` | `string` | Required; exactly `mail.chapter_reward.subject` |
| 4 | `body_text_key` | `string` | Required; exactly `mail.chapter_reward.body` |
| 5 | `source` | `RewardMailSourceV1` | Required |

Tags 6–19 are reserved for compatible payload expansion. The payload contains no display text, locale, coin, sender account, Session, network address, or internal service address.

### 6.1 `RewardMailAttachmentV1`

| Tag | Field | Type | Rule |
|---:|---|---|---|
| 1 | `item_id` | `uint32` | Required stable item identity |
| 2 | `quantity` | `uint32` | Required overflow quantity, greater than zero |

Tags 3–9 are reserved. An item appears at most once. If reward configuration contains duplicate item entries, the Actor MUST combine them with checked arithmetic before inventory allocation and event creation.

Inventory allocation is deterministic in ascending `item_id` order:

1. combine configured reward item quantities by `item_id`;
2. for each item, place as much as allowed by the accepted inventory type and stack limits;
3. put only the remaining positive quantity in `attachments`;
4. sort attachments by ascending `item_id`.

No attachment may duplicate an amount already credited to inventory. A reward with no overflow creates no event.

### 6.2 `RewardMailSourceV1`

| Tag | Field | Type | Rule |
|---:|---|---|---|
| 1 | `chapter_id` | `uint32` | Required claimed chapter |
| 2 | `chapter_config_version` | `uint64` | Required config snapshot/frozen chapter version used by the claim |
| 3 | `request_id` | `bytes` | Required UUID; equals envelope `caused_by_request_id` |

Tags 4–9 are reserved. Chapter and request metadata is audit and localization context; it grants no authority to repeat the reward.

## 7. Creation and player-command meaning

Within one Actor mailbox execution, reward claim MUST:

```text
validate claim and idempotency
→ credit coin
→ allocate fitting item quantities to inventory
→ create one event for all positive overflow item quantities
→ mark the claimed chapter and activate the next chapter
→ increment player_seq exactly once
→ store the terminal idempotency result and event_id
→ mark the checkpoint Dirty
```

A replay of the same successful player request returns the original result and event ID and creates no additional event. A new request against an already claimed chapter fails under `idempotency-and-errors.md`.

The client-facing `items_pending_mail` receipt means that one pending Outbox event was recorded atomically with the Actor's reward-claim state. It never means that Mail Service has created, displayed, or delivered a mail.

Under accepted V3 asynchronous Dirty writeback, the command can be acknowledged before that checkpoint and relational Outbox row commit to MySQL. Therefore an acknowledged but unflushed claim, including its pending event, may roll back after abnormal Zone loss. The event becomes database-durable only when the checkpoint and `PENDING` Outbox row commit together. No implementation or user text may describe the pre-flush acknowledgement as durable across abnormal Zone loss.

## 8. Deterministic serialization and immutable fingerprint

The Actor MUST validate and normalize the payload, sort attachments, then encode `CreateRewardMailV1` using the selected Protobuf runtime's deterministic mode. V1 payload writers emit fields in canonical tag order, emit no unknown fields, use minimal varints, and omit no required field. There are no maps.

`payload_sha256` is:

```text
SHA-256(exact deterministic CreateRewardMailV1 bytes)
```

The relay MUST publish the exact stored payload bytes; decode-and-re-encode publication is forbidden.

For consumer deduplication, the immutable event fingerprint is:

```text
SHA-256(
  exact deterministic EventEnvelopeV1 bytes
)
```

Before computing it, the consumer verifies the envelope is minimally valid and the payload hash matches. The consumer MUST store either the exact immutable envelope bytes or this fingerprint together with `event_id`. It MUST NOT use broker headers, delivery attempt, partition, offset, relay timestamps, or mutable Outbox state in the fingerprint.

## 9. Size limits

- Encoded `CreateRewardMailV1` payload: at most 48 KiB.
- Encoded `EventEnvelopeV1`: at most 64 KiB.
- Attachments: 1–100 unique entries.
- Each localization key: at most 96 UTF-8 bytes and restricted as in section 3.
- Relay and consumer MUST enforce limits before unbounded allocation.

An event exceeding a limit is an invariant violation. It MUST NOT be truncated, split into multiple logical mails, or silently drop attachments.

## 10. Partitioning and ordering

The broker partition/order key is exactly the 8-byte unsigned big-endian encoding of `aggregate_player_id`. It is not UTF-8 decimal text and is not a secret.

All events for one Player Actor therefore use the same key under a fixed topic partition configuration. `created_player_seq`, then `event_id` bytes, provides a stable diagnostic order. V1 consumers MUST NOT require global ordering, contiguous sequences, or the presence of every player sequence. A partition-count or key-algorithm change requires an accepted migration because it can disturb per-player transport order.

Deduplication relies on `event_id`, not on order, offset, epoch, player sequence, or request ID.

## 11. Relay semantics and acknowledgement boundary

Relay delivery is at-least-once:

1. atomically claim an eligible `PENDING` row, or an expired `IN_FLIGHT` row, using the mutable columns from `data-model.md`;
2. increment `attempt_count`;
3. validate immutable fields, payload hash, payload contract, and size;
4. publish the exact envelope with the player partition key;
5. wait for the broker's durable publish acknowledgement;
6. only then set `relay_status = DELIVERED`, clear the claim, and set `delivered_at_ms`.

Broker acknowledgement is the relay boundary. It does not mean that Mail Service has consumed the event or that a player can read or claim a mail. A relay crash after broker acknowledgement but before `DELIVERED` may republish the same `event_id`.

Retryable broker/network failure returns the row to `PENDING`, clears the claim, sets bounded `last_error_code`, and schedules `next_attempt_at_ms` with exponential backoff and full jitter. Defaults are base 1 second, factor 2, cap 5 minutes. There is no finite retry count for a valid pending reward event; after 20 attempts or 1 hour since creation, whichever comes first, an alert is mandatory while capped retries continue.

An immutable-row mismatch, payload hash mismatch, unsupported version, invalid payload, or size violation is relay poison. The relay MUST NOT publish it or mark it `DELIVERED`. Because `data-model.md` defines no terminal Outbox status, V1 records a bounded poison code in `last_error_code`, sets `next_attempt_at_ms` to the maximum supported time to prevent automatic reclaim, emits a sanitized dead-letter diagnostic copy, and pages an operator. Requeue requires explicit repair and audit; immutable event data MUST NOT be edited in place.

## 12. Mail Service consumption and atomicity

Mail Service MUST deduplicate by `event_id`. In one local database transaction it MUST:

1. lock or insert the consumer deduplication record for `event_id`;
2. if absent, validate the envelope and payload, create exactly one mail header, create all attachments, and store the immutable event fingerprint;
3. commit the mail header, every attachment, and deduplication record atomically;
4. acknowledge the broker message only after commit.

No partial attachment set may become visible. A transaction failure creates no mail, no attachments, and no deduplication success record.

If `event_id` already exists:

- equal immutable fingerprint returns `ALREADY_APPLIED`; it is success and the broker message is acknowledged;
- different immutable fingerprint returns `CORRUPT_CONFLICT`; no mail data is changed, a security/corruption alert and sanitized dead-letter record are emitted, and the poison broker message is acknowledged to stop an infinite retry loop.

For a first delivery:

- committed transaction returns `APPLIED` and is acknowledged;
- transient dependency/transaction failure returns `RETRYABLE_FAILURE` and is not acknowledged;
- malformed, unsupported, hash-invalid, recipient-mismatched, or policy-invalid content returns `INVALID_EVENT`, emits a sanitized dead-letter record, creates no mail, and is acknowledged to stop an infinite retry loop.

The dead-letter record MUST retain `event_id`, event type/version, payload hash, reason code, broker topic/partition/offset where available, and first/last observation times. It MUST NOT contain raw payload, player-visible text, credentials, tokens, cookies, account name, or internal stack trace.

The created mail is addressed only to `recipient_player_id`. The consumer MUST NOT infer another recipient from headers or localization metadata. Future read, claim, expiry, deletion, notification, and attachment-redemption semantics are outside this contract.

## 13. Privacy, logging, and observability

The event contains the minimum player data needed for delivery: recipient ID, item IDs/quantities, chapter/config metadata, request ID, event ID, versions, and server time. It MUST NOT contain account name, password, Session/cookie/ticket/CSRF values, IP address, device identifier, free-form user text, access token, internal host, or stack trace.

Logs and traces MAY contain event ID, event type/version, request ID, owner epoch, player sequence, payload hash, attempt count, result code, duration, and a keyed/hash-reduced player identifier. Production logs SHOULD avoid raw player ID. Raw payload and attachment lists MUST NOT be placed in normal logs or metric labels.

Required low-cardinality metrics include:

- Outbox pending and in-flight counts;
- oldest pending age;
- relay publish attempts, acknowledgements, failures, and latency;
- consumer applied, duplicate, retryable, corrupt, and invalid counts;
- consumer transaction latency;
- dead-letter count by bounded reason code;
- end-to-end age from `created_at_ms` to consumer commit.

Alerts cover oldest pending age, sustained relay failures, attempt threshold, poison/dead-letter events, payload-hash mismatch, immutable mismatch, and consumer retry backlog. Metric labels MUST NOT include event ID, request ID, player ID, item ID, payload hash, or error text.

## 14. Acceptance and failure tests

Implementation evidence MUST prove:

1. deterministic payload and envelope encoding produces identical bytes and SHA-256 values across supported Go and consumer runtimes;
2. all stable tags/enums round-trip and unsupported versions fail closed;
3. UUID, scalar, localization-key, required-field, sorting, uniqueness, and size validation is enforced before allocation or publication;
4. coin and fitting items stay in Actor state while only exact overflow item quantities appear as sorted attachments;
5. no-overflow reward creates no event, while multiple overflow items create one event;
6. duplicate configured item entries combine with checked arithmetic and never create duplicate attachments;
7. reward state, idempotency result, pending checkpoint record, and relational Outbox row commit or roll back at their documented Actor/flush boundaries;
8. same player-request replay grants no additional coin/item, creates no second event ID, and returns the original receipt;
9. a successful claim receipt says pending mail only and never claims that Mail Service has created or delivered it;
10. abnormal Zone loss before flush demonstrates the accepted rollback of claim state and its pending event; committed Outbox rows survive and retain relay progress;
11. relay uses the exact 8-byte player key and exact stored payload bytes;
12. publish failure retries with the same event ID and immutable content; crash after broker acknowledgement can duplicate delivery;
13. relay marks `DELIVERED` only after broker durable acknowledgement and never after a local send attempt alone;
14. payload/hash/immutable corruption is not published or silently repaired and reaches the sanitized relay dead-letter path;
15. first consumer delivery atomically creates one mail, every attachment, and one deduplication record before acknowledgement;
16. consumer transaction failure exposes no partial mail and causes retry;
17. same event ID and fingerprint returns successful `ALREADY_APPLIED` without creating another mail;
18. same event ID with different immutable fingerprint returns `CORRUPT_CONFLICT`, changes no mail data, alerts, dead-letters, and terminates poison redelivery;
19. malformed, unsupported, oversized, recipient-mismatched, and hash-invalid events create no mail and follow `INVALID_EVENT` poison handling;
20. per-player transport order is preserved under the fixed partition configuration, while consumer correctness does not depend on global or contiguous ordering;
21. logs, traces, metrics, alerts, and dead-letter records contain no prohibited secrets, raw payload, free-form text, or high-cardinality metric labels;
22. no test uses the event bus as a Player Actor Journal or recovery source.

## 15. Cross-contract consistency

`websocket-protocol.md` now follows the same V3 boundary as this contract: `items_pending_mail` means the pending event was recorded atomically in Actor state, not that it is already database-durable or that Mail Service created a mail.

The event becomes database-durable only when the asynchronous checkpoint/Outbox transaction commits. Making reward claim success wait for that commit would be a new synchronous durability exception and requires a separately accepted decision.
