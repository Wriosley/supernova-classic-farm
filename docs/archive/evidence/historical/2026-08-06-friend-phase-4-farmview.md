---
status: verified
date: 2026-08-06
evidence_type:
  - code
  - test
  - contract
---

# 好友功能阶段 4：公开 Patch 与访客 Presence 增量广播

## 完成范围

本阶段把阶段 3 的“一次性公开快照”扩展为增量 `FarmViewPatch` 广播：

- `server/internal/player/runtime.go`：
  - 新增 `FarmViewBroadcaster` 接口（`Broadcast(ctx, ownerPlayerID, patch)
    error`），Runtime 通过它而不是直接依赖 `package farmview`，避免
    `player -> farmview -> player` 循环依赖；
  - `Runtime.SetFarmViewBroadcaster` 允许在开发/测试环境不接线也能正常
    工作：`notifyPublicPlots` 总会在 Actor Mailbox 内把 `farmViewSeq++`
    并生成 `FarmViewPatch`，只有真正配置了 Broadcaster 才会异步扇出，
    未配置时只是不产生网络调用；
  - `notifyPublicPlots(ctx, a, ownerPlayerID, plotIDs)`：在同一次
    `mailbox.Do` 里递增 `farm_view_seq` 并投影 `plotIDs` 当前状态为
    `FarmViewPatch`（保证下一次 `GET_PLAYER_SNAPSHOT`/`ENTER_FRIEND_FARM`
    能立刻看到新 seq），随后用 `go func` 在 mailbox 之外调用
    `Broadcaster.Broadcast`（2 秒超时），失败只是吞掉，不影响已经返回
    给玩家的命令结果；
  - `publicPlotIDsChanged` 收集一次 `Handle` 调用里“访客可见”的地块
    变化：所有本次成熟事件涉及的地块（不区分触发它们的 action），加上
    成功且非重放的 `PLANT`/`APPLY_FERTILIZER`/`HARVEST`/`CLEAN_PLOT`
    自身的 `plot_id`；`BUY_SEEDS`、`BUY_FERTILIZER`、`SELL_CROP`、
    `CLAIM_CHAPTER_REWARD`、`GET_SHOP`、`GET_PLAYER_SNAPSHOT` 均不产生
    公开变化；
  - `materializeOnlineMaturities`（每秒在线成熟扫描）在转发
    `PLAYER_STATE_CHANGED` Push 之后，同样调用 `notifyPublicPlots`，让
    离线于当前命令、纯因时间到期成熟的地块也能广播给访客。
- `server/internal/player/farm_visit_snapshot.go`：新增
  `buildFarmViewPatch`，复用既有 `publicPlotView` 投影，对入参
  `plotIDs` 去重、按 `plot_id` 排序后生成 `PublicPlotView` 列表，附带
  当前 Actor incarnation 的 `farm_view_epoch` 与刚递增的 `farm_view_seq`。
- `server/internal/player/grpc_push.go`：`GRPCPushForwarder` 新增
  `PublishFarmViewPatch(ctx, gateID, recipientPlayerIDs, patch)`，实现
  `farmview.PatchPublisher`；与既有 `PublishPlayerStateChanged`/
  `PublishFarmPresence` 共用同一个 gRPC 连接，但一次调用可以携带多个
  `recipient_player_id`。
- `server/internal/farmview/broadcast.go`（新增）：
  - `PatchPublisher`、`VisitorLister` 两个小接口把 Gate 推送和访客枚举
    解耦成可测试的依赖；
  - `Broadcaster.Broadcast` 把收件人分组为 `owner_gate_id -> {owner}` 加
    `visitor.GateID -> {visitor...}`，同一 Gate 的收件人合并为一次
    `PublishFarmViewPatch` 调用；访客列表为空时仍然会给农场主自己的
    Gate 推一次（覆盖“农场主本人在线查看自己农场，成熟 Push 也要
    触发公开 Patch”的场景）；任一 Gate 调用失败用 `errors.Join` 汇总
    但不阻止其余 Gate 收到推送。
- `server/internal/visit/owner_service.go`：新增
  `OwnerService.ListVisitors(ownerPlayerID) []VisitRecord`，包一层已有
  `Registry.ListVisitors`，让 `cmd/zone/main.go` 可以把 `OwnerService`
  直接当作 `farmview.VisitorLister` 使用，不用导出 Registry。
- `server/internal/gateway/grpc_push.go`：新增
  `GRPCPushServer.PublishFarmViewPatch`——校验 `gate_id`、
  非空收件人、`patch.owner_player_id`、`patch.version.farm_view_epoch`
  非空、`patch.version.farm_view_seq > 0`，然后对每个
  `recipient_player_id` 各构造一个 `FARM_VIEW_CHANGED` PUSH Envelope
  （不带 `state_version`）投递给 `PushHub`。
- `server/internal/gateway/push_hub.go`：`validatePushEnvelope` 新增
  `FARM_VIEW_CHANGED` 分支，要求 `state_version == nil`、payload 存在且
  `owner_player_id`/`farm_view_epoch`/`farm_view_seq` 均非零。
- `server/cmd/gate/main.go`：`AllowedCallers` 为
  `GatePushService.PublishFarmViewPatch` 放行
  `zone-local`/`zone-a`/`zone-b`，与已有的
  `PublishPlayerStateChanged`/`PublishFarmPresence` 一致。
