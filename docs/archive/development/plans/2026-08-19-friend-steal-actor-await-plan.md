---
status: proposed
updated: 2026-08-19
---

# 好友偷菜 Actor Await 改造计划

## 1. 目标

把当前 Visitor Zone 的偷菜编排收进访客 Player Actor 的两段式 await：Actor 在 mailbox
内准备操作，释放 mailbox 等待 Owner Zone RPC，再回到同一个 mailbox 串行提交结果。

目标不是缩短一次跨 Zone RPC 的网络时间，而是：

- 明确所有访客状态读取/修改都属于访客 Actor；
- 外部 RPC 等待期间不占住 mailbox worker；
- 同一访客的其他命令仍能执行；
- continuation 恢复时重新验证 epoch、幂等记录和预留状态；
- 同一 `interaction_id` 不重复修改 Owner 或 Visitor。

本文件仅为 proposed 方案，不改变当前已接受的一致性契约。

## 2. 当前链路

```text
Gate
  -> VisitorZoneService.ExecuteFriendAction
  -> OwnerFarmClient.ApplyVisitorAction (mailbox 外同步等待)
  -> Owner Actor ApplyVisitorAction
  -> Visitor Runtime.ApplyVisitorFriendSideEffect
  -> Visitor Actor mailbox 提交 receipt/金币副作用
```

当前 Visitor mailbox 并没有被 Owner RPC 阻塞，但编排逻辑在 RPC server 层，Owner 成功
与 Visitor 提交之间存在崩溃窗口。当前合同明确接受这种异步 Dirty、跨 Actor 非持久
exactly-once 边界。

## 3. 不能采用的简单版本

不能只做：

```text
Visitor Actor BeginAwait -> Suspend -> Owner RPC -> Resume -> 修改 Visitor
```

原因是 Suspend 后 mailbox 会继续处理其他命令。恢复前访客的金币、仓库、epoch、幂等
窗口甚至 Actor 生命周期都可能变化；Owner 已成功后，Visitor continuation 仍可能失败。
此外，当前直接偷菜路径只依据 Owner 返回结果应用护主金币惩罚，并不使用旧 Saga 的
仓库容量预留；如果产品要求恢复偷得作物入仓，则必须先明确奖励语义，不能顺手改变。

## 4. Proposed 状态机

### 阶段 A：Visitor Actor mailbox 内 Prepare

1. 校验 Actor `owner_epoch`、访问会话、请求字段和 action。
2. 以 `interaction_id + canonical digest` 查询 Visitor receipt/pending record：
   - 已完成且 digest 相同：直接重放；
   - ID 相同但 digest 不同：`REQUEST_ID_CONFLICT`；
   - 已 pending：复用原 pending，不重复发起并发 Owner RPC。
3. 冻结 continuation 所需输入：Owner ID、visit ID、plot ID、crop ID、farm view
   epoch/seq、当前 Visitor owner epoch。
4. 若最终语义包含物品奖励，预留仓库 stack/type 容量；若维持当前仅金币/护主副作用，
   只需写入 pending receipt，不做虚假仓库预留。
5. `Suspend()`，mailbox worker 释放。

### 阶段 B：mailbox 外 Owner RPC

1. 使用 Coordinator SDK 本地路由快照解析 Owner Zone。
2. 携带 Owner `owner_epoch`/`route_version` 调用 `ApplyVisitorAction`。
3. `FailedPrecondition` 时按旧 route version 失效、触发 resync，并且最多重试一次。
4. RPC 使用独立的有界后台 context；客户端 context 取消不能中断已可能到达 Owner 的
   请求，否则结果会变成未知。
5. Owner 继续用 `interaction_id` receipt 去重。

### 阶段 C：Visitor Actor mailbox 内 Resume

1. continuation 重新进入原 Visitor mailbox。
2. 重新验证 Actor 生命周期和 owner epoch；epoch 已变化时禁止把旧结果提交到新 Actor。
3. 重新读取 pending/receipt，确保 interaction 和 digest 仍匹配。
4. Owner 返回稳定业务拒绝：释放预留，记录终结失败并响应。
5. Owner 成功：原子应用访客副作用、写 VISITOR committed receipt、删除 pending/预留、
   增加 `player_seq/checkpoint_revision` 并标 Dirty。
