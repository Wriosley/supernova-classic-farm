---
status: measured
date: 2026-08-03
scope: R3 snapshot benchmark tool smoke test
---

# R3 benchrunner smoke evidence

## Claim boundary

This evidence proves only that the first `benchrunner` tool can register an
isolated `bench_` account, authenticate through the public HTTP/CSRF/Ticket/
Protobuf WebSocket path, repeatedly request `GET_PLAYER_SNAPSHOT`, and write
machine-readable result files. It is not the R3 performance baseline.

## Tool verification

```powershell
cd server
go test ./cmd/benchrunner
go build ./cmd/benchrunner

cd ..
.\benchmark\scripts\run-r3-snapshot.ps1 `
  -Concurrency "1" `
  -WarmupSeconds 0 `
  -DurationSeconds 1 `
  -RunID smoke_no_stack
```

The package test and build passed. The one-second local smoke generated:

```text
benchmark/results/smoke_no_stack/environment.json
benchmark/results/smoke_no_stack/summary.json
benchmark/results/smoke_no_stack/latency.csv
benchmark/results/smoke_no_stack/report.md
```

The repeated run reused the same `bench_smoke_no_stack_001` account through
the login fallback and produced 2,962 successful snapshot responses, zero
errors, 2,961.61 QPS, P50 520 µs, P95 563 µs and P99 1,111 µs. The captured
environment was Windows/amd64, Go 1.26.4 and eight logical CPUs.

## Limitations

The run used no warmup and only one second, so it is a functional smoke result,
not a stable measurement. It also verifies service readiness but cannot prove
from HTTP readiness alone that the stack used MySQL mode. Do not cite these
numbers in capacity or defense material. The real R3 run must use the
MySQL-backed dual-Zone start command, default 10-second warmup and 60-second
measurement at all planned concurrency levels.
