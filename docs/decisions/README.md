# Architecture Decision Records

This directory is a chronological ledger of architecture decisions and their evolution. It is **not** a list of decisions that must all be applied to the current system.

## How to read this directory

- An ADR records what was decided at a particular point in the project, together with alternatives, reasoning, costs, and validation methods.
- `accepted` means the owner accepted that decision at that time. It does not by itself prove that the decision is still the current implementation target.
- `superseded` means a later decision explicitly replaced it; keep the file for design-history and defense evidence.
- Some older ADRs may still say `accepted` while a newer architecture narrows or replaces part of them. Resolve conflicts using `../context/CURRENT.md`, the current architecture document, and the newest ADR explicitly referenced there.
- Never load all ADRs into an AI prompt and ask it to implement them together. Select only the ADRs relevant to the current task.

Current-state entry points:

1. `../context/CURRENT.md` — current status and effective decision map;
2. `../architecture/stateful-zone-v3-architecture.md` — current distributed architecture;
3. `../architecture/single-player-vertical-loop-business-architecture.md` — current first-slice business design.

## Decision history index

| ADR | Decision at the time | Current interpretation |
|---|---|---|
| ADR-0001 | Start with a modular monolith | Early implementation history; it does not define the current distributed runtime. |
| ADR-0002 | Stateless target-scale hybrid architecture | Superseded V1 history. |
| ADR-0003 | Adopt stateful Player Actor Zone | Current Actor/Zone foundation; V2 Journal-specific parts are overridden by V3 and ADR-0006. |
| ADR-0004 | Separate placement planning from quorum ownership | V2-era decision history; ADR-0008 is the current V3 Coordinator statement. |
| ADR-0005 | Kafka Journal in production and MySQL append table in prototype | Superseded by ADR-0006; not used by the current V3 write path. |
| ADR-0006 | Use asynchronous Dirty checkpoint writeback | Current V3 persistence decision. |
| ADR-0008 | Retain majority-authorized Shard Coordinator in V3 | Current V3 ownership decision. |
| ADR-0009 | Keep current chapter-task progress in Player Actor | Current first-slice task decision. |

The table is a navigation aid. If it conflicts with `CURRENT.md` or a newer accepted architecture/ADR, update this README rather than rewriting historical ADR content.

## What belongs in an ADR

Use ADRs only for decisions that materially affect architecture, data correctness, interfaces, non-functional requirements, dependencies, or the project's ability to meet delivery goals.

```text
proposed -> accepted -> superseded
                     -> rejected
```

- Do not rewrite the reasoning of an accepted ADR to make it look as if the project always used the newest design.
- If a later decision changes it, create a new ADR and link both records.
- An AI may draft an ADR, but it stays `proposed` until the owner can explain and accept it.

## Naming

```text
ADR-0001-short-decision-title.md
```

Start from `ADR-0000-template.md`.
