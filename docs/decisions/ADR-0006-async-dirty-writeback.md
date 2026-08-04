---
status: accepted
date: 2026-07-29
owners:
  - project-owner
supersedes:
  - ADR-0005-kafka-journal-and-mysql-prototype
related:
  - ADR-0003-stateful-player-actor-zone.md
  - ../architecture/stateful-zone-v3-architecture.md
---

# ADR-0006：玩家状态采用异步 Dirty 落库

## 背景

V2 为了保证已经返回成功的写操作不会因 Zone 崩溃而丢失，把可靠 Journal 放进每次关键写的同步链路。项目评审确认：普通农场资产可以接受异常 Zone 宕机时回退最近几秒状态，优先降低实现、恢复和讲解复杂度。

## 考虑过的方案

### 方案 A：保留同步 Journal

响应前提交可靠日志，恢复时加载 Snapshot 并重放尾部。可靠性最强，但需要 Journal 协议、恢复索引、重放、清理水位和生产者 fencing。

### 方案 B：内存先改，Dirty 状态异步写 MySQL

Actor 串行修改内存并立即响应，统一 Flusher 批量保存玩家检查点。实现和恢复最简单，但明确允许最近未落库状态丢失。

### 方案 C：普通资产异步，高价值资产同步

可以按资产等级提供不同保证，但当前没有真实付费资产，提前引入两套语义会扩大范围。

## 决定

采用方案 B。

- Zone Actor 内存是在线运行时权威；
- MySQL 是最近持久化恢复检查点；
- 写路径为 `Validate → Apply memory → mark Dirty → reply`；
- Flusher 默认每一秒批量写回；
- 单次事务保存玩家快照、`player_seq`、`owner_epoch`、近期幂等结果和 Outbox；
- 正常停机、Actor 回收和可控迁移必须刷完 Dirty；
- 数据库持续异常时限流并停止关键写；
- 不使用本地 WAL 或 Kafka Journal 补救；
- Event Bus 可以承载 Outbox 下游事件，但不是玩家恢复来源；
- 当前金币、种子、作物、肥料和任务进度使用同一持久化语义。

## 理由

- 在线命令不等待数据库或消息系统；
- 同一 Actor 多次变更可以合并为一次检查点写入；
- 恢复只加载数据库检查点，不需要 Journal 尾部重放；
- 更适合当前单人、三周原型的实现和验证范围。

## 后果与风险

- Zone 异常退出可能让玩家看到最近操作回退；
- 健康数据库条件下的五秒丢失窗口只是目标，不是绝对保证；
- 尚未落库的 `request_id` 结果也可能丢失；
- MySQL 承担批量检查点流量，必须压测并按逻辑映射横向拆分；
- Outbox 必须与玩家快照同事务；
- 未来出现真实付费或高价值资产时，需要重新评审混合持久化。

## 验证方法

1. 一秒内多次 Actor 修改能够合并落库；
2. 正常停机和 Actor 回收刷完 Dirty 后恢复一致；
3. 强杀 Zone 后恢复到最近数据库检查点；
4. DB 故障时 Dirty 不被误清除，并触发重试、限流和拒绝写；
5. 快照、幂等窗口和 Outbox 原子提交；
6. 旧 `owner_epoch` 不能覆盖新 Owner 检查点。
