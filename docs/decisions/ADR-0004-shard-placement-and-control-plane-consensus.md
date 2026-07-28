---
status: accepted
date: 2026-07-28
owners:
  - project-owner
related:
  - ADR-0003-stateful-player-actor-zone.md
  - ../architecture/stateful-zone-v2-architecture.md
---

# ADR-0004：Shard 位置规划与控制面共识分离

## 背景

V2 需要把 4096 个逻辑分片分配到多个有状态 Zone。单纯使用一致性哈希可以计算理想位置，但网络分区、Zone 上下线或不同节点看到不同成员列表时，可能出现两个节点都认为自己是 Owner。项目还必须明确哪些逻辑需要自行实现，哪些应复用成熟共识能力。

## 考虑过的方案

### 方案 A：所有节点直接按一致性哈希计算 Owner

实现简单，普通请求不需要路由表。但成员视图不一致时会算出不同 Owner，也难以表达迁移中的 Drain、Recovering 和 epoch，不能满足不出现双写的要求。

### 方案 B：单个 Placement Controller 直接分配

业务逻辑直观，但 Controller 故障后无法安全判断旧分配是否仍有效，形成单点故障；网络分区时也缺少权威提交条件。

### 方案 C：一致性哈希规划候选，多数派提交权威映射

Placement Planner 用 Rendezvous Hashing 和负载指标选择候选 Zone。Coordinator Leader 提议 `RouteEntry`，三个控制节点中至少两个提交后，新的 Owner 和 `route_epoch` 才生效。Gateway 读取已提交映射，Journal 与 Snapshot 按 epoch 隔离旧 Owner。

## 决定

采用方案 C。

- 玩家到逻辑分片使用版本化的 `stable_hash64(player_id) % 4096`。
- 逻辑分片到候选 Zone 默认使用 Rendezvous Hashing；CPU、Actor 内存、邮箱积压、故障域和迁移并发作为负载修正条件。
- 一致性哈希只产生候选位置，不直接授予写权限。
- Coordinator 采用三节点控制面；Leader 提议路由变更，至少 2/3 提交后才成为权威 `RouteEntry`。
- Follower 不独立分配 Shard；Zone 向当前 Leader 心跳或续租，不需要向全部控制节点逐个续租。
- Gateway 缓存已提交的 `shard_id → owner_zone_id + route_epoch + route_version`，普通请求不经过 Coordinator。
- 旧路由返回 `NOT_OWNER`，Gateway 刷新路由并使用相同 `request_id` 重试。
- 失去多数派时禁止新分配和 epoch 变化；已有 Owner 只能在租约有效且 Journal 多数派接受其 epoch 时继续写，租约到期即停止。
- Journal 与 Snapshot 必须校验 `route_epoch`，作为隔离旧 Owner 的最终防线。
- 项目自行实现哈希、Placement Planner、路由模型、Owner 状态机、缓存刷新、迁移编排和 fencing 接口；不从零实现 Raft、选主或复制日志。
- 共识存储或库的具体产品仍待选型。单节点模拟不能作为多数派高可用证据。

## 理由

- 把“算出理想位置”和“授予权威写权限”分开，避免把一致性哈希误当成一致性协议。
- Rendezvous Hashing 实现和解释较简单，成员变化时只移动部分分片，适合 4096 个固定逻辑分片。
- 多数派提交保证网络分区时最多一侧能够产生新的权威 Owner。
- Gateway 使用缓存路由，控制面不会进入每个玩家请求的数据链路。
- 复用成熟共识实现能把三周原型集中在农场特有的路由、Actor、Journal 和迁移机制上。

## 后果与风险

- Coordinator 元数据和 Journal epoch 校验都成为正确性边界，二者必须采用相同的 epoch 语义。
- 负载修正会使实际分配不完全等于纯哈希结果，因此必须持久化权威路由表。
- 控制面失去多数派或 Owner 租约过期时，部分 Shard 会暂时不可写。
- Zone 上下线可能触发大量候选变化，需要限制同时迁移的 Shard 数量并设置稳定窗口。
- 具体共识产品的延迟、可用性和三可用区行为仍需原型验证。

## 验证方法

1. 相同健康 Zone 集合下，所有 Planner 对同一 Shard 得到相同候选顺序。
2. 增减一个 Zone 时，只迁移预期范围内的 Shard，且负载修正不超过迁移并发限制。
3. Leader 提议但未获 2/3 提交时，不发布新 Owner 和 route version。
4. 隔离一个 Coordinator 后，剩余两个节点仍能提交；只剩一个节点时不能重新分配。
5. 旧 Owner 使用旧 epoch 写 Journal 或 Snapshot 时被拒绝。
6. Gateway 命中旧路由时收到 `NOT_OWNER`，刷新后用相同 `request_id` 安全重试。
7. 区分模拟测试和真实三节点故障证据，不把单节点行为写成多数派容错结论。
