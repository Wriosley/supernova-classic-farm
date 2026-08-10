---
status: verified
date: 2026-08-10
evidence_type:
  - code
  - test
---

# 广播与业务解耦

## 完成范围

公开农场广播从“业务/Runtime 直接猜 Action 并起 goroutine 推送”拆成：

```text
业务 DomainChanges
-> Actor mailbox 内生成有序 FarmViewPatch
-> farmview.Dispatcher（有界队列 + 固定 worker）
-> Broadcaster（访客/Gate 分组）
-> Gate Push
```

未做：广播持久化、离线重放、Gate 可靠缓存、自动重连（留给 sprint 06）。

## 删除的业务广播耦合点

| 旧点 | 现状 |
|---|---|
| `publicPlotIDsChanged` 中央 Action switch | 已删除；业务显式返回 `DomainChanges` |
| `notifyPublicPlots` + 每事件 detached goroutine | 改为 `publishFarmViewChanges` → `Dispatcher.Enqueue` |
| `pendingSyncStep.farmViewPlotIDs` | 改为 `domainChanges DomainChanges` |
| Runtime 查询访客 / 选 Gate / 调 Push | 仅 Dispatcher→Broadcaster 负责 |
| Zone 直接挂 Broadcaster | `SetFarmViewDispatcher` + `defer Close` |

## DomainChanges → FarmViewEvent 路径

1. plant / fertilizer / harvest / clean / owner catch / 成熟结算 / FriendInteraction owner 变更报告 `PlotChanged`。
2. `Runtime.Handle` 在 mailbox 内合并 `DomainChanges`（含同批成熟）。
3. `publishFarmViewChanges` 在 owner mailbox 内 `farm_view_seq++` 并 `buildFarmViewPatch`。
4. mailbox 外只 `Enqueue` 不可变 Patch；失败业务不 bump、不入队。
5. FriendInteraction：SaveCAS 成功后才 publish；失败/未持久化不广播；durable 重放不二次 bump。

## Dispatcher 配置

| 项 | 默认 |
|---|---|
| 队列 | 256（队满丢**最新**） |
| worker | 2 |
| Broadcast 超时 | 2s |
| Close 排空超时 | 3s |

指标/日志：`published` / `failed` / `dropped`；队满与广播失败打 Warn。

## 客户端恢复语义

`web/src/lib/farm-view.ts` 的 `decideFarmViewPatch`：

- `seq == local + 1` → apply
- `seq <= local` → ignore
- `seq > local + 1` → resync（拉完整快照）
- epoch 变化 → resync

`App.vue` 访客路径已改用该决策函数。

## 验证命令与结果

```bash
cd server
go test ./internal/player \
  -run 'DomainChanges|Plant|Fertilizer|Matur|Harvest|Clean|Steal|Pest|Help|FarmView|SyncPersist|PublicPlot' \
  -count=1
# ok

go test -race ./internal/player \
  -run 'FarmViewEvent|FarmViewPatch|PublicPlot|PublishFarmView|BuySeedsDoesNotBump' \
  -count=10
# ok

go test -race ./internal/farmview -count=10
# ok

go test -race ./internal/player ./internal/interaction \
  -run 'SyncPersist|Steal|Pest|Catch|Help|Reconcile' \
  -count=10
# ok

go test -race \
  ./internal/player \
  ./internal/farmview \
  ./internal/interaction \
  ./internal/visit \
  -count=1
# ok

go test ./... -count=1
go vet ./...
# ok
```

前端：

```bash
cd web
npx tsx --test src/lib/farm-view.test.ts
# 4 pass（顺序 / 重复 / gap / epoch）
```

## 未重跑项（与 sprint 01/02 一致）

- 三客户端 / 完整好友 E2E（需本机 kind 或 `start-servers.sh --dual-zone --tcaplus`）；本轮以单元与包级 race 为准。
- 广播仍为**内存 best-effort**；丢包靠客户端 gap/epoch 拉快照，不依赖广播历史。

## 主要文件

| 文件 | 作用 |
|---|---|
| `server/internal/player/domain_change.go` | 领域变化模型 |
| `server/internal/player/farm_view_events.go` | mailbox 内构造 Patch 并 Enqueue |
| `server/internal/farmview/dispatcher.go` | 有界异步投递 |
| `server/cmd/zone/main.go` | 组装 Dispatcher |
| `web/src/lib/farm-view.ts` | 客户端合并决策 |
| `docs/study/tasks/基本联机闭环复盘考虑/friend.md` | 去掉“业务直接广播”旧描述 |
