---
status: accepted-translation
version: 1
date: 2026-07-30
owners:
  - project-owner
source: http-api.md
related:
  - ../architecture/stateful-zone-v3-architecture.md
  - websocket-protocol.md
  - idempotency-and-errors.md
---

# HTTP 认证与引导契约 V1（中文阅读镜像）

## 1. 范围与权威

本文件是第一阶段账号注册、登录、HTTP Session 管理、一次性 WebSocket Ticket 签发、Gateway 发现和版本化客户端配置引导的完整中文阅读镜像。规范性英文源文件是 `http-api.md`；两者有差异时以英文源文件为准。

本契约只定义公开的 H5 到 LoginSvr HTTP 边界。游戏命令仍通过 Client 到 GateSvr 的 WebSocket；玩家状态、Shard 路由、Dirty 检查点和内部 RPC 不在本契约范围内。

规范词 `MUST`、`MUST NOT`、`SHOULD` 和 `MAY` 保留通常的约束含义。已经发布的 Protobuf 字段号和枚举值不得改作其他含义。

四类安全对象必须明确区分：

- **HTTP Session** 是服务端保存、可撤销的登录记录，客户端只能通过 HttpOnly Cookie 持有其不透明引用；
- **`ws_ticket`** 是通过有效 HTTP Session 签发的短期 Bearer Secret，只能消费一次；
- **WebSocket 连接认证** 只在 GateSvr 于 `AUTH` 阶段原子消费 Ticket 时发生；
- **客户端配置** 是公开、不可变、通过哈希校验的展示配置包，既不是认证凭据，也不是交易权威。

## 2. 传输与标量约定

### 2.1 HTTP 与 Protobuf

- 生产端点只能使用 HTTPS。必须支持 TLS 1.2 或更高版本，优先使用 TLS 1.3。
- API 请求和响应体使用二进制 Protobuf，`Content-Type: application/x-protobuf`。
- 客户端发送 `Accept: application/x-protobuf`。不支持的请求媒体类型返回 `415`；响应类型不可接受时返回 `406`。
- 带 Body 的请求必须只包含一条完整消息。尾随字节、错误 Wire 数据、值冲突的重复 Singular 字段和不规范语义值都必须拒绝。
- 认证端点请求不得使用 `Content-Encoding`。不可变配置对象也使用 Identity Encoding，使摘要始终覆盖收到的 Body 原始字节。
- API 请求 Body 上限为 16 KiB，配置对象上限为 2 MiB。必须在无界内存分配前执行限制。
- GET 请求没有请求体。成功响应只有 `HEAD`、`204` 和 `304` 可以没有 Body。
- 除不可变客户端配置对象外，API 响应（包括错误）都使用 `Cache-Control: no-store`。
- 未知 Protobuf 字段会被忽略；所选 Runtime 支持时应保留。所有已知字段仍须完成语义校验。

### 2.2 标量

| 含义 | Protobuf 类型 | 规则 |
|---|---|---|
| `player_id` | `uint64` | H5 使用生成的 `bigint` 或十进制字符串，绝不使用 JavaScript `number` |
| 版本 | `uint64` | 不透明相等性 Token |
| 时间 | `int64` | UTC Unix 毫秒；服务端时间权威 |
| 哈希 | `bytes` | 原始摘要字节，不是十六进制文本 |
| 请求/Ticket 签发 ID | `string` | 规范小写 UUID |
| 账号名 | `string` | 3–32 个小写 ASCII 字符；首字符为 `a-z`，其余为 `a-z`、`0-9` 或 `_` |

密码为 12–128 个 Unicode Scalar Value，不得 Trim 或 Unicode Normalize，只能通过密码校验器比较。拒绝 NUL 和无效 UTF-8。

### 2.3 关联 ID

客户端可以发送规范小写 UUID 格式的 `X-Request-ID`。服务端必须在每个 API 响应中返回 `X-Request-ID`：保留有效的客户端值，否则生成新值。它只作为日志关联元数据，不是幂等键，不得包含 Secret，也不得被当作 Secret。

## 3. 端点