- `server/cmd/zone/main.go`：在 `ownerFarmService` 创建之后构造
  `farmview.NewBroadcaster(pushForwarder, ownerFarmService, gatewayID)`
  并调用 `runtime.SetFarmViewBroadcaster`，把 Phase 4 广播接进真实
  Zone 进程；`pushForwarder`（`*player.GRPCPushForwarder`）同时满足
  `farmview.PatchPublisher`，`ownerFarmService`（`*visit.OwnerService`）
  同时满足 `farmview.VisitorLister`。
- `web/src/lib/ws.ts`：新增 `setFarmViewChangedHandler`；`handleMessage`
  新增 `FARM_VIEW_CHANGED` 分支，校验无 `state_version`、payload 为
  `farmViewChangedPush`、`owner_player_id`/`version`/`farm_view_epoch`/
  `farm_view_seq` 均非零/非空，其余情况仍按既有规则 `failProtocol`
  断开连接。
- `web/src/App.vue`：
  - 订阅 `setFarmViewChangedHandler(handleFarmViewChanged)`；
  - 新增 `applyFarmViewPatch`，按 `plot_id` 合并 `plot_upserts` 到当前
    `visitSnapshot`；
  - `handleFarmViewChanged`：只处理与当前 `visitOwnerId` 匹配的 Patch；
    `farm_view_epoch` 变化（Owner 重启/迁移/重新创建 Actor）或
    `farm_view_seq` 跳号（`> local + 1`）都调用 `enterFriendFarm` 重新
    `ENTER_FRIEND_FARM` 换回完整快照；`seq == local + 1` 原地合并；
    `seq <= local` 视为重复/乱序，直接忽略；农场主进入/离开的
    `FarmPresencePush` 提示条逻辑未受影响。

## 明确未执行

- 投虫、捉虫、清理、偷菜四种互动仍未实现（阶段 5、6 范围），本阶段的
  `FarmViewPatch` 仅由种植/施肥/收获/清理/自然成熟触发。
- `docs/archive/development/plans/friend_design_plan/06-分阶段实施方案.md` 里阶段 4 列出的
  `server/internal/farmview/push.go` 文件未新增：投递逻辑合并进了
  `broadcast.go`（`Broadcaster` 本身）和既有的
  `server/internal/gateway/grpc_push.go`/`push_hub.go`，没有再拆出单独
  文件；行为覆盖范围与计划一致。
- 未新增端到端真实进程联调（多个真实浏览器/多个真实 Zone 进程互相
  看到彼此的 Patch），仍以单元测试（`bufconn`、内存 Runtime、stub
  Broadcaster/VisitorLister）验收。

## 质量门

以下命令均通过：

```text
cd server && go build ./...
cd server && go vet ./...
cd server && go test ./...
cd server && go test ./... -race
cd web && npm run build   # vue-tsc --noEmit && vite build
```

新增/修改测试：

- `server/internal/farmview/broadcast_test.go`：
  `TestBroadcastAlwaysIncludesOwnerOnOwnerGate`、
  `TestBroadcastGroupsVisitorsByGateAndIncludesOwner`、
  `TestBroadcastCoalescesVisitorOnOwnerGate`、
  `TestBroadcastSkipsVisitorsWithEmptyGateID`、
  `TestBroadcastReturnsJoinedErrorAndStillCallsEveryGate`、
  `TestNewBroadcasterRejectsMissingDependencies`、
  `TestBroadcastRejectsMissingArguments`。
- `server/internal/player/farm_view_notify_test.go`：
  `TestBuySeedsDoesNotBumpFarmViewSeq`（私有购买不增加
  `farm_view_seq`，也不触发 Broadcast）、
  `TestPlantBumpsFarmViewSeqAndBroadcastsPatch`（`PLANT` 之后
  `farm_view_seq=1` 且 Broadcast 收到含该地块的 Patch）、
  `TestHarvestAndCleanPlotEachBumpFarmViewSeqOnce`（种植、收获、清理
  三次公开变化各让 `farm_view_seq` 加一，Broadcast 各调用一次）、
  `TestRuntimeWorksWithoutFarmViewBroadcasterConfigured`（未配置
  Broadcaster 时 `farm_view_seq` 依然正确递增，不 panic）。
- `server/internal/visit/owner_service_test.go`：
  `TestOwnerServiceListVisitorsWrapsRegistry` 验证
  `OwnerService.ListVisitors` 转发到 Registry 且按 owner 隔离。
- `server/internal/gateway/grpc_adapters_test.go`：
  `TestGRPCPushServerFansFarmViewPatchOutToEveryRecipient`、
  `TestGRPCPushServerRejectsInvalidFarmViewPatchPushes`（覆盖错误
  `gate_id`、空收件人列表、收件人为 0、`patch` 为空、
  `owner_player_id`/`farm_view_epoch`/`farm_view_seq` 缺失等七种非法
  组合）。

## 启动方式

```bash
./start-servers.sh --dual-zone --tcaplus
```

Zone 启动时会用既有 `GATEWAY_ID`/`GATE_RPC_URL` 构造
`farmview.Broadcaster` 并注入 Runtime；无需新增环境变量。浏览器打开
`web/`（`npm run dev`），两个账号互相成为好友后，一方进入另一方农场，
农场主随后种植/施肥/收获/清理或自然成熟时，访客侧应在不重新
`ENTER_FRIEND_FARM` 的情况下看到对应地块实时刷新。
