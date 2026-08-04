---
status: accepted
version: 1
date: 2026-07-30
owners:
  - project-owner
source: data-model.md
related:
  - websocket-protocol.zh-CN.md
  - idempotency-and-errors.zh-CN.md
  - ../architecture/stateful-zone-v3-architecture.md
  - ../architecture/single-player-vertical-loop-business-architecture.md
  - ../decisions/ADR-0006-async-dirty-writeback.md
  - ../decisions/ADR-0008-v3-quorum-shard-coordinator.md
  - ../decisions/ADR-0009-player-actor-task-progress.md
---

# V3 数据模型契约 V1

## 1. 范围与权威性

本文是英文规范源 `data-model.md` 的完整中文阅读镜像。若翻译有歧义，以英文规范源为准。规范词 `MUST`、`MUST NOT`、`SHOULD` 和 `MAY` 分别表示“必须”“禁止”“应当”和“可以”。

本契约定义 V3 有状态 Player Actor 路径第一阶段的完整逻辑数据模型，覆盖：

- 内存中的 Player Actor 聚合；
- 客户端 `PlayerSnapshot` 投影；
- 序列化 Player 检查点；
- 关系型信封、索引、Outbox 和 fence 数据；
- 已提交的 ShardMap 控制面数据；
- 内存 Dirty Queue 和批量写 DTO；
- 恢复持久化状态所需的配置引用。

本文不定义 SQL DDL、生成的 Protobuf、RPC 代码、迁移程序或奖励邮件事件载荷。奖励邮件载荷属于 `event-contracts.md`。

V3 是当前权威。MySQL 是最近恢复检查点，不是活跃 Actor 的在线事实。当前路径不存在同步 Journal、Kafka 重放、逐命令数据库事务、本地 WAL，也不保证已响应但尚未刷盘的普通写在 Zone 异常丢失后仍然存在。

## 2. 表示选择与模型边界

第一版实现必须使用：

1. 确定性、带版本的 Protobuf `PlayerCheckpointV1` blob，作为可完整恢复的 Player Actor 聚合；
2. 最小关系型信封列，服务于 fencing、CAS、查找、可观测性和迁移；
3. 与首次创建对应事件的检查点在同一事务中物化的不可变关系型 Outbox 行；
4. 独立的 Coordinator 所有 ShardMap 状态和数据库所有 shard fence。

该选择让恢复只需解码一个聚合、保留未知 Protobuf 字段，并避免把同玩家不变量拆到多个可变业务表中。关系型列禁止成为第二份在线业务权威。

以下表示彼此不同：

| 表示 | 权威性与用途 | 不包含 |
|---|---|---|
| 内存 Actor 状态 | Owner Actor 活跃时的在线权威；只能由其 Mailbox 修改 | 数据库 Relay 状态、Coordinator 内部信息 |
| 客户端 `PlayerSnapshot` | `websocket-protocol.md` 定义的脱敏读取投影 | 幂等、Outbox、Dirty 元数据、内部小数、冻结任务内部字段 |
| `PlayerCheckpointV1` | 从 Actor 内存复制的完整序列化恢复聚合 | 密码、Session、连接、订阅、Mailbox、Timer |
| 关系型信封/索引 | blob 外围的 CAS、fence、版本和查找元数据 | 可独立修改的金币、仓库、地块或任务 |
| 控制面数据 | 已提交路由、租约和 shard fence 权威 | 玩家业务状态 |

客户端投影必须来自当前 Actor 内存，禁止把可能过期的数据库检查点当作实时状态。

## 3. 通用标量、编码与排序规则

除非本文另有规定，标量含义与 `websocket-protocol.md` 一致。

| 含义 | 逻辑类型 | 规则 |
|---|---|---|
| Player/epoch/sequence/version | `uint64` | 非负；H5 禁止使用 JavaScript `number` |
| 领域/配置 ID | `uint32` | 除非明确表示缺省，否则零无效 |
| 金币 | `int64` | 整数金币单位；余额必须 `>= 0`；算术必须检查溢出 |
| 数量/进度 | `uint32` | 检查算术；仓库数量为 `1..300` |
| 时间 | `int64` | 服务端 UTC Unix 毫秒；仅标记为可选的字段可用 `0` 表示缺省 |
| UUID | 16 个原始字节 | RFC 4122 字节序；非零；文本投影使用规范小写形式 |
| 哈希 | 原始字节 | SHA-256 值必须恰好 32 字节 |
| 不透明 Protobuf | bytes | 指定已接受消息版本的确定性序列化结果 |

