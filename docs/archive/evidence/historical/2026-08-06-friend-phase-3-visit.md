---
status: verified
date: 2026-08-06
evidence_type:
  - code
  - test
  - contract
---

# 好友功能阶段 3：访问会话与公开快照

## 完成范围

核心业务包（`server/internal/visit/*`、`server/internal/farmview/*`、
`server/internal/player/farm_visit_snapshot.go`、
`server/internal/gateway/grpc_push.go` 的 `PublishFarmPresence`、
`server/internal/player/grpc_push.go` 的 `PublishFarmPresence`、
`push_hub` 对 `FARM_PRESENCE_CHANGED` 的校验）在本阶段之前已完成并通过
单元测试；本阶段完成的是把这些包接线进真正的 Zone / Gate 进程，并补齐
最小 H5：

- `server/cmd/zone/main.go`：
  - 读取 `FRIEND_RPC_URL`（默认 `http://127.0.0.1:8085`），复用已有的
    `COORDINATOR_URL`；
  - 创建 `visit.NewFriendRPCClient`、`visit.NewZoneOwnerFarmClient`、
    `visit.NewService`、`visit.NewOwnerService`；
  - 启动 `ownerFarmService.RunEvictionLoop` 后台协程；
  - `AllowedCallers` 新增
    `VisitorZoneService.{Enter,Heartbeat,Exit}FriendFarm ← {gate}` 与
    `OwnerFarmService.{EnterVisitor,RefreshVisitorHeartbeat,ExitVisitor,GetPublicFarmSnapshot} ← {zone-local,zone-a,zone-b}`；
  - 注册 `VisitorZoneServiceServer`、`OwnerFarmServiceServer`；
  - 退出时关闭 friend/owner gRPC 客户端。
- `server/internal/gateway/grpc_visitor.go`（新增）：`GRPCVisitorZoneCommander`
  以 `gate` 身份对 `VisitorZoneService` 发起 `Enter`/`Heartbeat`/`Exit`
  RPC，并把结果编组回 `*wsv1.WsEnvelope`（域错误写入 `Error` 字段，
  `codes.FailedPrecondition` 映射为 `ErrNotOwner` 以复用既有重试路径）。
- `server/internal/gateway/grpc_friend.go`（新增）：`GRPCFriendCommander`
  以 `gate` 身份调用 FriendSvr 的 `CreateShareCode`/`RedeemShareCode`/
  `ListFriends`，并把 `friendv1.FriendView` 映射为 `wsv1.FriendView`。
- `server/internal/gateway/gateway.go`：
  - `Config`/`Handler` 新增可选的 `Visitor`/`Friends` 客户端字段；
  - `validateRequestTuple` 新增 `CREATE_FRIEND_CODE`、
    `REDEEM_FRIEND_CODE`、`LIST_FRIENDS`、`ENTER_FRIEND_FARM`、
    `FARM_HEARTBEAT`、`EXIT_FRIEND_FARM` 六个 action 的元组校验；
  - `handleGame` 按 action 分流到新增的 `handleFriendAction`（无
    Shard，直连 FriendSvr，2 秒超时）与 `handleVisitAction`（解析访客
    自己的 Shard 路由，3 秒超时，`ErrNotOwner` 重试一次，与既有 Zone
    命令路径同构）；未配置 `Friends`/`Visitor` 时返回
    `SERVICE_UNAVAILABLE` 而不是 panic，保持旧测试对 `Config{Zone: ...}`
    的兼容性。
- `server/cmd/gate/main.go`：读取 `FRIEND_RPC_URL`，构造
  `NewGRPCFriendCommander`、`NewGRPCVisitorZoneCommander` 并注入
  `gateway.NewHandler`；`AllowedCallers` 为
  `GatePushService.PublishFarmPresence` 放行 `zone-local`/`zone-a`/`zone-b`；
  退出时关闭两个新客户端。
