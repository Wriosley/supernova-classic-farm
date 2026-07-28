---
status: proposed
date: 2026-07-28
owners:
  - project-owner
related:
  - ../decisions/ADR-0002-target-scale-hybrid-architecture.md
  - target-30m-dau-architecture.md
---

# 经典农场 V2：有状态 Player Actor 目标架构

## 1. 文档定位

本文是对现有无状态 V1 的独立候选方案，不直接覆盖 V1，也不声称已经实现或达到 3000 万 DAU。V2 选择有状态 Player Actor 作为在线运行模型，同时要求已经成功响应的写操作可恢复。

```mermaid
flowchart LR
    V1["V1：无状态 Zone<br/>MySQL 是实时事实"] --> REVIEW["架构重新评审<br/>数据库往返、并发与状态局部性"]
    REVIEW --> V2["V2：有状态 Player Actor<br/>内存是运行时状态"]
    V2 --> J["响应前写可靠 Journal"]
    J --> S["异步生成数据库快照"]
```

V2 的四个核心不变量：

1. 一个逻辑分片同一时刻只有一个具备写权限的 Active Zone Owner。
2. 同一玩家的写命令在一个 Player Actor 邮箱内串行执行。
3. 写命令只有在 Durable Journal 可靠提交后才能修改正式内存并返回成功。
4. 玩家状态可以由“最近快照 + 快照后的连续 Journal 事件”确定性恢复。

## 2. 总体架构

```mermaid
flowchart TB
    C["H5 客户端"]

    subgraph ACCESS["接入层"]
        LB["负载均衡"]
        GW["API Gateway<br/>认证、限流、request_id"]
        RTG["Realtime Gateway<br/>WebSocket 连接与订阅"]
        ROUTER["Shard Router<br/>player_id → shard_id → Zone"]
    end

    subgraph CONTROL["控制面"]
        COORD["Shard Coordinator 集群<br/>租约、epoch、迁移"]
        ROUTE["版本化分片路由表<br/>shard_id → Zone"]
    end

    subgraph ZONE["有状态 Zone 集群"]
        OWNER["Shard Owner"]
        AM["Actor Manager<br/>按需加载与回收"]
        MB["Player Actor Mailbox<br/>同玩家串行"]
        DECIDE["Decide<br/>校验并生成事件"]
        APPLY["Apply<br/>修改内存状态"]
        MODULES["玩家模块<br/>钱包、仓库、农场、任务、图鉴、宠物"]
    end

    subgraph DURABLE["可靠存储层"]
        JOURNAL["Durable Journal<br/>请求、顺序、所有权与恢复事实"]
        SNAPSHOT["Snapshot DB<br/>玩家快照与 snapshot_seq"]
        SW["Snapshot Writer<br/>合并脏状态"]
    end

    subgraph ASYNC["异步服务层"]
        RELAY["Event Relay"]
        MQ["Kafka 兼容事件总线候选"]
        FRIEND["Friend Service"]
        MAIL["Mail Service"]
        RANK["Rank Service"]
        ANALYTICS["统计与归档"]
    end

    C --> LB --> GW
    C <-->|"WebSocket"| RTG
    GW --> ROUTER
    ROUTER --> ROUTE
    ROUTER --> OWNER
    COORD --> ROUTE
    COORD --> OWNER
    OWNER --> AM --> MB --> DECIDE
    DECIDE -->|"确定性事件"| JOURNAL
    JOURNAL -->|"可靠提交"| APPLY
    APPLY --> MODULES
    APPLY -->|"响应"| GW
    AM -->|"加载快照"| SNAPSHOT
    AM -->|"重放尾部事件"| JOURNAL
    APPLY -->|"标记脏状态"| SW
    SW -->|"异步检查点"| SNAPSHOT
    JOURNAL --> RELAY --> MQ
    MQ --> MAIL
    MQ --> RANK
    MQ --> ANALYTICS
    MQ --> RTG
    OWNER <-->|"好友关系查询"| FRIEND
```

状态权威关系：

```mermaid
flowchart LR
    M["Player Actor 内存<br/>当前运行时状态"]
    J["Durable Journal<br/>已确认操作的持久化事实"]
    S["Snapshot DB<br/>恢复检查点"]
    J -->|"按序 Apply"| M
    M -->|"异步生成"| S
    S -->|"加载基线"| M
```

