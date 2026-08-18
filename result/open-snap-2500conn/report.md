# R3 snapshot benchmark: 20260817_071649

This is a local single-instance baseline, not a production capacity claim.

| pool | target QPS | achieved QPS | shed | P50 (ms) | P95 (ms) | P99 (ms) | max (ms) | errors |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 500 | 2500 | 2498.20 | 0 | 91.654 | 226.534 | 331.842 | 786.791 | 0 |

## Error categories


## Parameters

- `concurrency`: `500`
- `duration`: `2m0s`
- `gate_url`: `ws://127.0.0.1:32591/ws`
- `login_url`: `http://127.0.0.1:31238`
- `max_samples`: `1000000`
- `mode`: `open`
- `origin`: `http://21.130.223.195:1616`
- `ping_interval`: `30s`
- `target_qps`: `2500`
- `timeout`: `15s`
- `warmup`: `10s`
