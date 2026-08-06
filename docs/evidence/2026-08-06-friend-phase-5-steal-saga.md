---
status: verified
date: 2026-08-06
evidence_type:
  - code
  - test
  - contract
---

# 好友功能阶段 5：互动 Saga 基础与偷菜闭环

## 完成范围

本阶段只实现 `STEAL_FRIEND_CROP` 一种互动，验证跨 Actor 同步 CAS 与
`FriendInteraction` Saga 恢复模型；投虫、捉虫、清理留给阶段 6，复用同一套
`server/internal/interaction` 基础设施。

### 1. 地块冻结偷菜模型

- `server/internal/player/state.go`：`Plot` 新增
  `StealCount`/`StealQuantity`/`MaxStealTimes`/`ProtectedOwnerYield`
  （对应 `PlotStateRecord` 字段 16–19）。
- `server/internal/player/checkpoint.go`：
  - `Plot.Record`/`plotFromRecord` 往返这四个字段；
  - `validatePlotRecord` 沿用既有 MATURE 校验路径，偷菜字段本身允许为 0
    （代表“不可偷”，向后兼容阶段 5 之前种下的作物）；
  - `checkpointFromState`/编码路径新增对
    `FriendReservations`/`FriendReceipts`/`FriendTaskCreditReceipts` 的
    确定性排序（按 `interaction_id`/复合键排序），满足契约“同一状态必须
    编码为同一字节序列”的要求。
- `server/internal/player/config.go`：
  - `CropConfig` 新增 `StealQuantity`/`MaxStealTimes`/`ProtectedOwnerYield`；
  - 新增开发期常量 `developmentStealQuantity=1`、
    `developmentMaxStealTimes=2`、`developmentProtectedOwnerYield=1`，
    接入 `NewDevelopmentConfigSnapshot`；
  - 新增 `ConfigSnapshot.SoleStealableCrop()`：在偷菜字段只被 config 而非
    请求携带的前提下（`StealFriendCropRequest` 本身不带
    `crop_item_id`/`quantity`），为 Zone 的 `ExecuteFriendAction` 解析出
    唯一可偷作物；这是本阶段一个记录在案的简化，见“已知限制”。
- `server/internal/player/plant.go`：`PLANT` 把 `crop.StealQuantity`/
  `MaxStealTimes`/`ProtectedOwnerYield` 与 `BaseYield` 一起原子性地冻结进
  新 `Plot`，此后热更新 `CropConfig` 不影响已种下的地块。
- `server/internal/player/public_plot_view.go`（新增）：
  `CanSteal(plot *Plot) bool`，规则见代码注释：`MATURE` +
  `steal_quantity>0` + `max_steal_times>0` + `steal_count<max` +
  `base_yield >= stolen_quantity+steal_quantity+protected_owner_yield`
  （用 `uint64` 中间和避免溢出）。
- `server/internal/player/farm_visit_snapshot.go`、
  `server/internal/farmview/view.go`：两处 `PublicPlotView.can_steal`
  投影都改为调用 `player.CanSteal`，避免逻辑分叉；`farmview` 不反向依赖
  `player` 内部结构，只通过导出的 `Plot`/`CanSteal` 使用。

### 2. Player 侧同步 Saga 步骤

`server/internal/player/steal_crop.go`（新增）在 Actor Mailbox 内运行、
通过既有 `flushPlayerLocked` 同步落盘（区别于普通命令的异步 Dirty）：

- `ReserveSteal`：懒升级好友 Schema v2；按 `interaction_id` 去重（同一
  interaction 重放直接返回 `alreadyReserved=true`，字段不一致视为语义冲突
  拒绝）；结合其它存活 reservation 校验仓库类型上限 100、堆叠上限 300；
  追加/更新一条 `action=STEAL, status=RESERVED` 的
  `FriendResourceReservation`；只递增 `checkpoint_revision`（reservation
  不是对外可见的业务状态），同步 `SaveCAS`。
