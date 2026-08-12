---
status: proposed
date: 2026-08-12
decision-makers:
  - project-owner
supersedes:
  - ADR-0008-v3-quorum-shard-coordinator.md (仅替代生产控制面的共识实现方式)
superseded-by:
---

# ADR-0012：Kubernetes 发现、Tcaplus 权威路由与主动发布的 Coordinator

## Context

当前代码已经实现静态双 Zone 路由原型：

- `stable_hash64(player_id) % 4096` 将玩家稳定映射到逻辑 Shard；
- Coordinator 对静态 `zone-a/zone-b` 使用 Rendezvous Hashing；
- 单进程 `routing.Map` 保存 4096 条内存 `RouteEntry`；
- Gate/Info 启动拉完整路由快照并使用本地原子缓存；
- Zone 启动拉授权快照，之后每 5 秒轮询完整快照；
- Gate 命中旧路由后依靠 `NOT_OWNER` 清缓存并查询单 Shard；
- 人工迁移支持 Source Drain、mailbox 排空、Dirty flush、manifest、
  `PREPARING -> ACTIVE`、`owner_epoch`、Tcaplus Fence CAS 和进度恢复。

当前限制：

1. Zone 成员和 endpoint 由环境变量写死；
2. Coordinator 无条件替静态 Zone 续租，即使 Zone 已死亡；
3. `ShardFence` 只间接保存 owner/epoch/version，没有完整 Current `ShardRoute`；
4. Coordinator 重启先按静态成员重算，再用 Fence/Progress 覆盖；
5. 路由变化不主动通知 Gate、Info 和 Zone；
6. 迁移由人工 HTTP 同步驱动，没有 Planner、Queue、Worker 和 Failover；
7. Coordinator 单副本，Zone 是两个无稳定身份的 Deployment；
8. Zone 死亡后的并发冷 Actor Load 没有全局/per-Shard 限流。

项目需要支持 8 个 Zone 并扩容到第 9 个，成员变化只迁移必要 Shard；支持正常
迁移、故障转移、主动发布和平滑更新，同时保持单写 Owner。30 million DAU 是设计
目标，不是本地已验证能力；本文数字均为待压测起始配置。

## Owner's initial reasoning

项目所有者提出：Zone 由 Kubernetes/etcd 提供注册和存活信息；Coordinator 管理
玩家路由并通知订阅者；Gate、Info 等各持本地缓存；新增 Zone 最小迁移；Source
停止服务、处理 Actor 并落库后 Target Load；Zone 死亡时暂停对应 Shard 再迁移；
权威路由存 Tcaplus；Coordinator 三副本主从接管。

关于发布顺序，最终修正为：最小权威状态必须先落盘，随后立即通知；审计、统计等
非关键数据可以异步落盘。

## Considered options

### Option A：静态列表，每次成员变化重新生成整张路由表

- Benefits：改动最少，适合固定双 Zone 演示。
- Costs：无法运行时发现、自动 Drain/Failover/发布；重算可能覆盖 Current。

### Option B：独立 etcd 注册 + 自研三节点 Raft Coordinator

- Benefits：独立发现和多数派 Route Log。
- Costs：Kubernetes 已提供 Watch/Lease；直接访问其内部 etcd 不是稳定业务接口；
  额外 etcd、自研 Raft 和故障验证超出交付范围。

### Option C：Kubernetes 发现/选主 + Tcaplus 权威路由/CAS

- Benefits：复用现有 Kubernetes、Tcaplus、Fence 和迁移代码，可逐阶段落地。
- Costs：正确性共同依赖 Kubernetes Lease、Tcaplus CAS 和 Zone Fence；不等价于
  独立的 2/3 Route Log；控制面或 Tcaplus 不可用时停止 ownership 切换。

## Decision

采用 Option C。

### 1. 保持两级映射

```text
player_id --stable FNV-1a--> shard_id --dynamic Current Route--> Zone
```

- 固定 4096 Shard；不修改 `ShardForPlayer` 和 `HashAlgorithmVersion=1`；
- 迁移只改变 `owner_zone_id`，不改变 `shard_id`；
- 普通命令只读进程内缓存，不同步访问 Coordinator。

### 2. Zone 身份

