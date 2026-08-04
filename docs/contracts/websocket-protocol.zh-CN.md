---
status: accepted-translation
version: 1
date: 2026-07-30
source: websocket-protocol.md
owners:
  - project-owner
related:
  - ../architecture/stateful-zone-v3-architecture.md
  - ../architecture/single-player-vertical-loop-business-architecture.md
  - idempotency-and-errors.zh-CN.md
---

# WebSocket 协议 V1（中文版）

> 本文件是 `websocket-protocol.md` 的中文阅读副本。实现时两份文件应保持语义一致；若翻译尚未同步，以英文原始契约为准。

## 1. 范围

本契约定义第一版 H5 Client 到 GateSvr 的游戏协议：

```text
认证
→ 加载玩家快照和商店
→ 购买种子
→ 种植
→ 施肥
→ 成熟
→ 收获
→ 清理地块
→ 出售作物
→ 更新并领取章节任务奖励
```

HTTP 注册、登录和 WS Ticket 签发属于后续 `http-api.md`。存储格式和 Zone 内部 RPC 格式使用独立契约。

文中的“必须”“禁止”“应当”和“可以”分别表示强制要求、禁止行为、推荐行为和可选行为。

## 2. 传输规则

- 生产环境使用安全 WebSocket（`wss`），本地开发可以使用 `ws`。
- 每个应用层消息帧都是一条完整的二进制 Protobuf `WsEnvelope`。
- 一个信封禁止拆成多条应用消息。
- 解码后的单条 WebSocket 消息最大为 64 KiB。
- V1 的每条信封都使用 `protocol_version = 1`。
- 未知 Protobuf 字段应当忽略；所选运行库支持时应予以保留。
- 已发布的字段编号和枚举编号禁止重新绑定其他含义。
- 浏览器和服务端必须从同一份已接受的 `.proto` 版本生成类型。

## 3. 基础类型约定

| 含义 | Protobuf 类型 | 规则 |
|---|---|---|
| `player_id` | `uint64` | H5 使用生成的 `bigint` 或十进制字符串，禁止使用 JavaScript `number` |
| `owner_epoch`、`player_seq` | `uint64` | 在各自作用域内单调递增 |
| 配置版本、价格版本 | `uint64` | 只用于相等比较，H5 不对其做算术运算 |
| 物品、作物、地块、章节、任务 ID | `uint32` | 稳定的配置或领域 ID |
| 数量 | `uint32` | 除非字段另有说明，否则必须大于零 |
| 金币 | `int64` | 必须保持非负；H5 使用 `bigint` 或十进制字符串 |
| 时间 | `int64` | UTC Unix 毫秒；业务判断只相信服务端时间 |
| `request_id` | `string` | 客户端生成的规范小写 UUID |
| 哈希 | `bytes` | 原始字节，不使用十六进制文本 |

所有展示文字来自带版本的客户端配置包。协议只传 ID 和错误参数等权威事实。

## 4. 稳定枚举

### 4.1 `MessageKind`

| 数值 | 名称 | 含义 |
|---:|---|---|
| 0 | `MESSAGE_KIND_UNSPECIFIED` | 非法默认值 |
| 1 | `REQUEST` | 客户端请求 |
| 2 | `RESPONSE` | 通过 `request_id` 关联的响应 |
| 3 | `PUSH` | 服务端主动推送 |

### 4.2 `Action`

| 数值 | 名称 |
|---:|---|
| 0 | `ACTION_UNSPECIFIED` |
| 1 | `AUTH` |
| 2 | `PING` |
| 100 | `GET_PLAYER_SNAPSHOT` |
| 101 | `GET_SHOP` |
| 200 | `BUY_SEEDS` |
| 201 | `PLANT` |
| 202 | `APPLY_FERTILIZER` |
| 203 | `HARVEST` |
| 204 | `CLEAN_PLOT` |
| 205 | `SELL_CROP` |
| 206 | `CLAIM_CHAPTER_REWARD` |
| 207 | `BUY_FERTILIZER` |
| 1000 | `PLAYER_STATE_CHANGED` |

编号 3–99、102–199、208–999 和 1001–1999 保留给后续兼容扩展。已经删除的编号也必须永久保留，不能复用。

## 5. 公共信封

`WsEnvelope` 的逻辑字段：

