---
status: tested-unit
date: 2026-07-31
scope: MySQL registration transaction and Player checkpoint activation code
---

# MySQL registration and checkpoint unit evidence

## Claim boundary

This evidence covers compiled Go code and mocked-SQL unit tests for:

- atomically inserting one account, initial `PlayerCheckpointV1` and first
  Session in a single transaction;
- rolling back registration when the checkpoint insert fails;
- deterministic checkpoint encoding and SHA-256 verification;
- rejecting checkpoint envelope/blob disagreement;
- activating a Player Actor from a checkpoint loader without silently creating
  development state when loading fails.

It does not prove that the migration executes on MySQL 8.4, that data survives
a real process restart, or that Dirty writeback and database fencing work.

## Commands

From `server`:

```powershell
go test ./...
go vet ./...
```

Both commands passed on 2026-07-31.

The startup script also passed PowerShell parse validation, and
`git diff --check` reported no whitespace errors.

## Implemented boundary

- `deploy/migrations/000002_auth_player_checkpoint.up.sql` defines accounts,
  HTTP Sessions and the accepted Player checkpoint envelope.
- LoginSvr selects the MySQL adapter only when `MYSQL_DSN` is non-empty.
- Registration uses one serializable transaction for account, checkpoint and
  Session visibility.
- ZoneSvr selects a MySQL checkpoint loader only when `MYSQL_DSN` is non-empty.
- A Player Actor is activated from the validated deterministic checkpoint blob.
- Without `MYSQL_DSN`, the existing explicit in-memory development path remains
  available for the first-stage E2E.

## Remaining limitations

- This file records only unit evidence. A later live registration/checkpoint
  run is recorded in `2026-07-31-mysql-authenticated-snapshot-e2e.md`; restart
  recovery remains unverified.
- Auth SQL DDL is an implementation schema, not a frozen contract. The local
  slice currently chooses `AUTO_INCREMENT player_id`, `db_shard_id = 0`,
  initial `checkpoint_revision = 1`, `owner_epoch = 1` and one empty plot;
  these conventions must be reviewed before they are treated as accepted.
- WS Ticket and CSRF records remain process-local even in MySQL mode.
- Online Player Actors remain in Zone memory by design; only activation loading
  is implemented.
- Dirty mutation/writeback, checkpoint CAS, Outbox persistence, shard fences,
  Actor eviction flush and recovery under ownership change remain unimplemented.