- `logical_zone_id`：稳定身份，参与 Rendezvous 和 ownership；
- `incarnation_id`：每次启动的新 UUID，识别旧进程；
- `endpoint`：当前地址，不参与 placement score。

Zone 改为 StatefulSet。logical ID 使用
`UUIDv5(cluster_id + namespace + zone_pool + ordinal)` 确定性生成。普通重启保持
logical ID、更新 incarnation，不 Rebalance；新增 Zone 使用新 ID；蓝绿的新 pool
使用新 IDs。ZoneIdentity 不单独持久化，ShardRoute 保存 owner logical ID。

### 3. Kubernetes 发现但不授权

Coordinator Watch EndpointSlice/Pod：

- `/livez` 只检查进程/runtime，不查 Tcaplus；
- `/readyz` 表示初始化完成，探针只读内存 ready 状态；
- Endpoint 消失、Pod Failed 或连续 3 次 `/livez` 失败才确认 DEAD；
- SDK 仅上报 refused、timeout、reset、gRPC `UNAVAILABLE`，先进入 SUSPECT；
- 业务错误、`STORAGE_UNAVAILABLE` 不得判定 Zone 死亡；
- SUSPECT 暂停对应 Shard，恢复不改变 epoch。

Pod 存在不代表拥有 Shard。Current Route、`owner_epoch` 和 Fence 才授予写权限。

### 4. 普通 Rendezvous 计算 Desired

沿用当前确定性算法：

```text
score = first_u64(SHA-256(
  assignment_domain || 0 || big_endian(shard_id) || 0 || logical_zone_id
))
desired_owner = score 最大的 HEALTHY Zone
```

同分按 logical ID 字节序。新增 Zone 不改变旧 Zone 得分；只迁移新 Zone 得分超过
旧最大值的 Shard。只保证统计均衡，不强制等分或二次负载修正。

- `Current`：已提交、可发布的权威路由；
- `Desired`：健康成员集合计算的目标；
- `MigrationTask`：`Current.owner != Desired.owner` 的差异。

Desired 绝不能直接覆盖 Current。

### 5. Tcaplus 保存完整 Current

新增 `ShardRoute`，至少保存：

```text
shard_id, owner_zone_id, owner_endpoint, owner_epoch,
route_version, map_version, state,
previous_owner_zone_id, transition_id, updated_at_ms
```

继续保留 `ShardFence`（checkpoint/Dirty 写权限）和 `MigrationProgress`（恢复步骤、
源/目标和 manifest）。

启动规则：空表才初始化 4096 条 Current；非空只加载 Current；加载后再计算
Desired；Progress 与 Current/Fence 交叉校验后恢复；endpoint 变化不得静默改变
owner/epoch。

### 6. 权威提交先于发布

```text
Fence CAS
-> Target ShardReady
-> Active ShardRoute CAS
-> 更新 Coordinator 内存 Current
-> 立即发布 RouteBatch
-> 异步写审计、统计和非关键历史
```

- Fence/Active Route 写失败：不发布，Shard 保持暂停；
- SDK ACK 不参与提交，慢订阅者不能阻塞；
- 重启以 Tcaplus Current/Fence 恢复，不从订阅缓存反推；
- MigrationProgress 关键步骤同步持久化，完成后的历史可以异步。

### 7. 进程内 Coordinator SDK

Gate、Info、Zone 各嵌入共享 Go SDK：

- 每 Pod持有不可变 `[4096]RouteEntry`，不用独立 Pod/Redis；
- 对业务暴露 `ResolvePlayer`、`ResolveShard`；
- 启动先拉 Snapshot，再建立 SDK→Leader 双向 gRPC `WatchRoutes`；
- 按 `map_version` 应用 RouteBatch；重复忽略，gap 全量 Resync；
- 保留 HTTP Snapshot、单 Shard查询和 `NOT_OWNER` 作为兜底。

Watch 包括 Subscribe、Snapshot、RouteBatch、AvailabilityChanged、Ack、Ping/Pong、
ResyncRequired。起始配置：30 秒 Ping、90 秒无消息重连，断线缓存最多使用到
`min(route lease expiry, disconnected_at+90s)`。每连接有界队列从 128 批次开始；
溢出时 Resync/断开，不阻塞发布。数值待压测。

### 8. 持久化迁移队列

