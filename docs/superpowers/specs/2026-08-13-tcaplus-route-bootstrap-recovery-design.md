# Tcaplus Route Bootstrap Recovery Design

## Goal

Make the first durable Coordinator RouteStore bootstrap complete reliably even
when writing 4096 `ShardRoute` rows takes longer than the old 60-second startup
budget. A Coordinator may be interrupted and restarted during bootstrap, but it
must not become Ready until all routes and the authoritative `ShardMapMeta` row
are complete.

## Scope and invariants

- This applies only when `ShardMapMeta` is absent. Once Meta exists, durable
  Current remains authoritative and static bootstrap input must not overwrite or
  recompute it.
- A missing Meta plus any subset of `ShardRoute` rows is an interrupted,
  uncommitted bootstrap. Those rows are not Current authority.
- A fresh bootstrap candidate CAS-overwrites every existing partial row and
  inserts every missing row. This ensures all 4096 rows belong to one current
  initialization attempt instead of mixing volatile lease identities and
  timestamps from different process starts.
- `AlreadyExists` is handled by reading that row's record version and applying
  the current candidate through CAS.
- `ShardMapMeta` is inserted only after all 4096 route rows have been observed or
  inserted and validated.
- Coordinator readiness remains downstream of successful RouteStore bootstrap.

## Startup budget

Replace the fixed 60-second durable-route bootstrap context with a configurable
duration. The default is 10 minutes. Invalid or non-positive configuration
fails startup.

The timeout bounds one process attempt; it does not erase progress. A later
restart overwrites the still-uncommitted partial rows with its fresh complete
candidate and inserts missing rows. Persistent database
unavailability still fails startup rather than allowing an incomplete control
plane to become Ready.

## Load path

Normal `Load` uses the records returned by the Route traversal directly instead
of issuing a second `DoGet` for every row. It sorts and validates the complete
set against Meta.

Record versions are required only for a pending cross-row recovery. When Meta
contains a pending intent, RouteStore performs one `DoGet` for the pending Shard
to obtain its current value and record version, applies the documented exact
recovery rules, finalizes Meta, and reloads the snapshot.

This preserves CAS safety while removing the current Traverse-plus-N-reads
startup pattern.

## Failure handling

- Context timeout: return the context error; keep successfully inserted rows for
  the next attempt.
- Existing partial row with no Meta: read its record version and CAS-overwrite
  it with the fresh candidate row.
- Meta appears during concurrent bootstrap: load and validate the winner's
  complete durable snapshot.
- Meta exists but routes are missing or inconsistent: `ErrRouteStoreCorrupt`;
  never use bootstrap repair after a committed Meta exists.
- Route insert returns `AlreadyExists`: fetch that one row and require exact
  equality before continuing.

## Verification

Automated tests must prove:

1. partial routes without Meta are CAS-overwritten while missing rows are
   inserted;
2. Meta-present durable routes are never overwritten by bootstrap;
3. interruption followed by retry produces one fresh set of exactly 4096
   routes and one Meta;
4. Meta is never inserted before all route rows are complete;
5. normal Load performs traversal without per-route `DoGet` calls;
6. pending recovery performs only the target-row read required for CAS;
7. the default bootstrap timeout is 10 minutes and accepts an explicit valid
   override;
8. existing pending-intent, commit-conflict and full Coordinator regressions
   remain green.

After offline verification, run live Tcaplus bootstrap against the existing
partial table, confirm the final row count and Meta, restart Coordinator, and
record redacted timing and recovery evidence. Durable mode must remain disabled
by default until that live restart succeeds.