类似 Map 的 repeated 集合必须键唯一并按键升序序列化：

- 仓库按 `item_id`；
- 地块按 `plot_id`；
- 任务按 `task_id`；
- 幂等结果按 `(completed_at_ms, request_id bytes)`；
- 待处理 Outbox 按 `(created_at_ms, event_id bytes)`。

在没有显式迁移时，解码并重新编码检查点必须保留未知 Protobuf 字段。已发布字段号和枚举号永远禁止复用。

### 3.1 小数与成长算术

权威速度值和速度修正使用有符号定点数 `RateDecimal6`；成熟值和累计成长值使用有符号定点数 `GrowthDecimal9`：

```text
RateDecimal6.scaled_value = 实际速度 × 1,000,000
GrowthDecimal9.scaled_value = 实际成长值 × 1,000,000,000
```

两种类型的逻辑类型都是 `int64`；权威计算禁止引入浮点数。速度单位是“成长单位/秒”。V1 的直观数值可以精确表示：

```text
成熟值 100       = GrowthDecimal9(100,000,000,000)
基础速度 1       = RateDecimal6(1,000,000)
肥料修正 +0.5    = RateDecimal6(500,000)
虫害修正 -0.3    = RateDecimal6(-300,000)
```

结算公式：

```text
effective_rate_scaled6 =
    base_rate_scaled6 + active_modifier_scaled6...

delta_growth_scaled9 =
    elapsed_ms × effective_rate_scaled6

new_growth_scaled9 =
    min(maturity_scaled9, old_growth_scaled9 + delta_growth_scaled9)
```

该比例关系让毫秒结算保持精确：`毫秒 × 1e6 比例速度` 直接得到 `1e9 比例成长值`。因此多次结算与一次长区间结算结果相同，不需要舍入或持久化余数。中间值必须先使用经过溢出检查的至少 128 位整数，再检查范围并写入 `int64`。成长值限制在 `[0, maturity_value]`；成长中地块的有效速度必须保持为正。`effective_now = max(server_now, last_settled_at_ms)`。Buff 区间为 `[start_at_ms, end_at_ms)`。

## 4. 内存 Player Actor 聚合

一个 Actor 恰好拥有一个 `player_id`，逻辑内容为：

```text
PlayerActorState {
  player_id
  logical_shard_id
  owner_zone_id
  owner_epoch
  player_seq
  checkpoint_revision
  coin_balance
  inventory
  plots
  current_chapter
  recent_results
  pending_outbox
  last_applied_config_version
  dirty_metadata
}
```

`owner_zone_id`、Dirty 时间、进行中的刷盘 token、Mailbox 和 Timer 只属于运行时。其余字段都表示在检查点或其信封中。

不变量：

1. `logical_shard_id = stable_hash64(player_id) % 4096`。
2. `owner_epoch` 是已提交所有权 epoch，关键写或刷盘前必须与数据库 fence 匹配。
3. `player_seq` 沿事务已提交的检查点链单调递增。每个成功业务变更或每个独立固化的成熟转换恰好增加一次。可控 epoch 变化加载最新检查点并延续序号。异常丢失后，新 epoch 从最新已提交值开始；该值可能低于旧 Owner 已响应但丢失的内存值，因此可能重用未提交的序号，`(owner_epoch, player_seq)` 用于区分两段历史。
4. `checkpoint_revision` 沿事务已提交的检查点链单调递增。检查点内容的每次修改都增加一次，包括不改变 `player_seq` 的终结幂等失败、保留窗口清理和 Outbox 对账。异常丢失后，未提交的内存 revision 消失，新 Owner 从最新已提交 revision 继续。
5. 一个改变业务状态的成功命令使两个序号各增加一次。终结 Actor 业务失败只增加 `checkpoint_revision`。
6. 一个命令对金币、仓库、地块、章节、幂等和新 Outbox 记录的所有影响，必须先在 Actor 内存中原子应用，之后才能对外暴露结果。
7. Actor 激活必须先完成检查点校验、Outbox 对账和离线成熟结算，才能服务第一个快照或写请求。

