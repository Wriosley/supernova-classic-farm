# R3 snapshot benchmark: 20260817_070712

This is a local single-instance baseline, not a production capacity claim.

| pool | target QPS | achieved QPS | shed | P50 (ms) | P95 (ms) | P99 (ms) | max (ms) | errors |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 200 | 2500 | 2497.04 | 0 | 50.823 | 118.746 | 163.651 | 342.627 | 0 |
| 200 | 3000 | 2620.94 | 22031 | 70.929 | 147.345 | 189.732 | 365.463 | 0 |

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
- `target_qps`: `2500,3000`
- `timeout`: `5s`
- `warmup`: `10s`
