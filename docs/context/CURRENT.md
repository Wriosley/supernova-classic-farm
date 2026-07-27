---
status: active
updated: 2026-07-27
---

# Current Handoff

## Resume here

The 30-million-DAU production target is now the architecture starting point, not a final optional capacity chapter. Read:

1. `docs/context/PROJECT.md`;
2. `docs/architecture/target-30m-dau-architecture.md`;
3. `docs/decisions/ADR-0002-target-scale-hybrid-architecture.md`;
4. `docs/architecture/architecture.md` and the module companion.

Do not start implementation until the owner has reviewed the written target architecture. Redis and Kafka-compatible messaging remain product candidates, not accepted technology decisions.

## Direction change on 2026-07-27

- The mentor requested a design that directly explains how a 30-million-DAU service would be implemented.
- The earlier documents mainly covered business correctness and treated distribution as a future evidence-driven evolution.
- The owner chose a dual delivery: a complete production target architecture plus a scaled-down distributed prototype and load/failure evidence.
- ADR-0001 remains the earliest local business-loop decision but no longer represents the production target.
- ADR-0002 proposes a hybrid architecture centered on stateless player Zone shards; it remains proposed until written review.

## Confirmed capacity assumptions

- China mainland, single region, three availability zones; no global active-active design.
- 30 million DAU.
- Four sessions per active player per day, 15 minutes per session.
- 1.25 million average concurrent users and 3.75 million normal peak concurrent users.
- About 60 external requests per player per day, approximately 2:1 reads to writes.
- About 20,833 average external QPS and 62,500 normal peak external QPS.
- Shared-farm WebSocket connections: 750,000 normal and 1.125 million extreme.
- Thirty-second heartbeat; heartbeats stay in realtime gateway memory.
- Online history retains 30 days, then archives asynchronously.

## Confirmed target architecture

- API gateway plus trusted `player_id` routing.
- Stateless Zone instances; player truth does not live only in Go process memory.
- 1,024 logical player shards mapped to expandable physical MySQL clusters.
- Player farm, asset, idempotency, and Outbox data are co-sharded.
- MySQL is the final truth; only active/hot snapshots are cached.
- Account, friend, task, mail, realtime, and archive workloads have independent boundaries.
- Normal players have at most 200 friends.
- Task progress and rewards are reliable asynchronous flows; task failures do not roll back core farm actions.
- Cross-shard steal commits the owner's theft fact first, then delivers the thief reward asynchronously.
- A full thief inventory redirects the reward to a non-expiring system reward mail.
- HTTP handles commands and authoritative snapshots; WebSocket pushes committed changes.
- Core HTTP target is 99.95%; realtime target is 99.9%; normal asynchronous reward target is 99% within five seconds and eventual delivery.

## Candidate technologies, not accepted

- Redis for hot snapshots, Session, rate limits, and short-lived state.
- Kafka-compatible durable event log for partition ordering, consumer groups, retention, and replay.
- Alternatives to compare include Memcached/database reads for caching and RabbitMQ, NATS JetStream, Redis Streams, or Pulsar for messaging.
- Specific products, partition counts, retention, and single-instance capacities require prototype evidence.

## Distributed prototype sequence

1. Single-player local transactions, `request_id`, and Outbox.
2. Two stateless Zone instances, logical routing, and two simulated MySQL shards.
3. Asynchronous task processing, duplicate messages, and backlog catch-up.
4. Cross-shard stealing, asynchronous reward, full-inventory mail fallback, and idempotent mail claims.
5. Two realtime gateways, one room service, three clients, version gaps, and reconnect recovery.
6. HTTP, messaging, WebSocket, Redis-degradation, and process-failure evidence.

## Product code state

- No backend or frontend product code exists yet.
- No database schema, exact HTTP DTO, message broker, or cache product has been accepted.
- No runtime, correctness, availability, or performance claim has been verified.
- Tests and evidence do not yet exist.

## Next actions

1. Owner reviews `docs/architecture/target-30m-dau-architecture.md` and requests corrections.
2. Owner explains Kafka Topic/Partition/Consumer Group/offset and why Redis is a cache rather than truth.
3. Compare the smallest viable local Kafka-compatible, NATS JetStream, and RabbitMQ prototypes before accepting the broker.
4. After written-spec approval, create the detailed six-stage implementation plan.

## Verification state

- Business and target-architecture discussion: recorded.
- Written target architecture and ADR: drafted in this change.
- Technology products: candidates only.
- Product behavior and capacity: not implemented or measured.
