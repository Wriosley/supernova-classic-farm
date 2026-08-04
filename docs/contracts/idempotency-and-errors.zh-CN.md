---
status: accepted-translation
version: 1
date: 2026-07-30
source: idempotency-and-errors.md
owners:
  - project-owner
related:
  - websocket-protocol.zh-CN.md
  - ../architecture/single-player-vertical-loop-business-architecture.md
---

# 幂等与错误契约 V1（中文版）

> 本文件是 `idempotency-and-errors.md` 的中文阅读副本。实现时两份文件应保持语义一致；若翻译尚未同步，以英文原始契约为准。

## 1. 目的

本契约保证重试安全，并让 H5 能稳定处理失败。它区分：

- 确定没有执行的请求；
- GateSvr 无法判断执行结果的请求；
- 已经确定的业务结果；
- 已经不能继续承载可信命令的连接。

## 2. 错误结构

失败 RESPONSE 包含一个 `Error`：

| 字段 | 类型 | 含义 |
|---|---|---|
| `code` | `ErrorCode` | 稳定的机器可读枚举 |
| `params` | repeated 键值字符串 | 客户端本地化参数，禁止作为业务命令 |
| `retryable` | `bool` | 是否允许使用同一请求自动重试 |
| `retry_after_ms` | optional `uint32` | 可以重试时的最短等待时间 |
| `latest_shop_entry` | optional `ShopEntryView` | 价格变化时返回最新报价 |
| `current_plot` | optional `PlotView` | 可以取得时返回冲突地块的当前状态 |
| `debug_message` | optional `string` | 只用于开发和日志，H5 禁止直接展示给玩家 |

玩家看到的文字必须由 H5 根据 `code + params` 选择。服务端禁止把可能变化的自然语言当成协议语义。

首次执行且已经到达 Player Actor 的错误携带 Actor 当前 `state_version`。重放的终结 Actor 错误按第 9 节返回原始保存的 `state_version`，不能替换为 Actor 后续的当前版本。没有到达 Actor 的信封、认证和路由错误不携带玩家版本。

## 3. 稳定错误码

### 3.1 协议和连接

| 数值 | 错误码 | 客户端行为 |
|---:|---|---|
| 0 | `ERROR_UNSPECIFIED` | 视为客户端缺陷并记录日志 |
| 100 | `INVALID_ARGUMENT` | 修正请求，禁止自动重试 |
| 101 | `UNKNOWN_ACTION` | 客户端和服务端契约不一致，保持连接 |
| 102 | `UNSUPPORTED_PROTOCOL_VERSION` | 升级或刷新客户端，关闭连接 |
| 103 | `UNAUTHENTICATED` | 获取新 WS ticket，关闭连接 |
| 104 | `FORBIDDEN` | 禁止自动重试 |
| 105 | `REQUEST_ID_CONFLICT` | 修正业务意图后才能生成新 ID |
| 106 | `RATE_LIMITED` | 请求未准入时，等待 `retry_after_ms` 后使用同一 ID 重试 |

无法关联响应的 Protobuf 格式错误、超大消息或非法信封可以直接关闭连接，不发送 RESPONSE。

### 3.2 临时服务错误

| 数值 | 错误码 | 客户端行为 |
|---:|---|---|
| 200 | `SERVICE_UNAVAILABLE` | 使用同一 ID 退避重试 |
| 201 | `SERVER_BUSY` | 等待 `retry_after_ms` 后使用同一 ID 重试 |
| 202 | `REQUEST_OUTCOME_UNKNOWN` | 必须使用同一 ID 重试，禁止生成新 ID |
| 203 | `CONFIG_UNAVAILABLE` | 使用同一 ID 退避重试 |

`REQUEST_OUTCOME_UNKNOWN` 表示 GateSvr 无法证明 Actor 是否已经执行命令。这是必须保留原 `request_id` 的最重要场景。

### 3.3 商店和配置

| 数值 | 错误码 | 含义 |
|---:|---|---|
| 300 | `SHOP_ENTRY_NOT_FOUND` | 商店条目不存在 |
| 301 | `SHOP_ENTRY_DISABLED` | 商店条目已经停用 |
| 302 | `PRICE_CHANGED` | 报价版本变化；响应携带最新报价 |
| 303 | `CONFIG_ENTRY_DISABLED` | 种子、作物、肥料或出售规则已经停用 |

收到 `PRICE_CHANGED` 后，H5 更新报价并让玩家重新确认，然后使用新的 `request_id` 发起新的业务意图。

### 3.4 资产和仓库