必须引入 `checkpoint_revision`，原因是已接受的幂等契约要求保存终结失败，同时明确不增加 `player_seq`。只用 `player_seq` 做 Dirty CAS，无法保存同一客户端状态版本下的两个不同检查点内容。这是跨契约澄清；`player_seq` 和 WebSocket 排序语义不变。

## 5. 序列化 `PlayerCheckpointV1`

blob 的逻辑消息名是 `PlayerCheckpointV1`，精确字段如下：

| 字段 | 类型 | 规则 |
|---|---|---|
| `schema_version` | `uint32` | 必填；V1 为 `1` |
| `player_id` | `uint64` | 必填；等于关系型键 |
| `logical_shard_id` | `uint32` | 必填；`0..4095` 且由哈希推导 |
| `owner_epoch` | `uint64` | 必填；等于信封 epoch |
| `player_seq` | `uint64` | 必填；等于信封 sequence |
| `checkpoint_revision` | `uint64` | 必填；等于信封 revision |
| `coin_balance` | `int64` | 必填且非负 |
| `inventory` | repeated `InventoryStack` | 非零唯一堆栈，有序 |
| `plots` | repeated `PlotStateRecord` | 每个已拥有地块，有序 |
| `current_chapter` | `ChapterStateRecord` | 恰好一个当前/活跃章节记录 |
| `recent_results` | repeated `IdempotencyResultRecord` | 按第 7 节清理 |
| `pending_outbox` | repeated `PendingOutboxRecord` | 由本聚合创建且尚不确定已投递 |
| `last_applied_config_version` | `uint64` | 产生最近检查点变更时固定的快照 |
| `created_at_ms` | `int64` | 玩家初始化时间；不可变 |
| `updated_at_ms` | `int64` | 产生本 revision 的变更时间 |

V1 blob 解码后禁止超过 4 MiB。超过限制属于不变量破坏，会阻止写入/回收；禁止静默截断。

### 5.1 仓库与金币

`InventoryStack` 包含 `item_id uint32` 和 `quantity uint32`。

- 最多存在 100 个堆栈。
- 每个数量为 `1..300`。
- 数量变为零时删除堆栈。
- Item ID 是稳定配置身份；已停用物品仍必须可解码，并按已接受业务规则使用。
- 同一命令的金币变化与仓库变化是原子的。

### 5.2 地块与持久化效果

`PlotStateRecord` 包含：

| 字段 | 类型 | 存在条件/规则 |
|---|---|---|
| `plot_id` | `uint32` | 必填、唯一 |
| `state` | enum | `EMPTY=1`、`GROWING=2`、`MATURE=3`、`NEED_CLEANUP=4`；零无效 |
| `crop_id` | `uint32` | 非 EMPTY |
| `crop_item_id` | `uint32` | 非 EMPTY；收获入仓的物品身份 |
| `crop_config_version` | `uint64` | 非 EMPTY；种植时冻结的配置 |
| `planted_at_ms` | `int64` | 非 EMPTY |
| `maturity_value` | `GrowthDecimal9` | 非 EMPTY，`> 0`，已冻结 |
| `base_growth_rate` | `RateDecimal6` | 非 EMPTY，`> 0`，已冻结 |
| `base_yield` | `uint32` | 非 EMPTY，`> 0`，已冻结 |
| `stolen_quantity` | `uint32` | 非 EMPTY；`<= base_yield` |
| `settled_growth_value` | `GrowthDecimal9` | GROWING/MATURE；在范围内 |
| `last_settled_at_ms` | `int64` | GROWING/MATURE |
| `estimated_mature_at_ms` | optional `int64` | GROWING 缓存；可重建 |
| `fertilizer_effect` | optional `TimedEffectRecord` | 仅 GROWING |
| `pest_effect` | optional `TimedEffectRecord` | 仅 GROWING |

`TimedEffectRecord` 包含 `effect_instance_id UUID`、`effect_kind`、`effect_item_or_pest_id uint32`、可选 `source_player_id uint64`、`config_version uint64`、`modifier RateDecimal6`、`start_at_ms int64` 和 `end_at_ms int64`。

状态不变量：

- `EMPTY` 不含作物、结算或效果字段。
- `GROWING` 含全部成长字段，每种效果最多一个。
- `MATURE` 的成长值封顶，不含活跃效果和预计成熟时间。
- `NEED_CLEANUP` 为显示/审计保留作物身份、配置和种植时间，但不含成长或效果字段。
- 过期效果在历史区间完成结算前必须保留；结算后删除。
- `estimated_mature_at_ms` 不具权威性，缺失或不一致时必须重算。

