---
date: 2026-07-28
status: recorded
related:
  - ../architecture/stateful-zone-v2-architecture.md
  - ../decisions/ADR-0003-stateful-player-actor-zone.md
  - ../decisions/ADR-0005-kafka-journal-and-mysql-prototype.md
---

# V2 有状态架构采用与文档接力更新

## 目标与边界

统一 1024/4096 的分片矛盾，明确 V2 是否为当前策略，并更新后续 AI 的最小接力上下文。此次只整理设计与文档，不实现代码，也不把规划容量写成实测结果。

## 输入上下文

- 无状态 V1 目标架构与旧未决问题计划。
- V2 有状态 Player Actor 架构及容量模型。
- 项目负责人与导师/MT 关于有状态 Zone、Actor、Journal 和不丢已确认写的讨论。
- 当前 `PROJECT.md`、`CURRENT.md`、架构索引和 ADR。

## 人类决定

- 当前生产目标采用 V2，而不是继续以无状态 V1 为主。
- 已确认成功的写操作不能丢失。
- 使用有状态 Player Actor、响应前可靠 Journal、异步 Snapshot。
- 逻辑分片统一为 4096。
- V1 保留作为设计演进材料，不删除。
- 生产 Journal 采用分区写入层 + Kafka 三副本，三周原型使用 MySQL 追加表；具体 Kafka fencing、恢复索引和集群参数仍待设计与验证。

## AI 完成的整理

- 修正 V2 中 1024 与 4096 的矛盾，并明确分片函数必须版本化。
- 新增 ADR-0003，记录替代方案、选择理由、代价和验证方法。
- 将 V1 架构和旧 V1 计划标记为 superseded。
- 更新架构入口、稳定项目上下文和 `CURRENT.md` 接力记忆。
- 说明 `ai-context` 只保留学习资料和指针，项目决策以仓库 ADR 和当前架构为准。
- 补充分片位置规划：Rendezvous Hashing 与负载修正只产生候选 Zone，多数派提交才授予 Owner；项目不从零实现 Raft。
- 补全 Journal 选型：生产目标采用 Journal 写入层 + Kafka 三副本，三周原型通过统一 `AppendMutation` 接口落到 MySQL `journal_events` 追加表。

## 验证与限制

已检查文档引用、关键术语和 Git 差异；尚未实现或压测任何 V2 组件。60 个 Zone、4096 个逻辑分片、Journal 吞吐等仍是规划参数，后续必须用原型证据修订。
