---
status: measured
date: 2026-07-31
scope: fixed-point growth settlement and maturity materialization
---

# Growth and maturity test evidence

## Claim boundary

Automated tests prove that the current no-effect crop path:

1. computes growth as exact `elapsed_ms * RateDecimal6` into
   `GrowthDecimal9`;
2. uses arbitrary-width intermediate multiplication before reducing to
   checkpoint `int64` fields;
3. clamps growth at maturity and does not move growth backward when the server
   clock is behind `last_settled_at_ms`;
4. changes a due plot from `GROWING` to `MATURE`, clears its estimated maturity
   and exposes the deterministic harvestable quantity;
5. increments `player_seq` and `checkpoint_revision` once per independently
   materialized plot;
6. materializes overdue plots before an activated Actor serves its first
   snapshot, then flushes the new revision using the persisted checkpoint
   revision as the CAS base;
7. supports the local one-second online scan and request-time due check.
8. splits growth exactly across fertilizer `[start_at_ms, end_at_ms)` boundaries
   and recomputes maturity under the current and future rates.

## Commands and result

```powershell
cd server
go test ./...
go vet ./...
```

Both commands passed.

Focused tests cover:

```text
50 seconds at rate 1 -> growth 50 of 100
100 seconds at rate 1 -> MATURE
clock rollback -> no negative elapsed growth
very large elapsed time -> no multiplication overflow
checkpoint seq=2/revision=3 activation after due time
-> snapshot seq=3/MATURE
-> Dirty checkpoint revision=4 with CAS expected revision=3
online scheduler scan -> seq=3/MATURE
fertilizer +0.5 for 60 seconds
-> growth 75 at 50 seconds
-> effect capacity 90 plus 10 base growth
-> estimated and actual maturity at 70 seconds
```

## Limitations

- No live MySQL run waited beyond the 100-second development maturity time;
  activation recovery is covered with the recording checkpoint store.
- Fertilizer intervals are implemented; pest creation and cross-player pest
  behavior are not.
- The scheduler loop is unit-tested through its scan function; wall-clock
  one-second latency and load behavior are not measured.
- Maturity Push publication to connected Gate clients is not implemented.