| 方法 | 路径 | 认证 | CSRF | 成功状态 |
|---|---|---|---|---:|
| GET | `/v1/auth/csrf` | 无 | Origin 检查 | 200 |
| POST | `/v1/auth/register` | 无 | 必需 | 201 |
| POST | `/v1/auth/login` | 无 | 必需 | 200 |
| GET | `/v1/auth/session` | Session | 不需要 | 200 |
| POST | `/v1/auth/logout` | Session 可选 | 必需 | 200 |
| POST | `/v1/ws-tickets` | Session | 必需 | 201 |
| GET | `/v1/gateways` | Session | 不需要 | 200 |
| GET | `/v1/bootstrap` | Session | 不需要 | 200 |

任何端点都不得在 URL 或 Query String 中接收凭据、Session ID、CSRF Token 或 Ticket。

## 4. 公共消息与错误

`SessionView`：

| Tag | 字段 | 类型 | 规则 |
|---:|---|---|---|
| 1 | `player_id` | `uint64` | 已认证玩家 |
| 2 | `account_name` | `string` | 规范账号名 |
| 3 | `created_at_ms` | `int64` | Session 创建时间 |
| 4 | `idle_expires_at_ms` | `int64` | 当前空闲截止时间 |
| 5 | `absolute_expires_at_ms` | `int64` | 不可延长的绝对截止时间 |

`HttpErrorCode`：

| 值 | 名称 | 含义 |
|---:|---|---|
| 0 | `HTTP_ERROR_UNSPECIFIED` | 无效默认值 |
| 100 | `INVALID_ARGUMENT` | 请求格式或语义无效 |
| 101 | `UNSUPPORTED_MEDIA_TYPE` | 请求体不是二进制 Protobuf |
| 102 | `NOT_ACCEPTABLE` | 客户端不接受二进制 Protobuf |
| 103 | `PAYLOAD_TOO_LARGE` | 解码后/Body 超过限制 |
| 200 | `UNAUTHENTICATED` | Session 缺失、过期或已撤销 |
| 201 | `INVALID_CREDENTIALS` | 通用账号名/密码错误 |
| 202 | `FORBIDDEN` | 已认证主体没有权限 |
| 203 | `CSRF_REJECTED` | Origin 或 CSRF 证明失败 |
| 204 | `ACCOUNT_NAME_UNAVAILABLE` | 注册账号名不可用 |
| 300 | `GATEWAY_NOT_FOUND` | 所选 Gateway 未知或不可用 |
| 301 | `TICKET_REQUEST_CONFLICT` | Ticket 签发 ID 被不同语义复用 |
| 302 | `TICKET_REPLAY_EXPIRED` | 已保留的 Ticket 结果不能再重放 |
| 303 | `CLIENT_CONFIG_UNAVAILABLE` | 没有兼容的已发布配置 |
| 400 | `RATE_LIMITED` | 准入被拒绝，稍后重试 |
| 500 | `SERVICE_UNAVAILABLE` | 依赖或服务暂时故障 |
| 501 | `INTERNAL_ERROR` | 非预期服务端故障 |

`HttpError`：

| Tag | 字段 | 类型 | 规则 |
|---:|---|---|---|
| 1 | `code` | `HttpErrorCode` | 稳定机器可读值 |
| 2 | `params` | repeated `ErrorParam` | 只用于本地化参数 |
| 3 | `retryable` | `bool` | 是否允许自动重试 |
| 4 | `retry_after_ms` | optional `uint32` | 适用时的最短等待 |
| 5 | `correlation_id` | `string` | 等于响应 `X-Request-ID` |
| 6 | `debug_message` | optional `string` | 仅开发环境；生产必须缺失且客户端绝不展示 |

`ErrorParam` 的 `key` 为 Tag 1、`value` 为 Tag 2，类型均为字符串。参数不得包含密码、Cookie、CSRF 值、Ticket、被通用错误隐藏的账号存在性、内部地址或 Stack Trace。每个带 Body 的非 2xx API 响应只包含一条 `HttpError`。

HTTP 状态映射：

| 状态 | 用途 |
|---:|---|
| 400 | Protobuf 语义错误或 `INVALID_ARGUMENT` |
| 401 | `UNAUTHENTICATED` 或通用 `INVALID_CREDENTIALS`；清除无效 Session Cookie |
| 403 | `FORBIDDEN` 或 `CSRF_REJECTED` |
| 406 | `NOT_ACCEPTABLE` |
| 409 | 账号名、Ticket 签发 ID 或当前资源冲突 |
| 413 | `PAYLOAD_TOO_LARGE` |
| 415 | `UNSUPPORTED_MEDIA_TYPE` |
| 429 | `RATE_LIMITED`，同时返回 `Retry-After` 和 `retry_after_ms` |
| 500 | `INTERNAL_ERROR` |
| 503 | `SERVICE_UNAVAILABLE` 或 `CLIENT_CONFIG_UNAVAILABLE`，可带 `Retry-After` |

