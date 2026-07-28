# Architecture

Architecture documents explain how the whole system collaborates. They define topology, ownership, routing, consistency, realtime synchronization, capacity, and failure recovery without duplicating every DTO or table field.

## Current source of truth

- `stateful-zone-v2-architecture.md`: current accepted production target. It defines Player Actor ownership, Journal-before-response, asynchronous snapshots, 4096 logical shards, capacity assumptions, migration, and failure recovery. ADR-0003 selects V2; ADR-0004 separates hash-based placement planning from quorum-authorized ownership.
- `architecture.md`: business design overview and navigation. When an older distributed statement conflicts with V2 or ADR-0003, V2 and ADR-0003 win.

“Accepted target” means the owner selected this direction. It does not mean the system is implemented, measured, or proven to carry 30 million DAU.

## Supporting and historical documents

- `target-30m-dau-architecture.md`: superseded stateless V1, retained only for comparison and design-history evidence.
- `module-design-and-flows.md`: transitional combined module document; content will move into `../modules/` and `../contracts/` topic by topic.
- `documentation-system.md`: documentation boundaries, lifecycle, and migration design.

## Planned topic documents

Create these only after their design is discussed and confirmed:

- `capacity-model.md`
- `gateway-and-routing.md`
- `realtime-sync.md`
- `consistency-and-events.md`
- `data-ownership-and-sharding.md`
- `multi-az-and-disaster-recovery.md`

Major tradeoffs link to ADRs in `../decisions/`; precise formats belong in `../contracts/`; measured claims belong in `../evidence/`.