| 编号 | 字段 | 类型 | 出现条件和含义 |
|---:|---|---|---|
| 1 | `protocol_version` | `uint32` | 必填；V1 为 `1` |
| 2 | `message_kind` | `MessageKind` | 必填 |
| 3 | `action` | `Action` | 必填，必须与载荷匹配 |
| 4 | `request_id` | `string` | REQUEST/RESPONSE 必填；主动 PUSH 不填 |
| 5 | `target_player_id` | `uint64` | 游戏请求必填；AUTH/PING 不填 |
| 6 | `state_version` | `StateVersion` | 快照、推送、成功写入和已到 Actor 的业务失败携带 |
| 7 | `server_time_ms` | `int64` | 每条 RESPONSE/PUSH 必填 |
| 8 | `replayed` | `bool` | 只有返回已保存的幂等结果时为真 |
| 9 | `error` | `Error` | 只有失败 RESPONSE 才携带 |
| 10–99 | `payload` | Protobuf `oneof` | 只能有一个与消息类型和动作匹配的载荷 |

请求中禁止携带 `caller_player_id`。GateSvr 必须从认证连接中取得调用者身份。

单玩家阶段的所有游戏请求都必须把 `target_player_id` 设置为当前认证玩家。未来好友操作可以在通过权限校验后指向其他玩家。

### 5.1 `StateVersion`

| 编号 | 字段 | 类型 |
|---:|---|---|
| 1 | `owner_epoch` | `uint64` |
| 2 | `player_seq` | `uint64` |

比较规则：

1. 更高的 `owner_epoch` 必须替换低 epoch 的全部状态，即使新的 `player_seq` 更小。
2. 同一 epoch 中，只有 `player_seq = 本地版本 + 1` 的增量可以直接应用。
3. `player_seq <= 本地版本` 表示重复或历史重放，忽略其中的状态 Patch。
4. `player_seq > 本地版本 + 1` 表示出现缺口；客户端暂停应用增量并请求完整快照。

## 6. 认证和心跳

### 6.1 AUTH

AUTH 必须是第一条非心跳请求，并在连接建立后 10 秒内到达。

`AuthRequest`：

| 字段 | 类型 | 规则 |
|---|---|---|
| `ws_ticket` | `string` | 必填，短期且只能使用一次 |

成功的 `AuthResponse`：

| 字段                      | 类型       | 规则               |
| ----------------------- | -------- | ---------------- |
| `player_id`             | `uint64` | 与当前连接绑定的玩家       |
| `heartbeat_interval_ms` | `uint32` | V1 默认为 `30000`   |
| `client_config_version` | `uint64` | 客户端展示配置版本        |
| `client_config_url`     | `string` | 不可变 HTTP(S) 对象地址 |
| `client_config_sha256`  | `bytes`  | 下载文件精确字节的哈希      |
| `protocol_min`          | `uint32` | 服务端接受的最小协议版本     |
| `protocol_max`          | `uint32` | 服务端接受的最大协议版本     |

认证成功后 ticket 立即作废。断线重连时，通过仍有效的 HTTP Session 获取新 ticket。新登录会撤销旧 Session 并关闭旧 WebSocket。

AUTH 成功前，GateSvr 只接受 AUTH 和 PING。持续发送其他动作属于连接策略违规。

### 6.2 PING

`PingRequest`：

| 字段 | 类型 | 含义 |
|---|---|---|
| `ping_id` | `uint64` | 当前连接内的递增序号 |
| `client_sent_at_ms` | `int64` | 仅回显并测量往返时间，禁止用于业务判断 |

`PingResponse` 原样返回两个字段。信封中的 `server_time_ms` 提供服务端时间样本。PING 只由 GateSvr 处理，不路由到 Player Actor。

连续两个心跳周期没有收到任何消息时，服务端可以关闭连接。普通业务消息同样证明连接存活。

## 7. 客户端配置和商店

客户端可见配置是通过 HTTP 下载的不可变版本化静态包。开发环境可以使用本地静态服务，生产环境可以使用 CDN。

配置包包含名称、描述、图片地址和纯展示规则，但不是交易权威。H5 必须校验 SHA-256，并按版本缓存。

`GetShopRequest` 没有业务字段。

`GetShopResponse`：

| 字段 | 类型 | 含义 |
|---|---|---|
| `server_config_version` | `uint64` | 本次响应使用的 Zone 配置版本 |
| `entries` | repeated `ShopEntryView` | V1 全部启用的种子商品 |

