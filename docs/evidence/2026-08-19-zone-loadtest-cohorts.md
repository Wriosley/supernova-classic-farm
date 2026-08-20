---
status: observed
date: 2026-08-19
---

# Reproducible Zone load-test cohorts

## Goal

Create reusable player cohorts for two different Zone questions: an even
eight-Zone aggregate test and a single-Owner hotspot test. Authentication setup
must not create new-account counter contention during the measured runs.

## Benchrunner support

Benchrunner snapshot runs now support:

- `-identity-output`: safe CSV containing only account name, player ID and
  logical Shard ID after successful authentication;
- `-account-file`: deterministic account selection from the first CSV column;
- comma-separated Login URLs with stable per-account endpoint selection.

The identity output contains no password, cookie, CSRF value or WS ticket.
Focused tests and build passed.

## Candidate validation

The existing `bench_reg1000_001..1000` accounts were reused with four direct
Login endpoints, four setup workers, normal shared-HMAC ticket authentication
and Gate NodePort `172.18.0.2:32591`. All 1000 clients connected in 3m2.98s.
A one-second snapshot check observed 11,429.82 QPS, P99 299.988ms and zero
errors. That short result validates the candidate set; it is not a capacity
claim.

The live Gate `GATE_SKIP_AUTH` setting was changed to `false` before this run.
The load-test bypass implementation requires AUTH `target_player_id`, while the
common envelope validator rejects that field, so the bypass is unusable in the
current image. Normal ticket authentication succeeded across all three Gates.

## Cohorts at map version 12317

The 1000 identities were joined to the Coordinator's routable 4096-route
snapshot. Candidate counts across the eight Owners ranged from 111 to 137.

- `spread-800.csv`: exactly 100 accounts per Owner Zone;
- `hotspot-100.csv`: 100 accounts owned by zone-pool-6, Owner Zone ID
  `898e64e2-1616-565d-b11a-f6fe01eebec3`;
- `reg1000-routed.csv`: all identities with Shard, Owner, route version,
  map version and stable Login endpoint;
- `summary.json`: distribution summary.

Files and the reproducible join script are under `/data/workspace/yace/cohorts`
and `/data/workspace/yace/scripts/build-zone-cohorts.js`.

## Hotspot consumption check

A two-second, 100-client snapshot run loaded `hotspot-100.csv` successfully:
7988.12 QPS, P99 31.651ms and zero errors. Immediately observed CPU was 213m on
zone-pool-6 and 31–32m on every other Zone. Gate CPU was 332m, 207m and 127m
across gate-0/1/2; all remained Ready with zero restarts. This verifies the
cohort routing mechanism, not sustainable capacity or Gate balance quality.

## Use boundary

Owner routes may migrate. Before every formal run, compare the current
Coordinator map version with 12317. Rebuild the cohort files from the saved
identities whenever the map version changes; do not silently reuse stale Owner
labels.

