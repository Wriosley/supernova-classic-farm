# R3 snapshot benchmark: 20260817_071649

This is a local single-instance baseline, not a production capacity claim.

| concurrency | QPS | P50 (ms) | P95 (ms) | P99 (ms) | max (ms) | errors |
|---:|---:|---:|---:|---:|---:|---:|
| 2500 | 1378.46 | 816.917 | 1594.885 | 2056.840 | 4032.274 | 2500 |

## Error categories

- concurrency 2500: `failed to get reader: failed to read frame header: EOF`=2500

## Parameters

- `concurrency`: `2500,3000`
- `duration`: `3m0s`
- `gate_url`: `ws://127.0.0.1:32591/ws`
- `login_url`: `http://127.0.0.1:31238`
- `max_samples`: `3000000`
- `mode`: `closed`
- `origin`: `http://21.130.223.195:1616`
- `ping_interval`: `30s`
- `timeout`: `15s`
- `warmup`: `10s`