## 3. 路由与 Shard Coordinator

### 3.1 分片关系

```mermaid
flowchart TD
    P1["玩家 A"] --> S10["逻辑分片 10"]
    P2["玩家 B"] --> S10
    P3["玩家 C"] --> S27["逻辑分片 27"]
    P4["玩家 D"] --> S81["逻辑分片 81"]
    S10 --> Z1["Zone-1"]
    S27 --> Z2["Zone-2"]
    S81 --> Z3["Zone-3"]
```

`shard_id = hash(target_player_id) % 1024`。一个 Zone 可持有多个逻辑分片，一个逻辑分片包含多个玩家；1024 是当前规划假设，仍需验证迁移粒度和路由表开销。

### 3.2 Coordinator 内部职责

```mermaid
flowchart TB
    subgraph COORD["Shard Coordinator 集群"]
        LEADER["Coordinator Leader"]
        F1["Follower-1"]
        F2["Follower-2"]
        LEASE["Lease Manager<br/>续租与失效判定"]
        REBALANCE["Rebalance Planner<br/>计算目标分配"]
        MIGRATION["Migration Controller<br/>编排迁移"]
        ROUTEPUB["Route Publisher<br/>发布版本化路由"]
    end

    STORE["强一致元数据<br/>owner、epoch、lease、state、route_version"]
    ZONES["Zone 状态<br/>心跳、CPU、内存、Actor、邮箱"]
    GATES["Gateway 路由缓存"]

    LEADER <-->|"一致性复制"| F1
    LEADER <-->|"一致性复制"| F2
    LEADER --> LEASE
    LEADER --> REBALANCE
    LEADER --> MIGRATION
    LEADER --> ROUTEPUB
    ZONES --> LEASE --> STORE
    REBALANCE --> STORE
    MIGRATION --> STORE
    ROUTEPUB --> STORE
    ROUTEPUB -->|"route_version"| GATES
```

Coordinator 不进入普通请求的数据面：

```mermaid
sequenceDiagram
    participant C as Client
    participant G as Gateway
    participant R as Route Cache
    participant Z as Zone Owner
    participant O as Coordinator

    O->>R: 推送 route_version=108
    C->>G: 玩家请求
    G->>R: 查询 shard 37
    R-->>G: Zone-2, epoch=12
    G->>Z: 转发请求
    Z-->>G: 返回结果
    G-->>C: 返回结果
    Note over O,Z: Coordinator 不参与本次业务请求
```

### 3.3 所有权状态机

```mermaid
stateDiagram-v2
    [*] --> Unassigned
    Unassigned --> Assigning: 选择 Zone
    Assigning --> Active: Zone Ready 且路由发布
    Active --> Draining: 正常迁移
    Draining --> Handoff: 邮箱排空
    Handoff --> Recovering: epoch 切换
    Active --> Recovering: 租约失效或 Zone 故障
    Recovering --> Active: 新 Zone 恢复并 Ready
    Active --> Unassigned: 分片下线
```

### 3.4 旧路由刷新

```mermaid
sequenceDiagram
    participant G as Gateway
    participant O as Old Zone
    participant C as Coordinator
    participant N as New Zone

    G->>O: 使用旧 route_version 请求
    O-->>G: NOT_OWNER, new_epoch=13
    G->>C: 拉取最新路由
    C-->>G: route_version=109
    G->>N: 使用相同 request_id 重试
```

## 4. Player Actor

### 4.1 Zone、分片与 Actor

```mermaid
flowchart TD
    Z["一个 Zone 实例"]
    Z --> S1["Shard 10"]
    Z --> S2["Shard 27"]
    Z --> S3["Shard 81"]
    S1 --> A1["Player Actor A"]
    S1 --> A2["Player Actor B"]
    S2 --> A3["Player Actor C"]
    S2 --> A4["Player Actor D"]
    S3 --> A5["Player Actor E"]
```

### 4.2 Actor 内部模块

```mermaid
flowchart TD
    P["Player Actor"]
    P --> M["有限邮箱"]
    P --> W["Wallet"]
    P --> I["Inventory"]
    P --> F["Farm"]
    P --> T["Task"]
    P --> C["Collection"]
    P --> PET["Pet"]
    P --> IDEM["近期幂等结果缓存"]
    P --> V["current_seq / snapshot_seq / epoch"]
```