- `ApplyStealOnOwner`：Mailbox 天然把并发访客串行化；按
  `interaction_id`+`OWNER APPLIED` 去重（重放返回完全相同的
  `result_payload`/`result_digest_sha256`）；用 `CanSteal` 校验；成功后把
  `StealCount++`、`StolenQuantity += frozen_quantity`；追加
  `action=STEAL, status=APPLIED` 的 `OWNER` 回执，回执内容是确定性 marshal
  的 `wsv1.FriendActionResponse{interaction_id, farm_patch}`；
  `PlayerSeq++`、`CheckpointRevision++`；同步 `SaveCAS` 成功后才在
  Mailbox 之外调用 `notifyPublicPlots` 递增 `farm_view_seq` 并通过既有
  Broadcaster 异步扇出 `FarmViewPatch`；`CanSteal` 失败返回
  `ErrStealNotAvailable`，不做任何地块/Checkpoint 修改。
- `CommitSteal`：按 `interaction_id`+`VISITOR COMMITTED` 去重（重放返回
  相同响应）；要求存在匹配的 `RESERVED` reservation，否则拒绝；消费
  reservation 并把冻结数量的作物加进访客仓库；追加
  `action=STEAL, status=COMMITTED` 的 `VISITOR` 回执；若任务 7
  （`TASK_STEAL_CROP`）存在且处于 `IN_PROGRESS`，按既有 claimant/封顶逻辑
  推进一次（并复用重放去重，保证重试不重复推进）；`PlayerSeq++` +
  `CheckpointRevision++`，同步 `SaveCAS`；返回 `PlayerStatePatch` 与最终
  `wsv1.FriendActionResponse`（携带从 Owner 回执里恢复出的
  `farm_patch`）。
- `ReleaseSteal`：用于 Owner 确定性拒绝（如 `STEAL_NOT_AVAILABLE`）后释放
  访客侧的预留；幂等（reservation 已消费/已释放时是空操作）；同步
  `SaveCAS`，只递增 `checkpoint_revision`，不产生对客户端可见的
  `PlayerSeq`。
- `server/internal/player/runtime.go`：`notifyPublicPlots` 改为返回
  构建出的 `FarmViewPatch`（原来只是 fire-and-forget），供
  `ApplyStealOnOwner` 这类同步调用方在自己的响应里使用；新增
  `Runtime.CurrentConfig()` 暴露当前 `ConfigSnapshot`（供
  `SoleStealableCrop` 使用）。

### 2.1 同步落盘的持久性不变量（审计修复）

首版实现里，上述四个步骤都是「先改 Actor 内存、再同步 `SaveCAS`」，但
**幂等标记与被保护的状态存放在同一块 Actor 内存里**：`FriendResourceReservation`、
`OWNER`/`VISITOR` `FriendInteractionReceipt`、`FriendTaskCreditReceipt`。
因此当第一次 `SaveCAS` 失败（fenced / stale / retryable）而 Actor 未被
驱逐时，Saga 用同一个 `interaction_id` 重试会命中这些**尚未落盘**的标记，
直接返回成功（`alreadyReserved` / `alreadyApplied` / `alreadyCommitted`），
违反阶段 5 的停止条件：**未落盘的内存不得被当作已完成的 Saga 步骤**。
后果包括：Saga 把 `VISITOR_RESERVED`/`OWNER_APPLIED` 当作已完成并前进，
`ApplyStealOnOwner` 的重试还会**永远不广播** `FarmViewPatch`（原实现只在
首次成功路径广播，重放路径返回 `nil`），进程重启后农场主地块的
`steal_count`/`stolen_quantity` 丢失而访客仓库已落盘。

修复（`server/internal/player/sync_persist.go`，新增）：

