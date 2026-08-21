---
status: completed
date: 2026-08-03
---

# Local development test platform (v1)

## Goal

Expose existing Go unit/vet, frontend checks and PowerShell E2E wrappers through
a loopback-only whitelist runner with tiered safety and MySQL credentials kept
out of the browser.

## Done

- Added `tests/catalog.json` and integrity validation in
  `server/internal/testcatalog`.
- Added `server/cmd/testrunner` with single-run lock, `.env` → `MYSQL_DSN`,
  log redaction, SSE status/logs and destructive confirmation.
- Added `tests/platform/web` Vue UI and `tests/platform/start-runner.ps1`.
- MySQL PowerShell wrappers prefer `.env`/environment before `Read-Host`.
- Sanitized `.env.example` placeholders.

## Boundary

Platform history is not formal evidence. Active Shard MySQL Migration remains
`destructive` / non-repeatable and must not be treated as a routine button.
