---
status: measured
date: 2026-08-03
scope: R3 snapshot read-path baseline and Gate connection-pool correction
---

# R3 snapshot read-path baseline

## Environment and load model

The measured host was `PHAINONYU-PC0`: Windows/amd64, Go 1.26.4 and eight
logical CPUs. The owner started the local dual-Zone stack with MySQL DSN before
the run. `benchrunner` created/reused isolated `bench_` accounts, then each
virtual user used one authenticated Protobuf WebSocket and repeatedly executed
closed-loop `GET_PLAYER_SNAPSHOT`.

Each measurement used a 10-second warmup, 60-second sample window and a 5-second
per-operation timeout. Raw JSON, CSV and generated reports are local ignored
artifacts under `benchmark/results/r3poolfix*_0803/`.

## Observed results

| Concurrent virtual users | Successful QPS | P50 | P95 | P99 | Maximum | Errors |
|---:|---:|---:|---:|---:|---:|---:|
| 1 | 3094.83 | 0.519 ms | 0.554 ms | 1.029 ms | 157.561 ms | 0 |
| 10 | 13250.00 | 0.602 ms | 1.512 ms | 2.090 ms | 93.561 ms | 0 |
| 25 | 15046.84 | 1.512 ms | 3.172 ms | 4.523 ms | 155.378 ms | 0 |
| 50 | 16080.84 | 2.749 ms | 6.094 ms | 9.225 ms | 24.700 ms | 0 |
| 100 | 13846.43 | 6.245 ms | 15.066 ms | 23.256 ms | 141.900 ms | 0 |

## Defect found and corrected

The initial 10-user runs repeatedly produced six `SERVICE_UNAVAILABLE`
responses. Local aggregate diagnostics narrowed these to Gate-to-Zone HTTP
transport failures. Gate used Go's default pool (two idle connections per
host, 90-second client idle timeout) while Zone closed idle connections after
30 seconds. Gate now permits 64 idle connections per host and retires them
after 20 seconds, before Zone does. The full 1/10/25/50/100 matrix then
completed with zero command failures; Gate's diagnostic counter remained empty.

The first runner also amplified one failed virtual user by continuing to write
to its invalid connection. It now stops that virtual user on its first error
and persists each completed concurrency stage immediately.

## Interpretation and limitations

Throughput peaked at about 16.1k successful snapshot requests per second at 50
virtual users on this host. At 100 users, throughput fell to 13.8k while P99
rose to 23.3 ms, so 50 users is the observed throughput knee for this specific
read-path setup.

This is not a production capacity or 30-million-DAU claim. The load generator,
Gate, both Zones and MySQL all ran on one Windows host; CPU and memory were not
sampled; the scenario is a read-only Actor snapshot and does not measure Dirty
writeback, Push latency, long-lived connection capacity or multi-machine
network costs.