未知路由可以返回 404。认证失败不得通过状态码、错误码、Body 大小类别或人为引入的时间差，区分账号不存在、密码错误、账号禁用或密码哈希版本不一致。

## 5. CSRF、Origin 与 CORS

### 5.1 CSRF 引导

`GET /v1/auth/csrf` 返回 `CsrfResponse`：

| Tag | 字段 | 类型 |
|---:|---|---|
| 1 | `csrf_token` | `string` |
| 2 | `expires_at_ms` | `int64` |

同时设置非 HttpOnly 的 `__Host-cf_csrf` Cookie。Token 至少包含 256 位熵，使用无 Padding 的 Base64url，2 小时过期，并在注册/登录成功后及之后定期轮换。

每个 POST 必须同时满足：

1. `Origin` 与配置的某个 H5 Origin 完全相等；
2. 浏览器提供 `Sec-Fetch-Site` 时，其值为 `same-origin`；只有显式配置的同站分离 Origin 部署可以使用 `same-site`；
3. `X-CSRF-Token` 与 CSRF Cookie 完全相等；
4. 服务端验证签名 Token 有效，并将其绑定到浏览器 CSRF Nonce；已认证时还绑定到当前 Session Generation。

浏览器 API 请求缺失 `Origin` 时必须拒绝。非浏览器客户端必须配置显式 Trusted-client Policy；生产环境不得静默豁免。

### 5.2 CORS

默认部署同源，不发送 CORS Header。如果 H5 和 API 拆分 Origin，它们必须保持同站，确保 `SameSite=Strict` Cookie 可用；服务端使用显式、精确的 Origin Allowlist、`Access-Control-Allow-Credentials: true` 和 `Vary: Origin`。禁止通配 Origin。Preflight 只允许文档定义的方法和 `Content-Type`、`Accept`、`X-CSRF-Token`、`X-Request-ID`。CORS 决策绝不能替代 CSRF 校验。

## 6. 注册、登录与密码

`RegisterRequest`：

| Tag | 字段 | 类型 |
|---:|---|---|
| 1 | `account_name` | `string` |
| 2 | `password` | `string` |

`RegisterResponse` 的 Tag 1 是 `session`（`SessionView`）。

注册先将账号名规范为小写，再检查唯一性。注册结果对外必须是原子的：只有账号已经处于 `ACTIVE` 状态、初始 Player 检查点及已接受的初始农场资源已经持久化，并且返回的 Session 有效时，才允许返回 201。任何失败都不得向 H5 暴露可用的部分配置账号或 Session。内部处于部分配置状态的账号不得通过认证或 Session 检查暴露，其配置过程必须可以安全重试和对账修复。

当账号和 Player 记录同库放置时，本地原型可以用一个 MySQL 事务满足该保证。生产部署如果把账号和 Player 分到不同 Shard，必须使用另行定义的幂等 Provisioning 状态机、Outbox 和对账语义；本 HTTP 契约不要求也不暗示这些数据库共享一个本地事务。

成功时轮换 CSRF、设置 Session Cookie 并返回 201。不可用或保留账号名返回 409 `ACCOUNT_NAME_UNAVAILABLE`；不得暴露现有玩家 ID、资料或内部 Provisioning 状态。

`LoginRequest`：

| Tag | 字段 | 类型 |
|---:|---|---|
| 1 | `account_name` | `string` |
| 2 | `password` | `string` |

`LoginResponse` 的 Tag 1 是 `session`（`SessionView`）。

登录成功时创建新的 Session Generation，撤销该账号的全部旧 Session，使其所有未消费 Ticket 失效，并让 GateSvr 使用 WebSocket Close Code 4409 关闭其已认证连接。撤销必须在返回新登录响应前成为权威状态。这是重复登录撤销，不是多设备支持。

无效登录始终返回 401 `INVALID_CREDENTIALS`。账号不存在时，服务端执行有界 Dummy Password Verification；适用的 Verifier 比较使用常量时间。

