# Simple Relational Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the JSON player save with simple MySQL relationship tables while preserving the current Qt protocol and adding demonstrable friend, farm-view, stealing, and friend-mail functions.

**Architecture:** Keep one Go HTTP/WebSocket process. HTTP handles registration, login and new friend endpoints; WebSocket keeps the existing Qt game actions. Handlers call direct MySQL functions, and each multi-table write uses one straightforward transaction.

**Tech Stack:** Go, `net/http`, `database/sql`, `github.com/go-sql-driver/mysql`, `github.com/coder/websocket`, MySQL 8, PowerShell.

**Spec:** `docs/superpowers/specs/2026-09-14-relational-backend-design.md`

## Global Constraints

- Work only on `class-mid`; do not merge `main`.
- Modify backend, SQL and backend documentation only; do not require Qt source changes.
- Preserve existing HTTP paths, WebSocket actions and old snapshot fields.
- Use simple English identifiers and Chinese comments.
- Use nine `class_mid_*` relation tables and discard old class-mid account data through a manual reset script.
- New players receive 16 plots and six inventory rows.
- Passwords accept 6–20 ASCII letters/digits and use the documented Caesar shift by 3; describe it as reversible classroom encoding.
- Do not restore distributed services, Actor, Protobuf, request fingerprinting or MemoryStore.
- Keep parameterized SQL, Session identity, transactions and the lock required to prevent duplicate stealing.
- Run relevant Go tests and record actual results in `docs/evidence/`; do not claim unrun checks passed.
- Do not commit or push automatically.

---

### Task 1: Define the relational schema and reset procedure

**Files:**
- Create: `server/sql/reset_class_mid.sql`
- Modify: `server/internal/game/mysql.go`
- Test: `server/internal/game/mysql_live_test.go`

**Interfaces:**
- Produces: `OpenDatabase(dsn string) (*sql.DB, error)`, `InitTables(ctx context.Context, db *sql.DB) error`.
- Produces tables: accounts, players, crops, inventory, plots, tasks, player_tasks, mails, friends.

- [ ] **Step 1: Change the live schema test to expect relation tables**

The test connects only when `CLASS_MID_TEST_MYSQL_DSN` is set, calls `InitTables`, queries `information_schema.tables`, and asserts all nine names exist.

```go
want := []string{
    "class_mid_accounts", "class_mid_players", "class_mid_crops",
    "class_mid_inventory", "class_mid_plots", "class_mid_tasks",
    "class_mid_player_tasks", "class_mid_mails", "class_mid_friends",
}
```

- [ ] **Step 2: Run the schema test and confirm it fails**

```powershell
cd server
go test ./internal/game -run TestLiveRelationalSchema -count=1 -v
```

Expected without the test DSN: SKIP. With the test DSN: FAIL because the new tables/functions do not exist.

- [ ] **Step 3: Write the manual reset SQL**

Drop child tables before parent tables, create the nine InnoDB tables with primary/foreign keys, add indexes on `(receiver_id, mail_id)` and `(player_id, friend_id)`, and insert the six crop rows plus small task definitions.

The plot key is `(player_id, plot_no)`, inventory key is `(player_id, crop_id)`, and friend key is `(player_id, friend_id)`. `class_mid_players` includes `state_version BIGINT UNSIGNED NOT NULL DEFAULT 1`.

- [ ] **Step 4: Replace old JSON table initialization**

`InitTables` contains only `CREATE TABLE IF NOT EXISTS` and `INSERT ... ON DUPLICATE KEY UPDATE` for fixed crops/tasks. It must never drop user data.

- [ ] **Step 5: Run schema verification**

```powershell
cd server
$env:CLASS_MID_TEST_MYSQL_DSN=$env:MYSQL_DSN
go test ./internal/game -run TestLiveRelationalSchema -count=1 -v
```

Expected: PASS and six rows in `class_mid_crops`.

### Task 2: Replace the model and password code

**Files:**
- Modify: `server/internal/game/model.go`
- Modify: `server/internal/game/password.go`
- Modify: `server/internal/game/rules_test.go`

**Interfaces:**
- Produces: `encodePassword(password string) string`, `validateCredentials(username, password string) error`.
- Produces response types: `State`, `Plot`, `InventoryItem`, `Crop`, `Friend`, `Mail`, `Command`, `Response`.

- [ ] **Step 1: Write focused Caesar and validation tests**

```go
func TestEncodePassword(t *testing.T) {
    if got := encodePassword("Farm789"); got != "Idup012" {
        t.Fatalf("got %q", got)
    }
}

func TestSimpleCredentialValidation(t *testing.T) {
    if validateCredentials("student_a", "Farm123") != nil { t.Fatal("valid input rejected") }
    if validateCredentials("student_a", "含中文") == nil { t.Fatal("non ASCII password accepted") }
}
```

- [ ] **Step 2: Run and confirm failure**

```powershell
cd server
go test ./internal/game -run "TestEncodePassword|TestSimpleCredentialValidation" -count=1
```

