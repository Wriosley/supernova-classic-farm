---
status: accepted
date: 2026-07-30
owners:
  - project-owner
related:
  - ADR-0003-stateful-player-actor-zone.md
  - ADR-0006-async-dirty-writeback.md
  - ../architecture/stateful-zone-v3-architecture.md
  - ../architecture/single-player-vertical-loop-business-architecture.md
---

# ADR-0009：当前章节任务进度归 Player Actor

## 背景

旧模块草稿把任务设计成独立 Task Service：核心行为写 Outbox，任务服务异步推进进度并自动发奖。第一条纵向闭环的任务只依赖购买、种植、施肥、收获和出售，这些动作已经在同一 Player Actor 内串行执行。

产品规则同时调整为章节任务、达标后手动领取奖励，并要求核心命令响应携带最新任务状态。若继续异步更新，会引入不必要的延迟、事件去重和双份状态权威；客户端上报进度则不可信。

## 考虑过的方案

### 方案 A：独立 Task Service 异步更新

模块独立，适合跨玩家、全服或高吞吐任务，但第一条闭环无法在命令响应中保证最新进度。

### 方案 B：Player Actor 同步推进当前章节

成功业务动作直接更新 Actor 内任务状态，与金币、仓库、农田一起版本化和 Dirty 落库。

### 方案 C：Actor 保存展示投影，Task Service 保存权威状态

读体验好，但形成两份任务状态，需要解决校正和冲突。

## 决定

采用方案 B。

- 当前章节、任务进度、完成和领取状态属于 Player Actor；
- 只有服务端已成功业务动作可以推进任务；
- 客户端不能上报任务进度或指定奖励；
- 一条业务命令同时修改核心状态和任务进度时，`player_seq` 只增加一次；
- 章节达标后进入 `CLAIMABLE`，不自动发奖；
- 玩家手动领取奖励；
- 领取命令在 Actor 内原子修改金币、可入仓物品、章节状态和下一章激活状态；
- 满仓奖励生成 `CreateRewardMail` Outbox；
- 任务状态与 Player Snapshot、幂等结果和 Outbox 一起 Dirty 落库；
- 第二章配置可以预加载，但领取上一章前不激活、不累计；
- 全服、排行榜、跨玩家或长期运营任务未来可以重新评审独立服务。

## 理由

- 任务事实与来源业务动作由同一 Actor 掌握；
- 不需要客户端上报或异步行为事件；
- 命令响应可以直接返回最新任务状态；
- 减少第一条闭环的消息、去重和故障路径；
- 符合当前单人优先和三周原型范围。

## 后果与风险

- Player Snapshot 增加任务状态；
- 任务配置变更和章节迁移需要 Actor 兼容；
- Zone 异常退出时，任务和对应业务状态一起回退到最近检查点；
- 独立任务分析、排行榜和全服统计仍需要异步投影；
- 旧文档中的“独立 Task Service、自动发奖、客户端短暂同步中”不再适用于当前章节任务。

## 验证方法

1. 只有成功购买、种植、施肥、收获和出售推进对应进度；
2. 重复 `request_id` 不重复增加进度；
3. 失败命令不增加进度或 `player_seq`；
4. 达标后只能成功领取一次；
5. 满仓奖励只生成一份邮件 Outbox；
6. 领取上一章奖励和激活下一章属于同一 Actor 状态变化；
7. Zone 恢复后任务与金币、仓库和农田来自同一检查点。
