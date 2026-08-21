---
status: proposed
version: 0.1
created: 2026-07-27
updated: 2026-07-27
owners:
  - project-owner
---

# Classic Farm 文档体系与 AI 开发交接设计

## 1. 目标

建立一套同时服务于方案讨论、最终答辩、跨 AI 交接和后续代码实施的文档体系。它必须让读者能够区分：尚未解决的问题、当前有效设计、重大决策理由、精确接口契约、实施顺序和已经取得的验证证据。

本设计只定义文档边界、目录、流转和迁移方式。它不改变农场业务规则，不把未确认技术选型转成 accepted，也不覆盖当前未提交的目标架构和开放问题计划。

## 2. 核心原则

1. 工作流 Plan 负责回答“还有什么问题要解决”，不充当最终方案。
2. 正式设计按系统边界组织，不按问题被发现的先后顺序组织。
3. 每个结论只有一个主要事实来源，其他文档使用链接，不复制大段内容。
4. AI 只读取当前任务所需的最小资料包，不依赖完整聊天记录。
5. 文件路径使用稳定英文名称，正文可以使用中文。
6. 先建立入口和目标文件，再逐段迁移内容；迁移完成并验证链接前不删除旧入口。
7. 计划值、候选方案和实测结论必须显式区分。

## 3. 目标目录

```text
docs/
├─ README.md
├─ context/
│  ├─ PROJECT.md
│  └─ CURRENT.md
├─ requirements/
│  ├─ README.md
│  ├─ product-scope.md
│  └─ non-functional-requirements.md
├─ architecture/
│  ├─ README.md
│  ├─ architecture.md
│  ├─ target-30m-dau-architecture.md
│  ├─ capacity-model.md
│  ├─ gateway-and-routing.md
│  ├─ realtime-sync.md
│  ├─ consistency-and-events.md
│  ├─ data-ownership-and-sharding.md
│  └─ multi-az-and-disaster-recovery.md
├─ modules/
│  ├─ README.md
│  ├─ account.md
│  ├─ farm.md
│  ├─ asset-and-shop.md
│  ├─ friend.md
│  ├─ task.md
│  ├─ mail.md
│  ├─ pet.md
│  └─ collection.md
├─ contracts/
│  ├─ README.md
│  ├─ http-api.md
│  ├─ websocket-protocol.md
│  ├─ event-contracts.md
│  ├─ data-model.md
│  └─ idempotency-and-errors.md
├─ decisions/
├─ plans/
├─ evidence/
└─ ai-workflow/
```

目录中的未创建文件表示目标结构，不表示内容已经设计完成。文件只在对应问题进入正式设计时创建，避免先生成大量空壳。

## 4. 各类文档边界

| 类型 | 回答的问题 | 可以包含 | 不应包含 |
|---|---|---|---|
| `context` | 项目是什么、现在做到哪 | 稳定事实、当前状态、下一步 | 详细协议和长篇讨论 |
| `requirements` | 产品和非功能要求是什么 | 角色、用例、范围、容量与可用性要求 | 具体代码结构 |
| `architecture` | 系统最终怎样协作 | 拓扑、所有权、数据流、一致性、容灾 | 每个 DTO 和 SQL 细节 |
| `modules` | 一个业务模块负责什么 | 状态、能力、不变量、主流程、依赖 | 其他模块的内部实现 |
| `contracts` | 代码必须遵守什么 | HTTP、WebSocket、事件、表、错误和幂等契约 | 选型讨论和未决分支 |
| `decisions` | 为什么选择这个方案 | 替代方案、理由、代价、验证方法 | 可随意修改的当前进度 |
| `plans` | 按什么顺序完成工作 | 检查项、文件、测试、完成标准 | 已生效事实的唯一副本 |
| `evidence` | 哪些能力已经得到证明 | 命令、环境、原始结果、限制 | 未执行的目标值 |
| `ai-workflow` | AI 协作过程发生了什么 | 触发原因、决策变化、未决问题和链接 | 完整聊天记录、正式规则副本 |

## 5. 当前文件的目标定位

| 当前文件 | 目标定位 | 迁移策略 |
|---|---|---|
| `architecture/architecture.md` | 系统总览和正式设计索引 | 保留；逐步缩短重复细节并链接专题文档 |
| `architecture/target-30m-dau-architecture.md` | 目标规模总览 | 保留；容量、实时、分片和容灾细节确认后拆到专题文件 |
| `architecture/module-design-and-flows.md` | 过渡期模块汇总 | 保留为迁移清单；模块内容迁出后改为索引或标记 superseded |
| `plans/2026-07-27-30m-dau-architecture-strategy-and-open-questions-plan.md` | 架构问题看板 | 保留；完成项链接到正式设计、契约和证据 |
| `context/PROJECT.md` | 稳定项目事实 | 保留 |
| `context/CURRENT.md` | 当前交接入口 | 保留；只在项目状态实质变化时更新 |
| `ai-workflow/*` | 简洁 AI 工作记录 | 保留；不得成为业务规则唯一来源 |

