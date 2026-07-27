# Documentation System Foundation Migration Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Establish the approved documentation navigation, source-of-truth boundaries, and architecture question board without moving existing core documents or losing uncommitted work.

**Architecture:** Keep the existing `context`, `architecture`, `decisions`, `plans`, `evidence`, and `ai-workflow` files in place. Add index files for the new layered system, align agent entry points, and normalize the open-question board; detailed domain extraction into module and contract documents happens in later topic-specific plans after each design is confirmed.

**Tech Stack:** Markdown, Git, Obsidian-compatible relative links, PowerShell verification commands.

## Global Constraints

- Follow `docs/architecture/documentation-system.md`.
- Do not move, delete, rewrite, stage, or commit `docs/architecture/target-30m-dau-architecture.md`; it already contains unrelated uncommitted sequence diagrams.
- Do not stage or commit `docs/.obsidian/`.
- Preserve `docs/plans/2026-07-27-30m-dau-architecture-strategy-and-open-questions-plan.md`; Task 3 intentionally edits and commits it.
- English file and directory names are stable identifiers; document bodies may use Chinese.
- A Plan is a work board, not a source of accepted product or architecture truth.
- Do not create empty topic documents for designs that have not been discussed and confirmed.
- Do not change `proposed` decisions to `accepted` during directory organization.
- Use exact-path staging for every commit; never use `git add .`.
- Do not push.

---

### Task 1: Create documentation navigation indexes

**Files:**
- Create: `docs/README.md`
- Create: `docs/architecture/README.md`
- Create: `docs/modules/README.md`
- Create: `docs/contracts/README.md`

**Interfaces:**
- Consumes: the boundaries and target tree in `docs/architecture/documentation-system.md`.
- Produces: stable entry points used by root README, AGENTS, CURRENT, humans, and AI workers.

- [ ] **Step 1: Verify the protected working-tree state**

Run:

```powershell
git status --short --branch
```

Expected protected entries:

```text
 M docs/architecture/target-30m-dau-architecture.md
?? docs/.obsidian/
?? docs/plans/2026-07-27-30m-dau-architecture-strategy-and-open-questions-plan.md
```

Stop if the target architecture has staged changes or if another tracked file is unexpectedly modified.

- [ ] **Step 2: Create the top-level documentation index**

Create `docs/README.md` with this content:

```markdown
# Classic Farm Documentation

This directory separates current project truth, design reasoning, executable contracts, work plans, and measured evidence.

## Start here

1. Read `../AGENTS.md`.
2. Read `context/PROJECT.md` for stable project facts.
3. Read `context/CURRENT.md` for the actual current state.
4. Read only the requirement, architecture, module, contract, ADR, plan, or evidence files relevant to the current task.

## Directory roles

- `requirements/`: confirmed or proposed product and non-functional requirements.
- `architecture/`: current system topology and cross-cutting design.
- `modules/`: business ownership, capabilities, invariants, and module flows.
- `contracts/`: precise HTTP, WebSocket, event, data, error, and idempotency rules.
- `decisions/`: major alternatives, decisions, costs, and validation methods.
- `plans/`: unresolved-question boards and bounded execution plans.
- `evidence/`: reproducible tests, measurements, and limitations.
- `context/`: stable project context and mutable current handoff.
- `ai-workflow/`: concise AI collaboration records; never formal truth by itself.

## Source-of-truth rules

- Product scope comes from confirmed requirements.
- Architecture tradeoffs come from the current architecture and latest accepted ADR.
- Precise request, event, error, and storage formats come from contracts.
- Actual capability and performance claims require evidence.
- Plans and AI work records cannot override requirements, accepted decisions, contracts, or evidence.

See `architecture/documentation-system.md` for the document lifecycle and migration rules.
```

- [ ] **Step 3: Create the architecture index**

Create `docs/architecture/README.md` with this content:

