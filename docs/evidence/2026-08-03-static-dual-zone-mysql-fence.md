---
status: completed
date: 2026-08-03
claim_scope: static dual-Zone MySQL Fence alignment
---

# Static Dual-Zone MySQL Fence Evidence

## Claim boundary

The implementation adds a bounded epoch-one bootstrap path that aligns the
existing 4096 MySQL Fence rows with Coordinator's static Zone A/B ShardMap.
It does not implement or claim MySQL-backed Shard migration.

## Code and test coverage added

- transactional validation and conversion of all 4096 bootstrap Fences;
- rejection of unexpected Owner, epoch, route version or missing rows;
- deterministic bootstrap transition identities;
- registration epoch selection from the locked per-Shard Fence;
- process-specific Zone identity in checkpoint Fence validation;
- startup ordering that aligns Fences before Login accepts registrations;
- a dual-Zone MySQL E2E for one persisted write in each Zone;
- suppression of the manual move endpoint while MySQL mode is active.

## Verification commands

```text
go test ./...
go vet ./...
tests/e2e/run-authenticated-snapshot.ps1
tests/e2e/run-dual-zone-routing.ps1
tests/e2e/run-dual-zone-mysql.ps1
```

## Current verification status

The owner ran the commands in PowerShell after the Cursor command runner
stopped returning process exit status.

Observed:

```text
go test ./...                                      PASS
go vet ./...                                       PASS
run-authenticated-snapshot.ps1                    PASS
run-dual-zone-routing.ps1                         PASS
run-dual-zone-mysql.ps1                           PASS

updated_shards=4096
zone_a_player=12 shard=145
zone_b_player=13 shard=3806
persisted_seq=1
```

The MySQL run started Coordinator before Login, atomically converted all 4096
original Fence rows, activated one Player through each Zone and observed both
Dirty checkpoints at `player_seq=1`. It also verified wrong-Zone rejection and
that MySQL mode exposes no manual move endpoint.

## Limitations

- Only original epoch-one bootstrap rows can be aligned.
- The Coordinator ShardMap remains process-local.
- Starting with prior migration state is intentionally rejected.
- MySQL mode exposes no manual move endpoint because Fence CAS and Dirty drain
  are not implemented.
- There is no stale-owner delayed-write runtime evidence yet.