```text
PLANNED -> SOURCE_DRAINING -> SOURCE_FLUSHED -> ROUTE_PREPARING
-> FENCE_ADVANCED -> TARGET_LOADING -> TARGET_READY
-> ROUTE_ACTIVE -> COMPLETED
```

- Draining 后拒绝该 Shard 新读写/激活；排空 mailbox，flush Dirty，生成 manifest；
- Fence 前可恢复 Source；Fence 后不能回旧 epoch，只能继续或更高 epoch 换目标；
- Target 第一版只 ShardReady，不预热 Actor；请求到达再 Load；
- 迁移返回可重试 `ZONE_MIGRATING`，重试沿用 request_id；
- Active Route 落盘后才发布。

优先级 `FAILOVER > DRAIN > REBALANCE`。并发起始值：全局 8、单 Source 2、单
Target 2。Rebalance 排序看 Actor/Dirty/mailbox/QPS，最后按 shard_id；指标只改变
顺序，不改变 Desired owner。

### 9. DEAD Zone 故障迁移

- SUSPECT 发布临时不可用，返回 `ZONE_UNAVAILABLE`；
- DEAD 后跳过 Source Drain/Flush/manifest；
- 选健康 Zone 中 Rendezvous 下一候选，推进 Fence/epoch；
- Target ShardReady 后提交 Active Route，Actor 延迟 Load；
- Target 再死则以更高 epoch 选第三 Zone；
- Tcaplus 不能推进 Fence 时保持不可用，禁止仅靠内存切换。

异常死亡可能丢失 ADR-0006 接受的未刷 Dirty 窗口，不得套用正常迁移的无损保证。

### 10. 三副本与 Kubernetes Lease

- 部署 3 个 Coordinator 副本；
- Kubernetes Lease 选单 Active Leader；
- 只有 Leader 执行 planning、migration、failover、权威 CAS 和发布；
- Follower 只健康检查和 Leader发现；Leader 丢 Lease 取消控制任务；
- 所有写入带旧 owner/epoch/version/transition 条件，CAS 拒绝旧 Leader覆盖。

本决定只替代 ADR-0008 的“2/3 Route Log”实现方式，不替代单 Owner、epoch、Fence
和普通请求不进控制面。不得把本方案描述为自研 Raft 或独立多数派 Route Log。

### 11. Actor 冷 Load 限流

保留“先注册 Loading Actor，再在首 mailbox 任务中 Load”，增加起始配置：

```text
global_active_limit=64, per_shard_active_limit=4
global_queue_limit=1024, per_shard_queue_limit=64
queue_timeout=2s, load_timeout=3s
```

获得全局和 per-Shard许可后才能 Load；同玩家仍只 Load 一次；队列满/超时返回
`ZONE_WARMING_UP`；Load 超时返回 `STORAGE_UNAVAILABLE`；Drain 取消排队激活并
处理已开始 Load。数值通过 Tcaplus 压测修订。

## Code boundaries for future Codex executors

必须优先复用：

- `server/internal/routing/routing.go`：Shard hash、Rendezvous、RouteEntry/Map；
- `server/internal/routing/authorization.go`：Zone 原子授权快照和 Drain；
- `server/internal/routing/tcaplus_control_store.go`：Fence/Progress CAS 模式；
- `server/internal/gateway/route_cache.go`：不可变路由缓存；
- `server/cmd/coordinator/migration.go`：迁移和恢复语义；
- `server/cmd/zone/lifecycle.go`：Source Drain、Target Prepare；
- `server/internal/player/runtime.go`：Actor 激活、Drain、Dirty flush；
- `deploy/k8s/`：当前 Deployment、Service、probes。

后续计划新增清晰边界，禁止继续扩大 `cmd/coordinator/main.go`/`migration.go`：

```text
server/internal/coordinator/membership/   Kubernetes发现与健康
server/internal/coordinator/placement/    Current→Desired纯计算
server/internal/coordinator/routestore/   ShardRoute权威持久化
server/internal/coordinator/publisher/    Watch订阅与发布
server/internal/coordinator/migration/    Queue/Worker/恢复状态机
server/internal/coordinator/leadership/   Kubernetes Lease适配
server/internal/coordinatorclient/        Gate/Info/Zone共享SDK
server/internal/player/activation/        Actor Load限流
```