- [ ] **Step 3: Implement simple models and Caesar encoding**

Remove `Receipt` and fingerprint fields. Keep old JSON names and add optional `crop_id`, `crop_name`, `inventory`, `shop`, `friends`, and `friend_id`. Rotate lowercase, uppercase and digits separately by three positions.

- [ ] **Step 4: Run the focused tests**

```powershell
cd server
go test ./internal/game -run "TestEncodePassword|TestSimpleCredentialValidation" -count=1
```

Expected: PASS.

### Task 3: Implement registration, login and relational snapshots

**Files:**
- Modify: `server/internal/game/mysql.go`
- Modify: `server/internal/game/http.go`
- Modify: `server/internal/game/http_test.go`
- Delete: `server/internal/game/store.go`
- Delete: `server/internal/game/store_test.go`

**Interfaces:**
- Produces: `NewServer(db *sql.DB) *Server`.
- Produces DB functions: `createPlayer`, `findAccount`, `loadState`, `listMails`, `markMailRead`.
- Keeps: `/healthz`, `/api/config`, `/api/register`, `/api/login`, `/api/logout`.

- [ ] **Step 1: Write a MySQL integration test for registration**

Register a unique username and assert one account, one player, 16 plots, six inventory rows and one welcome mail. Log in with the same password and assert a token and player ID are returned.

- [ ] **Step 2: Run and confirm failure**

```powershell
cd server
go test ./internal/game -run TestLiveRegisterRelationalPlayer -count=1 -v
```

- [ ] **Step 3: Implement direct registration transaction**

Use one `BEGIN`; insert account with `encodePassword`, insert player, loop plot numbers 1–16, insert six inventory rows, insert player tasks and welcome mail, then commit.

- [ ] **Step 4: Simplify login and Session handling**

Remove `authSlots` and PBKDF2 calls. Query account by username, compare stored password to `encodePassword(input)`, create token, store token-to-player mapping under the existing mutex, and return the old response shape.

- [ ] **Step 5: Assemble the old snapshot from relation tables**

`loadState` reads players, inventory, plots joined with crops, tasks, and shop. It fills `seeds` and `crops` from crop ID 1 while also returning all inventory/shop rows. It derives `MATURE` in the response when `now >= mature_at_ms`.

- [ ] **Step 6: Run registration and HTTP tests**

```powershell
cd server
go test ./internal/game -run "TestLiveRegisterRelationalPlayer|TestHTTP" -count=1 -v
```

Expected: PASS for enabled tests; environment-gated live tests may SKIP without DSN.

### Task 4: Replace Engine/apply with direct game transactions

**Files:**
- Create: `server/internal/game/game.go`
- Modify: `server/internal/game/websocket.go`
- Modify: `server/internal/game/rules_test.go`
- Delete: `server/internal/game/rules.go`

**Interfaces:**
- Produces: `executeGame(ctx context.Context, db *sql.DB, playerID int64, command Command) (State, error)`.
- Produces helpers: `buySeeds`, `buyFertilizer`, `plant`, `fertilize`, `harvest`, `cleanPlot`, `sellCrop`, `claimReward`.

- [ ] **Step 1: Write one relational farm-flow test**

The test registers a player, buys crop ID 1 seeds, plants plot 1, sets its maturity to the past as test setup, harvests, cleans, sells one crop, and asserts player, inventory and plot rows changed correctly.

- [ ] **Step 2: Run and confirm failure**

```powershell
cd server
go test ./internal/game -run TestLiveRelationalFarmFlow -count=1 -v
```

- [ ] **Step 3: Implement direct transactions**

Each write begins a transaction, performs readable `SELECT` checks, uses parameterized `UPDATE`, increments `class_mid_players.state_version`, and commits. Missing `crop_id` becomes 1 so the current Qt continues to operate on carrots.

- [ ] **Step 4: Keep WebSocket request/response compatibility**

Retain AUTH and `request_id` echo. Read actions call `loadState`; game actions call `executeGame`; mailbox actions call direct mail functions. Remove calls to Engine and Store.

- [ ] **Step 5: Run farm and WebSocket tests**

```powershell
cd server
go test ./internal/game -run "TestLiveRelationalFarmFlow|TestJSONLoginAndWebSocket" -count=1 -v
```

Expected: PASS for enabled tests; the live test may SKIP without its DSN.

### Task 5: Add simple friend HTTP endpoints

**Files:**
- Modify: `server/internal/game/http.go`
- Modify: `server/internal/game/mysql.go`
- Create: `server/internal/game/friend_test.go`

**Interfaces:**
- Adds: `POST /api/friends/add`, `GET /api/friends`, `GET /api/friends/{id}/farm`, `POST /api/friends/{id}/steal`, `POST /api/friends/{id}/mail`.
- Produces: `addFriend`, `listFriends`, `loadFriendFarm`, `stealCrop`, `sendFriendMail`.

- [ ] **Step 1: Write one end-to-end friend integration test**