| 数值 | 错误码 | 含义 |
|---:|---|---|
| 400 | `INSUFFICIENT_COINS` | 金币不足以支付总价 |
| 401 | `INVENTORY_TYPE_LIMIT` | 增加新物品种类会超过 100 种 |
| 402 | `INVENTORY_STACK_LIMIT` | 单种物品会超过 300 个 |
| 403 | `ITEM_NOT_OWNED` | 没有需要的种子或肥料 |
| 404 | `INSUFFICIENT_ITEM_QUANTITY` | 出售数量超过库存 |
| 405 | `ITEM_NOT_SELLABLE` | 物品没有启用的出售规则 |

收获同样使用仓库限制错误码，并且失败时整个收获不生效。

### 3.5 农田

| 数值 | 错误码 | 含义 |
|---:|---|---|
| 500 | `PLOT_NOT_FOUND` | 地块不存在 |
| 501 | `PLOT_STATE_CONFLICT` | 当前地块状态不允许该动作 |
| 502 | `FERTILIZER_ALREADY_ACTIVE` | 现有肥料尚未过期 |
| 503 | `CROP_NOT_MATURE` | 作物尚未成熟 |

安全且可以取得数据时，`PLOT_STATE_CONFLICT` 携带当前 `PlotView`，让 H5 只修复该地块，不必拉取完整快照。

### 3.6 任务

| 数值 | 错误码 | 含义 |
|---:|---|---|
| 600 | `CHAPTER_NOT_FOUND` | 章节不存在 |
| 601 | `CHAPTER_NOT_CLAIMABLE` | 任务尚未完成或章节未激活 |
| 602 | `CHAPTER_REWARD_ALREADY_CLAIMED` | 不同请求尝试领取已经领取的章节 |

原成功领奖请求的重试返回第一次成功结果，不能返回 `CHAPTER_REWARD_ALREADY_CLAIMED`。

## 4. 哪些失败关闭连接

以下情况关闭连接：

- 认证无效或过期；
- 协议版本不受支持；
- 新登录撤销当前 Session；
- 消息超过大小限制；
- 信封格式错误，无法安全解析；
- 持续非法流量或攻击。

以下情况保持连接：

- 所有普通业务错误；
- 可以安全回答的临时服务错误；
- 未知动作；
- 非法业务参数。

## 5. Request ID 规则

- H5 在第一次发送前生成规范小写 UUID。
- 所有 REQUEST 都使用 ID 关联响应。
- 只有写动作进入 Player Actor 幂等窗口。
- 重试必须保持 `request_id`、`action`、`target_player_id` 和所有语义载荷字段不变。
- 改变业务意图必须使用新 ID。
- GateSvr 处理 `NOT_OWNER` 时必须保留原 ID。
- 所有服务必须记录 request ID，但禁止把它当作秘密或认证凭证。

幂等作用域：

```text
(caller_player_id, request_id)
```

保存的指纹还覆盖动作、目标玩家和语义载荷。

## 6. 规范载荷指纹

Actor 完成 Protobuf 校验后，根据以下内容计算指纹：

```text
fingerprint_schema_version
+ protocol_version
+ action 枚举
+ target_player_id
+ 按契约顺序排列的动作专属语义字段
```

例子：

```text
BUY_SEEDS:
shop_entry_id, quantity, expected_price_version

SELL_CROP:
crop_item_id, expected_price_version, amount 分支,
存在 quantity 时再加入 quantity
```

指纹不能使用客户端提交的哈希文本。未知兼容 Protobuf 字段不影响 V1 语义，因此不进入 V1 指纹。

## 7. Actor 执行顺序

写请求执行流程：

```text
Gate 校验信封和认证
→ 路由到目标 Actor
→ 查询 (caller_player_id, request_id)
→ 已存在且指纹相同：返回已保存结果，replayed=true
→ 已存在但指纹不同：REQUEST_ID_CONFLICT
→ 新请求：固定服务端时间和一份配置快照
→ 校验业务前置条件
→ 执行成功或产生确定业务失败
→ 保存确定结果
→ 响应
```

成功执行：

```text
原子应用全部业务变化
→ 更新任务进度
→ player_seq++
→ 保存响应、指纹和 Outbox
→ checkpoint_revision++
→ 标记 Dirty
```

已经到达 Actor 的确定业务失败：

```text
不修改金币、仓库、地块、任务或 player_seq
→ 在幂等元数据中保存失败结果
→ checkpoint_revision++
→ 标记 Actor Dirty
```

