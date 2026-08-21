---
status: completed
date: 2026-08-03
---

# Static Dual-Zone MySQL Fence Alignment

## Goal and boundary

Continue after memory-only inactive-Shard migration by first making the static
dual-Zone assignment compatible with MySQL. Active Actor migration, epoch
advance and PREPARING recovery remain separate work.

## Human decision

The owner chose the staged option: align the static dual-Zone ShardMap and
MySQL Fences first, then implement migration.

## AI-assisted changes

- Added all-Shard transactional bootstrap reconciliation in Coordinator.
- Required explicit bootstrap authorization and rejected non-bootstrap Fence
  state.
- Changed registration to derive initial checkpoint epoch from its locked
  Fence.
- Parameterized the checkpoint writer with the current Zone identity.
- Enabled dual-Zone MySQL startup while withholding the move endpoint.
- Added a password-safe PowerShell wrapper and a two-Owner MySQL persistence
  E2E.
- Drafted the bounded plan and pending evidence record.

## Safety correction

The existing memory-only move endpoint cannot remain enabled in MySQL mode.
It changes Route epoch without advancing the database Fence, so a new Owner
could activate but every later Dirty flush would be fenced. The endpoint is
therefore omitted whenever Coordinator has `MYSQL_DSN`.

An independent read-only review then found three launcher defects. Bootstrap
authorization was moved from the parent environment to the Coordinator child
only, Coordinator Zone IDs/endpoints are now pinned to the launched processes,
and the MySQL wrapper no longer URL-encodes a password that the Go driver would
not decode. A follow-up review found caller-environment leakage, so both launch
scripts now restore every process-level variable they change. The E2E also
asserts that MySQL mode exposes no move endpoint.

## Verification

The owner ran verification manually after the command runner stopped reporting
process exit status:

```text
go test ./...                                    PASS
go vet ./...                                     PASS
run-authenticated-snapshot.ps1                  PASS
run-dual-zone-routing.ps1                       PASS
run-dual-zone-mysql.ps1                         PASS
```

The live MySQL run aligned 4096 Fences and persisted one `player_seq=1` write
for a Zone-A player and one for a Zone-B player.

## Remaining uncertainty

- The next migration phase still needs old-Owner final flush, PREPARING-bound
  Fence CAS, checkpoint load and recoverable activation.
- A database converted to the dual-Zone Fence assignment is mode-specific; it
  must not be reused for the `zone-local` MySQL mode without an explicit safe
  conversion procedure.
