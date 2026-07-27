---
status: active
updated: 2026-07-24
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

### Phase 1: target architecture and single-player core prototype

```text
register/login
→ enter own farm
→ buy seeds
→ plant
→ grow while offline
→ harvest
→ store/sell
→ claim basic task reward
```

### Confirmed delivery sequence

1. Write the 30-million-DAU target architecture before product implementation.
2. Implement the single-player transaction, idempotency, and Outbox core.
3. Add two stateless Zone instances and simulated player database shards.
4. Add asynchronous task processing and cross-shard reward delivery with mail fallback.
5. Add two realtime gateways, room subscriptions, version recovery, and three-client synchronization.
6. Run HTTP, messaging, WebSocket, and failure experiments; use evidence to revise the capacity model.

The production target and local prototype are separate claims. The prototype validates mechanisms and single-instance baselines; it does not claim to run 30 million DAU locally.

## Knowledge boundary

UC backend, xRPC, Actor, Proxyless, and related `ai-context` documents are reference material. The project must not adopt those techniques unless the farm's requirements and evidence justify them.

## Documentation rule

The repository is the source of truth for project facts and accepted decisions. Personal reasoning and learning journals may live in Obsidian, but must link to rather than duplicate accepted project documents.