Create players A and B, add B by username, verify both directed friend rows, query B's 16 plots, prepare one mature crop, steal it, assert A receives crop yield and one coin, assert B's plot is `NEED_CLEANUP`, send a mail, and assert B receives it.

- [ ] **Step 2: Run and confirm failure**

```powershell
cd server
go test ./internal/game -run TestLiveFriendFlow -count=1 -v
```

- [ ] **Step 3: Implement authenticated friend handlers**

Read the bearer token with the existing identity function. Use Go 1.22 route path variables for `{id}`. Reject self-add, non-friends and malformed IDs with the stable errors from the spec.

- [ ] **Step 4: Implement add, view, steal and mail SQL**

Add friendship in both directions in one transaction. For stealing, verify friendship, lock the exact target plot with `SELECT ... FOR UPDATE`, check maturity, update thief inventory and coins, set the target plot to `NEED_CLEANUP`, then commit.

- [ ] **Step 5: Run the friend test**

```powershell
cd server
go test ./internal/game -run TestLiveFriendFlow -count=1 -v
```

Expected: PASS when the test DSN is configured; otherwise SKIP.

### Task 6: Update startup and remove obsolete backend code

**Files:**
- Modify: `server/cmd/game/main.go`
- Delete or simplify: obsolete sqlmock tests in `server/internal/game/mysql_test.go`
- Modify: `server/go.mod`
- Modify: `server/go.sum`

**Interfaces:**
- `main.go` passes `*sql.DB` directly to `game.NewServer`.
- MySQL is required for the relational mode; startup reports a clear error if it is unavailable.

- [ ] **Step 1: Simplify startup wiring**

Remove MemoryStore selection and Store injection. Keep connection-pool settings, `InitTables`, HTTP server creation, signal handling and graceful shutdown.

- [ ] **Step 2: Remove obsolete dependencies and tests**

Delete tests that only verify the removed callback/JSON-store behavior. Retain tests that still prove current HTTP/WebSocket behavior. Run `go mod tidy`.

- [ ] **Step 3: Run all Go checks**

```powershell
cd server
go test ./... -count=1
go vet ./...
```

Expected: all non-environment tests PASS; live MySQL tests SKIP unless configured; vet exits zero.

### Task 7: Add the backend-only friend demonstration

**Files:**
- Create: `server/scripts/demo-friends.ps1`
- Modify: `docs/contracts/json-api.md`
- Modify: `docs/architecture.md`
- Modify: `docs/testing.md`

**Interfaces:**
- Script parameters: `-BaseUrl http://127.0.0.1:8080`, two unique usernames and passwords.
- Consumes all five friend HTTP endpoints and existing register/login/mailbox functions.

- [ ] **Step 1: Write the PowerShell demonstration**

Use `Invoke-RestMethod` to register/login A and B, add the friend, list friends, view 16 plots, steal a prepared mature plot, send a mail and print the returned JSON. Never print tokens or database passwords.

- [ ] **Step 2: Document the exact API additions and compatibility rules**

Document all request bodies, bearer headers, success responses, errors, six-crop fields, default carrot behavior and the fact that old Qt ignores new fields.

- [ ] **Step 3: Run the demonstration against local MySQL**

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\server\scripts\demo-friends.ps1
```

Expected: two users become friends, 16 plots are returned, the prepared mature crop is stolen once, and the friend mail is visible.

### Task 8: Final compatibility verification and evidence

**Files:**
- Create: `docs/evidence/2026-09-14-relational-backend.md`
- Modify: `docs/context/CURRENT.md`
- Modify: `docs/midterm-defense.md`

**Interfaces:**
- Records commands, results and limitations; introduces no runtime interfaces.

- [ ] **Step 1: Reset the local class-mid database manually**

```powershell
Get-Content .\server\sql\reset_class_mid.sql |
    & 'C:\Program Files\MySQL\MySQL Server 8.0\bin\mysql.exe' -u root -p $env:MYSQL_DATABASE
```

Enter the password interactively. Before execution, print `$env:MYSQL_DATABASE` and confirm it is the intended course database; do not print the password.

- [ ] **Step 2: Run backend verification**

```powershell
cd server
go test ./... -count=1
go vet ./...
```

- [ ] **Step 3: Build and smoke-test the unchanged Qt client**

```powershell
cmake --build C:\build\classic-farm-qt
C:\build\classic-farm-qt\classic_farm_smoke.exe
```

Expected: existing Qt register, login, AUTH, snapshot, carrot purchase and mailbox flow still works.

- [ ] **Step 4: Check formatting and Git scope**

```powershell
cd server
gofmt -w .\cmd\game\main.go .\internal\game\*.go
go test ./... -count=1
cd ..
git diff --check
git status --short
```

Confirm no `.env`, build cache, Qt Creator files or packaged binaries are staged.

- [ ] **Step 5: Record honest evidence**

Write only commands actually executed and their real outcomes. State that Caesar encoding is reversible, friend features have no Qt UI, request deduplication was removed, and testing is local coursework coverage rather than production security or capacity validation.
