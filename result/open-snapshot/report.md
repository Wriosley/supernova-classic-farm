# R3 snapshot benchmark: 20260817_064923

This is a local single-instance baseline, not a production capacity claim.

| pool | target QPS | achieved QPS | shed | P50 (ms) | P95 (ms) | P99 (ms) | max (ms) | errors |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 200 | 2000 | 1999.50 | 0 | 11.087 | 47.496 | 84.662 | 331.570 | 0 |
| 200 | 4000 | 2585.39 | 84153 | 72.517 | 146.785 | 189.748 | 352.393 | 0 |
| 200 | 6000 | 2627.76 | 201628 | 71.494 | 143.228 | 184.784 | 342.102 | 0 |

## Error categories


## Parameters

- `concurrency`: `200`
- `duration`: `1m0s`
- `gate_url`: `ws://127.0.0.1:32591/ws`
- `login_url`: `http://127.0.0.1:31238`
- `max_samples`: `1000000`
- `mode`: `open`
- `origin`: `http://21.130.223.195:1616`
- `ping_interval`: `30s`
- `target_qps`: `2000,4000,6000`
- `timeout`: `5s`
- `warmup`: `10s`
