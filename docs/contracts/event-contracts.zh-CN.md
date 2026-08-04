---
status: accepted-translation
version: 1
date: 2026-07-30
owners:
  - project-owner
related:
  - event-contracts.md
  - data-model.zh-CN.md
  - websocket-protocol.zh-CN.md
  - idempotency-and-errors.zh-CN.md
  - ../architecture/stateful-zone-v3-architecture.md
  - ../architecture/single-player-vertical-loop-business-architecture.md
---

# 奖励邮件事件契约 V1

## 1. 范围与权威性

本文是最小第一阶段 Outbox 事件契约的完整中文阅读镜像。规范性英文源是 [`event-contracts.md`](event-contracts.md)；如果中英文存在差异，以英文源为准。规范词 `MUST`、`MUST NOT`、`SHOULD` 和 `MAY` 分别表示必须、禁止、应该和可以。

V1 只定义一个事件：

```text
CREATE_REWARD_MAIL = 1
```

只有在成功领取章节奖励、且一个或多个奖励物品数量无法全部放入仓库时，Player Actor 才创建该事件。金币以及所有能放入仓库的物品数量都留在 Player Actor 事务中。只有溢出的物品数量成为邮件附件。事件必须至少包含一个附件，且禁止包含金币。

本契约定义逻辑 Protobuf 事件信封和载荷、不可变投递身份、顺序、Relay 与消费者行为、限制、隐私、可观测性和验收测试。它不定义生成的 `.proto`、代码、SQL、迁移、消息代理部署、邮件读取/领取/过期 API、邮件 UI、通知 UX 或完整邮件产品。

V3 仍然是权威。事件总线是异步副作用路径，不是 Journal，也不是玩家恢复源。任何消费者结果都不得修改或重建 Player Actor 状态。

## 2. 与数据模型的关系

`data-model.md` 负责定义 `PendingOutboxRecord`、不可变的关系型 `player_outbox` 字段以及 Relay 修改的列。本契约不新增或重命名 Outbox 列。

Actor 只在 `PendingOutboxRecord.payload` 中保存确定性 `CreateRewardMailV1` 字节。刷盘时，对应的不可变关系型记录接收相同的载荷字节和哈希。Relay 根据该记录构造 `EventEnvelopeV1`，且不改变任何不可变值。

映射关系必须严格如下：

| 信封字段 | Outbox 来源 |
|---|---|
| `event_id` | `event_id` |
| `event_type` | `event_type` |
| `event_contract_version` | `event_contract_version` |
| `aggregate_player_id` | `aggregate_player_id` |
| `caused_by_request_id` | `caused_by_request_id` |
| `created_owner_epoch` | `created_owner_epoch` |
| `created_player_seq` | `created_player_seq` |
| `created_at_ms` | `created_at_ms` |
| `payload` | `payload` |
| `payload_sha256` | `payload_sha256` |

`relay_status`、`attempt_count`、`next_attempt_at_ms`、`claim_owner`、`claim_until_ms`、`last_error_code` 和 `delivered_at_ms` 是 Relay 状态，禁止出现在发布的事件中。

## 3. 标量与兼容性规则

| 含义 | 逻辑 Protobuf 类型 | 规则 |
|---|---|---|
| UUID | `bytes` | RFC 4122 字节顺序的恰好 16 个非零字节 |
| 玩家 ID / Owner epoch / 玩家序列 / 配置版本 | `uint64` | 要求存在时非零；不得通过有符号转换检查 |
| 事件契约/Schema 版本 | `uint32` | 必须是受支持的非零版本；V1 为 `1` |
| 领域/配置 ID | `uint32` | 零值非法 |
| 数量 | `uint32` | 大于零；加法必须检查溢出 |
| 时间 | `int64` | 服务端时间产生的 UTC Unix 毫秒；大于零 |
| 哈希 | `bytes` | 恰好 32 个原始 SHA-256 字节 |
| 本地化键 | `string` | 小写 ASCII、点分隔键，1–96 字节 |

已发布的字段 tag 和枚举数字永远不得复用。删除后的数字仍保持预留。兼容的 V1 扩展可以使用新的可选字段，但字段缺失必须具有安全语义。改变必需校验、附件含义、收件人权威或去重方式的语义变化，必须使用 `event_contract_version = 2` 和新的载荷消息。

