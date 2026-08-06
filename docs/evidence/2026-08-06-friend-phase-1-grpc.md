---
status: verified
date: 2026-08-06
evidence_type:
  - code
  - runtime
  - kubernetes
  - security
---

# 好友功能阶段 1：内部 gRPC 与 HMAC 身份认证

## 完成范围

- Gate → Zone 普通游戏命令改为
  `GameCommandService.ExecutePlayerCommand` Unary gRPC；
- Zone → Gate `PLAYER_STATE_CHANGED` 改为
  `GatePushService.PublishPlayerStateChanged` Unary gRPC；
- 删除运行时旧 `/internal/v1/command` 和
  `/internal/v1/player-state-changes` HTTP 入口及适配器；
- Login 公开 HTTP、Ticket consume、Coordinator 路由/迁移 HTTP 保持不变；
- gRPC 与现有 HTTP 健康检查/生命周期接口通过 h2c 共享 8081/8082；
- 本机脚本注入仅用于开发的 HMAC key；Kubernetes 使用独立
  `classic-farm-internal-rpc` Secret。

## HMAC 验证

Unary interceptor 覆盖：

```text
caller service
full RPC method
timestamp
nonce
deterministic protobuf body SHA-256
HMAC-SHA-256 signature
```

服务端使用 method/caller allowlist、30 秒时间窗、nonce 防重放和常量时间
签名比较。单元测试确认有效请求通过，伪造 caller、过期 timestamp、重复
nonce、错误签名和被修改的请求体均被拒绝。

## 质量门

以下检查通过：

```text
buf lint
go test ./...
go vet ./...
bash -n start-servers.sh
kubectl kustomize deploy/k8s
npm run build
```

新增 gRPC adapter、Zone command server、Gate push server 和真实 gRPC
client/server 测试均通过。

## 本机纯 Tcaplus 双 Zone

使用新的 `start-servers.sh --dual-zone --tcaplus` 启动五进程栈后，
`TestDualZoneRoutingAndCache` 通过。验证覆盖：

- Gate RouteCache 首跳与 stale-route refresh；
- 错 Zone 的 gRPC `FailedPrecondition` 拒绝；
- 非活跃和活跃 Shard 迁移；
- Player Actor 状态和 epoch 延续；
- Zone → Gate Player Push。

## Kubernetes

重新构建并加载 Gate/Zone 镜像，创建内部 RPC Secret，滚动更新 Gate、
`zone-a`、`zone-b`。五个 Deployment 全部 Ready，三个新 Pod 重启次数
均为 0。通过端口转发运行同一个双 Zone E2E，结果通过；工作负载日志未
发现 gRPC 身份认证或业务错误。

## 明确未实现

- 未创建好友 Tcaplus 表；
- 未实现或部署 FriendSvr；
- 未启用好友代码、好友列表、农场访问或互动 Saga；
- HMAC 仍是最小集群方案，生产环境需要 mTLS/workload identity。
