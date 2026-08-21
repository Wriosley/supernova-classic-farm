---
status: measured
date: 2026-08-20
---

# Single-Zone connect-and-hold baseline

## Environment and model

- Three Gate Pods; one physical `zone-pool-0` Pod.
- Zone limit: 2 CPU and 1GiB memory.
- All 4096 authoritative Shards route to Zone 0.
- Migration Worker and Planner disabled during measurement.
- Gate load-test authentication bypass explicitly enabled.
- Each identity connects, authenticates, issues exactly one
  `GET_PLAYER_SNAPSHOT`, then holds the WebSocket and sends periodic PINGs.

## Results

| connections | setup | initial snapshot QPS | P99 | business errors | hold |
|---:|---:|---:|---:|---:|---:|
| 100 | 22ms | 1,073.85 | 92.633ms | 0 | 30s |
| 1,000 | 236ms | 10,789.13 | 89.203ms | 0 | 2m |
| 3,000 | 781ms | 6,387.20 | 449.950ms | 0 | 2m |

The QPS is a finite initial Snapshot burst, not sustained repeated-Snapshot
traffic. The 1,000 connection resource sample peaked at approximately 95m CPU
and 76Mi for Zone. At 3,000, Zone peaked at approximately 158m CPU and 114Mi;
Gate peaks were 36--49m CPU and 119--152Mi.

## First observed bottleneck

The 3,000-connection burst produced 952
`presence quick-info queue full` warnings. The Zone presence client has a
fixed 1,024-entry channel and one consumer that sends batches of up to 256 to
InfoSvr. Therefore the first quality boundary is an internal presence-update
queue burst limit, not Zone CPU or memory saturation. Although all Snapshot
responses succeeded, 3,000 connections is not a clean capacity point because
presence side effects were dropped.

The next action is to decide whether presence is part of the benchmark
acceptance contract, then measure/fix queueing and InfoSvr consumption before
raising the connection ladder. This evidence does not claim 3,000 connections
as a production-safe capacity.

## InfoSvr isolation and warm-state rerun

For a core-Zone isolation experiment, `ZONE_QUICK_INFO_ENABLED=false` removed
Zone QuickInfo/Presence publishing while retaining Gate, connection registry,
Actor loading, Snapshot and PING. A cold 3,000-player run completed at
5,910.78 QPS with P99 492.382ms and zero business errors. This cold result is
not directly comparable with the earlier partially warm run because Zone and
Gate had been restarted.

The same 3,000 identities were then used for one explicit warm-up Snapshot
burst, followed immediately (before the 60-second idle Actor eviction window)
by the measured two-minute hold run:

| mode | QPS | P50 | P95 | P99 | errors |
|---|---:|---:|---:|---:|---:|
| Info disabled, cold | 5,910.78 | 292.155ms | 476.849ms | 492.382ms | 0 |
| Info disabled, warm | 11,455.69 | 166.955ms | 248.867ms | 254.909ms | 0 |
| Info disabled, warm, 5,000 players | 10,832.18 | 259.820ms | 440.229ms | 450.125ms | 0 |

The warm measured run peaked at approximately 224m CPU/109Mi on Zone; Gate
peaks were 40--83m CPU and 133--195Mi. No Presence queue-full warning was
possible because the publisher was disabled. The large cold/warm difference
shows that checkpoint/Actor cache state materially affects this finite burst;
future maximum tests must label and control warm state. This isolated result
does not include InfoSvr-facing product behavior.

At 5,000 warm players, all connections established in 2.01 seconds and held
without reported request/PING errors. The two-second `kubectl top` collector
observed Zone peak 302m CPU, average 105.2m and peak 159Mi memory; three Gate
peaks were 75--115m CPU and 215--236Mi. Thus this run did not saturate the
2-CPU/1GiB Zone allocation. Because Kubernetes Metrics Server is aggregated,
these samples can miss a sub-second CPU spike and must not replace cgroup or
pprof evidence at the final saturation point.

## Paced arrival model calibration

Benchrunner now supports `-connect-rate` for `connect_hold`. When non-zero,
each player is scheduled independently, authenticates, immediately sends its
single Snapshot, and then holds the connection; there is no all-connections
Snapshot barrier. The legacy synchronized burst remains available with the
default rate zero.

Two disjoint 1,000-player cohorts validated pacing with InfoSvr publishing
disabled:

| offered arrivals | completion time | achieved QPS | P99 | errors |
|---:|---:|---:|---:|---:|
| 200 players/s | 5.023s | 199.09 | 29.418ms | 0 |
| 500 players/s | 2.028s | 493.06 | 32.449ms | 0 |

Both held connections for 30 seconds without reported errors. These are tool
calibrations, not Zone maximums: the achieved QPS is expected to follow the
offered player arrival rate until the service becomes unable to keep up.

## 10,000-player cold paced ladder

InfoSvr publishing remained disabled. Each step restarted the single Zone and
then all Gates so Actor/checkpoint state began cold and Gate gRPC connections
targeted the new Zone incarnation.

| offered arrivals | achieved QPS | offered achieved | Snapshot P99 | errors | Zone CPU peak | Zone memory peak |
|---:|---:|---:|---:|---:|---:|---:|
| 500/s | 470.12 | 94.0% | 30.386ms | 0 | 1.190 cores | 231Mi |
| 1,000/s | 687.37 | 68.7% | 43.664ms | 0 | 1.110 cores | 223Mi |
| 1,500/s | 812.89 | 54.2% | 50.409ms | 0 | 1.146 cores | 239Mi |

The 1,500/s step required 12.302 seconds to complete 10,000 arrivals rather
than the offered 6.667 seconds. Snapshot latency remained low and all service
requests succeeded, while Zone never reached its 2-core limit and Gate peaks
remained below 0.3 core each. The measured completion plateau therefore cannot
yet be called the Zone ceiling. The unmeasured connection/WebSocket AUTH/Zone
connection-register phase or the single load-generator host is the leading
boundary. The ladder stopped before 2,000/s; benchrunner must split dial, AUTH,
connection-register-visible wait and Snapshot timing, and generator CPU/socket
capacity must be recorded.

## Connection lifecycle isolation and Zone-core ladder

Phase timing at 1,500 offered arrivals/s identified the limiting setup stage:
with 400 workers, `connect_auth` P99 was 911.977ms and `worker_queue` P99 was
4,815.710ms. Raising workers to 800 made `connect_auth` P99 2,001.052ms,
matching Gate's two-second Zone connection-register RPC timeout. Snapshot P99
remained 14.732ms. The approximately 800/s platform was therefore caused by
the synchronous connection lifecycle call made before AUTH response, not by
the Snapshot command or Zone CPU.

Gate now has a load-test-only `GATE_SKIP_CONNECTION_SYNC` switch. It defaults
to false and, when explicitly enabled, skips connection register, refresh and
unregister. This also excludes online presence and precise push-routing
semantics, so the following figures isolate only AUTH bypass, Gate routing,
Zone Actor lookup and one Snapshot per arriving player. They are not full
business-path capacity.

The same 10,000 warm identities produced:

| offered arrivals | achieved QPS | Snapshot P50 | Snapshot P95 | Snapshot P99 | errors |
|---:|---:|---:|---:|---:|---:|
| 1,500/s | 1,499.89 | 0.590ms | 1.216ms | 6.239ms | 0 |
| 3,000/s | 2,998.94 | 0.690ms | 3.815ms | 7.534ms | 0 |
| 4,500/s | 4,493.75 | 0.965ms | 14.524ms | 49.366ms | 0 |
| 6,000/s | 5,994.57 | 2.561ms | 72.695ms | 90.177ms | 0 |

At 1,500/s, connection-sync isolation reduced `connect_auth` P99 from
911.977ms to 12.841ms and `worker_queue` P99 from 4,815.710ms to 4.498ms.
The service accepted the complete 6,000/s finite burst without errors, but
Snapshot tail latency shows a clear knee between 3,000/s and 4,500/s. Under a
P99 <= 50ms criterion, 4,500/s is only a boundary observation, while 3,000/s
is the currently demonstrated conservative Zone-core arrival waterline.

The 1-second Metrics Server samples did not capture credible CPU peaks for
these 1.7--6.7 second bursts. A longer cohort or sustained command workload,
plus cgroup or pprof CPU collection, is required before attributing the knee
to the 2-core limit or publishing a resource-normalized maximum.

Raw reports: `../../yace/raw/zcore1500w400/report.md`,
`../../yace/raw/zcore3000w400/report.md`,
`../../yace/raw/zcore4500w800/report.md`, and
`../../yace/raw/zcore6000w800/report.md`.