V1 写入方只能输出本文定义的字段。消费者必须忽略兼容的未知字段；如果继续中继或保存原始事件字节，则必须保留这些字段。消费者必须拒绝不支持的 `event_contract_version`，禁止猜测解码器。

## 4. 稳定枚举

### 4.1 `EventType`

| 值 | 名称 | 规则 |
|---:|---|---|
| 0 | `EVENT_TYPE_UNSPECIFIED` | 非法 |
| 1 | `CREATE_REWARD_MAIL` | V1 奖励溢出事件 |

值 2–99 为未来接受的事件类型预留。

### 4.2 `ConsumeResultCode`

| 值 | 名称 | 含义 |
|---:|---|---|
| 0 | `CONSUME_RESULT_UNSPECIFIED` | 非法 |
| 1 | `APPLIED` | 邮件及附件已提交 |
| 2 | `ALREADY_APPLIED` | 相同 `event_id` 和不可变指纹已经提交 |
| 3 | `RETRYABLE_FAILURE` | 消费者事务未提交；允许重试 |
| 4 | `CORRUPT_CONFLICT` | 相同 `event_id` 已存在但不可变内容不同 |
| 5 | `INVALID_EVENT` | 可确定判定为格式错误、不支持或违反策略的事件 |

`APPLIED` 和 `ALREADY_APPLIED` 都是消费成功。`CORRUPT_CONFLICT` 和 `INVALID_EVENT` 是毒消息结果，不是业务应用成功。

## 5. 逻辑 `EventEnvelopeV1`

发布的 Protobuf 消息逻辑结构如下：

| Tag | 字段 | 类型 | 规则 |
|---:|---|---|---|
| 1 | `event_contract_version` | `uint32` | 必需；必须等于 `1` |
| 2 | `event_id` | `bytes` | 必需 UUID；全局不可变投递身份 |
| 3 | `event_type` | `EventType` | 必需；必须等于 `CREATE_REWARD_MAIL` |
| 4 | `aggregate_player_id` | `uint64` | 必需；Player Actor 和收件人身份 |
| 5 | `caused_by_request_id` | `bytes` | 必需；`CLAIM_CHAPTER_REWARD` 的 UUID |
| 6 | `created_owner_epoch` | `uint64` | 必需；创建时的 Actor epoch |
| 7 | `created_player_seq` | `uint64` | 必需；领奖后的业务版本 |
| 8 | `created_at_ms` | `int64` | 必需；服务端创建时间 |
| 9 | `payload` | `bytes` | 确定性 `CreateRewardMailV1` 字节 |
| 10 | `payload_sha256` | `bytes` | tag 9 中精确字节的 SHA-256 |

Tag 11–19 为兼容的公共元数据预留。Tag 20–99 为未来接受的信封扩展预留。

信封元组必须与不可变 Outbox 记录一致。Relay 必须在发布前验证 `SHA-256(payload) == payload_sha256`，并按受支持契约校验载荷。不匹配属于数据损坏，绝不是可重试的消息代理错误。

## 6. 逻辑 `CreateRewardMailV1`

载荷具有以下精确字段：

| Tag | 字段 | 类型 | 规则 |
|---:|---|---|---|
| 1 | `recipient_player_id` | `uint64` | 必需；等于信封 `aggregate_player_id` |
| 2 | `attachments` | repeated `RewardMailAttachmentV1` | 必需；1–100 项，按 `item_id` 唯一且升序排列 |
| 3 | `subject_text_key` | `string` | 必需；严格等于 `mail.chapter_reward.subject` |
| 4 | `body_text_key` | `string` | 必需；严格等于 `mail.chapter_reward.body` |
| 5 | `source` | `RewardMailSourceV1` | 必需 |

Tag 6–19 为兼容的载荷扩展预留。载荷不包含展示文本、locale、金币、发件人账号、Session、网络地址或内部服务地址。

### 6.1 `RewardMailAttachmentV1`

