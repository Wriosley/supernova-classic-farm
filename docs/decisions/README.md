# Architecture Decision Records

Use ADRs only for decisions that materially affect architecture, data correctness, interfaces, non-functional requirements, dependencies, or the project's ability to meet its delivery goals.

## Current records

- `ADR-0001-modular-monolith-first.md`: accepted for local code organization and the earliest single-player slice.
- `ADR-0002-target-scale-hybrid-architecture.md`: superseded stateless V1 production target.
- `ADR-0003-stateful-player-actor-zone.md`: accepted current production target.
- `ADR-0004-shard-placement-and-control-plane-consensus.md`: accepted Shard placement and ownership-authority model.

## Lifecycle

```text
proposed → accepted → superseded
                   ↘ rejected
```

- Do not rewrite an accepted ADR.
- If a later decision changes it, create a new ADR and link both records.
- An AI may draft an ADR, but it stays `proposed` until the owner can explain and accept it.

## Naming

```text
ADR-0001-short-decision-title.md
```

Start from `ADR-0000-template.md`.
