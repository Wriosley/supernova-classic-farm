---
date: 2026-08-03
scope: direct cleanup and fertilizer purchase
---

# AI work record: fertilizer shop and cleanup

## Request

Allow cleaning a harvested plot before claiming chapter one, and sell basic
fertilizer at the same 2-coin unit price as seeds.

## Decisions applied

- Keep cleanup server rules unchanged: only `NEED_CLEANUP` is authoritative.
  Remove only the contradictory H5 chapter-reward gate.
- Add independent `BUY_FERTILIZER` instead of treating fertilizer as a seed
  purchase, so buying fertilizer cannot progress the seed task.
- Reuse the existing quoted-price, Actor serialization, idempotency, Dirty
  checkpoint and inventory-stack semantics.

## Validation

Generated shared Protobuf types, passed focused Player/Gateway Go tests, and
built the Vue production client. See
`../evidence/2026-08-03-fertilizer-shop-cleanup.md`.