### 5.3 当前章节

`ChapterStateRecord` 包含：

| 字段 | 类型 | 规则 |
|---|---|---|
| `chapter_id` | `uint32` | 必填 |
| `chapter_config_version` | `uint64` | 激活/冻结本章时使用的配置 |
| `status` | enum | `IN_PROGRESS=1`、`CLAIMABLE=2`、`CLAIMED=3`；零无效 |
| `activated_at_ms` | `int64` | 必填 |
| `claimed_at_ms` | optional `int64` | 仅已领取时存在 |
| `tasks` | repeated `TaskStateRecord` | 唯一、有序 |
| `next_chapter_id` | optional `uint32` | 已配置时冻结的迁移目标 |

`TaskStateRecord` 包含 `task_id uint32`、`task_config_version uint64`、`metric enum`、`current_value uint32`、`target_value uint32` 和 `completed bool`。

- `target_value` 和 `metric` 在章节激活时冻结，防止后续配置发布重新解释已持久化进度。
- `current_value` 在 `target_value` 饱和；`completed` 等于 `current_value >= target_value`。
- `CLAIMABLE` 表示所有任务完成。
- 只有服务端成功动作推进进度。
- 领取操作原子记录奖励影响、标记本章已领取并激活下一章状态。检查点只保存最终当前章节；历史章节分析不在 V1 范围。

## 6. 配置快照引用

ConfigSvr 仍是权威。Zone 原子替换完整不可变 `ConfigSnapshot(version)`，一个命令固定一个快照。

持久化引用为：

- 顶层 `last_applied_config_version`；
- `crop_config_version` 以及冻结的成熟值、速度和产量；
- 每个效果的 `config_version` 以及冻结的修正和区间；
- `chapter_config_version` 以及每个任务的配置版本、指标和目标；
- 稳定的 item/crop/effect/chapter/task ID。

Shop `price_version` 是输入相等性 token，保存在幂等指纹和原始回执中，不属于当前玩家状态。持久化状态引用的配置项只允许停用，禁止物理删除。即使没有历史配置，恢复也必须依靠冻结字段成功；需要当前配置的当前动作若无法获得配置，返回 `CONFIG_UNAVAILABLE`。

## 7. 幂等结果记录

`IdempotencyResultRecord` 包含：

| 字段 | 类型 | 规则 |
|---|---|---|
| `caller_player_id` | `uint64` | V1 等于检查点玩家 |
| `request_id` | UUID | 与 caller 组成键 |
| `fingerprint_schema_version` | `uint32` | V1 为 `1` |
| `protocol_version` | `uint32` | 原始协议 |
| `action` | `uint32` | 原始稳定枚举号 |
| `target_player_id` | `uint64` | 原始目标 |
| `payload_fingerprint_sha256` | 32 bytes | 规范指纹 |
| `completed_at_ms` | `int64` | 服务端完成时间 |
| `success` | `bool` | 终结结果 |
| `result_owner_epoch` | `uint64` | 原响应版本 |
| `result_player_seq` | `uint64` | 原响应版本 |
| `response_payload_type` | `uint32` | 稳定 action/result 判别值 |
| `response_payload` | bytes | 确定性的紧凑 typed receipt/patch |
| `error_payload` | optional bytes | 终结失败的确定性 V1 `Error` |
| `outbox_ids` | repeated UUID | 唯一、有序 |

每个 `(caller_player_id, request_id)` 恰好一条记录。保留响应编码后必须满足 64 KiB WebSocket 限制。禁止保留完整快照。

每次插入前必须清理，激活时应当清理：

1. 删除 `completed_at_ms <= server_now - 24h` 的记录；
2. 插入/保留新的终结记录；
3. 若仍超过 100 条，按 `(completed_at_ms, request_id)` 删除最旧记录。

因此窗口最多 100 条，且不存在超过 24 小时的结果。清理修改检查点并增加 `checkpoint_revision`，但不增加 `player_seq`。按 ADR-0006，异常丢失可能让结果及关联业务变化一起回退。

## 8. 待处理 Outbox 记录与关系型 Relay 状态

检查点中的 `PendingOutboxRecord` 包含：

