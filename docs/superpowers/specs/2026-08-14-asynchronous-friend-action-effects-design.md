---
status: proposed
date: 2026-08-14
scope: friend-action-latency
---

# 好友互动异步副作用设计

## 1. 目标与现状

当前偷菜真机单次端到端耗时为 `410.381926ms`。首次成功路径同步执行
FriendInteraction Get/Insert、访客 reservation Checkpoint、Owner Checkpoint、
Visitor Checkpoint 和四次 FriendInteraction 状态 CAS。投虫、捉虫、清理田地沿用
相同的跨 Actor Saga，因此也承担相同类型的串行持久化成本。

本设计统一改造四种好友互动：

| 动作 | Owner 同步权威修改 | Visitor 异步副作用 |
|---|---|---|
| 偷菜 | 扣减地块可偷数量、冻结宠物结果 | 作物 `×1`、任务/次数、宠物罚款 |
| 投虫 | 增加虫害 | 无物品/金币奖励；提交任务/次数 |
| 捉虫 | 清除虫害 | 金币 `+1`、任务/次数 |
| 清理田地 | 将可清理地块变为空地 | 金币 `+1`、任务/次数 |

目标是将客户端同步路径压缩为 PREPARED Outbox Insert 与 Owner Checkpoint CAS，
Owner 提交成功后立即返回；Visitor 副作用可靠异步完成。目标指标为互动响应
`p95 < 100ms`、Visitor 副作用到账 `p95 < 500ms`，两者均为待压测验证目标，
不是已测结论。

## 2. 权威边界

- OWNER Receipt：Owner 地块修改是否提交的唯一权威证据。
- PlayerOutbox：是否仍欠 Visitor 副作用的可恢复任务索引。
- VISITOR Receipt：Visitor 副作用是否已经提交的唯一权威证据。
- FriendInteraction：只保存最终审计摘要，不再驱动恢复或记录中间状态。
- Actor Mailbox：Owner 和 Visitor 各自状态修改的唯一串行入口。

Outbox 使用至少一次投递，VISITOR Receipt 将业务效果收敛为恰好一次。

## 3. 请求身份

`interaction_id` 由 visitor、owner、request_id 和 action 确定性生成。请求摘要覆盖
visitor/owner、action、visit、plot、crop、pest 和 farm-view identity。相同 ID 与相同
摘要允许重放；相同 ID 与不同摘要返回 `REQUEST_ID_CONFLICT`，不得猜测或覆盖。

## 4. PREPARED Outbox

PREPARED Outbox 由当前 Owner Zone 在修改 Owner Actor 前创建。它自包含：

- event/interaction ID、action、request digest；
- owner/visitor player ID、owner logical shard；
- plot/crop/pest identity；
- Visitor 目标副作用：作物、金币、任务/次数和宠物结果；
- created/retry/attempt/last-error 字段；
- `PREPARED | READY | DELIVERED | CANCELED | CORRUPT` 状态。

PREPARED 仅证明请求存在，不能直接执行。Worker 必须校验同一 interaction 的 OWNER
Receipt 和 request digest 后才能推进为 READY。Owner 明确拒绝的 PREPARED 可异步取消；
Receipt 冲突必须进入 CORRUPT、停止投递并告警。

先写 PREPARED 的原因是消除“Owner 已提交但可扫描任务尚未创建”的不可发现窗口。
返回成功前必须至少确认 PREPARED durable 且 Owner Checkpoint CAS 成功。

## 5. Owner 同步路径

1. Visitor Zone解析并校验请求，生成 identity/digest。
2. 路由到 Owner Zone，由 Owner Zone幂等创建 PREPARED Outbox。
3. 请求进入 Owner Actor Mailbox。
4. Owner检查动作前置条件与已有 OWNER Receipt。
5. 首次成功时在一次 Checkpoint CAS内提交地块变化、冻结结果和 OWNER Receipt；重放
   时返回已有结果，不能重复修改。
6. 注册 event ID到本 Zone Dispatcher的内存 open-task index。
7. 返回动作成功；不等待 Visitor Checkpoint、最终 FriendInteraction或 DELIVERED。

响应不携带尚未提交的 `visitor_patch` 或虚假的 Visitor state version。Owner farm patch
继续走现有 `FARM_VIEW_CHANGED`。

## 6. Zone 级 Dispatcher

每个 Zone启动一个长期存在的 `FriendActionDispatcher`，包含：

- 全部本 Zone未完成 ID的 `openTasks`；
- 有界 `readyQueue`；
- 按 retry_at排序的内存 `retryHeap`；
- 固定并发 Worker Pool；
- event ID去重和按 Visitor Shard分区的公平调度。

每次互动只注册 event ID，不创建永久 goroutine。队列满不能丢弃 open task；调度器
逐步将 READY任务交给 Worker。失败任务持久化 attempt/retry/error，并由 retryHeap
重新调度。

正常运行不周期性全表扫描。以下事件触发 durable recovery scan：

