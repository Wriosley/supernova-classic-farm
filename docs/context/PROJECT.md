---
status: active
updated: 2026-07-28
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
- Midterm materials should be prepared by 2026-07-29.
- Final defense: 2026-08-21.
- Final materials should be frozen by 2026-08-18.
- Capacity design target: 30 million DAU.
- Multiplayer scope will initially target at most three simultaneous users in one farm, after the single-player loop is complete.

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

## Delivery strategy

The production target uses the accepted stateful Player Actor Zone V2. The local deliverable is a scaled-down implementation of the same critical mechanisms, not a separate stateless architecture.

The first product slice remains:

```text
register/login
→ enter own farm
→ buy seeds
→ plant
→ grow while offline
→ harvest
→ store/sell
→ update a basic task
```

### Confirmed delivery sequence

1. Finish reviewing the V2 target architecture and map every opening requirement to an owner, request flow, state transition, and validation method.
2. Define the minimum V2 contracts: command envelope, `request_id`, Journal record, Snapshot record, route entry, `route_epoch`, and error semantics.
3. Implement one player's Actor loop and common `AppendMutation` interface; use a MySQL `journal_events` append table for commit-before-response, deterministic replay, idempotency, epoch rejection, and asynchronous Snapshot.
4. Add two stateful Zone instances, 4096 logical-shard routing, single-owner fencing, route refresh, migration, and rollback experiments.
5. Add asynchronous task processing, cross-player reward delivery with mail fallback, and subscribe-first realtime synchronization for three clients.
6. Add and benchmark the production Kafka Journal adapter, then run Actor memory, Zone throughput, Journal, replay, Snapshot, WebSocket, and failure experiments; use evidence to replace capacity assumptions.

The production target and local prototype are separate claims. The prototype validates mechanisms and single-instance baselines; it does not claim to run 30 million DAU locally.

## Knowledge boundary

UC backend, xRPC, Actor, Proxyless, and related `ai-context` documents are reference material. The project adopts the Player Actor direction only through ADR-0003 and the farm's own constraints; company implementation details must not be copied into this repository.

## Documentation rule

The repository is the source of truth for project facts and accepted decisions. Personal reasoning and learning journals may live in Obsidian, but must link to rather than duplicate accepted project documents.