| 字段 | 类型 | 规则 |
|---|---|---|
| `event_id` | UUID | 全局唯一；重试稳定 |
| `event_type` | enum | V1 包含 `CREATE_REWARD_MAIL=1` |
| `event_contract_version` | `uint32` | 已接受事件载荷版本 |
| `aggregate_player_id` | `uint64` | 检查点玩家 |
| `caused_by_request_id` | UUID | 来源写请求 |
| `created_owner_epoch` | `uint64` | 创建时 epoch |
| `created_player_seq` | `uint64` | 创建后的业务版本 |
| `created_at_ms` | `int64` | 服务端时间 |
| `payload` | bytes | `event-contracts.md` 定义的确定性事件载荷 |
| `payload_sha256` | 32 bytes | 精确载荷字节的哈希 |

本文有意不定义奖励邮件载荷字段。

Relay 为每个事件使用一条逻辑 `player_outbox` 行：

| 列 | 类型 | 规则 |
|---|---|---|
| `event_id` | UUID | 主键 |
| `db_shard_id` | `uint32` | 物理位置 |
| `aggregate_player_id` | `uint64` | 与状态联合索引 |
| `logical_shard_id` | `uint32` | 由哈希推导 |
| `event_type` | `uint32` | 不可变 |
| `event_contract_version` | `uint32` | 不可变 |
| `caused_by_request_id` | UUID | 不可变 |
| `created_owner_epoch` | `uint64` | 不可变 |
| `created_player_seq` | `uint64` | 不可变 |
| `created_at_ms` | `int64` | 不可变 |
| `payload` | bytes | 不可变 |
| `payload_sha256` | 32 bytes | 不可变 |
| `relay_status` | enum | `PENDING=1`、`IN_FLIGHT=2`、`DELIVERED=3` |
| `attempt_count` | `uint32` | 从零开始 |
| `next_attempt_at_ms` | `int64` | Relay 调度时间 |
| `claim_owner` | optional string | Relay Worker 身份 |
| `claim_until_ms` | optional `int64` | 可过期 claim |
| `last_error_code` | optional string | 有界诊断 |
| `delivered_at_ms` | optional `int64` | 仅已投递时存在 |

创建原子性：

- 含新 pending 记录的检查点 blob 与对应 `PENDING` 行必须在同一数据库事务提交；
- 按 `event_id` 幂等插入；
- 仅当所有不可变字段和载荷哈希都匹配时，已有行才可接受；否则刷盘以数据损坏失败；
- 检查点 CAS 禁止重置已有行的 Relay 状态或尝试次数。

Relay 是至少一次投递。下游消费者必须按 `event_id` 去重。Worker 原子 claim 一个符合条件的 `PENDING` 行（或过期 `IN_FLIGHT` 行）、增加尝试次数、发布，然后标记 `DELIVERED`；失败时以有界指数退避和抖动把行退回 `PENDING`。

Actor 激活时和回收前，Outbox 对账读取检查点 `pending_outbox` ID 的状态。`DELIVERED` 记录从 Actor 内存删除；该操作增加 `checkpoint_revision` 并产生 Dirty。因此 blob 中过期的 pending 副本最多引起幂等状态查询，绝不会产生第二个逻辑事件。已投递行必须至少保留 24 小时，并保留到任何检查点都不再可能引用它；更长归档期限属于运维策略。

## 9. 关系型 Player 检查点信封

每个玩家一条逻辑 `player_checkpoints` 行：

| 列 | 类型 | 规则/索引用途 |
|---|---|---|
| `player_id` | `uint64` | 主键 |
| `db_shard_id` | `uint32` | 物理数据库路由 |
| `logical_shard_id` | `uint32` | 索引；由哈希推导 |
| `owner_epoch` | `uint64` | 最近接受刷盘的 epoch |
| `player_seq` | `uint64` | 客户端业务版本 |
| `checkpoint_revision` | `uint64` | CAS 持久化版本 |
| `checkpoint_schema_version` | `uint32` | V1 为 `1` |
| `checkpoint_blob` | bytes | 确定性 `PlayerCheckpointV1` |
| `checkpoint_sha256` | 32 bytes | 精确 blob 哈希 |
| `last_applied_config_version` | `uint64` | 诊断/迁移索引 |
| `created_at_ms` | `int64` | 不可变 |
| `updated_at_ms` | `int64` | 最近成功刷盘时间 |

信封值必须与 blob 值匹配。不匹配即数据损坏，必须停止 Actor 激活或刷盘；禁止静默选择其中一份。

