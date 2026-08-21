---
status: in-progress
date: 2026-08-15
scope: MailSvr / Zone / Coordinator SDK / red-dot delivery
---

# Mail 与好友农场红点直达 Zone 改造计划

## 目标

移除生产调用链对 InfoSvr 红点中转的依赖：

```text
私信创建
-> MailSvr 本地 Coordinator SDK 解析收件人 Shard Route
-> 收件人 Owner Zone
-> Gate
-> H5 邮箱红点

Owner 地块首次成熟且可偷
-> Owner Zone 查询 FriendSvr 获取好友列表
-> Owner Zone 复用本地 Coordinator SDK 解析好友 Shard Route
-> 好友 Owner Zone
-> Gate
-> H5 好友农场红点
```

InfoSvr 的现有 RPC 和实现暂时保留，便于回滚和兼容，但 MailSvr、Zone
不再调用 `SetMailRedDot` / `NotifyOwnerPlotStealable`。本阶段不删除
InfoSvr Deployment，也不改 H5 红点协议。

## 当前问题

- MailSvr 创建私信后同步调用 InfoSvr，InfoSvr 再解析路由并调用 Zone，
  多了一跳和一个可用性依赖。
- Zone 已经知道地块成熟，却把好友查询、路由和投递都交给 InfoSvr。
- InfoSvr 当前按 `owner_endpoint` 合批，但 `DispatchRedDot` 要求请求内所有
  recipient 都属于 `recipient_route.logical_shard_id`；同一 Zone 持有多个
  Shard 时可能混入不同 Shard 并被目标 Zone 拒绝。
- `RED_DOT_CHANGED` 是在线 best-effort Push；离线邮件仍由登录后的
  `CHECK_MAILBOX_INDICATOR` 补偿，好友农场红点仍允许离线丢失。

## 设计边界

### 1. 共用直达投递组件

新增一个不依赖 InfoSvr 的内部组件（建议 `server/internal/reddot`）：

- `RouteResolver`：按 player ID / shard ID 返回 committed ACTIVE Route；
- `ZoneDispatcher`：按 endpoint 调用 `ZoneNotificationService.DispatchRedDot`；
- `Delivery`：去重 recipient，按完整 Shard Route 分组并投递；
- `NOT_OWNER`：使旧 Route 失效或触发 `ForceResync`，重新解析受影响玩家，
  使用相同 `notification_id` 最多重试一次；
- 单组和总 recipient 数设置上限，失败只记录日志，不反向回滚邮件或成熟状态。

分组键至少包含：

```text
shard_id + owner_zone_id + owner_epoch + route_version + owner_endpoint
```

禁止只按 endpoint 合批。

InfoSvr 先继续使用自己的旧实现；确认直达链路稳定后，后续任务再选择让
InfoSvr 复用公共组件或彻底移除其红点接口。

### 2. MailSvr 内置 Coordinator SDK

- Coordinator 协议新增 `SUBSCRIBER_KIND_MAIL`，生成 Go 代码；SDK 对该类型
  固定使用 HMAC caller service `mail`。
- Coordinator Watch/GetSnapshot 的 allowlist 增加 `mail`。
- MailSvr 启动时创建一个 SDK，Subscriber ID 使用稳定的 Mail 实例 ID，
  在 ready 前完成完整 Route Snapshot 首次同步。
- 使用 SDK 的内存 RouteCache 解析收件人，普通红点路径不逐次 HTTP 查询
  Coordinator。
- MailSvr 的邮件领取 `ApplyMailReward` 也改为消费同一个 RouteResolver，
  避免同一进程同时维护 SDK 与旧 HTTP 单 Shard 查询两套路由语义。
- `CreateGiftMail` / `CreateSystemRewardMail` 在私信持久化成功后调用直达
  Delivery。Push 失败不删除邮件、不回滚 source-event dedup、不使创建接口失败。
- 删除 MailSvr 对 `INFO_RPC_URL` 的运行依赖；配置新增
  `MAIL_INSTANCE_ID`、`COORDINATOR_RPC_URL`，部署中明确启用 SDK。

