# Tests

Unit tests stay next to their Go or web source files (`*_test.go`, package scripts).
This directory holds cross-cutting E2E wrappers and the **local development test platform**.

## Source of truth

- Formal measured claims and defense evidence remain under `docs/evidence/`.
- Platform run history under `tests/platform/.history/` is local convenience only and must not replace evidence files.

## Catalog

[`catalog.json`](catalog.json) is the explicit whitelist of runnable checks.

When you add a test that the platform should expose:

1. Add or update an entry in `catalog.json`.
2. Keep `files` accurate so integrity validation continues to pass.
3. Mark `destructive: true` and `repeatable: false` for anything that permanently advances Fence epochs or otherwise makes the database unsuitable for bootstrap/restart claims.
4. Run `go test ./internal/testcatalog` from `server/`.

Every `tests/e2e/*.ps1` script must be referenced by the catalog (`files` or `command.script`), including shared helpers such as `_mysql-env.ps1`.

## Local test platform

The runner is a loopback-only Go process. The UI never accepts arbitrary shell commands; it only posts a catalog `id`.

### MySQL credentials

Put local values in the repo-root `.env` (gitignored):

```env
MYSQL_HOST=127.0.0.1
MYSQL_PORT=3306
MYSQL_DATABASE=classicfarm
MYSQL_USER=classicfarm
MYSQL_PASSWORD=请在本地填写
```

Copy from `.env.example` and fill the password locally. The runner reads `.env` on the server, builds `MYSQL_DSN` for child processes, and redacts passwords/DSNs from API responses and streamed logs. The browser only sees `mysqlConfigured: true|false`.

PowerShell MySQL wrappers prefer process env / `.env`, and only prompt with `Read-Host` when no password is configured.

### Start

From the repo root:

```powershell
# API + optional built UI on http://127.0.0.1:7199
./tests/platform/start-runner.ps1 -BuildUI
```

Or in two terminals for UI hot reload:

```powershell
# terminal 1
cd server
go run ./cmd/testrunner -addr 127.0.0.1:7199

# terminal 2
cd tests/platform/web
npm install
npm run dev
# open http://127.0.0.1:7198
```

### Safety tiers

| Tier | Meaning |
|---|---|
| `safe` | Unit/vet/frontend; no service ports |
| `service` | Starts local processes and occupies ports; global single-run lock |
| `mysql` | Requires configured local MySQL; runner pings before start |
| `destructive` | Mutates durable Fence/migration state; requires typed confirmation `I_UNDERSTAND_DESTRUCTIVE` |

`e2e-dual-zone-mysql` (**Active Shard MySQL Migration**) is `destructive` and `repeatable: false`. After it succeeds, that database must not be reused for epoch-one bootstrap tests or Coordinator restart-recovery claims.

## Existing E2E scripts

PowerShell wrappers live in `tests/e2e/`. Prefer launching them through the platform, or invoke a script directly when debugging. Do not pass MySQL passwords on the command line.