```markdown
# Architecture

Architecture documents explain how the whole system collaborates. They define topology, ownership, routing, consistency, realtime synchronization, capacity, and failure recovery without duplicating every DTO or table field.

## Current documents

- `architecture.md`: current effective system overview and navigation entry.
- `target-30m-dau-architecture.md`: target-scale architecture and distributed prototype direction.
- `module-design-and-flows.md`: transitional combined module document; content will move into `../modules/` and `../contracts/` topic by topic.
- `documentation-system.md`: documentation boundaries, lifecycle, and migration design.

## Planned topic documents

Create these only after their design is discussed and confirmed:

- `capacity-model.md`
- `gateway-and-routing.md`
- `realtime-sync.md`
- `consistency-and-events.md`
- `data-ownership-and-sharding.md`
- `multi-az-and-disaster-recovery.md`

Major tradeoffs link to ADRs in `../decisions/`; precise formats belong in `../contracts/`; measured claims belong in `../evidence/`.
```

- [ ] **Step 4: Create the module index**

Create `docs/modules/README.md` with this content:

```markdown
# Modules

Each module document owns one business boundary and must state its scope, data ownership, shard key, capabilities, invariants, normal flows, transaction boundaries, failure recovery, security, capacity, and tests.

Planned module documents are account, farm, asset-and-shop, friend, task, mail, pet, and collection. Create a module file only when its current design can be extracted from confirmed rules without inventing unresolved behavior.

Cross-module topology belongs in `../architecture/`. Exact HTTP, WebSocket, event, data, error, and idempotency formats belong in `../contracts/`.
```

- [ ] **Step 5: Create the contract index**

Create `docs/contracts/README.md` with this content:

```markdown
# Contracts

Contracts are the precise inputs to implementation. They define HTTP requests and responses, WebSocket messages, domain events, tables and indexes, error semantics, and idempotency behavior.

Planned contract documents are `http-api.md`, `websocket-protocol.md`, `event-contracts.md`, `data-model.md`, and `idempotency-and-errors.md`. Create them only after the corresponding design is confirmed; do not place unresolved alternatives in normative contracts.

Architecture explains why components collaborate. Module documents explain ownership and behavior. Contracts specify the exact formats code must follow.
```

- [ ] **Step 6: Validate the new indexes**

Run:

```powershell
git diff --check -- docs/README.md docs/architecture/README.md docs/modules/README.md docs/contracts/README.md
rg -n 'TODO|TBD|<<<<<<<|=======|>>>>>>>' docs/README.md docs/architecture/README.md docs/modules/README.md docs/contracts/README.md
```

Expected: `git diff --check` exits 0; `rg` returns no matches.

- [ ] **Step 7: Commit only the index files**

```powershell
git add -- docs/README.md docs/architecture/README.md docs/modules/README.md docs/contracts/README.md
git diff --cached --check
git commit -m "docs: add layered documentation indexes"
```

Expected: four new README files committed; protected entries remain unstaged.

---

### Task 2: Align repository and agent reading order

**Files:**
- Modify: `AGENTS.md`
- Modify: `CLAUDE.md`
- Modify: `README.md`
- Modify: `docs/requirements/README.md`
- Modify: `docs/plans/README.md`
- Modify: `docs/evidence/README.md`
- Modify: `docs/ai-workflow/README.md`
- Modify: `docs/context/CURRENT.md`

**Interfaces:**
- Consumes: the indexes created by Task 1.
- Produces: one consistent reading order and explicit source-of-truth boundaries for every agent entry point.

- [ ] **Step 1: Update the AGENTS source-of-truth reading order**

In `AGENTS.md`, replace the numbered list under `## Source of truth` with:

```markdown
1. `docs/README.md`
2. `docs/context/PROJECT.md`
3. `docs/context/CURRENT.md`
4. Only the requirement, architecture, module, contract, ADR, plan, or evidence files relevant to the current task
```

Extend `## Documentation boundaries` with:

```markdown
- `docs/README.md`: documentation map, reading order, and source-of-truth rules.
- `docs/requirements/`: product and non-functional requirements.
- `docs/modules/`: business ownership, capabilities, invariants, and module flows.
- `docs/contracts/`: precise implementation-facing HTTP, WebSocket, event, data, error, and idempotency rules.
```

Keep all existing security, decision, and completion rules unchanged.

- [ ] **Step 2: Update the Claude entry point**

Replace the second paragraph in `CLAUDE.md` with:

```markdown
Start each project task by reading `docs/README.md`, `docs/context/PROJECT.md`, and `docs/context/CURRENT.md`. Read only the additional requirement, architecture, module, contract, decision, plan, or evidence files needed for the current task.
```

- [ ] **Step 3: Update the root README document entry list**

Replace the bullets under `## 文档入口` in `README.md` with:

```markdown
- `AGENTS.md`：所有 AI 和开发者共同遵守的工作规则。
- `docs/README.md`：文档地图、阅读顺序和事实来源规则。
- `docs/context/PROJECT.md`：稳定的项目目标、边界与事实。
- `docs/context/CURRENT.md`：当前进度、问题和下一步。
- `docs/architecture/`：系统总览与跨模块设计。
- `docs/modules/`：业务模块所有权、能力和不变量。
- `docs/contracts/`：HTTP、WebSocket、事件、数据和幂等契约。
- `docs/decisions/`：架构决策记录。
- `docs/plans/`：开放问题看板和实施计划。
- `docs/evidence/`：测试、压测和故障实验证据。
```

- [ ] **Step 4: Clarify the existing directory README boundaries**

Append this sentence to `docs/requirements/README.md`:

```markdown
Requirements state what the product must do; architecture and contracts state how the confirmed requirements are implemented.
```

Append this paragraph to `docs/plans/README.md`:

```markdown
Open-question plans track unresolved work. When an item is resolved, link its architecture, module, contract, ADR, or evidence output instead of copying the full conclusion into the plan.
```

Append this sentence to `docs/evidence/README.md`:

```markdown
Evidence records observed results and limitations; it does not silently convert a proposed design into an accepted decision.
```

Append this sentence to `docs/ai-workflow/README.md`:

```markdown
AI workflow records are traceability material, not the source of formal product, architecture, interface, or performance truth.
```

- [ ] **Step 5: Update CURRENT with the approved documentation workflow**

In `docs/context/CURRENT.md`, replace the list under `## Resume here` with:

```markdown
1. `docs/README.md`;
2. `docs/context/PROJECT.md` and this file;
3. `docs/plans/2026-07-27-30m-dau-architecture-strategy-and-open-questions-plan.md` for unresolved architecture work;
4. only the architecture, module, contract, ADR, or evidence files relevant to the current task.
```

After the paragraph following that list, insert:

```markdown
## Documentation workflow

- The workflow-based architecture plan is the open-question board, not the final design.
- Confirmed cross-cutting conclusions move to `docs/architecture/`; confirmed business ownership and behavior move to `docs/modules/`; exact implementation formats move to `docs/contracts/`.
- Major tradeoffs receive ADRs, implementation order stays in `docs/plans/`, and executed tests or measurements go to `docs/evidence/`.
- `docs/architecture/documentation-system.md` defines the migration and AI handoff rules.
```

Replace `## Next actions` with:

```markdown
## Next actions

1. Normalize and commit the architecture open-question board without staging unrelated target-architecture diagrams.
2. Record the confirmed subscribe-first realtime initial-sync design in architecture and contract documents after its remaining fields and failure limits are reviewed.
3. Resolve the capacity, gateway/routing, multi-AZ, shard-migration, idempotency-retention, and data-ownership work items in the board.
4. Create a phase-specific implementation plan only when the required module and contract documents meet the documentation-system completion standard.
```

Do not change capacity assumptions, candidate technology status, or product-code verification claims.

- [ ] **Step 6: Validate reading-order consistency**