缓存确定失败可以防止失败响应丢失后，同一个 ID 因玩家状态变化而意外变成成功。玩家修正条件或发起新意图时必须使用新 ID。

`checkpoint_revision` 是 `data-model.md` 定义的持久化 CAS 版本，不属于客户端 `state_version`。任何检查点内容变更都增加它，即使 `player_seq` 保持不变。

Actor 准入前发生的格式错误、认证失败、限流拒绝和路由不可用，不保存在 Player Actor 中。

## 8. 保存的结果

每条保留的写结果包含：

| 字段 | 含义 |
|---|---|
| `request_id` | 幂等键组成部分 |
| `action` | 原始动作 |
| `payload_fingerprint` | 规范语义指纹 |
| `completed_at_ms` | 服务端完成时间 |
| `success` | 确定结果 |
| `state_version` | 原始结果版本 |
| `response_payload` | 紧凑保存的原始类型化凭据和 Patch |
| `error` | 失败时的原始确定业务错误 |
| `outbox_ids` | 该命令创建的 Outbox ID |

每名玩家最多保留最新 100 条写结果，最长保留 24 小时。先删除超过 24 小时的记录；剩余超过 100 条时，再删除最旧记录。

保存的响应必须低于协议消息大小上限。完整 Player Snapshot 属于查询响应，禁止进入幂等窗口。

## 9. 重放行为

相同 ID 且指纹相同：

- 返回第一次确定成功或失败；
- 信封设置 `replayed = true`；
- 返回原始 `state_version`、业务凭据、Patch 和错误；
- 禁止再次校验、扣除资产、推进任务、创建 Outbox 或增加 `player_seq`。

客户端可以展示业务凭据，但必须按正常版本规则决定是否应用 Patch：

- 原序号小于等于本地：忽略 Patch；
- 正好为本地加一：应用；
- 存在缺口：请求完整快照；
- epoch 不同：使用完整快照替换。

ID 超过保留窗口后，服务端无法识别它。客户端禁止自动重试超过 24 小时的写意图；必须刷新状态，并让玩家发起新的业务意图。

## 10. 重试策略

### 使用同一 ID 自动重试

只允许：

- 收到关联响应前连接断开；
- 客户端等待超时；
- `SERVICE_UNAVAILABLE`；
- `SERVER_BUSY`；
- `REQUEST_OUTCOME_UNKNOWN`；
- `CONFIG_UNAVAILABLE`；
- 响应明确说明未准入的 `RATE_LIMITED`。

使用带随机抖动的有上限指数退避。HTTP Session 过期或业务意图超过 24 小时后停止。

### 刷新后使用新 ID

以下情况需要新 ID：

- `PRICE_CHANGED` 后玩家确认最新价格；
- 修正非法数量；
- `PLOT_STATE_CONFLICT` 后发起新的地块动作；
- 修改任何载荷；
- 已经超过幂等保留窗口。

### 禁止自动重试

以下情况不能自动重复：

- 金币或物品不足；
- 仓库达到限制；
- 肥料仍然有效；
- 作物尚未成熟；
- 任务不可领取；
- 操作无权限。

## 11. Dirty 恢复的影响

幂等窗口与玩家状态和 Outbox 属于同一个 Dirty 检查点。

Dirty 持久化使用 `checkpoint_revision` 排列检查点内容；WebSocket 状态排序仍然使用 `(owner_epoch, player_seq)`。

Zone 异常退出后，最近业务变化及其幂等结果可能一起回退到 MySQL 最近检查点。新的 `owner_epoch` 强制客户端接受完整快照。

V3 不承诺在尚未刷盘的异常故障中仍然保持持久化严格 Exactly Once。它提供：

- Active Actor 和已保留检查点范围内的单次执行；
- 玩家状态、幂等结果和待处理 Outbox 的原子恢复；
- ADR-0006 已明确接受的有限数据丢失窗口。

## 12. 必须验证的测试

1. 相同 ID 和载荷的每种写命令只执行一次；
2. 相同 ID 改变任一字段都会返回 `REQUEST_ID_CONFLICT`；
3. 成功重放返回原版本和原业务凭据；
4. Actor 状态后来变化后，原失败结果仍会被重放；
5. 查询请求 ID 只关联响应，不进入保留窗口；
6. `NOT_OWNER` 重新路由保持 ID，且不会重复执行；
7. 价格变化必须在玩家确认后使用新 ID；
8. 领奖重放不能重复增加库存或创建邮件 Outbox；
9. 保留窗口同时满足最多 100 条和最长 24 小时；
10. epoch 恢复从同一检查点恢复玩家状态、幂等记录和 Outbox。