1. Zone启动，扫描当前所拥有 Shard的未完成任务并重建内存索引；
2. Zone获得新 Shard所有权，扫描该 Shard的未完成任务；
3. 管理员按 Shard执行 repair，或启用低频可关闭的审计扫描。

当前 PlayerOutbox 主键不能高效按 Shard范围读取。原型可在上述恢复点 Traverse后按
ownership过滤；生产目标必须增加 Shard/bucket可读索引或使用分区消息队列，禁止把
频繁全表 Traverse描述为可扩展方案。

## 7. Visitor 投递

Worker按 visitor player ID计算 logical shard，通过 Coordinator SDK解析当前 ACTIVE
route，并调用 Visitor Zone内部 RPC。同 Zone可以走本地适配器，但仍必须进入 Visitor
Actor Mailbox。

Visitor Actor冷激活时加载一次完整 PlayerCheckpoint；已激活 Actor不为每次奖励额外
查询数据库。激活时从 `friend_receipts` 构建：

```text
receiptIndex[interaction_id + role] -> receipt
```

Mailbox内：

- 找到摘要一致的 VISITOR Receipt：返回 AlreadyApplied；
- 找到摘要冲突的 Receipt：失败关闭并告警；
- 未找到：应用该 action的作物/金币/任务/次数/罚款，追加 VISITOR Receipt，并在同一次
  Visitor Checkpoint CAS提交。

在线 Visitor在提交后收到带真实 state version的 `PLAYER_STATE_CHANGED`；离线 Push可
丢失，下次登录从 Checkpoint恢复。

## 8. 最终 FriendInteraction

Visitor返回 Applied或 AlreadyApplied后，后台只写一次 `COMPLETED`
FriendInteraction，保存 request、owner result、visitor result和完成时间。不再写
INIT、VISITOR_RESERVED、OWNER_APPLIED、VISITOR_COMMITTED。

最终记录用于审计、统计、客服查询和 Receipt清理依据，不是恢复入口。最终记录成功后
Outbox推进为 DELIVERED。若最终记录或 DELIVERED写入窗口崩溃，Worker根据 Receipt与
摘要安全补写。

## 9. 崩溃与迁移恢复

| durable evidence | 恢复动作 |
|---|---|
| PREPARED，OWNER Receipt不存在 | 禁止发副作用；等待同请求重试或超时取消 |
| PREPARED/READY，OWNER Receipt存在 | 验证摘要并投递 Visitor |
| VISITOR Receipt不存在 | Visitor执行副作用并提交 Receipt |
| VISITOR Receipt存在 | 返回 AlreadyApplied，不重复副作用 |
| Visitor已提交，最终记录缺失 | 补写最终 FriendInteraction |
| 最终记录存在，Outbox未完成 | 验证摘要后补写 DELIVERED |

Owner任务归属跟随 owner logical shard。Shard迁移时旧 Zone停止处理，新 Zone在 ACTIVE
接管后恢复该 Shard任务。Visitor Shard迁移只要求 Worker重新解析路由；相同
interaction ID的重试由 VISITOR Receipt去重。

## 10. Receipt保留

OWNER/VISITOR Receipt都保存在各自 `PlayerCheckpoint.friend_receipts`，并随 Actor常驻
内存。可增加5秒响应缓存，但不能5秒删除持久化 Receipt。Receipt只有在以下条件全部
成立后才能清理：

- Outbox已 DELIVERED；
- 最终 FriendInteraction已保存；
- 配置的幂等/重试保留窗口已过期。

初始保留窗口沿用24小时语义，实施计划必须增加有界清理和大小测试，避免 Checkpoint
无限增长。

## 11. 过载保护

指标至少包括 pending count、oldest pending age、delivery p50/p95/p99、retry count、
worker busy ratio和Tcaplus latency/error。若积压超过配置阈值，必须在 PREPARED/Owner
修改前返回 retryable `SYSTEM_BUSY`。已经返回成功的任务不可丢弃或取消。

Worker并发、队列容量和阈值由压测确定。不得用无限 goroutine或无限数据库并发隐藏
积压。

## 12. 兼容与上线

- 使用 feature flag启用新路径，默认先关闭；旧 Saga保留回滚能力。
- 新旧路径不得同时处理同一个 interaction；路由选择冻结在首次请求结果中。
- PlayerOutbox/Tcaplus字段必须遵守31字符字段名限制。
- 协议和存储升级先部署兼容读，再启用写入，最后开启 Dispatcher。
- 上线前必须覆盖四动作、幂等冲突、全部崩溃窗口、Zone重启、Shard接管、过载拒绝和
  旧路径回滚。

## 13. 验收标准

1. 四类动作Owner响应不等待Visitor提交。
2. 偷菜作物、捉虫/清理各1金币、投虫无物品/金币奖励符合规则。
3. 重复投递不重复作物、金币、任务、次数或罚款。
4. Zone在每个持久化边界崩溃后都能收敛。
5. 正常运行不依赖频繁全表扫描。
6. Actor已激活时Visitor路径只有内存Receipt查询与一次Checkpoint CAS。
7. 性能报告分别给出响应与Visitor到账的p50/p95/p99，未达100ms时保留真实结论。
