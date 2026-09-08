# class-mid 测试说明

本文列出当前仓库实际存在的测试、验证目标和运行方法。Go 测试位于 `server/internal/game`，前端测试位于 `web/tests`。

## 一键基础验证

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\dev.ps1 -Action test
```

该脚本运行 Go 测试和 Vue 生产构建。开发时建议再分别运行前端单元测试和 vet。

## Go 规则与安全测试

| 测试 | 验证内容 |
|---|---|
| `TestOwnerLoopAndReplay` | 购买、去重、种植、施肥、成熟收获、出售、领奖和清理的完整闭环 |
| `TestConcurrentPurchasesCannotOverspend` | 20 个并发购买不能把同一玩家金币扣成负数 |
| `TestFailedOperationDoesNotMutateState` | 金币不足或仓库满时不留下部分修改 |
| `TestCredentials` | 输入校验、密码哈希、正确/错误密码和损坏哈希 |

```powershell
cd server
go test ./internal/game -run "TestOwnerLoopAndReplay|TestConcurrentPurchasesCannotOverspend|TestFailedOperationDoesNotMutateState|TestCredentials" -count=1
```

## HTTP 与 WebSocket 测试

| 测试 | 验证内容 |
|---|---|
| `TestJSONLoginAndWebSocket` | 注册、重复注册、错误密码、登录、AUTH、购买、注销关闭连接 |
| `TestRejectUnknownFieldsAndUnauthenticatedGame` | 拒绝 HTTP 未知字段和未认证游戏操作 |
| `TestJSONMailbox` | 欢迎邮件、已读、跨玩家隔离和非法 mail_id |

这些测试使用 `httptest.Server` 和真实 WebSocket 客户端，存储使用 MemoryStore，不依赖 MySQL。

## 存储测试

| 测试 | 验证内容 |
|---|---|
| `TestMemoryMailboxWelcomeAndOwnership` | 每个新玩家收到欢迎邮件，只能标记自己的邮件 |
| `TestMemoryMailboxReturnsCopy` | 返回邮件是副本，调用者不能篡改内部内容 |
| `TestMySQLCreateWelcomeCommit` | 三项注册写入成功后提交 |
| `TestMySQLCreateWelcomeRollback` | 欢迎邮件失败时注册整体回滚 |
| `TestMySQLMailboxOwnership` | 邮件 SQL 含 player_id，跨玩家更新被拒绝 |
| `TestMySQLTransactionRollbackAndCommit` | 存档成功提交，写入失败回滚且不报告成功 |

sqlmock 检查 SQL 顺序和事务，不需要启动 MySQL：

```powershell
cd server
go test ./internal/game -run "TestMySQL" -count=1
```

## 真实 MySQL 测试

`TestLiveMySQLRecovery` 只有设置 `CLASS_MID_TEST_MYSQL_DSN` 才运行。它创建唯一测试账号，写入欢迎邮件，执行购买，换一个数据库连接验证状态恢复和请求去重，再验证邮件和已读持久化，最后清理独立测试数据。

```powershell
cd server
$env:CLASS_MID_TEST_MYSQL_DSN = $env:MYSQL_DSN
go test ./internal/game -run TestLiveMySQLRecovery -count=1 -v
```

不要把 DSN 或密码写进命令历史、日志、文档和 Git。如果使用 `.env`，可参考 `start-servers.ps1` 在当前进程中读取。

## Vue 测试与构建

| 测试 | 验证内容 |
|---|---|
| `unconfirmed request survives re-login and is isolated per player` | 未确认写请求按玩家保存、读取和删除，不串号 |
| `counts unread mail and replaces local list with server response` | 未读计数和服务器权威邮件列表替换 |

```powershell
cd web
npm.cmd test
npm.cmd run build
```

构建命令同时执行 `vue-tsc --noEmit` 和 Vite 生产构建。

## 静态检查

```powershell
cd server
go vet ./...
Get-ChildItem .\internal\game, .\cmd\game -Filter *.go -Recurse |
    ForEach-Object { gofmt -d $_.FullName }

cd ..
git diff --check
```

## 浏览器人工冒烟

1. MySQL 模式启动后端并启动 Vite。
2. 注册唯一测试账号。
3. 确认显示 4 块地且所有作物文案为胡萝卜。
4. 买 3 颗种子，确认金币从 10 变为 4。
5. 打开邮箱，确认一封未读欢迎邮件。
6. 点击邮件，关闭并再次打开，确认仍为已读。

已有验证记录覆盖上述核心步骤。

## Qt 客户端

源码在 `qt/`。`classic_farm_smoke` 对本机游戏服务做注册、快照、购买和欢迎邮件检查。构建目录必须是纯英文路径。

```powershell
$env:PATH = "C:\Qt\Tools\mingw1310_64\bin;C:\Qt\Tools\CMake_64\bin;C:\Qt\Tools\Ninja;C:\Qt\6.11.2\mingw_64\bin;" + $env:PATH
cmake -G Ninja -DCMAKE_PREFIX_PATH=C:/Qt/6.11.2/mingw_64 -DCMAKE_BUILD_TYPE=Release -S .\qt -B C:\build\classic-farm-qt
cmake --build C:\build\classic-farm-qt
C:\build\classic-farm-qt\classic_farm_smoke.exe
```

## 当前未覆盖范围

- 尚无 Qt GUI 自动化点击测试；当前 Qt 验证为编译与无界面冒烟。
- 没有完整浏览器自动化套件，当前 UI 端到端检查为人工冒烟。
- 没有覆盖 MySQL 提交结果未知的所有网络故障。
- 没有公网安全、TLS、高可用和大规模性能测试。
- 没有为所有参数边界逐项建立独立用例，核心失败分支由规则和协议测试覆盖。
