---
status: accepted
date: 2026-07-29
owners:
  - project-owner
related:
  - ADR-0003-stateful-player-actor-zone.md
  - ADR-0004-shard-placement-and-control-plane-consensus.md
  - ADR-0006-async-dirty-writeback.md
  - ../architecture/stateful-zone-v3-architecture.md
---

# ADR-0008：V3 保留多数派 Shard Coordinator

## 背景

V3 用异步 Dirty 取代同步 Journal，只改变玩家状态持久化方式。Shard Owner 的唯一授权是独立问题：允许异常宕机丢失最近几秒，并不意味着可以接受网络分区时出现两个写 Owner。

## 考虑过的方案

### 方案 A：单 Active RouteManager

实现简单，但生产目标缺少网络分区下的唯一 Owner 授权。

### 方案 B：生产和原型都实现三节点共识

语义一致，但三周内同时实现业务、Actor、Dirty、WebSocket 和完整共识验证，范围过大。

### 方案 C：生产多数派，原型兼容单节点

生产保留严格单 Owner；原型使用相同 RouteEntry、租约、epoch、状态机和 fencing 接口，只验证业务接入，不声称控制面高可用。

## 决定

采用方案 C。

- `stable_hash64(player_id) % 4096` 映射到版本化逻辑 Shard；
- Placement Planner 使用 Rendezvous Hashing 和负载指标提出候选 Zone；
- 候选位置不直接授予写权限；
- 生产 Coordinator 三节点，至少 2/3 提交后 RouteEntry 才权威；
- RouteEntry 至少包含 `shard_id`、`owner_zone_id`、`owner_epoch`、`route_version`、`state` 和 `lease_term`；
- Zone 只向当前 Leader 续租；
- GateSvr 只缓存 committed `ACTIVE` 路由；
- 普通玩家命令不访问 Coordinator；
- 失去多数派时禁止新分配和 epoch 变化；
- 已有 Owner 只在租约有效期内继续关键写；
- 切换采用 `PREPARING → ACTIVE`；
- 新 Owner 在旧 Owner 停止或租约过期、数据库 fence 更新成功、加载检查点并 Ready 后才能 ACTIVE；
- Dirty 写入校验 `owner_epoch`；
- 生产复用成熟共识存储或 Raft 库，不自行实现 Raft；
- 本地原型只实现单 Coordinator 进程，但保持相同外部语义。

## 理由

- 多数派 Coordinator 解决 Owner 唯一性，不进入普通玩家命令链路；
- 异步 Dirty 解决在线延迟，两者可以独立组合；
- 一致性哈希只负责候选位置，多数派提交才授予写权限；
- GateSvr 使用本地路由缓存，不为每条命令增加共识往返；
- 原型和生产保持相同接入接口。

## 后果与风险

- 生产仍需共识组件、租约、Leader 切换和网络分区测试；
- Coordinator 与 MySQL fence 之间需要可恢复、幂等的状态推进；
- 控制面失去多数派或 Owner 租约过期时，部分 Shard 会暂时不可写；
- 单节点原型不能证明生产控制面高可用。

## 验证方法

1. 普通命令只读取 GateSvr 路由缓存；
2. 只有 `ACTIVE` RouteEntry 能接收写命令；
3. 旧 Owner 租约到期后停止关键写；
4. 新 epoch 更新 fence 后，旧 epoch Dirty 被拒绝；
5. `PREPARING` 状态可以在 Coordinator 重启后幂等完成或回滚；
6. 单节点原型验证两个 Zone、`NOT_OWNER` 刷新、租约、epoch 和旧写拒绝；
7. 生产方案另行补充三节点故障和网络分区证据。