V1 不为金币、仓库、地块、章节和近期结果建立独立可变关系表。可以异步构建运维投影，但它们不具权威性。

## 10. ShardMap 已提交数据

生产 Coordinator 的多数派已提交日志/状态存储是权威。单节点原型暴露同一逻辑 schema，但不声称共识能力。

`ShardMapSnapshot` 包含：

| 字段 | 类型 | 规则 |
|---|---|---|
| `shard_count` | `uint32` | V1 恰好 `4096` |
| `hash_algorithm_version` | `uint32` | V1 恰好 `1` |
| `map_version` | `uint64` | 每次已提交 Map 变化增加 |
| `committed_term` | `uint64` | 共识任期；原型也持久化 |
| `committed_index` | `uint64` | 单调递增提交索引 |
| `entries` | 4096 个 `ShardRouteEntry` | 每个 `shard_id` 一条，有序 |

`ShardRouteEntry` 包含：

| 字段 | 类型 | 规则 |
|---|---|---|
| `shard_id` | `uint32` | `0..4095` |
| `owner_zone_id` | optional string | 除 `UNASSIGNED` 外必填 |
| `owner_epoch` | `uint64` | 每次所有权授予增加；永不复用 |
| `route_version` | `uint64` | 本 Entry 每次已提交变化增加 |
| `state` | enum | `UNASSIGNED=1`、`PREPARING=2`、`ACTIVE=3` |
| `lease_term` | `uint64` | 授予租约的 Leader 任期 |
| `lease_id` | UUID | 唯一授予身份 |
| `lease_expires_at_ms` | `int64` | Coordinator 服务端时间 |
| `previous_owner_zone_id` | optional string | 迁移/故障交接 |
| `transition_id` | optional UUID | `PREPARING` 时必填 |
| `updated_at_ms` | `int64` | 提交时间 |

只有已提交、租约当前有效的 `ACTIVE` Entry 可路由写请求。`PREPARING` 永远不可路由。一次转换先提交 `PREPARING(epoch=N+1)`，等待旧 Owner 停止或租约过期，推进数据库 fence，加载检查点并达到 Ready，然后在不改变该已准备 epoch 的情况下提交 `ACTIVE`。Coordinator 重启必须按同一 `transition_id` 幂等继续或放弃；被放弃转换的 epoch 不复用。

## 11. 数据库 shard fence

每个玩家数据库 shard 为放置在其中的每个逻辑 shard 保存一条逻辑 `shard_fences` 行：

| 列 | 类型 | 规则 |
|---|---|---|
| `logical_shard_id` | `uint32` | DB shard 内主键 |
| `owner_zone_id` | string | 当前数据库写 Owner |
| `owner_epoch` | `uint64` | 当前 fenced epoch |
| `route_version` | `uint64` | 授权的已提交路由 |
| `transition_id` | UUID | 授权交接 |
| `fenced_at_ms` | `int64` | CAS 的数据库时间 |

Fence 推进是数据库 CAS：

```text
只接受已提交 PREPARING Entry
且请求 owner_epoch > 已存 owner_epoch
且 transition_id/owner/route 与该 Entry 匹配
→ 原子替换 fence
```

精确重放已经应用的同一转换幂等成功。相同/更低 epoch 但元数据不同则拒绝。任一所需数据库 shard fence 无法推进时，新 Owner 禁止进入 `ACTIVE`。

每个检查点刷盘事务必须读取/锁定 fence 行，或使用等价条件，并要求 `(logical_shard_id, owner_zone_id, owner_epoch)` 精确匹配。Zone 的租约检查和数据库 fence 检查解决不同故障，两者都必须存在。

## 12. Dirty Queue 与批量写 DTO

Dirty Queue 是 Zone 内存，不是持久化恢复数据。每个活跃玩家最多一条合并 Entry：

`DirtyQueueEntry`：

| 字段 | 类型 | 规则 |
|---|---|---|
| `player_id` | `uint64` | Queue 键 |
| `db_shard_id` | `uint32` | 批量分组键 |
| `logical_shard_id` | `uint32` | 由哈希推导 |
| `owner_zone_id` | string | 当前 Owner |
| `owner_epoch` | `uint64` | 捕获的所有权 |
| `latest_checkpoint_revision` | `uint64` | 最近已知 Dirty revision |
| `first_dirty_at_ms` | `int64` | 合并/重试时保留 |
| `last_dirty_at_ms` | `int64` | 最近变更 |
| `next_attempt_at_ms` | `int64` | 退避调度 |
| `attempt_count` | `uint32` | 连续失败数 |
| `in_flight_revision` | optional `uint64` | 每玩家最多一个刷盘副本 |