- `runtimeActor` 新增 mailbox 私有、不持久化的 `syncPending`
  （`map[string]pendingSyncStep`）：一个**durable-pending 标记**，记录
  「本进程改了内存但还没证明落盘」的步骤及其
  `revision`（可选带上欠广播的 `plot_id`）。键按步骤种类 +
  `interaction_id`/`relation_id` 命名空间化，因为访客的
  reserve/commit/release 三步共用同一个 `interaction_id`。
- `Runtime.settleSyncStepLocked` 是所有同步 Saga 步骤（首次与每次同 ID
  重试）唯一的收尾路径：标记存在且 `persistedRevision < 标记 revision`
  时，`markDirty` + 既有 `flushPlayerLocked` 重新发起 `SaveCAS`，再回
  mailbox 校验 `persistedRevision` 确实覆盖了该 revision，否则返回
  `ErrCheckpointNotDurable`/底层 CAS 错误（**失败仍是失败**）。证明落盘后
  才在 mailbox 内摘除标记；因为 checkpoint 是整状态快照且
  `persistedRevision` 单调递增，「revision 被覆盖」即等价于「该步骤已落盘」。
- 广播恰好一次：`ApplyStealOnOwner` 把 `plot_id` 记在标记上，
  `notifyPublicPlots` 由**摘除标记的那一次调用**执行（摘除在 mailbox 内，
  并发重试只有一个赢家）。所以首刷失败后的重试返回
  `alreadyApplied=true` **并且**返回它刚落盘的 `FarmViewPatch`（Saga/RPC
  已经原样转发 `farm_patch`，无需改动）；而对本就落盘的重放仍返回
  `nil`，`farm_view_seq` 只递增一次。
- 不影响普通命令：只有同步步骤标记过的 revision 会被提前刷出，且仍走同一个
  `flushPlayerLocked`（mailbox 内取整状态快照 + 针对 Actor
  `persistedRevision`/token 的 CAS），不会与周期 Dirty 刷盘交错或抢跑；
  `Handle` 走的普通命令仍然只 `markDirty`。
- `ApplyFriendTaskCredit`（阶段 2 同样的同步模式）存在完全相同的缺陷，
  一并改为走 `settleSyncStepLocked`；顺带把原来在 mailbox 之外读
  `a.state.CheckpointRevision` 的两处并发读收进 mailbox。
- 已知残余限制：标记是内存态。若 `SaveCAS` 实际成功但确认失败、且 Actor
  随后被驱逐/迁移，重启后从 checkpoint 读到回执的重放会返回
  `alreadyApplied=true` 且不带 `farm_patch`（欠的那次广播丢失）。访客的
  `farm_view_seq` 去重与 ENTER/HEARTBEAT 快照会自愈这类视图偏差，业务状态
  本身没有错误；跨进程持久化这个标记需要新的 checkpoint 字段（冻结契约，
  不在本阶段范围）。

### 3. `server/internal/interaction` 包（Saga + Tcaplus Store）

- `store.go`：`Store` 接口（`Get`/`Insert`/`Update`，`Update` 走
  Tcaplus 记录版本 CAS，`ErrNotFound`/`ErrAlreadyExists`/
  `ErrVersionConflict` 哨兵错误）。
- `tcaplus_store.go`：`TcaplusStore` 照搬
  `server/internal/friend/tcaplus_store.go` 的重试/CAS 模式实现同一接口。
- `memory_store.go`：`MemoryStore`，非 Tcaplus 开发模式下的等价实现，同样
  支持 `Traverse`（配合 `Reconciler`）。
- `ids.go`：`ParseInteractionID`（把 WS 规范 UUID `request_id` 解析成 16
  字节）、`RequestDigest`（对 action、visitor/owner ID、visit_id、
  plot_id、pest_id=0 做确定性 SHA-256，相同 `interaction_id` 配不同摘要
  视为冲突）。