目录和签名可在计划核对依赖后微调，但职责不得耦合回业务 handler。每阶段必须保持
静态双 Zone回归可运行，直到替代路径验收。

## Rationale

- 固定 Shard 将玩家分区与 Zone 生命周期分离；
- Rendezvous 只算候选位置，Current/Fence 才授权；
- Current/Desired 分离避免成员变化直接切换；
- 先落权威状态再发布，避免崩溃后路由回退；
- SDK 缓存保持 Coordinator 不在普通命令链路；
- 复用现有 Drain/Fence/epoch 比重写风险低；
- 首版不预热，先用限流保护 Tcaplus，再由压测决定。

## Consequences

### Positive

- 支持运行时扩缩容、最小迁移、主动更新、故障接管和平滑发布；
- Coordinator 重启不依赖静态列表重算 Current；
- 现有业务链路和 Player Actor 模型无需重写。

### Negative

- 新增 Kubernetes client/RBAC/Lease、gRPC stream、ShardRoute CAS；
- 路由、可用性和迁移进度多版本使调试更复杂；
- 动态迁移造成 Tcaplus Load 峰值；
- 控制存储故障时为防双写必须牺牲可用性。

### Risks

- Lease 延迟与旧 Leader in-flight 操作竞争；
- Pod 存在不等于 runtime 健康；
- 慢订阅者/version gap 导致缓存滞后；
- ShardRoute/Fence 边界错误导致双 Owner或永久暂停；
- Rendezvous 不解决业务热点；DEAD 误判导致不必要迁移；
- 无预热时 Failover 有冷 Load 延迟。

## Validation

后续计划必须把验证拆入对应阶段：

1. 相同成员生成相同 Desired，Player→Shard 不变；
2. 8→9 只迁移 Desired 改变的 Shard，记录实测数量/分布；
3. 重启恢复 Current/epoch/version，不回退；
4. Fence/Active Route 写失败时 SDK 看不到新 Owner；
5. Watch 重复、乱序、丢批次、断线、慢订阅正确 Resync；
6. 普通缓存命中不查询 Coordinator；
7. 迁移每个持久状态杀 Coordinator 后可恢复，旧 epoch 被拒绝；
8. 误报只 SUSPECT，恢复不换 epoch，DEAD 才 Failover；
9. `STORAGE_UNAVAILABLE` 不触发迁移；
10. Leader 切换时旧 Leader CAS 失败，新 Leader恢复任务；
11. 同玩家 100 请求只 Load 一次，激活并发不超限；
12. 新 pool 逐 Shard接管，旧 pool 清空后退出；
13. 证据写入 `docs/evidence/` 并标记 measured/derived/assumed。

## Revisit conditions

- Tcaplus 无法满足单行 CAS、遍历恢复或延迟目标；
- Coordinator 需要跨 Kubernetes 集群；
- 必须在 Kubernetes API 不可用时继续改变 ownership；
- 纯 Rendezvous 的数量/热点倾斜经测量不可接受；
- 冷 Load 限流仍不能满足目标，需要预热/内存迁移；
- 需要严格独立的 2/3 Route Log，届时引入成熟共识存储并新建 ADR。

## Related evidence and discussions

- `docs/architecture/stateful-zone-v3-architecture.md`
- `docs/decisions/ADR-0008-v3-quorum-shard-coordinator.md`
- `docs/plans/2026-08-03-static-dual-zone-routing-plan.md`
- `docs/plans/2026-08-03-coordinator-preparing-recovery-plan.md`
- Future plan: `docs/plans/final_delivery_sprint/07-1-Coordinator动态路由控制面.md`

## Owner review checklist

改为 `accepted` 前，项目所有者需要能够回答：

1. Shard ID、logical Zone ID、incarnation、endpoint、owner epoch 各做什么？
2. Rendezvous 与 Fence 分别解决什么？
3. 为什么 Desired 不能直接覆盖 Current？
4. 为什么先落 Fence/Active Route，再通知订阅者？
5. 正常迁移与 Failover 各保证什么、损失什么？
6. Kubernetes Lease、Tcaplus CAS、owner epoch 各防哪类错误？
7. 为什么本方案不能表述为自研三节点 Raft？

