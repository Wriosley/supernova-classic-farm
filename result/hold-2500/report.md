# R3 snapshot benchmark: 20260817_071649

This is a local single-instance baseline, not a production capacity claim.

| concurrency | QPS | P50 (ms) | P95 (ms) | P99 (ms) | max (ms) | errors |
|---:|---:|---:|---:|---:|---:|---:|
| 2500 | 83.25 | 9.128 | 34.554 | 42.106 | 51.861 | 0 |

## Error categories


## Parameters

- `concurrency`: `2500`
- `duration`: `3m0s`
- `gate_url`: `ws://127.0.0.1:32591/ws`
- `login_url`: `http://127.0.0.1:31238`
- `max_samples`: `1000000`
- `mode`: `closed`
- `origin`: `http://21.130.223.195:1616`
- `ping_interval`: `30s`
- `timeout`: `15s`
- `warmup`: `10s`
