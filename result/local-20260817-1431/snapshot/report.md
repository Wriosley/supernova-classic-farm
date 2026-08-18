# R3 snapshot benchmark: 20260817_063159

This is a local single-instance baseline, not a production capacity claim.

| concurrency | QPS | P50 (ms) | P95 (ms) | P99 (ms) | max (ms) | errors |
|---:|---:|---:|---:|---:|---:|---:|
| 10 | 1914.95 | 4.205 | 13.063 | 19.677 | 61.563 | 0 |
| 20 | 2195.70 | 7.982 | 20.485 | 28.527 | 93.908 | 0 |
| 50 | 2383.42 | 19.225 | 40.657 | 52.723 | 129.666 | 0 |

## Error categories


## Parameters

- `duration`: `3m0s`
- `ping_interval`: `30s`
- `max_samples`: `1000000`
- `gate_url`: `ws://127.0.0.1:32591/ws`
- `origin`: `http://21.130.223.195:1616`
- `concurrency`: `10,20,50`
- `warmup`: `10s`
- `timeout`: `5s`
- `login_url`: `http://127.0.0.1:31238`