密码必须使用 Argon2id 哈希、每个密码至少 16 字节的独立密码学随机 Salt 和至少 32 字节输出。生产起始参数为内存 64 MiB、迭代 3、并行度 1；每个账号保存参数和哈希版本，策略提高后在成功登录时升级。服务端 Pepper 应保存在数据库外并带版本以支持轮换。明文密码、可逆加密、日志、Trace、Analytics 和幂等存储都不得包含密码。资源参数必须根据登录容量和拒绝服务限制做 Benchmark；没有新的已接受替代决策时，不得低于 Argon2id 19 MiB、2 次迭代、并行度 1。

## 7. HTTP Session

生产 Cookie：

```text
__Host-cf_session=<opaque random value>; Secure; HttpOnly; SameSite=Strict; Path=/
```

不得设置 `Domain`。Cookie 值至少包含 256 位熵，不携带 Player ID、角色、过期时间或其他客户端可读权威。服务端只保存 Keyed Digest。登录和权限/认证状态变化时轮换 Session ID。

Session 默认值：

- 空闲生命周期：12 小时；
- 绝对生命周期：创建后 7 天；
- 已认证活动可以延长空闲截止时间，但不得超过绝对截止时间；
- 持久化刷新写入应合并到最多每 5 分钟一次；
- 过期或撤销后必须立即禁止签发 Ticket，并按情况使用 4401 或 4409 关闭已认证 WebSocket。

`GET /v1/auth/session` 返回 `SessionResponse`：Tag 1 为 `session`，Tag 2 为 `server_time_ms`。Session 缺失、过期或已撤销时返回 401 并清除 Cookie。

`POST /v1/auth/logout` 使用空 `LogoutRequest`。`LogoutResponse` 的 Tag 1 为 `logged_out`（`bool`）。登出具有幂等性：只要 CSRF 证明有效，就撤销存在的当前 Session、使未消费 Ticket 失效、使用 4401 关闭其 WebSocket、清除两类 Cookie，并返回 200 和 `logged_out = true`。

Session 状态和 Cookie 不得缓存。Session 查询、撤销和 Ticket 消费必须使用不会接受已知旧 Session Generation 的一致性级别。

## 8. Gateway 发现

`GatewayEndpoint`：

| Tag | 字段 | 类型 | 规则 |
|---:|---|---|---|
| 1 | `gateway_id` | `string` | 稳定、不透明的部署 ID |
| 2 | `websocket_url` | `string` | 生产 `wss://` URL；URL 不带 Ticket |
| 3 | `region` | `string` | 路由 Hint，不是授权 |
| 4 | `priority` | `uint32` | 数值越小越先尝试 |
| 5 | `expires_at_ms` | `int64` | 发现记录新鲜度 |

`GatewayDiscoveryResponse`：

| Tag | 字段 | 类型 |
|---:|---|---|
| 1 | `gateways` | repeated `GatewayEndpoint` |
| 2 | `server_time_ms` | `int64` |

`GET /v1/gateways` 返回至少一个当前可用 Gateway，否则返回 503。客户端按 Priority 排序，可以 Failover 到响应中的另一个端点，但必须为实际使用的 Gateway 申请 Ticket。Gateway URL 必须来自服务端控制的 Allowlist；客户端不得跟随发现结果访问不可信 Scheme，也不得注入自定义 Gateway ID。

## 9. 一次性 WebSocket Ticket

`WsTicketRequest`：

| Tag | 字段 | 类型 | 规则 |
|---:|---|---|---|
| 1 | `ticket_request_id` | `string` | 规范小写 UUID |
| 2 | `gateway_id` | `string` | 当前发现的一个 Gateway |

`WsTicketResponse`：

| Tag | 字段 | 类型 |
|---:|---|---|
| 1 | `ws_ticket` | `string` |
| 2 | `expires_at_ms` | `int64` |
| 3 | `gateway_id` | `string` |

Ticket 规则：