- `saga.go`：`StealSaga`，状态机严格是
  `INIT -> VISITOR_RESERVED -> OWNER_APPLIED -> VISITOR_COMMITTED ->
  COMPLETED`，以及 `INIT/*_RESERVED -> RELEASING -> ABORTED`；每步之后都
  立刻 `Update` 持久化，可从任意中断点恢复；`VisitorSteps`/
  `OwnerFarmClient` 两个窄接口让本包既不依赖 `internal/player` 也不依赖
  `internal/visit`，避免循环引用；`Execute`/`Resume` 区分“新建或加载”与
  “只加载”两种入口，供 RPC 直接调用与 `Reconciler` 复用同一状态机。
- `domain_errors.go`：把 `player.Runtime` 的哨兵错误（如
  `ErrStealNotAvailable`）映射成 `wsv1.ErrorCode`。
- `reconciler.go`：`Reconciler.ReconcileDue` 用 `Traverse` 扫描所有未终态
  且 `retry_at_ms` 已到期的记录，通过 `RequestResolver` 接口重新解析
  `StealRequest` 的易失字段（`VisitorOwnerEpoch`/`OwnerRoute`/
  `CropItemID`/`Quantity`，因为它们不持久化在 `FriendInteraction` 上），
  然后调用 `StealSaga.Resume` 继续推进。

### 4. Visit/RPC 接线

- `server/internal/visit/owner_client.go`：`OwnerFarmClient` 接口新增
  `ApplyVisitorAction`；`ZoneOwnerFarmClient` 实现时会重新解析并覆盖
  `OwnerRoute`（与 `EnterVisitor`/`Heartbeat`/`ExitVisitor` 一致的“每次
  调用都用 Coordinator 最新路由”模式）。
- `server/internal/visit/owner_service.go`：新增
  `OwnerService.ValidateVisitorAction`，包一层 `registry.Validate` 并把
  `ErrVisitNotFound`/`ErrVisitExpired` 转成 `wsv1.Error`。
- `server/cmd/zone/friend_rpc.go`：
  - `VisitorZoneService.ExecuteFriendAction`：校验参数、解析
    `interaction_id`、通过 `ownerAuthorization` 确认自己是调用者 Shard 的
    owner、用 `SoleStealableCrop` 解出 `crop_item_id`/`quantity`、调用
    `StealSaga.Execute`，把 `AbortedError`/`ErrDigestConflict`/
    `ErrOutcomeUnknown` 映射成内联 `wsv1.Error`；只接受
    `STEAL_FRIEND_CROP`，其余 action 返回 `UNKNOWN_ACTION`。
  - `OwnerFarmService.ApplyVisitorAction`：校验参数、`validateRoute` 做
    Fence 校验、`OwnerService.ValidateVisitorAction` 校验访问会话、调用
    `Runtime.ApplyStealOnOwner`，把 `ErrStealNotAvailable`/`ErrNotOwner`
    映射成对应的错误响应；只接受 `STEAL_FRIEND_CROP`。
- `server/cmd/zone/interaction_wiring.go`（新增）：`zoneStealResolver`
  实现 `interaction.RequestResolver`，用于 `Reconciler` 重新解析易失字段。
- `server/cmd/zone/main.go`：
  - `tcaplusdb.Open` 增加 `FriendInteraction` 表；
  - 按 `STORAGE_MODE` 选择 `interaction.NewTcaplusStore`（真实 Tcaplus）或
    `interaction.NewMemoryStore`（开发期/MySQL 模式，避免引入 MySQL 运行
    时依赖）；
  - 构造 `StealSaga`（`interaction.NewStealSaga(store, runtime,
    ownerFarmClient)`，`runtime` 满足 `VisitorSteps`、
    `*visit.ZoneOwnerFarmClient` 满足 `OwnerFarmClient`）与 `Reconciler`，
    以 5 秒 ticker 驱动 `ReconcileDue`；
  - 把 `StealSaga`/`Runtime` 分别注入 `visitorZoneRPCServer`/
    `ownerFarmRPCServer`；
  - HMAC allowlist 新增 `ExecuteFriendAction <- gate`、
    `ApplyVisitorAction <- zone-local/zone-a/zone-b`。