标记 Dirty 更新 `latest_checkpoint_revision` 和 `last_dirty_at_ms`，但保留 `first_dirty_at_ms`。禁止为 Actor 重复创建 Timer。

Flusher 在 Actor 串行保护下复制不可变 DTO：

`DirtyBatchWriteRequest` 包含 `batch_id UUID`、`db_shard_id`、`owner_zone_id`、`created_at_ms` 和 repeated `PlayerCheckpointWrite` Entry。

`PlayerCheckpointWrite` 包含全部关系型信封值、确定性 `checkpoint_blob`、`checkpoint_sha256`，以及该复制检查点所需的 repeated Outbox 行。Entry 唯一并按 `player_id` 排序。每个玩家使用一个数据库事务，因此某玩家失败不会回滚同批其他玩家。

`PlayerCheckpointWriteResult` 返回 `player_id`、`copied_checkpoint_revision` 和以下一种状态：

- `APPLIED`；
- `ALREADY_APPLIED`（相同 revision 和 hash）；
- `STALE_COPY`（数据库已有更高 revision）；
- `FENCED`（Owner/epoch 不匹配）；
- `RETRYABLE_FAILURE`；
- `CORRUPT_CONFLICT`（同 revision 不同 hash，或 Outbox 不可变字段冲突）。

## 13. 刷盘 CAS、重试与过期写拒绝

对每个复制的玩家 DTO，Writer 必须执行：

1. 开始数据库事务；
2. 校验当前 shard fence 与 DTO 的逻辑 shard、Owner Zone 和 epoch 精确匹配；
3. 锁定/读取 Player 信封（若存在）；
4. 校验 blob/信封身份、版本和 SHA-256；
5. 若已存 `checkpoint_revision > incoming` 则拒绝；
6. revision 相等时，仅当 epoch、player sequence 和 hash 全部匹配才返回 `ALREADY_APPLIED`，否则返回 `CORRUPT_CONFLICT`；
7. 要求 incoming `checkpoint_revision > stored`、incoming `player_seq >= stored.player_seq`、incoming `owner_epoch >= stored.owner_epoch`；
8. 插入/校验所有新不可变 Outbox 行，且不修改已有 Relay 状态；
9. 替换信封和 blob；
10. 提交。

首个检查点只在当前精确 fence 下接受。相对数据库最新已存检查点，更高 epoch 不允许更低的 `player_seq` 或 `checkpoint_revision`；新 Owner 加载该检查点并延续两个已提交链序号。该比较不包含随异常旧 Owner 一起丢失的未刷内存值。

提交后，Flusher 向 Actor 报告复制 revision：

- 若 Actor `checkpoint_revision == copied`，清除 Dirty 和 in-flight token；
- 若 Actor revision 更高，只清除 in-flight token，保留 Dirty；
- `STALE_COPY` 触发重新复制，永远不允许覆盖；
- `FENCED` 立即停止该 Actor 写入，报告 `NOT_OWNER`，且不在旧 epoch 下重试；
- 可重试失败保留 Dirty，并使用有界指数退避和抖动；
- 数据损坏停止 Actor 写入并告警，禁止通过自动选择其中一份修复。

默认刷盘周期为一秒。`oldest_dirty_age > 3s` 时告警并开始准入限制；接近 5 秒时 Zone 停止新的关键写。读请求可以继续。这些是健康目标，不是持久性保证。

## 14. 生命周期与恢复保证

### 14.1 正常停机、回收与可控迁移

Zone 必须：

```text
停止接收新写
→ 排空 Actor Mailbox
→ 结算到期转换
→ 对账 Outbox 状态
→ 刷盘至 Actor 最终 checkpoint_revision
→ 确认没有 Dirty 或进行中写
→ 释放/推进所有权
```

可正常回收的 Actor 还必须没有连接、订阅者、Mailbox 工作或迁移，并满足已接受的三分钟空闲规则。刷盘失败会取消回收，并使 Actor 保持驻留以重试。若仍有 Dirty 状态，正常停机必须报告失败，禁止声称成功。

