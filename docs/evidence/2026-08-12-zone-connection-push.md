---
status: verified
date: 2026-08-12
evidence_type:
  - code
  - test
---

# Zone 连接注册与通用 Push

## 完成范围

Gate AUTH 后向 Owner Zone 注册玩家 WebSocket 连接；Zone 维护内存 `ConnectionRegistry` 与通用有界 `push.Dispatcher`；FarmView Broadcaster 改为按连接表解析 Owner Gate，去掉固定 `ownerGateID`。

```text
Gate AUTH
-> connection_id
-> RegisterPlayerConnection (Owner Zone)

每 30s / 成功命令
-> RefreshPlayerConnection（缺失则重新 Register）

断线
-> UnregisterPlayerConnection（旧 connection_id 不删新连接）

FarmView Event
-> farmview.Dispatcher
-> Broadcaster(ConnectionRegistry + VisitRegistry)
-> GateClientResolver / GRPCPushForwarder
```

## 关键组件

| 组件 | 路径 |
|---|---|
| ConnectionRegistry | `server/internal/connection/` |
| PlayerConnectionService | `proto/.../runtime.proto` + `cmd/zone/connection_rpc.go` |
| Gate 客户端 | `server/internal/gateway/grpc_connection.go` + `connection_lifecycle.go` |
| 通用 Push | `server/internal/push/`（`GateClientResolver`、`RedDotChanged` stub） |
| FarmView | `farmview.Broadcaster` 使用 `ConnectionLister` |

## 约束核对

- 连接不进入 Player Checkpoint
- 每 Zone 一个 FarmView Dispatcher + 一个通用 push.Dispatcher
- 当前单 Gate：`StaticGateResolver`；接口按 `gate_id` Resolve
- Push 失败不回滚业务；队列满丢弃并计数
- 访客仍用 VisitRegistry 的 `GateID`（跨 Zone 访客不在本 Zone 连接表）

## 验证

```bash
cd server
go test -race ./internal/connection ./internal/farmview ./internal/push ./internal/gateway ./internal/player ./cmd/zone ./cmd/gate -count=1
go test ./... -count=1
go vet ./...
# ok
```

## 未重跑

- kind / 真机 Gate AUTH → Zone 连接表 → FarmView 推送的现场联调（与近期 sprint 相同，以单测 + 构建为准）

## 下一子计划

`04-3D-InfoSvr红点通知.md`（依赖本阶段的连接注册与 `push.Dispatcher` / RedDot stub）。