`ShopEntryView`：

| 字段 | 类型 |
|---|---|
| `shop_entry_id` | `uint32` |
| `item_id` | `uint32` |
| `unit_price` | `int64` |
| `price_version` | `uint64` |
| `enabled` | `bool` |

客户端购买的是 `shop_entry_id` 指向的具体报价，而不是单独的物品 ID。这样同一物品以后可以同时存在普通价、折扣价和活动价。

## 8. 玩家读取模型

WebSocket Player Snapshot 是面向客户端的投影视图，不是内部 Dirty 检查点。它不包含密码、Session、近期幂等记录、Outbox 内部字段、Dirty 标记和内部十进制成长结算字段。

### 8.1 `PlayerSnapshot`

| 字段                      | 类型                       | 含义                    |
| ----------------------- | ------------------------ | --------------------- |
| `player_id`             | `uint64`                 | 快照所属玩家                |
| `coin_balance`          | `int64`                  | 当前权威金币                |
| `inventory`             | repeated `ItemStackView` | 只包含数量大于零的物品栈          |
| `plots`                 | repeated `PlotView`      | 按稳定 `plot_id` 排列的全部地块 |
| `current_chapter`       | `ChapterView`            | 当前章节和任务进度             |
| `server_config_version` | `uint64`                 | 读取时使用的 Zone 配置版本      |

信封负责携带 `state_version` 和 `server_time_ms`。

`GetPlayerSnapshotRequest` 没有业务字段。`GetPlayerSnapshotResponse` 包含一份 `PlayerSnapshot`。

GateSvr 必须先注册订阅并缓冲变化，再请求 Actor 快照。发送快照后，按顺序发送比快照更新的缓冲增量，丢弃较旧的重复增量。

### 8.2 `ItemStackView`

| 字段 | 类型 |
|---|---|
| `item_id` | `uint32` |
| `quantity` | `uint32` |

数量变成零时应从仓库列表中移除，不能保留零数量物品栈。

### 8.3 `PlotView`

`PlotState`：`UNSPECIFIED = 0`、`EMPTY = 1`、`GROWING = 2`、`MATURE = 3`、`NEED_CLEANUP = 4`。

| 字段                       | 类型           | 出现条件                        |
| ------------------------ | ------------ | --------------------------- |
| `plot_id`                | `uint32`     | 始终存在                        |
| `plot_state`             | `PlotState`  | 始终存在                        |
| `crop_id`                | `uint32`     | GROWING/MATURE/NEED_CLEANUP |
| `crop_config_version`    | `uint64`     | GROWING/MATURE/NEED_CLEANUP |
| `planted_at_ms`          | `int64`      | GROWING/MATURE/NEED_CLEANUP |
| `estimated_mature_at_ms` | `int64`      | 仅 GROWING                   |
| `harvestable_quantity`   | `uint32`     | 仅 MATURE                    |
| `fertilizer_effect`      | `EffectView` | 只在效果有效时存在                   |
| `pest_effect`            | `EffectView` | 只在效果有效时存在                   |

H5 根据服务端时间和展示配置推导种子、发芽、半成熟图片。这些只是展示阶段，不属于权威状态，也不产生 Push。

`EffectView` 包含 UUID 字符串形式的 `effect_instance_id`、`effect_item_id`、`effect_config_version`、`start_at_ms`、`end_at_ms` 和可选 `source_player_id`。内部十进制倍率不发送给客户端，展示文字来自客户端配置。

### 8.4 `ChapterView`

`ChapterStatus`：`UNSPECIFIED = 0`、`IN_PROGRESS = 1`、`CLAIMABLE = 2`、`CLAIMED = 3`。

| 字段 | 类型 |
|---|---|
| `chapter_id` | `uint32` |
| `status` | `ChapterStatus` |
| `tasks` | repeated `TaskProgressView` |

`TaskProgressView` 包含 `task_id`、`current_value`、`target_value` 和 `completed`；进度使用 `uint32`。

## 9. 状态 Patch

成功写命令和状态变化 Push 使用统一的替换式 `PlayerStatePatch`：