现阶段不直接移动上述核心文件，避免破坏已有链接和覆盖未提交内容。第一次整理只建立入口、目录和模板；后续按专题逐段迁移。

## 6. 一个问题怎样流转为可实现设计

```text
开放问题进入工作流 Plan
→ 负责人先描述理解和候选方案
→ 比较替代方案、失败场景、代价和验证方法
→ 结论写入对应 architecture 或 module 文档
→ 重大取舍新增 proposed ADR
→ 精确字段、状态和错误写入 contracts
→ Plan 项链接正式产物并标记完成
→ 为一个可交付阶段编写实施 Plan
→ 实现、测试和压测结果进入 evidence
→ CURRENT 更新真实进度
```

工作流任务完成后的推荐格式：

```markdown
- [x] A-01 修正快照与订阅漏事件窗口
  - 设计：../architecture/realtime-sync.md#initial-sync
  - 契约：../contracts/websocket-protocol.md#subscribe
  - 证据：../evidence/realtime-initial-sync.md
```

## 7. 正式模块设计的完成标准

一个模块只有回答以下问题后，才能进入详细实施计划：

1. 目标、范围和明确非目标；
2. 数据所有者、最终事实和分片键；
3. 对外 HTTP、内部能力和领域事件；
4. 正常流程与关键时序；
5. 事务边界和状态机；
6. 并发、幂等、重试、超时和失败恢复；
7. 错误码与客户端恢复行为；
8. 表、索引、唯一约束、保留和清理；
9. 安全、容量、监控和降级要求；
10. 单元、集成、并发、故障和验收测试。

未达到这些条件的内容保持 `proposed`，不能被实施 Plan 当作稳定契约。

## 8. AI 实施资料包

每个实施阶段只向 AI 提供与当前任务相关的资料：

1. `AGENTS.md`；
2. `context/PROJECT.md` 与 `context/CURRENT.md`；
3. `architecture/architecture.md` 和相关专题架构；
4. 当前模块设计；
5. 当前 HTTP、WebSocket、事件与数据契约；
6. 相关 accepted/proposed ADR，并明确其状态；
7. 当前实施 Plan；
8. 对应验收标准和已有 evidence。

AI 不应把 `ai-workflow`、聊天记录、UC 学习材料或开放问题中的候选分支当成已接受事实。整个系统通过多个可验证的纵向阶段完成，不要求一个提示一次生成全部生产代码。

## 9. 状态与事实来源规则

- 需求状态使用 `proposed`、`confirmed`、`superseded`；
- 架构和 ADR 使用 `proposed`、`accepted`、`superseded`；
- Plan 使用 `proposed`、`in_progress`、`completed`；
- Evidence 只描述实际执行结果，不使用 accepted 代替测试通过。

发生冲突时先确定冲突属于哪个领域：产品范围以 confirmed requirement 为准，架构取舍以最新 accepted ADR 和当前有效架构为准，精确调用与数据格式以对应 contract 为准，真实能力以 evidence 为准。Plan 和 AI 工作记录不能覆盖这些事实来源。

## 10. 整理实施顺序

1. 建立 `docs/README.md`，写清阅读路径和事实来源；
2. 建立 `architecture/README.md`、`modules/README.md`、`contracts/README.md`；
3. 修正开放问题 Plan 中已发现的三人上限冲突并加入网关、去重保留和所有权矩阵任务；
4. 把已经确认的实时初始同步协议写入 `architecture/realtime-sync.md` 与 `contracts/websocket-protocol.md`；
5. 按开放问题推进结果逐步创建容量、网关、分片、事件和多可用区专题；
6. 按账号、农场、资产商店、好友、任务、邮件、宠物和图鉴拆分模块设计；
7. 每迁移一个主题就修复链接并检查本地 Markdown 链接；
8. 所有引用迁移完成后，再决定是否把旧汇总文档改为索引或标记 superseded；
9. 更新 `AGENTS.md`、根 `README.md` 和 `CURRENT.md` 的阅读顺序；
10. 单独提交目录迁移，不与业务设计或代码实现混在同一提交。

## 11. 验证与回滚

目录整理完成必须验证：

- Git 差异只包含计划内文档；
- `docs/.obsidian/` 不进入提交；
- 所有相对 Markdown 链接目标存在；
- 没有同一规则在两个正式文件中给出不同结论；
- `AGENTS.md`、根 README、`docs/README.md` 和 CURRENT 的阅读路径一致；
- 未完成问题仍保持 proposed，不因移动文件变成 accepted；
- 历史 Git 提交可以恢复迁移前状态。

迁移采用小提交。若某一步出现链接或事实来源混乱，回滚该次迁移提交，不回滚此前已经确认的业务设计。

## 12. 本轮不做

- 不移动或删除当前核心文档；
- 不重写未提交的目标架构时序图；
- 不把开放问题看板中的候选方案转成正式决定；
- 不生成 Go、Vue、SQL 或部署代码；
- 不推送远端。
