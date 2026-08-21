---
status: accepted
date: 2026-07-28
owners:
  - project-owner
related:
  - ADR-0003-stateful-player-actor-zone.md
  - ADR-0004-shard-placement-and-control-plane-consensus.md
  - ../archive/architecture-v1-v2/stateful-zone-v2-architecture.md
---

# ADR-0005：生产 Journal 采用分区写入层与 Kafka，原型采用 MySQL 追加表

## 背景

V2 要求关键写操作只有在可靠持久化后才能修改正式内存并返回成功。同步随机更新完整 MySQL 玩家状态会把索引查找、数据页更新和多索引维护放进每次写请求；完全异步持久化又会丢失已经向客户端确认成功的状态。

项目还需要在三周内实现可运行原型，因此必须把生产目标的吞吐设计与原型的实现复杂度分开，同时保持相同正确性接口。

## 考虑过的方案

### 方案 A：每次关键写同步更新 MySQL 玩家表

实现和点查询直接，但把完整状态随机更新放在同步路径，难以通过顺序追加和批量复制扩展。它仍可作为原型基线，但不作为 3000 万 DAU 生产 Journal 目标。

### 方案 B：Zone 只写内存，后台异步保存

写延迟最低，但 Zone 在成功响应后、异步保存前崩溃会丢失已确认操作，违反核心约束。

### 方案 C：逻辑 Journal 写入层 + Kafka 三副本 + 异步 MySQL 快照

Journal 写入层验证 Owner、幂等和顺序，将 mutation 按 Shard 追加到 Kafka；Kafka 多副本确认后 Zone 才 Apply 并响应。Snapshot Consumer 异步合并完整玩家快照到 MySQL。

## 决定

采用方案 C，并为三周原型提供同接口的 MySQL 追加实现。

### 生产目标

- 关键状态写统一调用 `AppendMutation(shardID, ownerEpoch, playerSeq, requestID, mutation)`。
- Journal 写入层按 Shard 分区部署，校验 `owner_epoch`、`request_id` 和 `player_seq`。
- Kafka `player-journal` Topic 规划为 4096 个 Partition，与 4096 个逻辑 Shard 一一对应；消息 Key 为 `player_id`。
- 规划基线为三副本、`acks=all`、`min.insync.replicas=2`、幂等 Producer，以及禁止 unclean leader election。
- Kafka 返回满足确认条件的成功结果后，Journal 层才向 Zone 返回 committed；Zone 随后 Apply 内存并响应客户端。
- Journal 不可确认时，读请求可继续，关键写返回可重试错误；不得先返回成功。
- Snapshot Consumer 异步生成 MySQL 玩家快照，并使用 `snapshot_seq` 条件更新，旧快照不能覆盖新快照。
- Event Relay 从 `player-journal` 派生规范化事件到独立的 `domain-events` Topic，供任务、邮件、实时、排行榜和统计 Consumer Group 消费。
- Kafka Producer 的幂等和事务 fencing 不等于业务 `request_id` 与 `owner_epoch`；两者结合方式另行设计和验证。
- Kafka 按 Partition/Offset 读取，不天然支持玩家随机恢复。Journal 层必须补充快照 offset、玩家恢复索引或按 Shard 尾部重放方案。

### 三周原型

- 使用 MySQL `journal_events` 追加表实现相同 `AppendMutation` 接口。
- 表中保存 Shard、玩家、Owner epoch、玩家序号、request ID、mutation 和原结果。
- 用唯一约束、事务和故障注入验证持久化、重试幂等、连续序号、旧 Owner 拒绝和重启恢复。
- 原型结果只能证明接口语义和 MySQL 基线，不能代表 Kafka 或 3000 万 DAU 的实测能力。

### 不写 Journal 的请求

只读查询、心跳、WebSocket 订阅和可由时间计算的成熟状态不写 Journal。种植、收获、购买、出售、领奖及任何金币、种子、作物或道具修改必须写 Journal。

## 理由

- Kafka 的 Partition 追加模型可以把随机状态更新改为可批量、可分区、可横向扩展的顺序日志写入。
- 三副本和多副本确认能够把“成功响应”的持久化边界放在 Actor Apply 之前。
- Journal 写入层保留农场特有的 epoch、幂等和顺序语义，不错误假设 Kafka 理解业务字段。
- 独立 `domain-events` Topic 使下游消费和玩家恢复拥有不同格式、保留期及故障边界。
- MySQL 追加表降低三周原型复杂度，同时保持生产与原型的接口和故障语义一致。

## 后果与风险

- Journal 写入层不能成为单点，必须按 Shard 扩展并设计自身的路由与故障切换。
- 4096 个 Kafka Partition 的控制面和恢复成本必须压测，不能只按吞吐估算。
- Kafka 的 `acks=all` 与 `min.insync.replicas=2` 会在副本不足时拒绝写入，这是“不丢已确认数据”所接受的可用性代价。
- Snapshot 落后会增加 Kafka 重放尾部；快照安全水位必须约束 Journal 清理。
- 玩家随机恢复索引、Kafka Producer fencing 与业务 epoch 的结合仍是未决专项。
- `player-journal` 与 `domain-events` 是两个 Topic，即使使用同一集群也会产生额外存储和带宽。
- Kafka 发行版、Broker 数量、机型、批量参数、压缩算法、保留期和跨可用区部署仍需证据验证。

## 验证方法

1. Journal 未确认时 Actor 不 Apply，客户端不收到成功。
2. Kafka 或原型表提交成功但响应丢失时，相同 `request_id` 返回原结果且不重复修改状态。
3. 三副本中失去一个副本时按配置继续写；ISR 低于要求时拒绝关键写。
4. 旧 `owner_epoch`、重复 request ID 和乱序 player sequence 被 Journal 层拒绝或重放原结果。
5. Snapshot Writer 延迟到达时，小 `snapshot_seq` 无法覆盖大序号快照。
6. 从快照和 Journal 尾部恢复后的 Actor 状态与故障前一致。
7. 分别压测 MySQL 原型追加表和 Kafka 生产目标实现，不能用前者的数据声称后者达标。
8. 测量 4096 Partition 的 Broker 元数据、打开文件、重分配、批量吞吐、提交 p99 和恢复速度。

## 官方语义依据

- [Apache Kafka Producer 配置](https://kafka.apache.org/41/configuration/producer-configs/)：`acks`、幂等 Producer 和重试排序约束。
- [Apache Kafka Topic 配置](https://kafka.apache.org/42/configuration/topic-configs/)：`min.insync.replicas`、三副本确认与 unclean leader election。
- [Apache Kafka Producer API](https://kafka.apache.org/41/javadoc/org/apache/kafka/clients/producer/KafkaProducer.html)：事务提交和 Producer fencing 语义；本项目仍需把它与业务 epoch 结合。
