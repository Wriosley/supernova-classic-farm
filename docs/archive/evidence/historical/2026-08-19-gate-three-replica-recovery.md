---
status: observed
date: 2026-08-19
---

# Gate three-replica recovery before load testing

## Problem

`gate-2` repeatedly exited with
`invalid GATE_PORT "tcp://10.96.221.116:8081"`. Kubernetes had injected the
Gate Service link variable because the deployed StatefulSet Pod template did
not explicitly override `GATE_PORT`. Only two of three Gate Service endpoints
were Ready, so a multi-Gate capacity test would have had an invalid baseline.

## Change

The live Gate StatefulSet Pod template was patched with `GATE_PORT=""`. No
replica count, resource budget or business configuration was changed. The
StatefulSet completed a three-Pod rolling update.

## Validation

- `gate-0`, `gate-1` and `gate-2`: Ready `true`, restart count `0` after the
  rollout and delayed recheck;
- all three ran the same `supernova-gate:latest` image;
- Gate EndpointSlice contained three Ready addresses;
- each Pod logged `gate listening` on `0.0.0.0:8081` with a distinct instance
  ID and its own StatefulSet DNS endpoint;
- no recent log repeated `invalid GATE_PORT` or `gate service stopped`;
- idle observations were 1–2m CPU and 35–45Mi memory per Gate.

## Boundary

This proves the three-Gate baseline is healthy enough to begin controlled
load-test calibration. It is not throughput or connection-capacity evidence.

