---
status: verified
date: 2026-08-10
parent:
  - ./04-3-邮件与通知阶段总计划.md
depends_on:
  - ./03-广播与业务解耦.md
---

# Zone 连接注册与通用 Push Implementation Plan

> **For agentic workers:** 每次修改前检查当前 FarmView Dispatcher 实现，保留 03 阶段已经验证的领域事件和广播语义。

**Goal:** Gate 向 Owner Zone 注册玩家连接；Zone 通过一个通用有界 Dispatcher 向当前 Gate 推送 FarmView、红点和其他通知。

**Architecture:** 每个 Zone 维护自己拥有玩家的临时 `ConnectionRegistry`，每个 Zone 只有一个 Dispatcher。连接信息仅保存在内存，通过 Gate 心跳刷新，不进入 Player Actor 或 Checkpoint。

## 约束

- 当前只有一个 Gate，但接口支持未来多个 Gate。
- `player_id -> gate_id` 不持久化。
- `connection_id` 必须唯一，防止旧连接删除新连接。
- Register/Refresh 必须校验 Player Shard、Owner Zone、Epoch、Lease。
- Push 失败不能回滚业务。
- 队列和 Worker 必须有界。
- 不修改 03 阶段的 DomainChanges 和 FarmViewEvent 生成。

## 文件范围

```text
proto/classicfarm/v1/rpc/internal.proto
server/internal/connection/
server/internal/farmview/dispatcher.go
server/internal/farmview/broadcast.go
server/internal/player/grpc_push.go
server/internal/gateway/
server/cmd/gate/
server/cmd/zone/
```

实际 Protobuf 文件以当前仓库为准。

## Task 1：Connection Registry

- [x] 新增内存连接记录：

```go
type PlayerConnection struct {
    PlayerID     uint64
    GateID       string
    ConnectionID string
    ExpiresAt    time.Time
}
```

- [x] 实现：

```go
Register(PlayerConnection)
Refresh(playerID, gateID, connectionID, expiresAt)
Unregister(playerID, gateID, connectionID)
List(playerID) []PlayerConnection
EvictExpired(now) []PlayerConnection
RemoveShard(shardID)
```

- [x] 同一连接重复 Register/Refresh 必须幂等。
- [x] 旧 connection ID 的 Unregister 不得删除新连接。
- [x] 同一玩家允许多个 Gate/连接。
- [x] 返回结果按 `(gate_id, connection_id)` 稳定排序。
- [x] 编写并发和 race 测试。

## Task 2：Gate 注册、心跳和注销

- [x] 增加内部 RPC：

```text
RegisterPlayerConnection
RefreshPlayerConnection
UnregisterPlayerConnection
```

- [x] Gate AUTH 后通过 RouteCache 找到 Owner Zone并注册。
- [x] 每 30 秒刷新连接，Zone 租约 90 秒。
- [x] 每次普通命令转发成功时可顺便刷新。
- [x] WebSocket 断开时使用完整三元组注销。
- [x] Zone 返回 `NOT_OWNER` 时 Gate 刷新 Route 并重试一次。
- [x] Zone 重启后心跳能够自动重新注册。
- [x] Shard 迁移后旧 Zone 清理记录，Gate 向新 Zone 注册。

## Task 3：通用 Push Event

在保留 FarmView Event 的基础上增加通用事件：

```go
type PushEvent struct {
    NotificationID    string
    RecipientPlayerIDs []uint64
    Payload            PushPayload
}
```

`PushPayload` 至少支持：

```text
FarmViewPatch
RedDotChanged
PlayerStateChanged
FarmPresenceChanged
```

- [x] Dispatcher 根据 recipient 查询 Connection Registry。
- [x] 按 `gate_id` 分组。
- [x] 同一 Gate/玩家/连接去重。
- [x] 当前单 Gate 继续复用现有 `GRPCPushForwarder`。
- [x] 抽象 `GateClientResolver`，不在业务接口中写死唯一 Gate。
- [x] 队列满、玩家离线或 Push 失败时记录 dropped/failed，不阻塞业务。
- [x] Close 时停止接收并有界排空。

## Task 4：FarmView 迁移到通用 Dispatcher

- [x] Visit Registry 只负责返回 visitor player IDs。
- [x] owner + visitor IDs 交给通用 Dispatcher。
- [x] Dispatcher 使用 Connection Registry 查询 Gate。
- [x] 删除 FarmView 对固定 `ownerGateID` 的依赖。
- [x] 保留 FarmView sequence gap 和快照恢复。
- [x] 运行三客户端广播回归。

## Task 5：验证

```bash
cd server
go test -race ./internal/connection ./internal/farmview ./internal/gateway ./internal/player -count=1
go test ./... -count=1
go vet ./...
```

E2E：

```text
Gate AUTH
-> Zone出现连接记录
-> FarmView正常Push
-> Gate心跳刷新租约
-> 旧连接断开不删除新连接
-> Zone重启
-> Gate重新注册
-> Push恢复
```

创建：

```text
docs/archive/evidence/historical/2026-08-12-zone-connection-push.md
```

## 完成检查

- [x] 每 Zone 一个 Dispatcher；
- [x] 不为每 Actor 创建 Dispatcher；
- [x] 连接不进入 Checkpoint；
- [x] 心跳和超时清理通过；
- [x] 旧连接不能误删新连接；
- [x] FarmView 单 Gate 回归通过；
- [x] 接口支持未来多 Gate；
- [x] Evidence 和 `CURRENT.md` 更新。