| Tag | 字段 | 类型 | 规则 |
|---:|---|---|---|
| 1 | `item_id` | `uint32` | 必需；稳定物品身份 |
| 2 | `quantity` | `uint32` | 必需；大于零的溢出数量 |

Tag 3–9 预留。一个物品最多出现一次。如果奖励配置含有重复物品项，Actor 必须先使用带溢出检查的算术合并，再执行仓库分配和事件创建。

仓库分配按 `item_id` 升序确定性执行：

1. 按 `item_id` 合并配置的奖励物品数量；
2. 对每个物品，在已接受的仓库种类数和堆叠上限内尽可能放入；
3. 只将剩余的正数量写入 `attachments`；
4. 按 `item_id` 升序排列附件。

附件数量不得重复已经记入仓库的数量。奖励没有溢出时不创建事件。

### 6.2 `RewardMailSourceV1`

| Tag | 字段 | 类型 | 规则 |
|---:|---|---|---|
| 1 | `chapter_id` | `uint32` | 必需；已领取章节 |
| 2 | `chapter_config_version` | `uint64` | 必需；领奖使用的配置快照/冻结章节版本 |
| 3 | `request_id` | `bytes` | 必需 UUID；等于信封 `caused_by_request_id` |

Tag 4–9 预留。章节和请求元数据只用于审计与本地化上下文，不提供重复发放奖励的权威。

## 7. 创建与玩家命令含义

在一次 Actor Mailbox 执行中，领取奖励必须：

```text
校验领奖与幂等
→ 记入金币
→ 将可容纳的物品数量分配到仓库
→ 为全部正数溢出物品创建一个事件
→ 将章节标记为已领取并激活下一章
→ player_seq 恰好增加一次
→ 保存终态幂等结果和 event_id
→ 将检查点标记为 Dirty
```

相同玩家请求成功后的重放返回原结果和事件 ID，不创建额外事件。对已经领取的章节发起新请求时，按 `idempotency-and-errors.md` 失败。

客户端可见的 `items_pending_mail` 回执表示一个待处理 Outbox 事件已经与 Actor 的领奖状态原子记录。它绝不表示 Mail Service 已创建、展示或投递邮件。

根据已接受的 V3 异步 Dirty 写回，命令可能在检查点及关系型 Outbox 记录提交到 MySQL 前得到确认。因此，已经确认但尚未刷盘的领奖及其待处理事件，可能在 Zone 异常退出后一起回退。只有当检查点和 `PENDING` Outbox 记录共同提交后，事件才获得数据库持久性。任何实现或用户文本都不得把刷盘前确认描述为能跨 Zone 异常退出持久存在。

## 8. 确定性序列化与不可变指纹

Actor 必须校验并规范化载荷、排列附件，然后使用所选 Protobuf 运行时的确定性模式编码 `CreateRewardMailV1`。V1 载荷写入方按规范 tag 顺序输出字段，不输出未知字段，使用最短 varint，且不省略任何必需字段。消息中没有 map。

`payload_sha256` 为：

```text
SHA-256(精确的确定性 CreateRewardMailV1 字节)
```

Relay 必须发布精确的已存储载荷字节；禁止为发布而解码后重新编码。

消费者去重使用的不可变事件指纹为：

```text
SHA-256(
  精确的确定性 EventEnvelopeV1 字节
)
```

计算前，消费者验证信封具备最低合法性且载荷哈希一致。消费者必须将精确的不可变信封字节或该指纹与 `event_id` 一起存储。指纹不得使用消息代理 header、投递次数、partition、offset、Relay 时间戳或可变 Outbox 状态。

## 9. 大小限制

- 编码后的 `CreateRewardMailV1` 载荷：最多 48 KiB。
- 编码后的 `EventEnvelopeV1`：最多 64 KiB。
- 附件：1–100 个唯一项。
- 每个本地化键：最多 96 个 UTF-8 字节，并遵守第 3 节字符限制。
- Relay 和消费者必须在无界分配前执行限制。

超过限制的事件属于不变量违规。禁止截断、拆成多封逻辑邮件或静默丢弃附件。

## 10. 分区与顺序

消息代理的 partition/order key 必须是 `aggregate_player_id` 的恰好 8 字节无符号大端编码。它不是 UTF-8 十进制文本，也不是秘密。

