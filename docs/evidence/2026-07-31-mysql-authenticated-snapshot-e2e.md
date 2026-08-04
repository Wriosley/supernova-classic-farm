---
status: measured
date: 2026-07-31
scope: live MySQL registration and Player checkpoint path
---

# Live MySQL authenticated snapshot evidence

## Claim boundary

This evidence covers one owner-run local test using:

- MySQL Community Server 8.4.11 on `127.0.0.1:3306`;
- migration tables `accounts`, `auth_sessions`, `player_checkpoints` and
  `schema_migrations`;
- four real Go processes: LoginSvr, ZoneSvr, the single-node
  Coordinator-compatible process and GateSvr;
- the real MySQL registration transaction and Zone checkpoint loader.

It proves that LoginSvr committed a new account, first HTTP Session and initial
Player checkpoint to live MySQL, and that ZoneSvr loaded that checkpoint to
produce the Player Actor snapshot.

It does not prove login or checkpoint recovery after process restart, WS Ticket
durability, Dirty writeback, database fencing, availability or capacity.

## Reproduction

From the repository root:

```powershell
.\tests\e2e\run-mysql-authenticated-snapshot.ps1
```

The wrapper securely prompts for the `classicfarm` application password,
constructs `MYSQL_DSN` only in the process environment, runs the existing
four-process E2E and removes the temporary environment value afterward.

## Observed result

Key output from the owner-run test:

```text
REGISTER status=201 account=e2e_4d6e9783d465 player_id=1 csrf_rotated=true
BOOTSTRAP player_id=1 gateway_id=local-gateway websocket_url=ws://127.0.0.1:8081/ws
AUTH ... seven_fields_match_bootstrap=true
PING ... correlated=true
SNAPSHOT ... snapshot_player_id=1 owner_epoch=1 player_seq=0 coins=10 fertilizer=1 empty_plot=1
TICKET_REPLAY second_authentication_succeeded=false close_code=4401
--- PASS: TestAuthenticatedSnapshot (0.26s)
RESULT authenticated_snapshot_e2e=PASS adapter=mysql-checkpoint
```

Service logs identified the selected storage paths:

```text
using MySQL auth store; registration provisions account, Session, and Player checkpoint atomically
using MySQL Player checkpoint activation
state_adapter=mysql-checkpoint
```

## Dependency download explanation

The first Go build downloaded:

```text
github.com/go-sql-driver/mysql v1.10.0
filippo.io/edwards25519 v1.2.0
```

These are Go module dependencies used by the application and MySQL driver.
They are not another MySQL server installation. Go caches them for later builds.

## Follow-up

The later
`2026-07-31-mysql-restart-recovery-e2e.md` records a passing
`register -> BUY_SEEDS -> Dirty flush -> stop all four processes -> fresh
stack -> login -> mutated snapshot` test with `player_seq=1`.
