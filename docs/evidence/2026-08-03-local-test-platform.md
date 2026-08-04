---
status: completed
date: 2026-08-03
claim_scope: local test platform smoke
---

# Local test platform smoke

## Claim boundary

This records that the loopback whitelist runner starts, rejects unconfirmed
destructive runs, and can execute the catalogued `go-unit` entry. It does not
claim business E2E results or replace existing product evidence.

## Commands

```text
go test ./internal/testcatalog ./internal/testrunner
cd tests/platform/web && npm run build
go run ./cmd/testrunner -addr 127.0.0.1:7199
POST /api/tests/e2e-dual-zone-mysql/run without confirmToken
POST /api/tests/go-unit/run
```

## Observed results

```text
go test ./internal/testcatalog ./internal/testrunner   PASS
platform UI build                                      PASS
destructive run without confirm                        HTTP 409
go-unit via runner                                     PASS exit=0
```

## Limitations

- MySQL-backed and service E2E buttons were not all exercised in this smoke.
- Platform history under `tests/platform/.history/` is not formal evidence.
