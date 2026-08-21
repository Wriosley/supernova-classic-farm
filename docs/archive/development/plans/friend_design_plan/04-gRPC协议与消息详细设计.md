---
status: proposed
scope: 好友功能的内部 gRPC 边界、路由和消息语义
---

# 好友 gRPC 协议与消息详细设计

## 1. 传输边界

```text
H5 -> Gate：WebSocket + Protobuf
Gate -> 访客 Zone：gRPC
访客 Zone -> FriendSvr：gRPC
访客 Zone -> 农场主 Zone：gRPC
农场主 Zone -> Gate：gRPC
```

第一版全部使用 Unary RPC。Streaming 留给后续高频 Zone→Gate 广播场景，不改变本期业务语义。

已确认采用完整的游戏内部 gRPC 改造：

- H5 → Gate 继续使用 WebSocket + Protobuf；
- Login 的公开 HTTP、Ticket consume 和 Coordinator 路由 HTTP 本期不改；
- 现有 Gate → Zone 游戏命令从 HTTP + Protobuf 迁移为 gRPC；
- 现有 Zone → Gate Player Push 从 HTTP + Protobuf 迁移为 gRPC；
- 新增 FriendSvr、Zone → Zone 和好友 Push 均使用 gRPC；
- 旧 HTTP 游戏命令和 Push 入口在 gRPC E2E 通过后删除，不长期维护双入口。

## 2. 服务接口

```text
FriendService
- CreateShareCode
- RedeemShareCode
- ListFriends
- CheckMutualFriend

GameCommandService
- ExecutePlayerCommand

VisitorZoneService
- EnterFriendFarm
- HeartbeatFriendFarm
- ExitFriendFarm
- ExecuteFriendAction

OwnerFarmService
- EnterVisitor
- RefreshVisitorHeartbeat
- ExitVisitor
- GetPublicFarmSnapshot
- ApplyVisitorAction

GatePushService
- PublishFarmViewPatch
- PublishFarmPresence
- PublishPlayerStateChanged

PlayerSocialService
- ApplyFriendTaskCredit
```

`ApplyFriendTaskCredit` 由 FriendSvr 在 `FriendRelation` 激活后分别调用双方
玩家的 Owner Zone，以 `relation_id` 幂等推进第二章“加好友”任务。

## 3. 路由规则

| 请求 | Gate 首跳目标 |
|---|---|
| 普通自己农场命令 | `target_player_id` 所在 Zone |
| 好友列表/好友代码 | FriendSvr |
| `ENTER_FARM`、心跳、退出 | 认证访客所在 Zone |
| 投虫、捉虫、清理、偷取 | 认证访客所在 Zone |

访客 Zone 再按 `owner_player_id` 查路由并调用农场主 Zone。H5 不能指定内部 Zone、Gate 或路由元数据。

## 4. 内部身份字段

每个好友内部 RPC 需要携带并验证：

```text
caller_player_id
owner_player_id
visit_id
interaction_id
request_id
gate_id
```

规则：

- `caller_player_id` 只由 Gate 从认证 WebSocket 注入；
- `visit_id` 由 Owner Zone 的 VisitorRegistry 校验；
- `interaction_id` 用于跨 Actor 幂等；
- `gate_id` 只用于 Owner Zone 找到应该下发公开 Patch 的 Gate；
- 客户端请求体中的任何内部身份字段都不可信。

## 5. 内部服务身份认证

最小原型使用 Kubernetes Secret 注入的共享 HMAC key。每个 gRPC 请求的
Metadata 包含：

```text
x-cf-caller-service
x-cf-timestamp
x-cf-nonce
x-cf-body-sha256
x-cf-signature
```

签名覆盖 service、完整 RPC method、时间戳、nonce 和请求体 SHA-256。
服务端必须：

- 使用常量时间比较签名；
- 校验 caller service 是否有权调用该 method；
- 只接受 30 秒时间窗口；
- 在窗口内拒绝重复 nonce；
- 不记录签名、HMAC key、Ticket 或其他可重放 Secret；
- key 只从 Kubernetes Secret 或本地环境变量注入。

HMAC 是当前最小集群方案；生产部署应替换为 mTLS/workload identity。

## 6. 时间、错误和重试

默认 Deadline：

```text
好友校验 / 列表：2 秒
Enter / Heartbeat / Exit：3 秒
访客互动与 Saga 步骤：5 秒
公开 Push：2 秒
```

业务错误必须映射为稳定 Protobuf 错误，例如：

```text
NOT_MUTUAL_FRIEND
VISIT_NOT_FOUND
VISIT_EXPIRED
PLOT_NOT_ELIGIBLE
PEST_ALREADY_PRESENT
STEAL_NOT_AVAILABLE
INSUFFICIENT_ACTION_CHANCE
INVENTORY_CAPACITY_EXCEEDED
INTERACTION_OUTCOME_UNKNOWN
```

网络超时不等于失败。互动遇到结果未知时保留相同 `request_id`，由 Saga 对账，不允许 H5 换新 ID 自动重试。

## 7. 公开消息

```text
FarmVisitSnapshot
- owner_player_id
- farm_view_epoch
- farm_view_seq
- public plots

FarmViewPatch
- owner_player_id
- farm_view_epoch
- farm_view_seq
- changed public plots

FarmPresencePush
- ENTERED / LEFT
- visitor account_name（仅农场主配置允许时）
```

公开消息不得包含农场主金币、背包、章节任务、访客次数、Saga 内部状态或其他访客身份。

