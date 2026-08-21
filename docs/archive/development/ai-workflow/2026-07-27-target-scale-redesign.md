---
date: 2026-07-27
ai: Codex
task: Redesign architecture around 30-million-DAU target
status: recorded
---

# AI Work Record: Target-Scale Redesign

## Trigger

The mentor asked the owner not to rush implementation and to design directly for a service capable of supporting 30 million DAU. The previous architecture mainly established business correctness and treated distribution as a later optimization.

## Human decisions

- Deliver both a complete production target architecture and a scaled-down distributed prototype.
- Use a medium-engagement model: four 15-minute sessions per active player per day.
- Use three times average concurrency as the normal peak.
- Establish WebSocket connections only for shared/friend farms; single-player commands and snapshots use HTTP.
- Keep 30 days of online history, then archive.
- Choose the hybrid route centered on stateless player Zone shards.
- Use 1,024 logical player shards with logical-to-physical mapping.
- Keep MySQL as final truth and cache only hot data.
- Process tasks and cross-player rewards asynchronously.
- Redirect full-inventory steal rewards to non-expiring system reward mail.
- Use HTTP for commands/snapshots and WebSocket for committed-state push.
- Cap normal players at 200 friends.
- Target a single region with three availability zones.
- Approve the six-stage distributed prototype and failure-test sequence.

## Corrected earlier assumptions

- ADR-0001 remains useful for the earliest local business loop but no longer represents the production target.
- Task failure no longer rolls back a committed farm action in the production target.
- Cross-shard steal cannot rely on one MySQL transaction; the owner-shard theft fact is atomic and the thief reward is eventually delivered.
- Mail is no longer entirely deferred because it is required as the full-inventory reward fallback.
- “No visible room member limit” does not mean no technical protection; friend caps and rate limits are required.

## Technology decision discipline

Redis and Kafka-compatible messaging were introduced as candidates, not accepted products. Redis was proposed for reconstructable hot state, Session, TTL, and rate-limit use cases. Kafka was proposed for partitioned durable logs, independent Consumer Groups, backlog visibility, and replay. Alternatives and prototype evidence remain required before final selection.

## Evidence boundary

The capacity model, availability targets, per-instance WebSocket connection assumption, and instance counts are planning inputs. No code, tests, load results, availability measurements, or production claims exist yet.