- `start-servers.sh`、`start-servers.ps1`、`deploy/k8s/configmap.yaml`：
  新增/导出 `FRIEND_RPC_URL`，使 Gate 和所有 Zone 在本机 `--tcaplus` 模式
  与 kind 部署下都能解析到 FriendSvr。
- `web/src/lib/ws.ts`：新增 `createFriendCode`、`redeemFriendCode`、
  `listFriends`、`enterFriendFarm`、`farmHeartbeat`、`exitFriendFarm`
  六个请求封装；扩展 Push 分发以识别无版本的
  `FARM_PRESENCE_CHANGED`（此前仅认 `PLAYER_STATE_CHANGED`，否则会被
  当作协议错误断开连接）。
- `web/src/App.vue`：新增“好友与串门”卡片——生成/展示好友码、兑换
  好友码、好友列表、进入好友农场展示公开地块、30 秒心跳定时器、
  `VISIT_NOT_FOUND` 时自动重新进入（应对 Owner Zone 重启导致的内存
  访客表丢失）、切换好友农场前自动 `EXIT_FRIEND_FARM`、农场主收到
  `FarmPresencePush` 时的短暂提示条。原有单人游戏面板行为不变。

## 明确未执行

- `FarmViewPatch` 增量广播（`farm_view_seq`、公开地块变化的实时
  Push）留给阶段 4；本阶段的公开快照只在 `ENTER_FRIEND_FARM` 时一次性
  返回。
- 投虫、捉虫、清理、偷菜等互动 Saga 未实现；
  `ExecuteFriendAction`/`ApplyVisitorAction` 仍保持
  `Unimplemented`（阶段 5、6 范围）。
- 未新增网关和 `friend_rpc` 覆盖“端到端”真实进程联调测试，仍以单元/
  集成测试（`bufconn`、`httptest`、stub 依赖）验收，未跑真实 Tcaplus
  双玩家 E2E。

## 质量门

以下命令均通过：

```text
cd server && go build ./...
cd server && go test ./...
cd server && go vet ./...
cd web && npm run build   # vue-tsc --noEmit && vite build
```

新增测试：

- `server/cmd/zone/friend_rpc_test.go`：`visitorZoneRPCServer.EnterFriendFarm`
  拒绝非法参数与非 Shard Owner（用伪造的 `foreignAuthorization` 让
  `Entry` 返回一个非本 Zone 的 Owner）；`HeartbeatFriendFarm`/
  `ExitFriendFarm` 拒绝非法 `visit_id`；`ownerFarmRPCServer.EnterVisitor`
  拒绝非法参数与过期/伪造的 `CommittedRoute`。
- `server/internal/gateway/gateway_test.go`：
  - `TestValidateRequestTupleAcceptsFriendAndVisitActions` 覆盖六个新
    action 的元组校验（缺 `target_player_id`、缺 payload 均被拒绝）；
  - `TestFriendAndVisitActionsRejectedWhenClientsUnconfigured` 验证
    `Friends`/`Visitor` 未配置时返回 `SERVICE_UNAVAILABLE`；
  - `TestHandleGameRoutesFriendActionToFriendsClient` 验证
    `CREATE_FRIEND_CODE` 走 `Friends` 客户端并把调用者透传；
  - `TestHandleGameRoutesVisitActionToVisitorClientWithNotOwnerRetry`
    验证 `ENTER_FRIEND_FARM` 首次 `ErrNotOwner` 后按同一 `request_id`
    重试一次，与既有 Zone 命令重试语义一致。

## 启动方式

```bash
./start-servers.sh --dual-zone --tcaplus
```

Gate 和每个 Zone 都会解析 `FRIEND_RPC_URL=http://127.0.0.1:8085` 以调用
FriendSvr；Zone 之间通过既有的 `COORDINATOR_URL` 解析访客 Owner 路由。
浏览器打开 `web/`（`npm run dev`）登录两个不同账号后，可在“好友与
串门”卡片中互相生成/兑换好友码，再进入对方农场查看公开地块快照。