Run:

```powershell
rg -n 'docs/README.md|docs/modules|docs/contracts|open-question|事实来源|source-of-truth' AGENTS.md CLAUDE.md README.md docs/context/CURRENT.md docs/requirements/README.md docs/plans/README.md docs/evidence/README.md docs/ai-workflow/README.md
git diff --check -- AGENTS.md CLAUDE.md README.md docs/context/CURRENT.md docs/requirements/README.md docs/plans/README.md docs/evidence/README.md docs/ai-workflow/README.md
```

Expected: all entry points reference the layered documentation system; diff check exits 0.

- [ ] **Step 7: Commit only the reading-order changes**

```powershell
git add -- AGENTS.md CLAUDE.md README.md docs/context/CURRENT.md docs/requirements/README.md docs/plans/README.md docs/evidence/README.md docs/ai-workflow/README.md
git diff --cached --check
git commit -m "docs: align repository documentation workflow"
```

Expected: eight guidance and context files committed; target architecture, `.obsidian`, and the open-question plan remain unstaged.

---

### Task 3: Normalize and adopt the architecture open-question board

**Files:**
- Modify and add to Git: `docs/plans/2026-07-27-30m-dau-architecture-strategy-and-open-questions-plan.md`

**Interfaces:**
- Consumes: the review findings and the confirmed subscribe-first initial-sync direction.
- Produces: the authoritative board for unresolved architecture work, with explicit gateway, dedup-retention, ownership, and multi-AZ tasks.

- [ ] **Step 1: Correct the three-client constraint**

Replace the current global constraint:

```markdown
- 第一版多人范围按最多三人同时进入一个农场规划，若改变该上限必须重新计算广播与热点容量。
```

with:

```markdown
- 三个客户端同时进入一个农场只是本地原型验收基线，不是生产产品硬上限；生产热点农场的连接、进入速率、快照 QPS 和广播上限由 A-04、B-11 与 E-08 收敛。
```

- [ ] **Step 2: Record the chosen A-01 direction**

Replace A-01's explanation and completion standard with:

```markdown
  - 设计方向：先完成 WebSocket 订阅登记并返回 Ack，H5 在浏览器内存缓存增量，再通过 HTTP 获取权威快照；以快照版本为基线丢弃旧事件并连续应用新事件。
  - 完成标准：定义订阅 Ack、权威快照版本、`base_version/version` 连续性、客户端临时缓存上限、超时、溢出、断线和 `resync_required`；用自动化时序测试覆盖版本 10→11 的竞态。Redis 可以加速经版本校验的快照，但不能作为防漏事件机制。
```

- [ ] **Step 3: Add a unified ownership and shard-key work item**

After A-09, add:

```markdown
- [ ] **A-10 建立全领域数据所有权与分片键矩阵。**
  - 解释：农场、资产、宠物、图鉴、好友、任务、邮件、房间订阅和连接状态分散在不同段落，缺少一个可以检查重复写入和跨库调用的统一视图。
  - 完成标准：逐项列出最终事实所有者、主查询键、分片键、缓存、写入入口、一致性级别、保留期和禁止跨库修改规则；至少覆盖农场/地块、资产、账号、好友、任务、邮件、宠物、图鉴、房间订阅和 WebSocket 连接。
```

- [ ] **Step 4: Add the API gateway and player-routing boundary work item**

After C-12, add:

```markdown
- [ ] **C-13 设计 API Gateway、玩家路由与 Zone 的请求边界。**
  - 解释：当前只有高层拓扑，尚未说明调用者 A、目标玩家 B、业务授权和分片路由分别由谁验证，旧路由或错误重试可能把请求送到错误分片。
  - 完成标准：定义 Session 得到的可信 `caller_player_id`、请求携带的 `target_player_id/farm_id`、网关鉴权与限流、Zone 业务授权、目标玩家逻辑分片计算、`route_epoch` 校验、可信元数据传播、超时重试和错误返回；明确 API Gateway 不执行好友或农场业务规则。
```

