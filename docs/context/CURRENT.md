---
status: active
updated: 2026-07-28
---

# Current Handoff

## Resume here

The owner has selected V2 as the current production-target strategy. A new AI should read, in order:

1. `docs/README.md`;
2. `docs/context/PROJECT.md` and this file;
3. `docs/architecture/stateful-zone-v2-architecture.md`;
4. `docs/decisions/ADR-0003-stateful-player-actor-zone.md`;
5. only the requirement, module, contract, plan, or evidence files relevant to the current task.

Do not resume the stateless V1 architecture or its old open-question plan. They are retained only to explain the design evolution.

## Current accepted direction

- The 30-million-DAU production target uses stateful Player Actors in Zone processes.
- One logical shard has exactly one write-authorized Active Zone Owner at a time; one Zone owns many logical shards.
- The Gateway routes by the target player's stable logical shard. Rendezvous Hashing plus load correction proposes a candidate Zone, but only a Coordinator majority can commit the authoritative owner and `route_epoch`; stale writers are fenced.
- Commands for one player enter one Actor mailbox and execute serially.
- A successful write follows `Decide → Journal committed → Apply memory → reply`. Acknowledged writes must be recoverable.
- Snapshot DB is an asynchronous recovery checkpoint, not the real-time write path. Recovery loads a snapshot and replays later Journal entries.
- Task, mail, friend, realtime, and cross-player flows keep independent boundaries and use reliable, idempotent asynchronous delivery where a single-player atomic write is impossible.
- HTTP carries commands and authoritative snapshots. WebSocket carries committed changes; entering a friend's farm uses subscribe-first, then an HTTP snapshot, then versioned pushes.
- The project implements Shard hashing, placement planning, route caching, Owner state, migration, and epoch integration; it does not implement Raft or consensus replication from scratch.
- Redis, Kafka-compatible messaging, the Journal engine, the consensus store/library, and the Coordinator implementation are still product candidates, not accepted technology selections.

## Current capacity planning values

These are planning assumptions, not measured claims:

- 30 million DAU; 1.25 million average online; 3.75 million normal peak online.
- About 5 million peak resident Actors after disconnect, wake-up, and migration overlap.
- About 69,444 normal planning peak external QPS from the current per-scenario model.
- About 65,800 peak Zone commands/s after internal task and reward amplification.
- Journal design entry point: about 55,000 logical appends/s including margin.
- 750,000 normal and 1.125 million extreme WebSocket connections.
- 4096 logical player shards.
- Capacity sensitivity range: roughly 30–120 Zone instances; the pre-benchmark midpoint is 60 Zones, 20 per availability zone.

The 4096 shard count is versioned cluster configuration, not a scattered code constant. Once persisted data exists, changing the shard function or count requires a versioned migration.

## Document state

- `stateful-zone-v2-architecture.md`: current target architecture, accepted as direction and still awaiting prototype evidence.
- ADR-0003: accepted decision replacing the stateless V1 production target.
- ADR-0004: accepted decision separating hash-based placement planning from quorum-authorized ownership.
- `target-30m-dau-architecture.md`: superseded V1 history.
- `2026-07-27-30m-dau-architecture-strategy-and-open-questions-plan.md`: superseded V1 plan; do not execute it.
- `architecture.md`: business rules and navigation; V2 controls any distributed-architecture conflict.
- No exact protocol, schema, Journal product, message product, or cache product has been frozen.

## Product code and evidence state

- No backend or frontend product code exists yet.
- No database schema or exact HTTP/WebSocket DTO has been accepted.
- No runtime, correctness, availability, or performance claim has been verified.
- Tests and evidence do not yet exist.

## Next actions

1. Read V2 once from the request path, ownership model, write path, recovery path, migration path, and capacity model perspectives.
2. Create a requirement-coverage matrix for account/login, farm loop, shop/warehouse, share-link friends, three-person synchronization, tasks, pet, catalog, mail, weak network, and smooth updates.
3. Turn the first single-player V2 slice into exact contracts and a bounded implementation plan.
4. Prototype the Journal-before-response and replay path before adding the full asynchronous and realtime systems.
5. Store measurements and failure results under `docs/evidence/`, then revise capacity assumptions.

## AI memory rule

- This file is the short, authoritative project handoff and should stay concise.
- `docs/ai-workflow/` records what AI did, what the owner changed, and how work was verified.
- Obsidian and `ai-context` may keep learning notes and pointers, but must not duplicate the full architecture or override ADRs.
- When switching between Codex, CodeBuddy, or Claude, provide `AGENTS.md`, `PROJECT.md`, `CURRENT.md`, the relevant ADR, and only the task-specific design files.

## Verification state

- V2 architecture discussion and capacity model: recorded.
- V2 direction and 4096 logical shards: accepted by the owner.
- Technology products: candidates only.
- Product behavior and target capacity: not implemented or measured.