钱包、仓库、农场、任务、图鉴和宠物保持代码模块隔离，但共享同一玩家状态机；需要一起变化的数据可由一个事件原子 Apply。

### 4.3 Actor 生命周期

```mermaid
stateDiagram-v2
    [*] --> Cold
    Cold --> Loading: 首个请求或异步命令
    Loading --> Active: 快照加载与日志重放完成
    Loading --> Quarantined: 数据不连续或反序列化失败
    Active --> Active: 邮箱串行处理
    Active --> Passivating: 超过空闲阈值
    Passivating --> Cold: 可恢复状态已确认
    Active --> Migrating: 分片迁移
    Migrating --> Cold: 旧 Owner 释放
    Active --> Recovering: Actor panic
    Recovering --> Active: 重建成功
    Recovering --> Quarantined: 重建不一致
```

邮箱达到上限时返回 `PLAYER_BUSY`，不能无限排队。V2 第一版在等待 Journal 提交期间不执行该玩家的后续写命令，避免重入破坏顺序；不同玩家 Actor 仍可并行。

## 5. 写请求与可靠持久化

### 5.1 单玩家写入时序

```mermaid
sequenceDiagram
    participant C as Client
    participant G as Gateway
    participant A as Player Actor
    participant J as Durable Journal
    participant S as Snapshot Writer

    C->>G: BuySeed(request_id=R1)
    G->>A: 路由到玩家邮箱
    A->>A: Decide：校验金币、价格、容量
    A->>A: 生成 SeedPurchased(seq=101)
    A->>J: Append(player, epoch, seq, request, event, result)
    alt Journal 可靠提交
        J-->>A: committed
        A->>A: Apply：金币-30，种子+3
        A-->>G: 返回成功
        G-->>C: 返回成功
        A-->>S: 标记 snapshot dirty
    else 失败或结果未知
        J-->>A: failed / timeout
        A-->>G: RETRYABLE_JOURNAL_ERROR
        G-->>C: 使用相同 request_id 重试
    end
```

### 5.2 Decide 与 Apply

```mermaid
flowchart LR
    CMD["Command<br/>购买种子"] --> D["Decide(state, command)<br/>只校验，不修改"]
    D --> E["Event<br/>SeedPurchased"]
    E --> J["Journal Commit"]
    J --> A["Apply(state, event)<br/>确定性修改"]
    A --> R["Result"]
```

随机奖励、服务端时间结果和配置版本必须在 Decide 时固化进 Event。Apply 与日志重放不得重新随机、重新读取当前时间或依赖已变化的配置。

### 5.3 Journal 记录

```mermaid
classDiagram
    class PlayerJournalEntry {
        +string player_id
        +int shard_id
        +int64 epoch
        +int64 player_seq
        +int64 shard_position
        +string request_id
        +string event_id
        +string event_type
        +bytes payload
        +bytes result
        +timestamp created_at
    }
```

必须满足：

- `(player_id, player_seq)` 唯一且连续；
- `shard_position` 是 Journal 分片内的单调位置，用于迁移追平和 Event Relay 检查点，不代替玩家序号；
- `(player_id, request_id)` 唯一；
- 旧 `epoch` 写入被拒绝；
- 只有满足目标多副本持久化条件才返回 committed；
- Journal 后端产品、分片数、保留期和清理方式仍待选型与验证。

## 6. 快照、加载与恢复

### 6.1 异步快照

```mermaid
flowchart LR
    E101["Event 101"] --> A["Player Actor"]
    E102["Event 102"] --> A
    E103["Event 103"] --> A
    E104["Event 104"] --> A
    A --> W["Snapshot Writer<br/>合并脏状态"]
    W --> DB["Snapshot DB<br/>snapshot_seq=104"]
```

快照失败时 Journal 仍保证恢复，但日志尾部会增长。系统必须监控最老快照落后时间、最大版本差、脏 Actor 数、重试数和预计恢复耗时；超过保护阈值时限制相关分片写入。

### 6.2 加载与重放

```mermaid
sequenceDiagram
    participant A as Actor Manager
    participant S as Snapshot DB
    participant J as Durable Journal
    participant P as Player Actor

    A->>S: Load(player_id)
    S-->>A: snapshot_seq=98, snapshot_data
    A->>J: Read(player_id, seq>98)
    J-->>A: Event 99, 100, 101
    A->>A: 检查序号连续与 epoch 合法
    A->>P: OnLoad(snapshot)
    A->>P: Apply 99 → 100 → 101
    P-->>A: Ready(current_seq=101)
```