因此，在固定 topic 分区配置下，同一个 Player Actor 的全部事件使用相同 key。`created_player_seq`、再按 `event_id` 字节提供稳定的诊断顺序。V1 消费者不得依赖全局顺序、连续序列或每个玩家序列都存在。改变分区数量或 key 算法可能扰乱单玩家传输顺序，因此需要接受的迁移方案。

去重依赖 `event_id`，不依赖顺序、offset、epoch、玩家序列或请求 ID。

## 11. Relay 语义与确认边界

Relay 是至少一次投递：

1. 使用 `data-model.md` 中的可变列，原子认领符合条件的 `PENDING` 记录或认领已过期的 `IN_FLIGHT` 记录；
2. 增加 `attempt_count`；
3. 校验不可变字段、载荷哈希、载荷契约和大小；
4. 使用玩家 partition key 发布精确信封；
5. 等待消息代理的持久发布确认；
6. 只有此后才能设置 `relay_status = DELIVERED`、清除认领并设置 `delivered_at_ms`。

消息代理确认是 Relay 的确认边界。它不表示 Mail Service 已消费事件，也不表示玩家能够读取或领取邮件。Relay 在代理确认后、设置 `DELIVERED` 前崩溃时，可能重新发布相同 `event_id`。

可重试的代理/网络故障会把记录恢复为 `PENDING`、清除认领、设置有界的 `last_error_code`，并通过指数退避和全抖动调度 `next_attempt_at_ms`。默认基础值 1 秒、倍数 2、上限 5 分钟。对于合法的待处理奖励事件，不设有限重试次数；达到 20 次或从创建起经过 1 小时（以先到者为准）后必须告警，同时继续按上限退避重试。

不可变记录不匹配、载荷哈希不匹配、不支持的版本、非法载荷或大小违规属于 Relay 毒消息。Relay 禁止发布或标记为 `DELIVERED`。由于 `data-model.md` 没有定义终态 Outbox 状态，V1 在 `last_error_code` 中记录有界毒消息代码，把 `next_attempt_at_ms` 设置为实现支持的最大时间以阻止自动重新认领，发送一份脱敏死信诊断副本，并通知操作人员。重新入队必须经过显式修复和审计；禁止原地编辑不可变事件数据。

## 12. Mail Service 消费与原子性

Mail Service 必须按 `event_id` 去重。它必须在一个本地数据库事务中：

1. 锁定或插入 `event_id` 的消费者去重记录；
2. 如果不存在，则校验信封和载荷，创建恰好一个邮件头，创建全部附件，并保存不可变事件指纹；
3. 原子提交邮件头、每一个附件和去重记录；
4. 仅在提交后确认消息代理消息。

不得让部分附件集变为可见。事务失败时不创建邮件、附件或去重成功记录。

如果 `event_id` 已存在：

- 不可变指纹相等时返回 `ALREADY_APPLIED`；这是成功，并确认代理消息；
- 不可变指纹不同时返回 `CORRUPT_CONFLICT`；不改变邮件数据，发出安全/损坏告警及脱敏死信记录，并确认毒消息以停止无限重试。

首次投递时：

- 事务已提交则返回 `APPLIED` 并确认；
- 瞬时依赖/事务失败则返回 `RETRYABLE_FAILURE`，且不确认；
- 格式错误、不支持、哈希非法、收件人不匹配或违反策略的内容返回 `INVALID_EVENT`，发出脱敏死信记录，不创建邮件，并确认消息以停止无限重试。

死信记录必须保留 `event_id`、事件类型/版本、载荷哈希、原因代码、可获得时的代理 topic/partition/offset，以及首次/最后观察时间。禁止包含原始载荷、玩家可见文本、凭据、token、cookie、账号名或内部堆栈。

创建的邮件只能发给 `recipient_player_id`。消费者禁止从 header 或本地化元数据推导其他收件人。未来的读取、领取、过期、删除、通知和附件兑换语义不在本契约范围内。

## 13. 隐私、日志与可观测性

