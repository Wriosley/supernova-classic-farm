---
status: verified
date: 2026-08-06
evidence_type:
  - code
  - contract
  - test
---

# 好友功能阶段 0：协议与数据契约冻结

## 完成范围

- 新增 `FriendService` 和内部 Runtime gRPC 服务定义；
- 扩展 WebSocket 好友、访问、互动、公开农场消息和稳定错误码；
- 扩展 Player Checkpoint 的行动次数、偷取冻结参数、Saga reservation、
  interaction receipt 和好友任务幂等 receipt；
- 新增 `FriendCodeCurrent`、`FriendCodeLookup`、`FriendRelation`、
  `FriendList`、`FriendLinkSaga`、`FriendInteraction` Tcaplus PB 表结构；
- 定义第二章“加好友、偷菜、投虫”三个目标为 1 的任务；
- 配置 Go gRPC 与 Go/TypeScript Protobuf 代码生成；
- 固化 HMAC Metadata、Deadline、重试和公开数据边界。

评审补充了三个恢复必需字段：按 `relation_id` 持久化的好友任务回执、
`FriendInteraction.pest_id` 和 `EnterVisitorRequest.request_id`。

Tcaplus 控制台校验表描述文件时拒绝 proto3 显式 `optional`，因此
`FriendInteraction.pest_id` 改为普通 `uint32`，零值表示该互动不携带
害虫。内部 gRPC 契约不受影响。

## 质量门

以下命令均通过：

```text
buf lint
buf generate
cd deploy/tcaplus && buf lint && buf generate
cd server && go test ./...
cd server && go vet ./...
cd web && npm run build
cd web && npx tsx src/gen/smoke/roundtrip.ts
```

Go 和 TypeScript 生成物已更新；TypeScript Protobuf round-trip 通过。

## 明确未执行

- 未启动 Login、Gate、Coordinator、Zone 或 FriendSvr；
- 未创建或修改真实 TcaplusDB 表；
- 未实现 gRPC Handler、HMAC interceptor、FriendSvr 或好友业务运行时；
- 未部署 Kubernetes 工作负载。

这些实现工作从阶段 1 开始。
