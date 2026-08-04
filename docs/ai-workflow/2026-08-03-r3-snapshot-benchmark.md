---
date: 2026-08-03
scope: R3 snapshot benchmark and Gate connection-pool defect
---

# AI work record: R3 snapshot baseline

## Request

Create a root-level benchmark area, explain the load model, run 1–100 virtual
users against the real HTTP/CSRF/Ticket/Protobuf WebSocket path, and present
reproducible QPS and latency results.

## Iteration

The first runner revision exposed both a client issue and a service issue:

- one failed virtual user kept writing to an invalid connection and inflated
  error counts; the runner now stops it and persists every completed stage;
- repeated 10-user runs produced six `SERVICE_UNAVAILABLE` responses.

Loopback debug counters narrowed the latter from route/Zone/response validation
to Zone HTTP transport. Gate's default HTTP pool did not match Zone's shorter
idle timeout. The pool now keeps up to 64 idle connections per host and retires
them after 20 seconds, before Zone's 30-second timeout.

## Result

After the fix, the 1/10/25/50/100-user matrix completed with zero errors.
Throughput peaked around 16.1k snapshot QPS at 50 users and declined at 100
while P99 rose to 23.3 ms. See
`../evidence/2026-08-03-r3-snapshot-read-baseline.md`.

These are single-host read-path measurements, not production capacity claims.