### 5. Gate + H5

- `server/internal/gateway/gateway.go`：`validateRequestTuple` 新增
  `STEAL_FRIEND_CROP` 分支（`owner_player_id!=0`、`visit_id` 16 字节、
  `plot_id>0`；`target_player_id==caller` 复用已有通用校验）；
  `handleGame`/`handleVisitAction`/`callVisitor` 把 `STEAL_FRIEND_CROP`
  与既有三种访问 action 一样路由到调用者自己 Shard 所在的 Zone。
- `server/internal/gateway/grpc_visitor.go`：`VisitorZoneClient` 接口新增
  `Steal`；`GRPCVisitorZoneCommander.Steal` 调用
  `VisitorZoneService.ExecuteFriendAction`（`action=STEAL_FRIEND_CROP`），
  把结果映射回 `steal_friend_crop_response`/`Error`。
- `web/src/lib/ws.ts`：新增 `stealFriendCrop(playerId, ownerPlayerId,
  visitId, plotId)`。
- `web/src/App.vue`：
  - 只在 `plot.canSteal` 为真时渲染“偷菜”按钮；
  - `stealFriendCrop` 调用后把响应的 `visitor_patch` 通过既有 `applyPatch`
    合并进本地快照；**不**手动应用响应里的 `farm_patch`——农场主侧的
    `FARM_VIEW_CHANGED` Push（`handleFarmViewChanged`）会带着正确的
    `farm_view_seq` 去重独立到达，重复应用两次有竞态风险；
  - 也**不**手动推进本地 `stateVersion`：`FriendActionResponse`
    没有 `state_version` 字段（冻结契约），但 `CommitSteal` 确实推进了
    访客自己的 `player_seq`；这个一次性偏差会被下一次任意命令响应或
    `PLAYER_STATE_CHANGED` Push 触发的既有 gap 检测（
    `recoverSnapshotGap`）自愈，属已知、可接受的限制（见下）。

## 明确未执行 / 已知限制

- 投虫、捉虫、清理三种互动仍未实现（阶段 6 范围）；`FarmInteraction`
  Saga 状态机、`interaction` 包本身按“足够通用”设计，但目前只有 `StealSaga`
  一个具体实现。
- `SoleStealableCrop` 是阶段 5 的一个简化：`StealFriendCropRequest`
  本身不携带 `crop_item_id`/`quantity`，而当前开发期配置只定义了一种
  可偷作物，所以 Zone 直接从 `ConfigSnapshot` 里按“唯一可偷作物”解析。
  多作物场景需要 Zone 先读一次 Owner 的实际地块（如
  `GetPublicFarmSnapshot`）才能确定被偷的是哪种作物，本阶段未实现。
- 访客侧偷菜成功响应不带 `state_version`（冻结的 `FriendActionResponse`
  契约如此），因此 H5 只能乐观合并 `visitor_patch` 而不能推进本地
  `player_seq` 计数器；下一次普通命令或成熟 Push 到达时，既有的
  seq-gap 检测会自动触发一次完整快照重取，代价是一次多余的
  `GET_PLAYER_SNAPSHOT`，不产生数据错误。
- `docs/plans/friend_design_plan/05-验收与测试清单.md` §5 的“终态
  Interaction 保留 24 小时”一项：`Store` 实现均无删除方法（终态记录永久
  保留，不做主动清理），但 24 小时这个具体时长依赖 Tcaplus 表的 TTL/运维
  策略，不是本阶段 Go 代码可验证的内容，因此清单里注明为不在验证范围。
- 未新增端到端真实进程联调（多个真实浏览器互相偷菜）；服务端行为以
  `server/internal/interaction`、`server/internal/player/steal_crop_test.go`
  和 `server/cmd/zone/friend_rpc_steal_test.go` 的单元/集成测试覆盖，H5
  侧以 `npm run build`（含 `vue-tsc --noEmit`）类型检查为主，与阶段 2–4
  的验收方式一致。

