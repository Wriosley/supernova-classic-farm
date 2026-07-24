---
status: proposed
updated: 2026-07-24
---

# Current Architecture

## Status

This document is a proposed baseline. No product code exists yet, and no performance claim has been validated.

## Phase 1 boundary

The first implementation will support only the farm owner's single-player loop. Friend relationships and realtime multiplayer are outside the first vertical slice.

## Proposed baseline

```mermaid
flowchart LR
    H5["Vue H5 client"] -->|"HTTP"| API["Go application"]
    API --> AUTH["Account/Auth module"]
    API --> FARM["Farm module"]
    API --> INV["Inventory/Shop module"]
    API --> TASK["Task module"]
    AUTH --> DB[("Relational database")]
    FARM --> DB
    INV --> DB
    TASK --> DB
```

## Design principles

- Correctness before distribution.
- Server-authoritative game rules.
- Business operations go through module services rather than editing tables from handlers.
- Multi-step economy changes must be atomic.
- Store enough durable state to recover after process restart.
- Add distributed components only after a requirement or measurement justifies them.

## Planned evolution

```text
single-player correctness
→ friends and invitations
→ realtime room
→ concurrency and versioning
→ reconnect and weak-network behavior
→ load testing and target-scale evolution
```

Each transition requires an accepted ADR and a validation plan.
