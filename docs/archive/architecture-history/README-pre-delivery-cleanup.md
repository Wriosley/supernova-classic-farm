# Architecture

Architecture documents explain how the whole system collaborates. They define topology, ownership, routing, consistency, realtime synchronization, capacity, and failure recovery without duplicating every DTO or table field.

## Current source of truth

- `stateful-zone-v3-architecture.md`: current accepted production target. It defines stateful Player Actors, asynchronous Dirty checkpoint writeback, 4096 logical shards, majority-authorized ownership, epoch fencing, realtime routing, capacity assumptions, migration, and recovery. ADR-0003 provides the Actor/Zone foundation; ADR-0006 replaces the V2 Journal path; ADR-0008 defines the current V3 Coordinator; ADR-0009 locates current chapter tasks in the Player Actor.
- `single-player-vertical-loop-business-architecture.md`: accepted first implementation slice from buying seeds through task reward and plot cleanup.
- `architecture.md`: broader business-design overview and navigation. When an older distributed statement conflicts with V3, V3 wins.

“Accepted target” means the owner selected this direction. It does not mean the system is implemented, measured, or proven to carry 30 million DAU.

## Supporting documents

- `module-design-and-flows.md`: transitional combined module document; content moves into current architecture, `../modules/`, and `../contracts/` topic by topic.
- `documentation-system.md`: documentation boundaries, lifecycle, and migration design.

Superseded V1/V2 architecture documents are outside the current architecture
directory under `../archive/architecture-v1-v2/`. They are design history only.

Do not combine V1, V2, and V3 mechanisms into one implementation. Start from `../context/CURRENT.md`, then read only the current architecture and task-specific historical material.

## Planned topic documents

Create these only after their design is discussed and confirmed:

- `capacity-model.md`
- `gateway-and-routing.md`
- `realtime-sync.md`
- `consistency-and-events.md`
- `data-ownership-and-sharding.md`
- `multi-az-and-disaster-recovery.md`

Major tradeoffs link to ADRs in `../decisions/`; precise formats belong in `../contracts/`; measured claims belong in `../evidence/`.
