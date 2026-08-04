---
status: completed
date: 2026-08-03
claim_scope: local static dual-Zone routing
---

# Static Dual-Zone Routing Evidence

## Claim

The local memory-only prototype can materialize one committed Owner for every
logical Shard across two Zone processes, warm Gate from a complete route
Snapshot, route players to different Zones without a per-command Coordinator
lookup, and reject a command sent to the wrong Zone before Actor execution.

This is not evidence for Owner migration, multi-Zone MySQL fencing,
Coordinator high availability or production capacity.

## Automated verification

Targeted packages:

```text
go test ./internal/routing ./internal/gateway ./cmd/coordinator ./cmd/zone ./cmd/gate ./test/e2e
```

Result: PASS.

Full regression:

```text
go test ./...
go vet ./...
```

Result: PASS. The original single-Zone authenticated Snapshot E2E also passed
after Gate RouteCache and trusted ownership headers were enabled.

Coverage includes:

- deterministic Rendezvous placement independent of input order;
- addition/removal minimal-remapping properties;
- candidate and Snapshot validation;
- complete HTTP Snapshot and route lookup counters;
- Zone-side wrong Shard/Zone/epoch/expired-Lease rejection;
- immutable Gate cache hits;
- conditional version invalidation;
- twenty concurrent misses collapsed into one Resolve;
- cached `NOT_OWNER` refresh with two sends of the same `request_id`.

## Five-process E2E

The output below is the Phase A+B baseline captured before the runner gained
the inactive-Shard migration scenario. The current extended output is recorded
in `2026-08-03-manual-inactive-shard-migration.md`.

Command:

```text
tests/e2e/run-dual-zone-routing.ps1
```

Observed result:

```text
zone-a player_id=2 shard_id=1631
zone-b player_id=1 shard_id=2066
Coordinator snapshot_lookups=4
Coordinator shard_lookups=0
RESULT dual_zone_routing_e2e=PASS
```

The four Snapshot lookups were startup/control-plane reads by the test, two
Zones and Gate. Authentication, two player snapshots, one Zone-A seed purchase,
the Zone-B isolation snapshot and the wrong-Zone rejection did not increase
the single-Shard lookup count.

The direct wrong-Zone command returned HTTP 409 with `NOT_OWNER`. Player A's
purchase advanced only Player A to `player_seq=1` and eight coins; Player B
remained at `player_seq=0` and ten coins.

## Limits

- Both Owners use epoch 1 because this is static bootstrap, not migration.
- Coordinator renews configured Owners in-process; Zones do not yet acquire
  leases through a production Leader protocol.
- Gate refreshes an expired/missing Shard on demand. Snapshot Watch/push is not
  implemented.
- Dual-Zone launch intentionally refuses MySQL.
- The observed players prove routing behavior, not statistical load balance.
