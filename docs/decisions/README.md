# Architecture Decision Records

Use ADRs only for decisions that materially affect architecture, data correctness, interfaces, non-functional requirements, dependencies, or the project's ability to meet its delivery goals.

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
