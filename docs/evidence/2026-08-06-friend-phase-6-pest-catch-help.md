---
status: verified
date: 2026-08-06
evidence_type:
  - code
  - test
  - contract
---

# 好友功能阶段 6：投虫、捉虫和好友清理

## 完成范围

在阶段 5 偷菜 Saga 基础设施上补齐另外三种好友互动：

| 操作 | WS Action | 访客资源 | 农场主地块 |
|---|---|---|---|
| 投虫 | `APPLY_PEST_TO_FRIEND` (320) | 扣 1 次投虫机会 | `GROWING` 且无虫时写入 `pest_effect`（含 `source_player_id`） |
| 捉虫 | `CATCH_PEST_FOR_FRIEND` (321) | 扣 1 次捉虫机会 | 移除虫害；虫源不能捉自己投的虫 |
| 帮忙清理 | `HELP_CLEAN_FRIEND_PLOT` (322) | 扣 1 次清理机会 | `NEED_CLEANUP → EMPTY` |

偷菜路径保持不变（仍走 `StealSaga`）。三种新互动共用 `ActionSaga`。

### 1. 玩家侧

- `server/internal/player/config.go`：新增 `PestConfig`；开发默认
  `pest_id=1`、`modifier=-300_000`、`duration_ms=120_000`。
- `server/internal/player/public_plot_view.go`：`CanApplyPest` /
  `CanCatchPest` / `CanHelpClean`。
- `server/internal/player/friend_action_chance.go`：机会预留 /
  提交 / 释放（`reserved_action_chances=1`，同步 SaveCAS）。
- `server/internal/player/apply_pest.go`：Owner 写入虫害、访客提交扣机会
  并推进第二章任务 `TASK_APPLY_PEST_TO_FRIEND`（task id 8）。
- `server/internal/player/catch_pest.go`：Owner 移除虫害；
  `ErrPestSourceForbidden` 映射 `PEST_SOURCE_FORBIDDEN`。
- `server/internal/player/help_clean_plot.go`：Owner 清理地块（镜像
  `CLEAN_PLOT` 的公开结果，但走 Saga 同步 CAS）。
- 三种 Owner 应用成功后都调用 `notifyPublicPlots`，接入 `FarmViewPatch`。

### 2. Saga / Zone / Gate

- `server/internal/interaction/action_saga.go`：与 `StealSaga` 同态的状态机
  （INIT → VISITOR_RESERVED → OWNER_APPLIED → VISITOR_COMMITTED → COMPLETED /
  RELEASING → ABORTED）；投虫把 `pest_id` 写入 `FriendInteraction`。
- `server/internal/interaction/reconciler.go`：按 `record.Action` 分派到
  `StealSaga` 或 `ActionSaga`。
- `server/cmd/zone/friend_rpc.go`：`ExecuteFriendAction` /
  `ApplyVisitorAction` 分派四种 action。
- `server/internal/gateway/gateway.go` + `grpc_visitor.go`：校验并路由
  320–322，与偷菜共用 `ExecuteFriendAction` RPC。

### 3. H5

- `web/src/lib/ws.ts`：`applyPestToFriend` / `catchPestForFriend` /
  `helpCleanFriendPlot`。
- `web/src/App.vue`：统一 `runFriendAction`；错误码文案覆盖
  `PLOT_NOT_ELIGIBLE` / `PEST_ALREADY_PRESENT` / `PEST_SOURCE_FORBIDDEN` /
  `INSUFFICIENT_ACTION_CHANCE`。
- `web/src/components/FriendFarmDashboard.vue`：投虫 / 捉虫 / 偷菜 / 清理
  四工具；按地块状态高亮可点目标。开发期投虫固定 `pest_id=1`。

## 明确未执行 / 已知限制

- ~~**农场主本人捉虫**~~：已补单玩家 `CATCH_PEST`（208），主人免费捉虫；
  清理仍走既有免费 `CLEAN_PLOT`。主人农场 UI 展示 `pest_effect` /「有虫」
  角标，工具栏可选「捉虫」。
- 公开视图只暴露 `pest_active`，不暴露虫源；访客点「捉虫」后若是自己投的
  虫，由服务端返回 `PEST_SOURCE_FORBIDDEN`。
- 未做完整多进程浏览器 E2E（留给阶段 7）；本阶段以单元 / Zone / Gate
  包测试 + `npm run build` 验收。

## 质量门

```text
cd server && go test ./... -count=1
cd web && npm run build
```

上述命令均通过（2026-08-06）。

## 启动方式

需要**重新编译并重启** `./start-servers.sh --dual-zone --tcaplus`，
当前内存中的 Zone/Gate 二进制仍是阶段 5 编译产物，不含 320–322 路由。

浏览器刷新 H5 后进入好友农场，工具栏可选投虫 / 捉虫 / 偷菜 / 清理。