邮件红点不新增持久化：离线或投递失败仍由权威邮箱查询与
`CHECK_MAILBOX_INDICATOR` 修复显示。

### 3. Zone 自己发送好友农场成熟红点

- 保留 `Runtime.forwardMaturityEvents` 产生一次性、稳定 ID 的成熟事件：
  `stealable:{owner}:{plot}:{player_seq}`。
- 用新的 `zoneStealableNotifier` 替换 `infoStealableNotifier`：
  1. `Notify` 只把不可变成熟事件放入有界队列，不等待网络；
  2. 固定数量 worker 调 FriendSvr `ListFriends(owner_player_id)`；
  3. 构造 `FRIEND_FARM + SET + source_player_id=owner`；
  4. 通过共用 Delivery 直达每个好友当前 Owner Zone。
- FriendSvr `ListFriends` 的 HMAC allowlist 增加稳定 caller `zone`；不为每个
  动态 Zone 身份增加配置项。
- Zone 复用启动时已有的 Coordinator SDK/Snapshot，不创建第二个 Watch。
  需要把当前只在 switch 局部变量中的 SDK/RouteResolver 提升为 Zone 进程级
  依赖，同时兼容现有 `http-poll` 模式作为回滚路径。
- 本 Zone 也是目标 Zone 时仍走相同 `DispatchRedDot` 接口，保持
  authorization、connection registry 和 Gate 推送规则只有一份。
- 查询好友、解析路由或投递失败均为 best-effort，不阻塞 Actor mailbox、
  不回滚 `GROWING -> MATURE`。实际可偷性仍由 Owner Actor 在操作时校验。
- 成熟通知队列满时记录指标/日志并丢 Push，不允许反压 Actor；进程关闭时停止
  接收新事件并在有限 shutdown deadline 内处理已入队事件。

### 4. 安全与生命周期

- `ZoneNotificationService.DispatchRedDot` allowlist 增加 `mail` 和稳定 `zone`，
  保留 `info` 兼容调用者。
- SDK 断连超过 freshness TTL 时 Route 解析失败关闭，不使用过期路由猜测投递。
- 所有跨服务 RPC 使用现有 HMAC、有限 deadline 和消息大小上限。
- SDK、Friend client、Zone connection pool 在进程退出时关闭。
- 红点队列满、玩家离线、Gate 不可达均只丢 best-effort Push；权威业务状态
  与邮件数据不受影响。

## 实施任务

### Task 1：抽取并验证直达红点 Delivery

涉及文件：

```text
server/internal/reddot/*                 (新增)
server/internal/info/service.go          (仅在必要时复用小接口，不改外部行为)
server/internal/info/clients.go          (可迁移通用 Zone client)
```

- [ ] 先写测试：同 endpoint 不同 Shard 必须拆成不同请求。
- [ ] 测试 recipient 去重、零 ID、稳定排序和批量上限。
- [ ] 测试 ACTIVE Route 正常投递。
- [ ] 测试 `NOT_OWNER` 触发一次 resync/re-resolve，并保持 notification ID。
- [ ] 测试第二次失败只记录/返回 best-effort 结果，不无限重试。
- [ ] 实现可复用 RouteResolver、ZoneClient 和 Delivery。

### Task 2：给 MailSvr 接入 Coordinator SDK 并直发红点

涉及文件：

```text
proto/classicfarm/v1/coordinator/coordinator.proto
server/gen/classicfarm/v1/coordinator/*
server/internal/coordinatorclient/client.go
server/cmd/coordinator/grpc_wiring.go
server/internal/mail/service.go
server/internal/mail/zone_client.go
server/internal/mail/info_client.go
server/cmd/mail/main.go
server/cmd/mail/*_test.go
deploy/k8s/mail.yaml
deploy/k8s/configmap.yaml
```

- [ ] 先写生成描述符/SDK auth 测试，再增加 `SUBSCRIBER_KIND_MAIL` 并运行 buf
  生成，禁止手改 generated Go。