- 值是无 Padding Base64url 编码的不透明 Bearer Secret，至少 256 位熵，最长 128 字符；
- 明文只在本响应返回，不得记录日志、放入 URL、在 H5 中持久化到连接建立以后，或发送到 WebSocket `AuthRequest` 以外的任何位置；
- 生命周期为 30 秒，不延长；
- 绑定 `player_id`、Session ID 和 Generation、所选 `gateway_id` 与签发记录；
- GateSvr 使用从未使用到已消费的 Compare-and-set 原子消费；
- 只有 Ticket 未使用、未过期、属于该 Gateway 且 Session 仍有效时才能消费成功；
- 成功消费后固定连接的 `caller_player_id`，客户端消息字段绝不能替代它；
- 重放、过期、Gateway 错误或 Session 已撤销都导致认证失败，除通用 WebSocket 认证行为外不暴露原因；
- 登出、Session 过期和重复登录会使该 Session 的全部未使用 Ticket 失效。

对于一个 Session，使用新的 `ticket_request_id` 签发时，必须先使更早的未使用 Ticket 失效。30 秒内以相同 ID 和相同 Gateway 重试，返回相同 Ticket 和过期时间，不创建第二条记录；服务可以加密保留 Ticket，或以安全方式派生 Ticket 来支持重放。相同 ID 更换 Gateway 时返回 409 `TICKET_REQUEST_CONFLICT`。Ticket 已消费或过期后重放返回 409 `TICKET_REPLAY_EXPIRED`；客户端按需刷新 Session/发现结果并使用新 UUID。

Ticket 签发成功不代表 WebSocket 已认证。仍必须按照 `websocket-protocol.md`，在连接后 10 秒内把 `AUTH` 作为第一条非心跳 WebSocket 请求发送。

## 10. 客户端引导与不可变配置

`AuthBootstrap` 与 WebSocket 成功 `AuthResponse` 共享且只包含以下字段：

| Tag | 字段 | 类型 | 规则 |
|---:|---|---|---|
| 1 | `player_id` | `uint64` | Session 玩家 |
| 2 | `heartbeat_interval_ms` | `uint32` | V1 默认 `30000` |
| 3 | `client_config_version` | `uint64` | 要求的展示配置版本 |
| 4 | `client_config_url` | `string` | 不可变 HTTP(S) 对象 URL |
| 5 | `client_config_sha256` | `bytes` | 对象 Body 原始字节的 SHA-256 |
| 6 | `protocol_min` | `uint32` | 接受的最低 WebSocket 协议 |
| 7 | `protocol_max` | `uint32` | 接受的最高 WebSocket 协议 |

`ClientBootstrapResponse`：

| Tag | 字段 | 类型 |
|---:|---|---|
| 1 | `auth_bootstrap` | `AuthBootstrap` |
| 2 | `gateways` | repeated `GatewayEndpoint` |
| 3 | `server_time_ms` | `int64` |

`GET /v1/bootstrap` 是便利的预检接口，组合当前 Session 身份、Gateway 发现、协议兼容范围和配置发布信息。嵌套 `AuthBootstrap` 必须使用上述完全一致的字段名、类型和含义。成功的 WebSocket `AuthResponse` 仍是该连接的权威，并必须返回相同的七个逻辑字段。如果 HTTP Bootstrap 与 WebSocket AUTH 之间发生发布变化，使用更新的 AUTH 值；客户端加载并校验该版本后才能渲染游戏。本契约不会给已接受的 WebSocket AUTH 契约增加字段，也不会替代它。

`client_config_url` 指向二进制 Protobuf `ClientConfigPackage`：

| Tag | 字段 | 类型 | 含义 |
|---:|---|---|---|
| 1 | `schema_version` | `uint32` | 配置包 Schema；V1 为 1 |
| 2 | `client_config_version` | `uint64` | 等于 Bootstrap 版本 |
| 3 | `published_at_ms` | `int64` | 信息性服务端时间 |
| 10 | `locale_bundles` | repeated `LocaleBundle` | 本地化展示文本 |
| 11 | `assets` | repeated `AssetEntry` | 客户端资源引用 |
| 12 | `display_rules` | repeated `DisplayRule` | 非权威展示值 |

嵌套消息：

- `LocaleBundle`：`locale` 字符串为 Tag 1；repeated `TextEntry` 为 Tag 2。
- `TextEntry`：稳定 `key` 字符串为 Tag 1；本地化 `value` 字符串为 Tag 2。
- `AssetEntry`：稳定 `asset_key` 字符串为 Tag 1；不可变 `url` 字符串为 Tag 2；原始 `sha256` 字节为 Tag 3。
- `DisplayRule`：稳定 `key` 字符串为 Tag 1；不透明 Protobuf `bytes value` 为 Tag 2；`value_schema` 字符串为 Tag 3。