| 字段 | 类型 | 应用规则 |
|---|---|---|
| `coin_balance` | optional `int64` | 存在时替换 |
| `inventory_upserts` | repeated `ItemStackView` | 按 `item_id` 替换物品栈 |
| `inventory_removed_item_ids` | repeated `uint32` | 删除这些物品栈 |
| `plot_upserts` | repeated `PlotView` | 按 `plot_id` 替换地块 |
| `current_chapter` | optional `ChapterView` | 替换整个当前章节 |

缺少字段表示该部分没有变化。客户端必须先执行第 5.1 节的版本检查，之后才能应用 Patch。

命令响应是发起连接的权威更新；其他订阅者接收相同变化的 PUSH。GateSvr 应当抑制向发起连接发送重复 Push；即使未抑制，客户端也必须通过版本去重。

## 10. 写命令

每个新业务意图使用一个新 UUID。自动重试必须使用原 UUID 和完全相同的语义载荷。

所有成功写命令统一执行：

```text
固定 server_now 和一份 Zone 配置快照
→ 根据 Actor 当前状态校验
→ 原子修改玩家、农田和任务
→ player_seq 只增加一次
→ 保存幂等结果和必要 Outbox
→ 标记 Dirty
→ 返回业务凭据和 PlayerStatePatch
```

### 10.1 `BUY_SEEDS`

`BuySeedsRequest`：

| 字段 | 类型 |
|---|---|
| `shop_entry_id` | `uint32` |
| `quantity` | `uint32` |
| `expected_price_version` | `uint64` |

服务端从固定配置中推导物品和价格。客户端禁止提交可信成交价格。

`BuySeedsResponse` 包含 `shop_entry_id`、`item_id`、`quantity`、`unit_price`、`total_price` 和 `patch`。Patch 包含新金币、种子库存和当前章节。

### 10.2 `BUY_FERTILIZER`

`BuyFertilizerRequest` 与 `BuyFertilizerResponse` 的字段与 `BUY_SEEDS`
相同。数量必须在 1–50，单类物品堆叠上限是 300。固定报价必须是肥料；
服务端推导物品和价格，扣除 `unit_price × quantity`，并在 Patch 返回金币、
肥料库存和当前章节。该命令不会推进“购买种子”章节任务。

### 10.3 `PLANT`

`PlantRequest` 包含 `plot_id` 和 `seed_item_id`。

服务端把种子物品映射成作物，并固化作物配置。客户端不提交 `crop_id`、成长速度、成熟值、产量或时间。

`PlantResponse` 包含 `consumed_seed_item_id`，Patch 包含种子库存、地块和当前章节。

### 10.4 `APPLY_FERTILIZER`

`ApplyFertilizerRequest` 包含 `plot_id` 和 `fertilizer_item_id`。

Actor 先使用旧速度结算到当前服务端时间，再应用新效果。已有肥料效果时整个命令失败，且不消耗肥料。

`ApplyFertilizerResponse` 包含 `consumed_fertilizer_item_id`、`effect_instance_id`，Patch 包含库存、地块和当前章节。

### 10.5 `HARVEST`

`HarvestRequest` 包含 `plot_id`。

收获必须完整成功。仓库无法容纳全部产量时，不增加作物，地块继续保持 MATURE，任务也不推进。

`HarvestResponse` 包含 `crop_item_id`、`harvested_quantity`，Patch 包含库存、NEED_CLEANUP 地块和当前章节。

### 10.6 `CLEAN_PLOT`

`CleanPlotRequest` 包含 `plot_id`。

V1 自己清理地块不消耗物品、不发奖励、不推进任务。`NEED_CLEANUP` 地块无需先领取章节奖励即可清理。`CleanPlotResponse` 的 Patch 包含变成 EMPTY 的地块。

### 10.7 `SELL_CROP`

`SellCropRequest`：

| 字段 | 类型 | 规则 |
|---|---|---|
| `crop_item_id` | `uint32` | 必填 |
| `expected_price_version` | `uint64` | 必填 |
| `amount` | `oneof` | 必须且只能选择 `quantity` 或 `sell_all = true` |

使用 `sell_all` 时，Actor 在实际执行时读取当前完整库存数量。幂等重放返回第一次解析出的出售数量和结果。

`SellCropResponse` 包含 `crop_item_id`、`sold_quantity`、`unit_price`、`total_price`，Patch 包含库存、金币和当前章节。

### 10.8 `CLAIM_CHAPTER_REWARD`

`ClaimChapterRewardRequest` 包含 `chapter_id`。显式章节 ID 可以防止旧界面误领后续章节奖励。