- [ ] Coordinator 允许 `mail` 获取 Snapshot 和 Watch。
- [ ] MailSvr ready 前启动 SDK，并用统一 Resolver 注入领取与红点投递。
- [ ] 邮件创建成功后直发 MAIL 红点；验证 Push 失败不改变成功响应和邮件记录。
- [ ] 移除 MailSvr 对 InfoClient/`INFO_RPC_URL` 的接线，旧文件是否删除留到
  全链路通过后决定。
- [ ] 验证 SDK stale、NOT_OWNER、重复 notification 和进程关闭。

### Task 3：Zone 直发好友农场红点

涉及文件：

```text
server/cmd/zone/main.go
server/cmd/zone/info_client.go             (替换/退休)
server/cmd/zone/notification_rpc.go
server/cmd/zone/*_test.go
server/internal/player/runtime.go
server/cmd/friend/main.go
deploy/k8s/zone.yaml
```

- [ ] 为 FriendSvr `ListFriends` 增加 `zone` caller 测试及 allowlist。
- [ ] 测试一次成熟事件只构造一个稳定 notification ID。
- [ ] 测试成熟回调只入有界队列，不等待 FriendSvr 或目标 Zone 网络响应。
- [ ] 测试 Zone 查询好友并按好友 Shard 分组直发。
- [ ] 测试无好友、好友查询失败、目标离线和 Zone 投递失败均不影响成熟。
- [ ] 测试队列满时丢弃并记录、关闭时在 deadline 内退出。
- [ ] 测试本地目标与远端目标使用同一 authorization 入口。
- [ ] 在 SDK 模式复用已有 Watch；测试不会创建第二个 SDK。
- [ ] 删除 Zone 主路径对 `INFO_RPC_URL` 和 `infoStealableNotifier` 的依赖。

### Task 4：回归、部署与一次实时链路验证

- [ ] 运行 buf lint/generate 后确认 generated diff 仅来自协议变化。
- [ ] 运行聚焦 race 测试：

```bash
cd server
go test -race -count=1 \
  ./internal/reddot ./internal/coordinatorclient ./internal/mail \
  ./internal/player ./internal/info ./cmd/coordinator ./cmd/mail ./cmd/friend ./cmd/zone
```

- [ ] 构建并 load `coordinator`、`mail`、`friend`、`zone` 镜像，滚动更新；
  本任务不需要新 Tcaplus 表或字段。
- [ ] 确认 MailSvr、Zone 在 Coordinator Watch 中注册且所有 Pod Ready。
- [ ] 创建一封好友礼物邮件，记录：邮件持久化完成时间、Mail 直达 Zone、Gate
  收到 Push、H5 红点出现；只报告单次样本，不宣称 p95。
- [ ] 触发一次自然成熟，确认日志中没有调用 InfoSvr，在线好友收到带
  `source_player_id` 的红点并能进入对应农场。
- [ ] 临时停止 InfoSvr，再重复邮件与成熟用例，证明两条红点主链路不依赖它。
- [ ] 更新 `docs/context/CURRENT.md`，新增
  `docs/archive/evidence/historical/2026-08-15-direct-red-dot-routing.md`。

## 完成标准

- MailSvr 使用本地 Coordinator SDK 路由，邮件红点不经过 InfoSvr。
- Zone 使用已有 Coordinator SDK 和 FriendSvr 好友列表，成熟红点不经过
  InfoSvr。
- 同 endpoint 的不同 Shard 不会被错误合批。
- `NOT_OWNER` 最多刷新重试一次；SDK 过期时失败关闭。
- 邮件写入和地块成熟不会因红点失败而回滚。
- InfoSvr 停止时，两条在线红点链路仍可工作。
- H5 协议与离线邮箱 indicator 兼容行为不变。
- 不新增 Tcaplus 表，不迁移现有数据。

## 暂不处理

- 删除 InfoSvr、Info proto RPC 或其 Deployment。
- 持久化好友农场红点、离线补发好友成熟通知。
- 将红点变成权威业务状态或引入消息队列。
- p95/p99 压测；本任务只跑通并记录单次端到端时间。
