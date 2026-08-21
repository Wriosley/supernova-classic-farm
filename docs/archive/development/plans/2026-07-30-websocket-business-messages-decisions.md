---
status: completed
date: 2026-07-30
scope: V3 第一条单玩家纵向闭环的 Protobuf 业务消息
prerequisite:
  - 2026-07-30-minimum-websocket-contract-decisions.md
related:
  - ../../../architecture/single-player-vertical-loop-business-architecture.md
output:
  - ../../../contracts/websocket-protocol.md
---

# Protobuf 业务消息：教学版第二轮

## 1. 本轮要解决什么

第一轮已经确定连接和公共信封。第二轮确定：

- H5 怎样获得客户端可见配置；
- 每条命令的 Protobuf 请求字段；
- 每条响应返回哪些权威局部状态；
- Player Snapshot 包含哪些字段；
- Push 和错误怎样表达。

仍然只做契约设计，不生成 `.proto`、Go 代码或 SQL。

每次只讨论一个问题。技术上没有实际争议的字段由文档直接给出建议，只有影响产品行为、兼容性或系统边界的问题才交给项目负责人选择。

## 2. 为什么先讨论客户端配置

Player Snapshot 可以告诉 H5：

```text
仓库里有 item_id = 1001，数量 3
地块种着 crop_id = 2001
```

但仅靠这些 ID，H5 不知道：

```text
1001 叫什么、显示哪张图
2001 是什么作物、有哪些成长阶段图片
商店卖什么、当前展示价格是多少
肥料效果怎样向玩家描述
```

服务端 ConfigSvr 已经是业务计算的权威来源，但“服务端怎样加载配置”和“H5 怎样取得展示配置”是两个不同问题。不能默认让 H5 直接访问内部 ConfigSvr。

## Q1. H5 怎样取得客户端可见配置

### 方案 A：版本化静态配置包，通过 HTTP/CDN 下载

发布配置时产生一个不可变版本包。登录或 AUTH 响应告诉 H5：

```text
client_config_version
client_config_url
client_config_hash
```

H5 已缓存相同版本时不重复下载；版本变化时通过 HTTP 下载并校验。

本地开发可以先由普通静态 HTTP 服务提供文件，生产再放到 CDN，不改变客户端契约。

优点：

- 配置只下载一次，不会在每次 WebSocket 重连时重复发送；
- HTTP/CDN 适合大文件、缓存和断点重试；
- 30M DAU 下不会让 GateSvr 承担大量相同配置流量；
- 图片地址、名称和描述等纯展示数据不进入 Player Actor。

代价：

- 配置发布必须保证版本、文件和哈希一致；
- H5 可能短暂缓存旧配置，写命令仍必须由服务端按权威配置校验；
- 商店实时状态仍需要服务端响应确认。

### 方案 B：通过 WebSocket 请求整份客户端配置

H5 认证后向 GateSvr 发送 `GET_CLIENT_CONFIG`，服务器返回完整配置。

优点：

- 所有游戏数据使用同一条连接；
- 本地原型看起来直接。

代价：

- 每次重连可能重复传输同一份大配置；
- GateSvr 承担不必要的静态流量和内存分配；
- 大消息会挤占业务响应；
- 与第一轮 64 KiB 单消息限制容易冲突。

### 方案 C：把配置直接打包进 H5

每次构建前端时把配置一起编译进去。

优点：

- 运行时不需要额外下载；
- 第一份演示版本最简单。

代价：

- 修改价格或作物配置就要重新发布 H5；
- 前端包和服务端配置容易版本不一致；
- 不适合后续运营调整。

### 当前推荐

采用方案 A：

```text
版本化客户端配置包
+ HTTP 静态服务起步
+ 生产可无缝切换 CDN
+ 登录/AUTH 只告知版本、URL 和哈希
+ 服务端始终重新校验业务数据
```

配置包负责名称、图片地址、描述和基础展示规则。`GET_SHOP` 仍返回当前启用的商品、权威价格和 `price_version`，避免客户端把展示配置当成成交事实。

- [x] A：版本化配置包，通过 HTTP 下载，生产可使用 CDN（推荐） ✅ 2026-07-30
- [ ] B：通过 WebSocket 返回完整客户端配置
- [ ] C：配置固定打包进 H5
- [ ] 其他：

你的判断：

> 同意采用推荐方案。

## Q2. 购买时提交商品 ID 还是商店条目 ID

假设商店当前展示：

```text
商店条目 shop_entry_id = 5001
出售物品 item_id = 1001
单价 = 2
price_version = 8
```

H5 点击购买后，需要告诉服务端“购买的是哪一项报价”。

### 方案 A：提交 `shop_entry_id`

请求字段：

```text
shop_entry_id
quantity
expected_price_version
```

Player Actor 使用 Zone 当前配置，根据 `shop_entry_id` 找到实际 `item_id` 和权威价格。客户端不提交可信单价。

优点：

- 明确指向玩家看到的商店报价；
- 同一种物品以后可以出现在普通、活动或折扣条目中；
- 多种报价不会产生歧义；
- 服务端可以检查该商店条目是否仍然启用。

代价：

- 配置中需要为每个商店条目维护稳定 ID；
- 第一版只有一个普通商店时，看起来比直接传物品 ID 多一层。

### 方案 B：提交 `item_id`

请求字段：

```text
item_id
quantity
expected_price_version
```

优点：

- 第一版字段直观；
- 只有一种固定报价时足够。

代价：

- 同一种物品出现多个报价后，无法说明购买哪一个；
- 将来需要改变请求字段或增加商店类型；
- 物品身份和商店报价身份混在一起。

### 为什么必须提交 `expected_price_version`

玩家看到单价 2 后，配置可能已经把价格改成 3。服务端不能静默按新价格扣款，否则界面显示与实际成交不一致。

推荐流程：

```text
客户端提交看到的 price_version
→ 服务端与当前版本比较
→ 相同则按当前权威价格成交
→ 不同则返回 PRICE_CHANGED 和最新报价
→ 玩家确认后使用新的 request_id 重试
```

### 当前推荐

购买命令提交 `shop_entry_id + quantity + expected_price_version`。价格和实际物品由服务端配置推导。

- [x] A：提交商店条目 ID（采用为契约默认） ✅ 2026-07-30
- [ ] B：直接提交物品 ID
- [ ] 其他：

你的判断：

> 项目负责人要求不再逐项选择，由设计直接采用精简且可扩展的默认方案。

## 3. 完成结果

第二轮不再扩展为逐项问卷。完整业务消息、快照、增量 Push、重连、错误和幂等规则已经直接固化到：

- `../../../contracts/websocket-protocol.md`
- `../../../contracts/idempotency-and-errors.md`

后续实现以契约为准，本计划只保留讨论过程，不覆盖正式契约。