## 冻结 Proto：无改动

本阶段严格复用阶段 0 已冻结的
`proto/classicfarm/v1/{data,rpc,ws}` 与
`deploy/tcaplus/schema/.../friend_tables.proto`：`PlotStateRecord`
字段 16–19、`FriendInteraction` 表、`ExecuteFriendAction`/
`ApplyVisitorAction` RPC、`StealFriendCropRequest`/`FriendActionResponse`/
`PublicPlotView.can_steal` 均已在阶段 0 就位，没有修改任何 `.proto` 文件。

## 质量门

以下命令均通过：

```text
cd server && go build ./...
cd server && go vet ./...
cd server && go test ./...
cd server && go test ./... -race
cd web && npm run build   # vue-tsc --noEmit && vite build
kubectl kustomize deploy/k8s
```

新增/修改测试（按包）：

- `server/internal/player/public_plot_view_test.go`（新增）：
  `TestCanStealExactBoundaryRules`（成熟态/两种未成熟态、
  `steal_quantity=0`、`max_steal_times=0`、`steal_count` 恰好等于/超过
  `max`、`base_yield` 恰好等于/低于所需产量、`stolen_quantity` 过大导致
  保护产量不足，共 11 组子用例）、`TestCanStealRejectsNilPlot`、
  `TestCanStealHandlesLargeCountersWithoutOverflow`（近 `uint32` 上限值
  验证无溢出）。
- `server/internal/player/runtime_test.go`（新增）：
  `TestPlantFreezesStealFieldsAndRoundTripsThroughCheckpoint`——`PLANT`
  之后地块的偷菜字段等于开发期 `CropConfig` 冻结值，且经
  `MarshalCheckpoint`/`UnmarshalCheckpoint`/`StateFromCheckpoint` 往返后
  不变。
- `server/internal/player/steal_crop_test.go`（新增）：
  `TestReserveStealAppendsReservationAndIsIdempotent`、
  `TestReserveStealRejectsConflictingRetry`、
  `TestReserveStealRejectsOverStackLimit`、
  `TestReserveStealRejectsOverTypeLimitConsideringLiveReservations`、
  `TestApplyStealOnOwnerMutatesOnceAndDedupesRetry`、
  `TestApplyStealOnOwnerRejectsWhenNotStealableWithoutMutating`、
  `TestApplyStealOnOwnerConcurrentVisitorsRespectMaxStealTimes`、
  `TestCommitStealCreditsInventoryAndTaskExactlyOnce`、
  `TestCommitStealRejectsWithoutMatchingReservation`、
  `TestReleaseStealReleasesLiveReservationIdempotently`、
  `TestOrdinaryCommandRemainsAsyncDirtyAfterSyncInteraction`。
  （审计修复附带：新增 `developmentStateAt` fixture，把
  `NewDevelopmentState` 用真实墙上时钟打的 `created_at` 对齐到测试固定的
  `now`，否则这些用例在 UTC 09:00 之后会因
  `created_at > updated_at` 而失败——与本次缺陷无关的既有测试脆弱性。）