配置包可以描述名称、错误文本、图片和视觉阶段阈值。不得信任它来决定价格、余额、成长权威、成熟、产量、仓库限制、任务进度、奖励、授权或协议接受范围。

发布规则：

- 每个版本不可变；任何字节变化都必须使用新的 `client_config_version` 和 URL；
- URL 应包含版本和摘要，且绝不能重新指向其他内容；
- 响应 Header 为 `Content-Type: application/x-protobuf`、`Content-Encoding: identity`、`Cache-Control: public, max-age=31536000, immutable`；
- `ETag` 可以是带引号的小写十六进制摘要，但 Protobuf 字段仍为原始字节；
- H5 下载到临时 Buffer，执行 2 MiB 限制，先校验 SHA-256，再解析/激活；确认配置包版本与请求版本相等，然后按版本原子激活和缓存；
- 摘要不匹配、解析失败、Schema 不支持或版本不一致时丢弃对象并禁止游戏渲染；客户端使用有界退避重新获取 Bootstrap；
- 发布必须原子：在所有预期 Serving Path 都能取到不可变对象之前，Bootstrap 不得公布该对象。

## 11. 限流与滥用控制

以下是每个部署的初始默认值，不是容量声明。限制使用服务端单调时钟 Window/Token Bucket，受到攻击时可以更严格：

| 操作 | 默认值 |
|---|---|
| CSRF 引导 | 60 次/小时/IP |
| 注册 | 5 次/小时/IP |
| 登录 | 20 次/15 分钟/IP，且 10 次/15 分钟/账号名 Bucket |
| Session 检查 | 120 次/分钟/Session 或 IP |
| 登出 | 20 次/分钟/Session 或 IP |
| Gateway/Bootstrap 读取 | 60 次/分钟/Session |
| Ticket 签发 | 10 次/分钟/Session，且只允许一个未使用 Ticket |

账号名 Bucket 必须使用 Keyed Digest，且不得暴露账号是否存在。必须在昂贵密码哈希之前执行认证工作准入，防止攻击者耗尽哈希容量。限流拒绝不产生账号、Session 或 Ticket 变更，返回 429 和两类重试提示。反复发送格式错误、CSRF 无效或凭据流量，可以接受更长的 IP/Device-edge 惩罚。安全日志必须遮蔽凭据、Cookie、CSRF Token 和 Ticket。

## 12. 重试与缓存语义

- GET Session、Gateway、Bootstrap、CSRF 和不可变配置请求，在网络故障、429 或允许重试的 503 下，可以使用有界指数退避和 Jitter 重试。
- 注册和登录遇到网络结果不确定时不得自动重复。客户端先检查 `/v1/auth/session`；仍未认证时，让用户重新发起意图。
- 登出具有幂等性，可以重试；必要时先获取新 CSRF Token。
- Ticket 签发遵循第 9 节 `ticket_request_id` 重放规则。网络重试保留完全相同的 Body 和 ID。
- 400、401 凭据错误、403、406、409、413 或 415 不得用不变请求自动重试。
- 429 和 503 重试等待 `Retry-After` 与 `retry_after_ms` 中较大值，加入 Jitter，并在有界客户端截止时间停止。
- Auth/Session/Ticket API 调用禁止 Redirect。配置 URL 最多允许一次同源或显式可信 CDN Redirect，但最终字节仍必须通过摘要校验。

## 13. 生产与本地开发安全

生产环境必须：

- 在提供 H5 前将公开 HTTP 重定向到 HTTPS，然后启用 HSTS（`max-age` 至少 31536000；只有运维条件允许时才包含子域）；
- 设置 Secure Cookie，拒绝明文 API 与 WebSocket 流量，只信任来自已配置 Proxy 的代理元数据；
- Session/Ticket 签名或哈希 Key 和密码 Pepper 不得进入源码管理；
- 日志、Metric Label、Trace、URL 和错误 Body 不得包含 Secret 或凭据派生值。

