---
status: draft
date: 2026-07-31
audience:
  - MT
---

# 7 月 31 日项目进度同步提纲

## 同步目标

用 5–10 分钟说明当前已经完成的设计、正在进入的实现阶段、3000 万 DAU 目标的关键机制，以及尚未获得证据的部分。

## 口头提纲

### 1. 当前目标

项目是一个 Go 后端与 Vue 3 H5 的经典农场。第一阶段先完成农场主单玩家闭环，再扩展好友和多人。

最近的硬里程碑是 8 月 2 日：

```text
注册或登录
→ 获取一次性 WebSocket Ticket
→ 建立认证 Protobuf WebSocket
→ 通过 GateSvr 路由一条命令到 Player Actor
→ 收到 request_id 对应的响应
```

### 2. 昨天完成的架构收敛

当前生产目标已经收敛到 V3：

- Player Actor 在 Zone 内持有在线权威状态；
- 同玩家命令串行，不同玩家并行；
- 普通命令修改 Actor 内存并响应，Dirty 后台批量写 MySQL；
- 生产目标通过三节点多数派 Coordinator 授权路由，并结合 lease、`owner_epoch` 和数据库 fence 维持单 Shard 单写 Owner；
- 本地只实现接口兼容的单节点 Coordinator，不声称控制面高可用。

V3 相比 V2 去掉同步 Journal，缩短命令热路径；代价是 Zone 异常退出可能回退最近尚未刷盘的普通状态。

### 3. 今天新增的业务与契约进度

- 单玩家第一条闭环已经定义：购买、种植、施肥、成熟、收获、出售、任务、领奖和清理；
- 成长由服务端时间推导，不按玩家每秒持久化 Tick；
- ConfigSvr 是配置权威，一条命令固定使用一个配置快照；
- HTTP、WebSocket、错误与幂等、数据模型、奖励邮件事件契约已经冻结；
- 跨契约复核已经澄清失败重放版本、异常恢复后的序列单调边界和事件版本类型；共享 Proto 已通过双端 round-trip，四个 Go 进程的认证快照链路也已产生可重复运行证据。

### 4. 3000 万 DAU 设计的四个关键面

1. **连接面**：GateSvr 承担长期 WebSocket、心跳、背压和重连压力。
2. **数据面**：GateSvr 使用本地 committed ShardMap 缓存，把请求直接路由到唯一 Zone Owner；普通命令不访问 Coordinator。
3. **控制面**：4096 个逻辑 Shard 通过多数派路由授权、lease、`owner_epoch` 和数据库 fence 拒绝旧 Owner 写入，维持迁移和故障期间的单 Owner 语义。
4. **持久化面**：Actor 内存把数据库移出同步热路径，Dirty 合并批写承担恢复检查点吞吐。

这些机制还必须通过 Gate 连接成本、Actor 内存/GC 和 Dirty/MySQL 吞吐实验验证。

### 5. 当前证据边界

以下是设计目标，不是已经验证的能力：

- 3000 万 DAU；
- 375 万正常峰值 WebSocket；
- 约 500 万峰值驻留 Actor；
- 约 6.94 万条每秒游戏消息；
- 约 60 个 Zone 的中档规划点。

现在完成的是架构、业务规则、契约、跨契约复核、有界实施计划、共享 Proto、四个 Go 进程和 H5 snapshot 客户端。多进程协议客户端已跑通注册、Ticket、AUTH、PING、路由、Actor snapshot 和 Ticket 重放拒绝；Owner 也手动完成了一次浏览器注册到快照。MySQL 持久化、自动化浏览器验证与容量仍未完成。

### 6. 到 8 月 2 日的执行方式

- 共享 Proto、认证、路由、Actor snapshot 和 H5 展示代码已经落地；
- `GET_PLAYER_SNAPSHOT` 多进程链路已证明认证、路由、Actor 和响应关联；
- 端到端脚本已记录主路径、PING 和 Ticket 重放拒绝；手动浏览器演示发现并修复了一处注册后 CSRF 绑定缺陷；
- 8 月 2 日不承诺完整 Dirty Flusher、Outbox、生产共识、好友、多人或完整业务闭环。

### 7. 当前主要风险

- 多进程协议链路和手动浏览器 smoke 已运行，但浏览器 UI 尚无自动化证据；
- 当前 Login 和 Player 状态使用明确的内存开发适配器，不满足 durable registration 或 checkpoint 恢复；
- 当前注册只写 Login 内存，Player Actor 在首次快照时由 Zone 懒创建；两者还没有 MySQL 原子初始化边界；
- 当前机器可用 Go 1.26.4、Node 20.20.0 和 protoc 35.1，但没有可用的 Docker 命令；Compose 可以作为交付配置，实际本地 MySQL 验证仍需安装运行环境或明确使用临时内存适配器；
- 容量数字尚无运行和统计证据。

控制方式是共享契约单一来源、目录互斥、先打通最小链路，并在无法及时完成完整依赖时使用接口兼容的本地适配器，但明确记录限制。

## 建议展示顺序

1. `docs/context/CURRENT.md` 的里程碑状态；
2. `docs/architecture/stateful-zone-v3-architecture.md` 的总体架构图；
3. `docs/plans/2026-07-31-v3-first-stage-implementation-plan.md` 的验收范围。

`docs/contracts/` 和单玩家业务架构作为被追问时的备用材料，不在主叙述中逐份打开。

## 同步前检查

- 将“已经完成”“正在进行”“规划假设”“尚未验证”分开表述；
- 根据同步前最后一次测试结果修改实现进度；
- 没有 evidence 的链路或性能不得说成已经跑通；
- 准备解释 V3 为什么接受未刷 Dirty 回退，以及为什么普通命令不访问 Coordinator。

## 预期追问与短答

### 为什么不用 V2 的同步 Journal？

同步 Journal 能缩小已确认状态的丢失窗口，但把持久化依赖放回每条普通命令的响应前。V3 优先验证 Actor 内存热路径和 Dirty 合并吞吐，并明确接受异常 Zone 的未刷状态回退；这是一项已记录代价，不是免费优化。

### Coordinator 为什么不成为每条命令的瓶颈？

Coordinator 只提交和发布 Shard 所有权。GateSvr 缓存 committed ACTIVE 路由，普通命令直接访问 Zone；只有路由过期、迁移、租约或故障时才进入控制面处理。

### 4096 个 Shard 和约 60 个 Zone 是什么关系？

4096 是稳定的逻辑路由和迁移粒度，一个 Zone 持有多个 Shard。约 60 个 Zone 只是压测前的容量规划点，最终数量必须由单 Zone Actor 内存、命令吞吐和 Dirty 能力反推。

### 幂等如何在异常回退后保证？

幂等结果和业务状态在同一个 checkpoint 中一起落库，正常重试可返回原结果。异常 Zone 丢失未刷 checkpoint 时，两者可能一起回退，因此 V3 不承诺未刷响应跨异常故障的 durable exactly-once。

### 8 月 2 日为什么先做 snapshot 而不是完整种植闭环？

`GET_PLAYER_SNAPSHOT` 可以用最小业务复杂度证明 HTTP Session、Ticket、WS AUTH、路由、Actor 串行边界、版本和响应关联。链路稳定后再逐步增加写命令、Dirty 和完整业务闭环，定位问题更清晰。
