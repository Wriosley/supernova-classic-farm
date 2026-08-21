---
status: measured
date: 2026-07-31
scope: local authenticated snapshot path
---

# Authenticated snapshot end-to-end evidence

## Claim boundary

This evidence covers one local, loopback-only run of four real Go processes:
LoginSvr, ZoneSvr, the single-node Coordinator-compatible process, and GateSvr.
Login and Zone used their explicit development-only in-memory adapters. The run
is not persistence or checkpoint-recovery evidence.

It does not automate the browser UI, measure capacity or latency, prove
production high availability, or validate the 30-million-DAU design target.

## Environment

- Windows 10.0.26200, amd64
- Go `go1.26.4 windows/amd64`
- Windows PowerShell `5.1.26100.8457`
- No Docker or MySQL was used.
- Repository root: `E:\workspace\supernova-classic-farm`

## Reproduction

From the repository root:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\tests\e2e\run-authenticated-snapshot.ps1
```

The complete Go suite was also run from `server`:

```powershell
go test ./...
```

It passed. Outside the runner, the cross-process package skips unless
`E2E_RUN=1`, so ordinary unit-suite runs do not require live services.

The runner:

1. refuses to run if ports 8080–8083 are already occupied;
2. builds each service into a temporary directory;
3. starts Login first and waits for both `/readyz` and the immutable config;
4. starts and waits for Zone, Coordinator, then Gate;
5. runs `go test ./test/e2e -count=1 -v` from `server`;
6. stops every started process in reverse order in `finally`, prints service
   logs, and removes its temporary binaries and logs.

## Observed key output

Raw key lines from the successful run:

```text
BUILD service=login
BUILD service=zone
BUILD service=coordinator
BUILD service=gate
START service=login pid=17260
READY service=login-health url=http://127.0.0.1:8080/readyz status=200
READY service=login-config url=http://127.0.0.1:8080/v1/client-config/1 status=200
START service=zone pid=22156
READY service=zone url=http://127.0.0.1:8082/readyz status=200
START service=coordinator pid=46864
READY service=coordinator url=http://127.0.0.1:8083/readyz status=200
START service=gate pid=28524
READY service=gate url=http://127.0.0.1:8081/readyz status=200
TEST command=go test ./test/e2e -count=1 -v
REGISTER status=201 account=e2e_291dd38bdf8e player_id=1 csrf_rotated=true
BOOTSTRAP player_id=1 gateway_id=local-gateway websocket_url=ws://127.0.0.1:8081/ws config_version=1 protocol=1..1
CONFIG bytes=11 sha256=dbd01629801b6486450a9a74c32c3be770280aa6a89a3b8178c4a83490bb75ec schema_version=1 client_config_version=1
TICKET status=201 gateway_id=local-gateway expires_at_ms=1785464982759
AUTH request_id=aa9b7fdb-a579-4fd4-89ff-8afdacb1a90d seven_fields_match_bootstrap=true
PING request_id=c30738f5-31b5-475a-8b97-9bc10fd85c44 ping_id=17 correlated=true
SNAPSHOT request_id=0c65ea0d-8aef-4011-81ef-4474c28d89e5 target_player_id=1 snapshot_player_id=1 owner_epoch=1 player_seq=0 coins=10 fertilizer=1 empty_plot=1
TICKET_REPLAY second_authentication_succeeded=false close_code=4401
--- PASS: TestAuthenticatedSnapshot (0.08s)
PASS
ok github.com/Wriosley/supernova-classic-farm/server/test/e2e 0.780s
RESULT authenticated_snapshot_e2e=PASS adapter=in-memory-development-only
STOP pid=28524 exited=True
STOP pid=46864 exited=True
STOP pid=22156 exited=True
STOP pid=17260 exited=True
```

Service startup logs explicitly identified the adapters and control-plane
boundary:

```text
using development-only in-memory auth store; registrations are not durable
state_adapter=lazy-in-memory-development-only
consensus=false high_availability=false
```

## What was checked

- Binary-Protobuf HTTP with exact H5 Origin and a cookie jar obtained CSRF.
- Registration used a unique valid account and returned a valid Session.
- Registration rotated the CSRF cookie; the rotated proof issued the ticket.
- Bootstrap supplied the selected Gateway; the ticket was bound to its ID.
- The immutable config body was size-limited, SHA-256 verified, decoded, and
  checked for schema/config version agreement.
- Binary-Protobuf WebSocket AUTH used the H5 Origin and the one-time ticket.
- All seven AUTH fields matched the HTTP bootstrap fields.
- PING returned the same `request_id`, `ping_id`, and client timestamp.
- `GET_PLAYER_SNAPSHOT` returned the same request ID and target/player ID,
  `(owner_epoch=1, player_seq=0)`, 10 coins, one basic fertilizer, and one empty
  plot.
- A second connection using the consumed ticket failed with close code 4401.
- All four child processes were observed stopped by the runner.

## Limitations

- This run proves only in-memory development behavior. Restart recovery,
  durable registration, MySQL checkpoints, Dirty writeback, and Outbox
  durability were not exercised.
- The PING response was correlated through the real Gate process, but this
  black-box run has no Actor-activation or Zone-call counter. Therefore it does
  not independently prove that PING avoided the Actor; that property requires
  separate instrumented/unit evidence.
- No browser UI actions were automated.
- No throughput, concurrency, latency, availability, or 30-million-DAU
  capability was measured.