- [ ] **Step 5: Add consumer-dedup retention and cleanup rules**

After D-10, add:

```markdown
- [ ] **D-11 定义消费者去重记录的保留、重放与清理边界。**
  - 解释：Inbox 记录若早于消息保留期或人工重放窗口删除，旧事件可能再次产生业务效果；永久保留又会让去重表无限增长。
  - 完成标准：规定临时去重保留期不短于消息保留期、最大重试时间和允许重放窗口之和；区分可清理 Inbox 与必须留在资产流水、奖励、偷菜或邮件业务表中的永久唯一事实；定义时间分区、限速清理和清理后重放行为。
```

- [ ] **Step 6: Make the F-04 component requirements explicit**

Replace F-04's completion standard with:

```markdown
  - 完成标准：分别为消息系统、Redis、Session 真相源和分片路由配置列出副本分布、选主/仲裁、写确认、网络分区行为、单区丢失后的可写性、允许的数据损失、降级路径、恢复步骤和故障实验；不能用同一套“主从”描述替代四种不同语义。
```

After F-08, add:

```markdown
- [ ] **F-09 设计 API Gateway 与 Realtime Gateway 的三可用区部署和切流。**
  - 解释：无状态网关也会持有连接、发送队列和短期路由状态，单区故障会触发 HTTP 切流和 WebSocket 重连风暴。
  - 完成标准：定义三区实例分布、负载均衡健康检查、停止接流、连接排水、单区摘除、重连抖动、订阅重建、N+1 容量和切流期间的请求/连接恢复语义。
```

- [ ] **Step 7: Update the recommended ranges**

In `## 推荐推进顺序`, replace the four affected lines with:

```markdown
1. 完成 A-01～A-10：先消除实时竞态，明确多人、注册、宠物、图鉴、时间、经济语义和统一数据所有权。
3. 完成 D-01～D-11：在写业务代码前固定幂等、Outbox、消费者事务、去重保留和跨分片状态机。
4. 完成 C-01～C-13：设计分片迁移、其他服务分片、连接、缓存一致性以及 API Gateway/玩家路由/Zone 边界。
6. 完成 F-01～F-09：把三可用区从无状态计算扩展到所有状态型依赖、网关切流和灾难恢复。
```

- [ ] **Step 8: Validate the board as a plan, not accepted design**

Run:

```powershell
git diff --check -- docs/plans/2026-07-27-30m-dau-architecture-strategy-and-open-questions-plan.md
rg -n 'A-10|C-13|D-11|F-09|route_epoch|base_version|三个客户端.*验收基线' docs/plans/2026-07-27-30m-dau-architecture-strategy-and-open-questions-plan.md
rg -n 'status: accepted|生产.*最多三人' docs/plans/2026-07-27-30m-dau-architecture-strategy-and-open-questions-plan.md
```

Expected: first two commands succeed with required matches; the final `rg` returns no matches.

- [ ] **Step 9: Commit only the normalized board**

```powershell
git add -- docs/plans/2026-07-27-30m-dau-architecture-strategy-and-open-questions-plan.md
git diff --cached --check
git commit -m "docs: adopt target-scale architecture question board"
```

Expected: the plan becomes tracked; target architecture and `.obsidian` remain unstaged.

- [ ] **Step 10: Run the final foundation verification**

Run:

```powershell
git status --short --branch
git log -4 --oneline
git diff --check
rg -n 'docs/README.md' AGENTS.md CLAUDE.md README.md docs/context/CURRENT.md
```

Expected:

- the three new documentation commits are visible;
- no tracked documentation changes remain except the pre-existing target-architecture sequence diagrams;
- `docs/.obsidian/` remains untracked;
- every entry point links to `docs/README.md`.

Do not push. The next plan is topic-specific: finish the realtime initial-sync design and WebSocket contract, then review them before implementation.