若序号缺口、快照损坏或事件无法确定性 Apply，Actor 进入 `Quarantined`，停止写服务并报警。

### 6.3 响应丢失后的幂等重试

```mermaid
sequenceDiagram
    participant C as Client
    participant O as Old Zone
    participant J as Journal
    participant N as New Zone

    C->>O: request_id=R1
    O->>J: 提交 R1
    J-->>O: committed
    O--xC: 响应前崩溃
    C->>N: 使用 R1 重试
    N->>J: 恢复或查询 R1
    J-->>N: 已成功及原 result
    N-->>C: 返回相同结果
```

## 7. 异步事件与跨玩家流程

### 7.1 Journal 与事件总线的职责

```mermaid
flowchart LR
    J["Durable Journal<br/>玩家恢复与幂等事实"] --> R["Event Relay"]
    R --> Q["Kafka 兼容事件总线候选<br/>多消费组与积压"]
    Q --> T["任务规则处理"]
    Q --> M["邮件"]
    Q --> RK["排行榜"]
    Q --> WS["实时推送"]
    Q --> A["统计与归档"]
```

Kafka 类系统不直接被定义为第一版唯一的玩家随机恢复存储；它承担下游传播。若未来希望合并 Journal 与消息系统，必须先解决按玩家随机恢复、快照截断、长期保留和所有权 fencing。

任务规则处理器消费农场事件并生成幂等的 `AdvanceTaskProgress` 命令，再按玩家路由回 Player Actor。Player Actor 保存玩家任务进度和领奖幂等状态；任务规则处理器只负责规则计算，不形成第二份可写任务状态。

### 7.2 偷菜与奖励

```mermaid
sequenceDiagram
    participant C as 玩家 A
    participant B as Player Actor B
    participant J as Journal
    participant Q as Event Bus
    participant A as Player Actor A
    participant M as Mail Service

    C->>B: 偷取 B 的作物(request_id)
    B->>B: 校验成熟、剩余量、好友与重复偷取
    B->>J: 提交 CropStolen(theft_id)
    J-->>B: committed
    B->>B: Apply：扣减可偷数量
    B-->>C: 偷菜请求已接受
    J->>Q: 发布 StealRewardRequested
    Q->>A: 以 theft_id 幂等发放
    alt 仓库有容量
        A->>J: 提交 StealRewardGranted
        A->>A: Apply：作物入仓
    else 仓库已满
        A->>M: 以 mail_request_id 幂等创建奖励邮件
    end
```

偷菜事实属于农场主 B；奖励属于玩家 A。两者是两个可靠步骤，不使用跨 Zone 分布式事务，奖励允许异步到账。

### 7.3 数据归属

```mermaid
flowchart TD
    PA["Player Actor"] --> CORE["钱包、仓库、农场、任务、图鉴、宠物"]
    FS["Friend Service"] --> FRIEND["好友关系"]
    MS["Mail Service"] --> MAIL["邮件与附件领取"]
    RS["Rank Service"] --> RANK["全局排行榜"]
    PS["Payment Service"] --> PAY["支付订单"]
    RG["Realtime Gateway"] --> CONN["连接、会话、订阅"]
```

## 8. HTTP 快照与 WebSocket 增量

```mermaid
sequenceDiagram
    participant C as Client A
    participant R as Realtime Gateway
    participant Z as Player Actor B

    C->>R: Subscribe(farm=B)
    R-->>C: SubscribeAck(subscription_id)
    C->>Z: HTTP GetFarmSnapshot(B)
    Z-->>C: Snapshot(version=10)
    R-->>C: Delta(version=11)
    C->>C: 丢弃 version<=10，应用 11
    R-->>C: Delta(version=13)
    C->>C: 发现缺少 version=12
    C->>Z: 重新获取权威快照
```

Realtime Gateway 只持有连接和订阅，不持有权威农场状态。好友农场快照按被访问者 B 路由到其 Zone Owner。

## 9. 故障接管、迁移与 fencing

### 9.1 Zone 故障接管