- `server/internal/player/sync_persist_test.go`（新增，§2.1 的回归测试）：
  `TestReserveStealRetriesSaveCASAfterFailedSyncFlush`（首刷失败→无成功返回、
  未落盘；同 ID 重试**重新发起** `SaveCAS` 并落盘后才成功）、
  `TestReserveStealFencedFlushNeverReportsSuccess`（fenced 反复重试始终失败，
  不会因内存保留而变成成功）、
  `TestApplyStealOnOwnerRetryAfterFailedFlushBroadcastsOnce`（失败刷盘时
  不广播、`farm_view_seq` 不动；落盘的那次重试返回 `farm_patch` 并广播
  恰好一次；之后的重放不广播、不再递增 seq、不再写盘）、
  `TestCommitStealRetriesSaveCASAfterFailedSyncFlush`（失败后仓库/任务未落盘，
  重试落盘且只记一次）、
  `TestReleaseStealRetriesSaveCASAfterFailedSyncFlush`、
  `TestApplyFriendTaskCreditRetriesSaveCASAfterFailedSyncFlush`、
  `TestOrdinaryCommandStaysAsyncWhileSyncStepIsPending`（有未落盘标记时，
  `BUY_SEEDS` 仍然只进 Dirty 队列、不自己发起 `SaveCAS`）、
  `TestSettleSyncStepRejectsAFlushThatDidNotReachTheMarkedRevision`（直接钉住
  `settleSyncStepLocked` 的最后一道防线：`SaveCAS` 被接受本身不算证明，只有
  `persistedRevision` 覆盖标记 revision 才算；否则返回 `ErrCheckpointNotDurable`
  且**保留标记**，重试路径不被吞掉。该分支平时只在竞态下可达，故用白盒方式
  把标记 revision 设到本次刷盘够不到的位置来稳定复现）。
- `server/internal/interaction/`（新增包，含）：
  `ids_test.go`（`ParseInteractionID`/`RequestDigest`）、
  `memory_store_test.go`、`tcaplus_store_test.go`（含 CAS 冲突）、
  `reconciler_test.go`（含到期/未到期两种场景）、`saga_test.go`
  （happy path、摘要冲突、Owner 确定性拒绝、传输失败不误判成功、崩溃
  窗口 A/B/C）。
- `server/cmd/zone/friend_rpc_steal_test.go`（新增）：
  `TestZoneStealFriendCropEndToEnd`（用真实
  `player.Runtime`（内存 `CheckpointStore`）+ 真实
  `visit.OwnerService`/`OwnerService.EnterVisitor` + 进程内
  `loopbackOwnerFarmClient` 模拟 `ZoneOwnerFarmClient` 的“重解析路由后
  转发”行为，跑通 `ExecuteFriendAction -> StealSaga -> ApplyVisitorAction
  -> CommitSteal` 全链路，含重试幂等）、
  `TestZoneStealFriendCropRejectsUnsupportedAction`、
  `TestZoneStealFriendCropRejectsInvalidArgs`、
  `TestZoneStealFriendCropUnavailableWithoutSagaWiring`、
  `TestOwnerFarmRPCServerApplyVisitorActionRejectsUnsupportedAction`、
  `TestOwnerFarmRPCServerApplyVisitorActionRejectsInvalidArgs`、
  `TestOwnerFarmRPCServerApplyVisitorActionRequiresLiveVisit`。
- `server/internal/gateway/gateway_test.go`（新增）：
  `TestValidateRequestTupleAcceptsFriendAndVisitActions` 增加
  "steal friend crop" 用例、
  `TestValidateRequestTupleRejectsInvalidStealFriendCrop`（`owner_player_id`
  为 0、`visit_id` 过短/缺失、`plot_id` 为 0 四组）、
  `TestHandleGameRoutesStealFriendCropToVisitorClient`。

## 启动方式

```bash
./start-servers.sh --dual-zone --tcaplus
```

Zone 启动时会自动打开 `FriendInteraction` Tcaplus 表并接线
`StealSaga`/`Reconciler`（5 秒 ticker），无需新增环境变量（`.env.example`/
`deploy/k8s/configmap.yaml` 里的 `TCAPLUS_FRIEND_INTERACTION_TABLE` 在
阶段 0 已经就位）。浏览器打开 `web/`（`npm run dev`），两个账号互相成为
好友后，一方进入另一方农场；农场主种下一株作物并等待成熟后，访客侧地块
会出现“偷菜”按钮，点击后访客仓库增加对应作物、农场主地块的
被偷次数/被偷数量递增，且该地块很快在访客视图中不再可偷（若已达到
`max_steal_times`）或仍可再偷一次（若未达到上限）。