`ClaimChapterRewardResponse`：

| 字段 | 类型 |
|---|---|
| `chapter_id` | `uint32` |
| `coin_granted` | `int64` |
| `items_added_to_inventory` | repeated `ItemStackView` |
| `items_pending_mail` | repeated `ItemStackView` |
| `patch` | `PlayerStatePatch` |

`items_pending_mail` 只表示待处理 `CreateRewardMail` Outbox 事件已经与 Actor 的领奖状态原子记录，禁止声称 Mail Service 已经创建或送达邮件。按照 V3 异步 Dirty 写回，只有检查点和关系型 Outbox 行提交后，事件才获得数据库持久性；已经确认但尚未刷盘的领奖及事件可能在 Zone 异常退出后一起回退。

## 11. Push

V1 只有一种 Push 动作：`PLAYER_STATE_CHANGED`。

`PlayerStateChangedPush`：

| 字段 | 类型 | 含义 |
|---|---|---|
| `reason` | `StateChangeReason` | `BUY_SEEDS`、`BUY_FERTILIZER`、`PLANT`、`APPLY_FERTILIZER`、`MATURED`、`HARVEST`、`CLEAN_PLOT`、`SELL_CROP`、`CLAIM_CHAPTER_REWARD` |
| `caused_by_request_id` | optional `string` | 命令导致变化时存在 |
| `patch` | `PlayerStatePatch` | 权威增量 |

自然成熟：

```text
结算到权威成熟时间
→ 地块变成 MATURE
→ 效果结束
→ player_seq++
→ 标记 Dirty
→ PUSH 新 PlotView
```

离线 Actor 激活时若多个地块成熟，按稳定 `plot_id` 顺序处理。第一次快照请求收到激活结算完成后的最终快照，不发送中间 Push。

## 12. 并发和路由

- 一条连接允许多个正在等待响应的请求。
- 客户端通过 `request_id` 关联响应，禁止依赖响应到达顺序。
- 一个 Player Actor 按 Mailbox 顺序串行执行所有命令。
- GateSvr 按 `target_player_id` 路由。
- 内部收到 `NOT_OWNER` 后，GateSvr 刷新已提交 ShardMap，并用相同 `request_id` 重试。
- `NOT_OWNER` 不作为普通业务错误暴露给客户端。
- 命令不携带全局 `expected_player_seq`，而是重新校验各自业务前置条件。

## 13. 重连和重新同步

重连顺序：

```text
使用 HTTP Session 获取新的一次性 ws_ticket
→ 建立 WebSocket
→ AUTH
→ 确认客户端配置版本已经缓存
→ GET_PLAYER_SNAPSHOT
→ 打开商店时 GET_SHOP
```

以下情况必须用完整 Player Snapshot 替换客户端视图：

- 本地没有快照；
- `owner_epoch` 变化；
- `player_seq` 出现缺口；
- 本地应用状态失败。

V1 不提供持久化增量补发接口。

## 14. 关闭连接

| 关闭码 | 含义 |
|---:|---|
| 1000 | 正常关闭 |
| 1002 | Protobuf 格式错误或信封组合非法 |
| 1009 | 消息超过 64 KiB |
| 1011 | 服务端连接级严重错误 |
| 4401 | AUTH 超时、ticket 无效或 Session 过期 |
| 4406 | 不支持的协议版本 |
| 4409 | 新登录撤销旧 Session |
| 4429 | 持续攻击或连接级限流违规 |

金币不足、仓库已满、作物未成熟、肥料仍有效、价格变化等普通业务错误使用失败 RESPONSE，并保持连接。

## 15. 验证清单

实现测试必须证明：

1. Go 和 TypeScript 生成类型可以完成二进制 Protobuf 往返；
2. AUTH 超时、一次性 ticket 和重复登录关闭正确；
3. 多个并发请求能通过 `request_id` 正确关联；
4. 所有命令载荷都会拒绝缺失、零值或非法字段；
5. 客户端无法设置价格、余额、成熟、产量或任务进度；
6. Patch 应用、重复抑制、缺口恢复和 epoch 替换正确；
7. 订阅与快照之间不存在丢 Push 的竞态；
8. 64 KiB 限制在无界内存分配前生效；
9. PING 只经过 GateSvr，不激活 Actor；
10. 每种业务错误都保持连接并遵守错误契约。