```mermaid
sequenceDiagram
    participant O as Old Zone
    participant C as Coordinator
    participant N as New Zone
    participant S as Snapshot DB
    participant J as Journal
    participant G as Gateway

    O--xC: 心跳中断
    C->>C: 等待租约过期，Shard→RECOVERING
    C->>N: Prepare Shard
    N->>S: 加载快照
    N->>J: 重放尾部事件
    C->>C: CAS owner，epoch 12→13
    C->>N: Grant epoch=13
    N-->>C: Ready
    C->>G: 发布新 route_version
    G->>N: 恢复转发
```

### 9.2 旧主隔离

```mermaid
sequenceDiagram
    participant O as Old Zone(epoch=12)
    participant C as Coordinator(epoch=13)
    participant J as Journal

    O->>C: 恢复心跳并声明 epoch=12
    C-->>O: Ownership Lost
    O->>J: 尝试追加 epoch=12
    J-->>O: FENCED_EPOCH
    O->>O: 停止并释放该分片 Actor
```

### 9.3 正常迁移

```mermaid
sequenceDiagram
    participant C as Coordinator
    participant O as Old Zone
    participant N as New Zone
    participant J as Journal
    participant G as Gateway

    C->>N: Prepare Shard
    N-->>C: 预加载完成
    C->>O: Drain Shard
    O->>O: 拒绝新写并排空邮箱
    O->>J: 确认 handoff_position=500
    O-->>C: Drained，提交活跃 Actor 清单
    C->>C: epoch 12→13
    C->>N: Grant epoch=13
    N->>J: 追平到 shard_position=500
    N-->>C: Ready
    C->>G: 发布新路由
    C->>O: Release Shard
```

epoch 切换前可取消迁移并恢复旧主；切换后不得让旧主直接恢复写入，只能继续完成新主恢复或重新选主。

迁移只需要恢复旧 Zone 中仍活跃的 Actor：旧主在 Drain 后提交活跃 Actor 清单及其 `player_seq`，新主从快照与 Journal 恢复这些 Actor；冷玩家不搬运内存，下次请求到达时继续按需加载。`handoff_position` 表示整个 Journal 分片的追平水位，不能与任一玩家的 `player_seq` 混用。

## 10. 三可用区部署

```mermaid
flowchart TB
    subgraph AZ1["AZ-1"]
        G1["Gateway"]
        Z1["Zone"]
        J1["Journal Replica"]
        S1["Snapshot Replica"]
        C1["Coordinator Node"]
    end

    subgraph AZ2["AZ-2"]
        G2["Gateway"]
        Z2["Zone"]
        J2["Journal Replica"]
        S2["Snapshot Replica"]
        C2["Coordinator Node"]
    end

    subgraph AZ3["AZ-3"]
        G3["Gateway"]
        Z3["Zone"]
        J3["Journal Replica"]
        S3["Snapshot Replica"]
        C3["Coordinator Node"]
    end

    Z1 --> J1
    Z1 --> J2
    Z1 --> J3
    Z2 --> J1
    Z2 --> J2
    Z2 --> J3
    Z3 --> J1
    Z3 --> J2
    Z3 --> J3
    C1 <-->|"共识"| C2
    C2 <-->|"共识"| C3
    C3 <-->|"共识"| C1
    S1 <-->|"复制/备份"| S2
    S2 <-->|"复制/备份"| S3
```

三区合计计算容量继续以正常峰值约 150% 作为规划起点；单区故障后，剩余两区必须同时具备接管所需的 CPU、Actor 内存、Journal 吞吐和 Snapshot 恢复能力。具体复制协议和成功确认条件取决于最终存储产品，不在没有证据时写死。

## 11. 容量模型

V1 已确认的容量入口继续使用：3000 万 DAU、125 万平均在线、375 万正常峰值在线、6.25 万正常峰值外部 QPS、约 2.1 万峰值外部写 QPS和约 75 万正常 WebSocket 连接。