事件只包含投递所需的最少玩家数据：收件人 ID、物品 ID/数量、章节/配置元数据、请求 ID、事件 ID、版本和服务端时间。禁止包含账号名、密码、Session/cookie/ticket/CSRF 值、IP 地址、设备标识、自由文本、访问 token、内部主机或堆栈。

日志和 trace 可以包含事件 ID、事件类型/版本、请求 ID、owner epoch、玩家序列、载荷哈希、尝试次数、结果代码、耗时，以及经过加密哈希/缩减的玩家标识。生产日志应该避免原始玩家 ID。普通日志或指标标签禁止记录原始载荷和附件列表。

必需的低基数指标包括：

- Outbox 待处理和认领中数量；
- 最老待处理时间；
- Relay 发布尝试、确认、失败和延迟；
- 消费者应用、重复、可重试、损坏和非法计数；
- 消费者事务延迟；
- 按有界原因代码统计的死信数；
- 从 `created_at_ms` 到消费者提交的端到端时间。

告警覆盖最老待处理时间、持续 Relay 失败、尝试次数阈值、毒消息/死信事件、载荷哈希不匹配、不可变内容不匹配和消费者重试积压。指标标签禁止包含事件 ID、请求 ID、玩家 ID、物品 ID、载荷哈希或错误文本。

## 14. 验收与失败测试

实现证据必须证明：

1. 确定性载荷和信封编码在受支持的 Go 与消费者运行时中产生相同字节及 SHA-256；
2. 所有稳定 tag/枚举可以往返，不支持的版本会关闭式失败；
3. UUID、标量、本地化键、必需字段、排序、唯一性和大小校验在分配或发布前执行；
4. 金币和可容纳物品保留在 Actor 状态中，只有精确的溢出物品数量以有序附件出现；
5. 没有溢出的奖励不创建事件，多个溢出物品只创建一个事件；
6. 配置中的重复物品项通过带检查算术合并，永不创建重复附件；
7. 奖励状态、幂等结果、待处理检查点记录和关系型 Outbox 记录，在其文档规定的 Actor/刷盘边界共同提交或回滚；
8. 相同玩家请求重放不会额外发放金币/物品、不会创建第二个事件 ID，并返回原回执；
9. 成功领奖回执只表达邮件待处理，绝不声称 Mail Service 已创建或投递；
10. 刷盘前强杀 Zone 会展示已接受的领奖状态及待处理事件回退；已提交 Outbox 记录存续并保留 Relay 进度；
11. Relay 使用精确的 8 字节玩家 key 和精确的已存储载荷字节；
12. 发布失败使用相同事件 ID 和不可变内容重试；代理确认后崩溃可能造成重复投递；
13. Relay 只在代理持久确认后标记 `DELIVERED`，不得仅在本地发送尝试后标记；
14. 载荷/哈希/不可变数据损坏不会被发布或静默修复，并进入脱敏 Relay 死信路径；
15. 首次消费者投递在确认前原子创建一个邮件、全部附件和一条去重记录；
16. 消费者事务失败不暴露部分邮件，并触发重试；
17. 相同事件 ID 和指纹返回成功的 `ALREADY_APPLIED`，不创建其他邮件；
18. 相同事件 ID 但不可变指纹不同会返回 `CORRUPT_CONFLICT`，不改变邮件数据，告警、死信并终止毒消息重投；
19. 格式错误、不支持、超大、收件人不匹配和哈希非法的事件不创建邮件，并遵循 `INVALID_EVENT` 毒消息处理；
20. 固定分区配置下保持单玩家传输顺序，但消费者正确性不依赖全局或连续顺序；
21. 日志、trace、指标、告警和死信记录不包含禁止的秘密、原始载荷、自由文本或高基数指标标签；
22. 任何测试都不把事件总线用作 Player Actor Journal 或恢复源。

## 15. 跨契约一致性

`websocket-protocol.md` 现在采用与本契约相同的 V3 边界：`items_pending_mail` 表示待处理事件已经在 Actor 状态中原子记录，不表示它已获得数据库持久性，也不表示 Mail Service 已创建邮件。

只有异步检查点/Outbox 事务提交后，事件才获得数据库持久性。如果要求领奖成功必须等待该提交，就会形成新的同步持久性例外，需要另行接受决策。
