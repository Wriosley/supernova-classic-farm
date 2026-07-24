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

### Phase 1: single-player vertical slice

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

### Later phases

1. Friendship and invitation links.
2. Up to three users in one farm.
3. Realtime synchronization and concurrency control.
4. Reconnect, weak-network behavior, and idempotency.
5. Capacity model, load testing, and evidence-driven optimization.

## Knowledge boundary

UC backend, xRPC, Actor, Proxyless, and related `ai-context` documents are reference material. The project must not adopt those techniques unless the farm's requirements and evidence justify them.

## Documentation rule

The repository is the source of truth for project facts and accepted decisions. Personal reasoning and learning journals may live in Obsidian, but must link to rather than duplicate accepted project documents.
