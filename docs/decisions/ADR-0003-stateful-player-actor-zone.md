---
status: accepted
date: 2026-07-28
owners:
  - project-owner
supersedes:
  - ADR-0002-target-scale-hybrid-architecture
related:
  - ../archive/architecture-v1-v2/stateful-zone-v2-architecture.md
---

# ADR-0003：生产目标采用有状态 Player Actor Zone

## 背景

无状态 V1 把 MySQL 作为每次玩家操作的实时事实。它易于故障切换，但每个命令需要数据库读写，难以利用同一玩家状态的局部性；同玩家并发还需要依赖数据库锁或条件更新。

项目随后参考有状态游戏后台的思路重新评审，但提出了一个不可退让的约束：已经向客户端返回成功的写操作不能因 Zone 崩溃而丢失。因此不能只把状态写进 Zone 内存后异步落数据库。

## 考虑过的方案

### 方案 A：保留无状态 Zone

每个请求从缓存或 MySQL 读取并在 MySQL 事务中提交。故障恢复简单，但数据库往返位于全部核心写请求的同步路径，同玩家顺序和热点状态局部性较差。

### 方案 B：有状态 Actor，成功后仅异步写数据库

写请求只更新内存，后台批量生成数据库快照。延迟最低，但成功响应与快照之间 Zone 故障会丢失已确认状态，不满足项目约束。

### 方案 C：有状态 Actor，响应前提交 Journal，异步 Snapshot

同玩家命令由 Actor 串行执行。事件先提交到可靠 Journal，提交成功后才应用内存并返回；Snapshot DB 异步保存恢复检查点。Zone 故障后通过“快照 + Journal 尾部”恢复。

## 决定

生产目标采用方案 C。

- 使用 `stable_hash64(target_player_id) % 4096` 得到逻辑分片。
- 4096 是版本化集群配置；产生持久化数据后不能直接改模，变更必须通过分片函数版本和在线迁移。
- 一个逻辑分片同一时刻只有一个具备写权限的 Active Zone Owner；一个 Zone 可以拥有多个分片。
- Coordinator 通过多数派控制面维护 `shard_id → zone_id + route_epoch`；Journal 和 Snapshot 的写入都校验 epoch，隔离旧 Owner。
- Gateway 使用可信调用者身份和目标玩家标识路由；业务授权仍由目标 Zone 校验。
- 每个玩家由一个 Player Actor 管理运行时状态和邮箱；同玩家命令串行，不同玩家并行。
- 核心写路径为 `Validate/Decide → Journal committed → Apply → reply`。随机值、服务端时间和配置版本在事件中固化，重放不得重新计算。
- Snapshot DB 异步保存 `snapshot_seq`；恢复加载快照并重放更大序号的 Journal 记录。
- 任务、邮件、好友、实时和跨玩家奖励不进入单玩家同步事务，使用可靠事件和幂等命令最终一致。
- V2 是当前生产目标；本地原型实现相同关键机制的缩小版，但不声称已经承载 3000 万 DAU。
- Journal、Coordinator、缓存和事件系统的具体产品仍待选型与证据验证。

## 理由

- Actor 邮箱直接表达同玩家顺序，减少锁竞争和重复状态加载。
- 活跃玩家状态留在 Zone 内存中，适合高频游戏命令和批量快照。
- Journal-before-response满足“不丢已确认写”的正确性边界。
- 逻辑分片把路由与物理 Zone 解耦，4096 个分片使约 60 个 Zone 下每个 Zone 有足够细的迁移粒度。
- Snapshot 合并多次变更，避免每次命令同步写完整数据库状态。
- V1 保留为历史对照，便于答辩解释为何改变方案。

## 后果与风险

- Zone 不再能任意接管请求，必须实现 Owner、租约或任期、epoch fencing、加载、回收和迁移。
- Journal 成为同步写路径，其延迟、吞吐、可用性和存储成本直接影响核心请求。
- Snapshot 延迟不会立即丢数据，但会增长恢复重放尾部；必须监控恢复时间和清理水位。
- Actor 内存、邮箱积压、GC、热点玩家和迁移重叠成为新的容量约束。
- 4096 不是 4096 个 Zone、数据库分片或消息分区；这些物理数量由各自容量和证据决定。
- 三可用区控制面和 Journal 多副本会增加实现与运维复杂度。
- 现阶段的 60 个 Zone 和其他容量数字都是规划假设，不是采购结论或实测能力。

## 对旧决策的影响

ADR-0002 的服务边界、异步任务、跨玩家奖励、邮件兜底和 HTTP/WebSocket 分工仍可复用；其“无状态 Zone、MySQL 实时事实、1024 分片和 Outbox 主写链路”在生产目标层面被本 ADR 取代。

ADR-0001 继续约束本地代码的模块边界和单人优先顺序，但不再定义生产运行时状态模型。

## 验证方法

1. 相同玩家的并发命令必须严格串行，不同玩家可并行。
2. Journal 提交前失败不能改变可恢复状态；提交后响应丢失时，相同 `request_id` 返回原结果且不重复生效。
3. Zone 在 Journal 提交后、Snapshot 前崩溃，重启后必须通过重放恢复已确认状态。
4. 旧 Owner 在迁移或网络分区后继续写入时，Journal 与 Snapshot 必须按旧 epoch 拒绝。
5. 迁移期间同一分片只允许一个写 Owner；失败可安全回滚或重新分配。
6. 压测记录单 Actor 内存、单 Zone 安全命令吞吐、Journal 提交 p99、Snapshot 落后和恢复速度。
7. 两个 Zone、多个逻辑分片和故障注入的本地原型形成可复现证据。