只有在显式开发 Profile 验证全部 Listener 和发布 URL 都仅限 Loopback 时，本地开发才可以使用 `http://localhost`、Loopback IP 和 `ws://`。由于 `__Host-` Cookie 要求 `Secure`，本地明文使用明确分离、Host-only 的 `cf_session_dev` 和 `cf_csrf_dev` Cookie。开发环境可以省略 HSTS 和 Secure，但必须保留 Session 的 HttpOnly、SameSite、Origin/CSRF 校验、不透明 Session、密码哈希、Ticket 生命周期/单次使用、通用凭据错误和重复登录撤销。任何 Bind 或发布地址超出 Loopback 时，开发 Profile 必须启动失败；绝不能由客户端 Header 开启该 Profile。

## 14. 必需验收与安全测试

实现测试必须证明：

1. Go 与 TypeScript 生成类型可以往返每种 HTTP 消息，并拒绝格式错误、尾随、超限和 Content-Type 错误的 Body。
2. 只有账号已经 ACTIVE、初始 Player 检查点/资源已经持久化且 Session 有效时，注册才返回 201；并发同名注册对外只暴露一个可用账号。
3. 在每个 Provisioning 步骤注入故障时，都不会暴露可认证的部分账号或可用 Session；重试/对账最终收敛且不会重复初始化 Player；同库事务路径和模拟的分 Shard 状态机/Outbox 路径都满足同一对外保证。
4. 密码从不以明文存储/记录；Argon2id 参数、独立 Salt、Dummy Verification 和登录时升级正常。
5. 未知账号、错误密码、禁用账号和非 ACTIVE Provisioning 状态返回同一个通用失败，不存在有意义的人为时间差。
6. 重复登录成功时，在新 Session 可用前撤销旧 Session/Ticket，并用 4409 关闭旧 WebSocket。
7. 空闲与绝对 Session 过期、轮换、Cookie 属性、撤销一致性和合并空闲刷新符合契约。
8. 每个 Mutation 都拒绝缺失/无效 Origin、跨站 Fetch Metadata、缺失/不匹配 CSRF 证明，以及 Session 轮换后的旧 Token。
9. CORS 在 Credentials 模式下绝不使用通配符，未列入 Origin 无法读取或修改 API。
10. 登出幂等，清 Cookie，使 Ticket 失效并关闭连接。
11. Ticket 签发 ID 重放只返回一个有效 Ticket；Payload 改变时冲突；新意图使旧 Ticket 失效。
12. 两次并发消费 Ticket 只能有一次成功；过期、错误 Gateway、已撤销 Session 和重放都通用失败。
13. URL、正常日志、Trace、Metric Label 和错误参数中都没有 Ticket、Cookie、CSRF 值和密码。
14. 生产 Gateway 发现只返回 Allowlist 中的 `wss` 端点，Ticket 绑定阻止跨 Gateway 使用。
15. HTTP Bootstrap 字段与 WebSocket AUTH 的名称/类型/含义完全相同；连接期间发布变化时使用 AUTH 时刻的值。
16. 配置发布前不被 Bootstrap 公布，发布后不可变；执行大小限制，并在摘要、版本、Schema 或解析不匹配时 Fail Closed。
17. 展示配置无法改变服务端价格、状态、奖励、授权或协议接受范围。
18. 限流覆盖文档中的每个 Scope，适用时在昂贵哈希前拒绝，包含重试提示且不产生变更。
19. 重试测试覆盖结果不确定的注册/登录、幂等登出、同 ID Ticket 重试、429/503 退避和禁止 Redirect。
20. 生产拒绝明文传输和不安全 Cookie；开发例外在任何非 Loopback Bind 或发布 URL 下 Fail Closed。
21. 对所有公开 Protobuf Decoder 和错误路径做 Fuzz，不发生 Panic、无界分配、Secret 泄漏或由未知字段产生权威。

## 15. 跨契约一致性

本契约只使用 V3，不恢复 V1 无状态 Zone 或 V2 Journal 行为。

已接受的 `websocket-protocol.md` 一处写“登录返回短期 Ticket”，其重连流程又单独写“HTTP Session 获取新 Ticket”。本契约将其解释为：注册/登录先建立 Session，随后通过已认证的 `/v1/ws-tickets` 调用签发 Ticket；注册/登录响应本身不携带 Ticket。

目前没有发现与已接受 WebSocket 或幂等/错误契约之间不可避免的冲突。WebSocket 游戏命令错误和 Actor 幂等仍由 `idempotency-and-errors.md` 管理；HTTP 错误枚举和 Ticket 签发重放是独立 Scope。
