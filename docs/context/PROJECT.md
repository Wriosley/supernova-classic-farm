---
status: active
updated: 2026-08-05
---

# Project Context

## Objective

Independently build and demonstrate a classic farm H5 game with a Go backend, then explain its architecture, capacity assumptions, bottlenecks, performance evidence, AI-assisted workflow, and iterative design process.

## Confirmed facts

- Developer: one person.
- Backend language: Go.
- H5 experience: beginner.
- Demonstration environment: local machine.
- Midterm review: 2026-07-31.
- First-stage minimum protocol, data-model and runnable-framework milestone: 2026-08-02.
- Final defense: 2026-08-21; final materials should be frozen by 2026-08-18.
- Capacity design target: 30 million DAU. This is a design target, not a locally verified capability.
- The first implementation slice is the farm owner's single-player loop. Friends and multiplayer follow after this loop is correct.
- Multiplayer scope initially targets at most three simultaneous users in one farm.

## Delivery requirements

- Account registration and login.
- Planting lifecycle.
- Shop and warehouse.
- Friends and share-link onboarding.
- Multiplayer farm state synchronization.
- Basic task system.
- Client and server deliverables.
- Architecture, capacity estimation, bottleneck analysis, and load-test validation for the target scale.
- AI Coding Workflow artifacts.
- Optional value: weak-network experience and smooth updates.

## Current delivery strategy

The only current production-target architecture is the accepted stateful Player Actor Zone V3:

- Player state is held in a Zone's Player Actor and same-player commands execute serially.
- Successful ordinary game commands update Actor memory first, mark the Actor
  Dirty, and reply without waiting for the recovery store.
- A Zone flusher asynchronously persists versioned player checkpoints through
  `CheckpointStore`; Tcaplus is the current prototype target and MySQL remains
  a rollback adapter.
- An abnormal Zone exit may lose the latest unflushed ordinary game state; normal shutdown, Actor eviction, and controlled migration must flush Dirty state first.
- A versioned 4096-logical-shard map, leases, `owner_epoch`, database fencing, and a majority-authorized production Coordinator preserve single-Owner semantics.
- The production target uses a three-node majority Coordinator. The local prototype uses a compatible single-node implementation and does not claim control-plane high availability.
- V1 and V2 remain design-history evidence only. In particular, V2's Journal-before-response and Kafka recovery path are not part of the current V3 write path.

The first product slice is:

```text
register/login
-> enter own farm
-> buy seeds
-> plant
-> fertilize and grow while online/offline
-> harvest
-> store/sell
-> update and claim a chapter task
-> clean the plot
```

The first-slice business rules are defined by `../architecture/single-player-vertical-loop-business-architecture.md`.

The first-stage completion standard is:

```text
the H5 client can register/login
-> establish an authenticated Protobuf WebSocket
-> send one game command through GateSvr
-> route it to the correct Player Actor
-> receive a correlated response
```

Before implementation, this milestone requires frozen minimum contracts for HTTP login and WS tickets, WebSocket commands and errors, client/player views, Player checkpoints, ShardMap, Dirty batches, Outbox and state versions.

## Prototype evidence boundary

The local prototype should exercise the smallest V3 path: WebSocket routing,
Actor serialization, in-Actor task progress, Dirty batching, Tcaplus checkpoint
recovery, a single-node Coordinator-compatible control plane, leases, epoch
rejection, Kubernetes-discovered dynamic Zone deployment and the reviewed friend
interaction slice.

The production target and local prototype are separate claims. The prototype validates mechanisms and measured single-instance baselines; it does not claim to run 30 million DAU locally.

## Knowledge boundary

UC backend, xRPC, Actor, Proxyless, and related `ai-context` documents are reference material. The project adopts ideas only after recording them in the farm's current architecture or decisions. Company implementation details must not be copied into this repository.

## Documentation rule

The repository is the source of truth for project facts and accepted current design. Personal reasoning and learning journals may live in Obsidian, but must link to rather than duplicate current project documents. `docs/decisions/` is a chronological decision-history ledger; the directory as a whole is not a list of simultaneously active decisions.