6. RPC 超时或结果未知：pending 保留为 UNKNOWN，不猜测失败；同 ID 重试或 reconciler
   查询/重放 Owner receipt 后收敛。

## 5. 并发与生命周期约束

- 一个 Visitor Actor 对同一 interaction 最多一个外部 RPC in-flight。
- 不同 interaction 可以并行 await，但必须设置每 Actor 上限（建议起始值 4），超过时
  返回 retryable busy，防止单玩家放大跨 Zone RPC。
- `mailbox.Idle()` 不能把 suspended await 当成可安全回收；Actor 需要独立的
  `pendingAwait` 计数。迁移、eviction、shutdown 必须等待或取消并收敛全部 await。
- Resume 失败不能只调用 `Complete` 丢弃结果；必须进入 UNKNOWN/对账队列。
- continuation 不得闭包持有可变 `State` 指针或旧 snapshot，只持有不可变 draft。
- Owner RPC 成功以后，客户端超时只影响响应，不得撤销已提交业务事实。

## 6. 与旧 Saga 的关系

有三个备选方案，需要负责人评审后才能形成 ADR：

1. **推荐：Direct + Await + pending receipt**：保留当前低延迟 Dirty 边界，增加 Actor
   内编排和 UNKNOWN 对账；实现量适中，但仍不承诺异常 Zone 丢失下持久 exactly-once。
2. **恢复 durable FriendInteraction Saga，并用 Await 驱动**：跨 Actor 恢复语义最强，
   但每步 Tcaplus CAS/同步持久化成本高，可能显著降低偷菜吞吐。
3. **仅移动 RPC 到 BeginAwait，不增加 pending/对账**：改动最少，但没有解决新增的
   Actor lifecycle、重复调用和半成功问题，不建议。

若产品要求“偷到的作物必须进入访客仓库”，优先重新评估方案 2 或在方案 1 中恢复
durable reservation；当前直接路径的实际语义是 Owner 修改 + Visitor 护主/金币副作用。

## 7. 实施分解

1. 在 `player.Runtime` 新增窄接口的 Owner action caller，避免 player 包依赖 visit。
2. 定义不可变 `preparedFriendAction` 和 Actor-owned pending await 状态。
3. 实现 `ExecuteFriendActionAwait`：Prepare/Suspend/RPC/Resume。
4. 将 `visitorZoneRPCServer.executeFriendActionDirect` 收敛为参数校验和 Runtime 调用。
5. 给 eviction、drain、shutdown 增加 pending-await gate。
6. 增加 UNKNOWN reconciler；明确复用旧 `FriendInteraction` 表还是新增 pending checkpoint
   字段，未决定前不编码存储格式。
7. 只先迁移 `STEAL_FRIEND_CROP`，验证后再推广 pest/catch/help。

## 8. 必须通过的测试

1. await 外部 RPC 等待时，同一 Actor 的 snapshot/普通命令能继续执行；
2. continuation 与穿插命令严格按 mailbox 顺序提交，不覆盖新状态；
3. 同 ID 同 payload 并发只调用一次 Owner；不同 payload 冲突；
4. Owner `FailedPrecondition` 只重路由一次；
5. Owner 成功、响应丢失后同 ID 重试不重复扣 Owner；
6. Visitor epoch 在等待期间变化时不提交旧 continuation，并进入对账；
7. Actor 有 suspended await 时不可 eviction/migration；
8. mailbox queue 满、Resume 提交失败、Zone shutdown 均不会永久泄漏 pending；
9. Owner 业务拒绝释放所有预留；
10. 压测分别报告 Owner RPC、await wait、Resume queue、Owner/Visitor mailbox 延迟和
    UNKNOWN 数量。

## 9. 压测验收

使用跨 Zone 好友 cohort，新建可重复的成熟可偷地块数据。至少测试 10/25/50/100 对，
对比改造前后：

- steal success/reject/system-error QPS；
- end-to-end P50/P95/P99；
- Prepare、Owner RPC、Resume queue、Resume apply 分段延迟；
- 每 Actor/每 Zone pending await 数和 UNKNOWN 数；
- Coordinator、Gate、Visitor Zone、Owner Zone CPU；
- Owner/Visitor receipt 一致性和重试重复修改数。

达到零重复 Owner 修改、零泄漏 pending、无 migration/eviction 违规后，才讨论接受 ADR。