### 14.2 Zone 异常丢失

恢复只在新 fenced epoch 下加载 MySQL 最近已提交检查点。不重放 Journal。该检查点之后的所有状态——包括金币、仓库、地块、章节进度、幂等结果和尚未持久化的 Outbox 事件——可能一起回退。已经提交的 Outbox 行保留 Relay 进度。更高 `owner_epoch` 强制客户端使用完整快照。

回退边界精确等于事务提交的最高 `checkpoint_revision`，而不是命令响应发送时间。V3 不为未刷盘的已确认写提供持久 exactly-once。

## 15. 兼容与迁移

- `schema_version` 选择 blob 解码器。V1 Writer 写版本 `1`。
- Reader 必须拒绝未知未来 schema 版本，除非存在已注册迁移器。
- 兼容扩展使用具有安全默认值的新 optional Protobuf 字段；旧字段号/枚举号永久保留。
- 无法用 optional 字段表达的语义变化，需要新检查点 schema 版本和显式确定性迁移器。
- 迁移在获得 fence 后、Actor 接收请求前运行；在有意义时保留未知字段；增加 `checkpoint_revision`；原子更新信封/blob；仅当客户端可见业务状态变化时才增加 `player_seq`。
- 从相同源 blob 开始的迁移必须可重试且幂等。
- 滚动发布必须遵循“先扩展读取，再写新版本，最后收缩旧版本”。Writer 禁止产生任何可能参与恢复的 Reader 无法解码的版本。
- shard 哈希算法或 shard 数变化需要另行接受的迁移契约；V1 固定算法版本 1 和 4096 shards。
- 事件载荷兼容由 `event_contract_version` 和 `event-contracts.md` 管理；Relay 代码把载荷字节视为不可变。

## 16. 验收与故障测试

实现证据必须证明：

1. 确定性检查点编解码保留全部字段和兼容未知字段；
2. 信封/blob 的 ID、epoch、sequence、revision、配置版本和 hash 不匹配会被拒绝；
3. 仓库执行 100 种、每种 300、无零堆栈，以及金币检查算术；
4. 不同时间分段的结算结果具有相同地块状态/存在字段不变量和定点成长结果；
5. 历史配置停用后，依靠冻结的作物、效果和任务字段仍能恢复；
6. 一个成功命令即使改变聚合多个部分，也只让 `player_seq` 和 `checkpoint_revision` 各增加一次；
7. 终结业务失败可持久化和重放，同时 `player_seq` 不变；
8. 保留窗口确定性同时执行最近 100 条和 24 小时限制；
9. 检查点和新创建 Outbox 行同时提交或回滚；
10. Relay 重试和过期 claim 可以重复尝试投递，但消费者按 `event_id` 去重只创建一封逻辑邮件；
11. 重放领奖不会产生第二个 Outbox ID 或第二份仓库奖励；
12. 检查点仍含过期 pending 副本时，已投递 Outbox 对账仍然安全；
13. 一秒合并写入最近复制 revision，Actor 在刷盘期间推进时仍保留 Dirty；
14. 相同 revision/相同 hash 幂等，相同 revision/不同 hash 以数据损坏失败；
15. 旧 epoch、错误 Zone 和过期 fence 写即使 player sequence 更高也被拒绝；
16. 已准备路由不可写，成功推进 fence 且 Owner Ready 前不可能进入 `ACTIVE`；
17. Coordinator 重启后继续或放弃 `PREPARING` 转换且不复用 epoch；
18. 正常停机、回收和可控迁移在释放所有权前刷完最终 revision；
19. 强制终止 Zone 后恰好恢复最近已提交检查点，并展示已接受回退；
20. 数据库延迟时保留 Dirty、增加 age 指标、应用限流、接近阈值时停止关键写，且永不增加 Journal/WAL 后备路径；
21. 检查点迁移确定、幂等，并在滚动 Reader/Writer 发布中安全；
22. 客户端快照不含幂等、Outbox、内部小数、Dirty 或控制面字段。

## 17. 跨契约一致性

设计对账发现：要在不增加 `player_seq` 的情况下持久化确定失败，需要独立的持久化 CAS 版本。V3 架构和幂等契约现在统一使用 `checkpoint_revision` 完成该职责，并保持 `(owner_epoch, player_seq)` 作为客户端状态版本的语义不变。
