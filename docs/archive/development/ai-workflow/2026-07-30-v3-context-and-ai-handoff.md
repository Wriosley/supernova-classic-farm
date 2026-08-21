---
date: 2026-07-30
status: recorded
related:
  - ../../../context/PROJECT.md
  - ../../../context/CURRENT.md
  - ../../../architecture/stateful-zone-v3-architecture.md
  - ../../../architecture/single-player-vertical-loop-business-architecture.md
  - ../../../decisions/README.md
  - ../../../decisions/ADR-0006-async-dirty-writeback.md
  - ../../../decisions/ADR-0008-v3-quorum-shard-coordinator.md
  - ../../../decisions/ADR-0009-player-actor-task-progress.md
---

# V3 上下文与 AI 接力更新

## 目标与边界

将跨 AI 接力入口从 V2 更新为 V3，并明确 ADR 与 AI 工作流记录保存的是决策和协作的历史演进，而不是所有内容都代表当前实现。此次只整理文档，不实现代码，不修改历史 ADR 的论证正文，也不把规划容量写成实测结果。

## 输入上下文

- 已接受的 V3 有状态 Zone 与异步 Dirty 架构。
- 已接受的第一条单玩家纵向业务闭环。
- V1、V2、Journal 和 Shard Coordinator 的历史讨论与 ADR。
- 现有 `AGENTS.md`、文档入口、`PROJECT.md`、`CURRENT.md` 和 AI 工作流记录。

## 人类决定

- 当前全面采用 V3。
- 普通游戏写操作先修改 Player Actor 内存，随后由 Dirty Queue 异步批量写入 MySQL。
- 接受异常 Zone 宕机时最近未落库状态可能回退；正常停机、回收和迁移必须先刷完 Dirty。
- V3 保留生产多数派 Shard Coordinator、租约、`owner_epoch` 和 MySQL fence；本地原型只实现语义兼容的单节点 Coordinator。
- 当前章节任务进度归 Player Actor。
- `docs/decisions/` 用于展示历史决策演变，不等于当前所有决策的合集。

## AI 完成的整理

- 将稳定项目上下文和当前交接状态更新为 V3。
- 删除当前接力中的 V2 Journal、Kafka Journal 和 MySQL `journal_events` 实施步骤。
- 给出当前架构、当前支持性 ADR 与历史 ADR 的明确映射。
- 更新架构索引，使 V3 和单玩家纵向闭环成为当前入口。
- 在 ADR README 中强调：`accepted` 表示当时被接受，不保证整份 ADR 的每个细节今天仍生效；冲突应由 `CURRENT.md`、当前架构和更新的 ADR 解决。
- 在 AI 工作流 README 中加入“先读 CURRENT、历史记录不覆盖当前事实”的接力规则。

## 后续 AI 的最小上下文包

所有任务先提供：

1. `AGENTS.md`；
2. `docs/README.md`；
3. `docs/context/PROJECT.md`；
4. `docs/context/CURRENT.md`。

当前业务设计任务再提供：

5. `docs/architecture/stateful-zone-v3-architecture.md`；
6. `docs/architecture/single-player-vertical-loop-business-architecture.md`；
7. 与当前问题直接相关的 ADR 或合同文件。

不要默认提供全部 ADR、全部聊天记录或完整 `ai-context`。这样可以减少 token 消耗，也能避免旧方案污染当前方案。

## 当前下一步

先继续审查并冻结第一条单玩家业务闭环，再把它转成 WebSocket 命令、响应、错误、幂等、配置版本和检查点合同。合同确认后再制定 Go 实现计划；好友和跨玩家流程不阻塞第一条闭环。

## 验证与限制

已检查当前文档入口、V3 关联 ADR、业务架构和历史工作流的引用关系。此次没有运行代码或性能测试；30 million DAU、约 60 个 Zone 和其他容量数字仍是待原型证据修正的规划值。