```mermaid
flowchart TD
    DAU["3000 万 DAU"] --> ONLINE["峰值在线 375 万"]
    ONLINE --> ACTOR["峰值驻留 Actor<br/>在线 + 断线保留 + 临时唤醒"]
    ACTOR --> MEM["Actor 内存需求"]
    ACTOR --> LOAD["加载与恢复吞吐"]

    QPS["外部峰值 QPS 6.25 万"] --> ZQPS["Zone CPU/QPS"]
    QPS --> WRITE["峰值写 QPS 约 2.1 万"]
    WRITE --> JQPS["Journal 逻辑追加吞吐"]
    JQPS --> REPLICA["多副本物理写入与网络放大"]
    WRITE --> SNAP["脏 Actor 与快照合并吞吐"]

    MEM --> COUNT["Zone 数量"]
    ZQPS --> COUNT
    LOAD --> COUNT
    JQPS --> COUNT
    COUNT --> HA["单区故障 + 发布 + 迁移余量"]
```

```text
Zone 实例数 = max(
  峰值 QPS / 单 Zone 安全 QPS,
  峰值写 QPS / 单 Zone 安全写 QPS,
  峰值 Actor 数 / 单 Zone 安全 Actor 数,
  Actor 总内存 / 单 Zone 安全内存
) + 故障、发布和迁移余量
```

单 Actor 内存、单 Zone 安全容量、Journal 条目大小、快照合并率、恢复速率和热点分片倾斜均为待测参数。

## 12. 观测与保护

```mermaid
flowchart LR
    Z["Zone"] --> ZM["Actor 数、邮箱、处理时长、GC、内存"]
    C["Coordinator"] --> CM["租约、迁移、路由版本、分片倾斜"]
    J["Journal"] --> JM["提交延迟、失败率、fencing、吞吐"]
    S["Snapshot"] --> SM["脏 Actor、版本差、最老落后、恢复重放量"]
    Q["Event Bus"] --> QM["积压、最老事件、重复、下游到账延迟"]
    R["Realtime"] --> RM["连接、订阅、广播、版本缺口、重连"]
```

保护动作：

```mermaid
flowchart TD
    R["请求进入"] --> M{"Actor 邮箱是否超限"}
    M -- "是" --> BUSY["PLAYER_BUSY<br/>退避重试"]
    M -- "否" --> J{"Journal 是否可确认"}
    J -- "否" --> RETRY["RETRYABLE_JOURNAL_ERROR<br/>不修改状态"]
    J -- "是" --> S{"分片是否仍有有效 epoch"}
    S -- "否" --> OWNER["NOT_OWNER<br/>刷新路由"]
    S -- "是" --> OK["Apply 并返回成功"]
```

## 13. 方案代价与未决项

V2 获得了同玩家串行、状态局部性和异步快照，但承担了新的复杂度：

- Player Actor 的加载、淘汰、内存和重入控制；
- Shard Coordinator、租约、epoch 与迁移状态机；
- Journal 的同步可靠提交延迟；
- 确定性事件、重放兼容和快照版本管理；
- 单 Actor 热点无法靠增加 Zone 直接拆分；
- 故障恢复期间优先一致性，相关分片可能短暂不可写。

尚未接受的产品和参数：

- Durable Journal 的具体后端及三可用区复制协议；
- Snapshot DB 产品、数据模型和快照频率；
- Coordinator 元数据实现；
- Journal 保留、归档与安全清理期限；
- Actor 空闲回收时间、邮箱上限和每 Zone Actor 上限；
- 单实例能力、实例数量、延迟目标和恢复目标。

## 14. 验证要求

```mermaid
flowchart TD
    D["V2 设计"] --> CORRECT["正确性<br/>串行、幂等、事件重放"]
    D --> CRASH["崩溃<br/>提交前后、响应丢失、Actor panic"]
    D --> OWNER["所有权<br/>旧路由、旧 epoch、双主隔离"]
    D --> MIGRATE["迁移<br/>Drain、handoff、追平、切换"]
    D --> LOAD["容量<br/>Actor 内存、Zone QPS、Journal、快照"]
    D --> REALTIME["实时<br/>订阅先行、版本缺口、重连"]
    D --> CROSS["跨玩家<br/>重复奖励、离线到账、满仓邮件"]
```

本机原型只验证机制与单实例基线，不能证明已经承载 3000 万 DAU。任何性能、可用性和恢复时间结论必须进入 `docs/evidence/` 并附可复现条件。

## 15. 文档演进

V1 保持原样作为对照基线。本文通过书面评审后，再新增 ADR 记录“为何从无状态 Zone 改为有状态 Player Actor”，并明确其对 ADR-0002 的 supersede 关系；在 ADR 接受前，不更新 `architecture.md` 为当前有效架构，也不把具体中间件写成已选型。
