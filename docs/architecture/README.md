# Architecture

Architecture documents explain how the whole system collaborates. They define topology, ownership, routing, consistency, realtime synchronization, capacity, and failure recovery without duplicating every DTO or table field.

## Current documents

- `architecture.md`: current effective system overview and navigation entry.
- `target-30m-dau-architecture.md`: target-scale architecture and distributed prototype direction.
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
