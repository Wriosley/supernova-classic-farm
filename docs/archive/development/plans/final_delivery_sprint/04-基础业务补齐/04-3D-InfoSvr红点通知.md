---
status: verified
date: 2026-08-10
parent:
  - ./04-3-邮件与通知阶段总计划.md
depends_on:
  - ./04-3E-Zone连接注册与通用Push.md
---

# InfoSvr 红点通知 Implementation Plan

> **For agentic workers:** InfoSvr 只负责红点事件转换和 Zone 路由，不保存红点状态，不维护 Gate 连接。

**Goal:** 建立独立 InfoSvr，将邮件和好友成熟事件转换为红点 Push，并通过接收玩家的 Owner Zone Dispatcher 推送。

**Architecture:** InfoSvr 持有与 Gate 相同语义的 Player RouteCache。事件根据 recipient player ID 路由到其 Owner Zone；Zone 使用 Connection Registry 找 Gate。红点允许丢失，不影响权威业务。

## 约束

- InfoSvr 不使用每玩家 Actor。
- InfoSvr 不持久化红点。
- InfoSvr 不维护 `player_id -> gate_id`。
- Coordinator 只提供 `player_id -> owner Zone`。
- `NOT_OWNER` 后刷新并重试一次。
- 红点点击清除发生在 H5 本地。
- 离线玩家的好友成熟红点允许丢失。

## 协议

红点消息建议为：

```proto
message RedDotChanged {
  string notification_id = 1;
  RedDotCategory category = 2;
  RedDotOperation operation = 3;
  optional uint64 source_player_id = 4;
}

enum RedDotCategory {
  RED_DOT_CATEGORY_UNSPECIFIED = 0;
  RED_DOT_CATEGORY_MAIL = 1;
  RED_DOT_CATEGORY_FRIEND_FARM = 2;
}

enum RedDotOperation {
  RED_DOT_OPERATION_UNSPECIFIED = 0;
  RED_DOT_OPERATION_SET = 1;
  RED_DOT_OPERATION_CLEAR = 2;
}
```

内部 RPC：

```text
SetMailRedDot(player_id, notification_id)
SetFriendFarmRedDot(recipient_ids, owner_player_id, notification_id)
```

## 文件范围

```text
server/cmd/info/
server/internal/info/
server/internal/gateway/
server/cmd/zone/
proto/classicfarm/v1/rpc/
proto/classicfarm/v1/ws/
deploy/k8s/
```

## Task 1：InfoSvr 骨架和 RouteCache

- [x] 建立 InfoSvr 启动、健康检查、配置和日志。
- [x] 复用 Gate RouteCache 的 Route Source、Snapshot 和 miss collapse 语义。
- [x] 根据 recipient player ID 计算 Shard。
- [x] 路由到 committed ACTIVE Owner Zone。
- [x] 禁止普通命中路径每次查询 Coordinator。

## Task 2：红点投递

- [x] 接收 Mail red dot 和 Friend Farm red dot。
- [x] 构造不可变 `RedDotChanged`。
- [x] 调用 Recipient Owner Zone 的通用 Push RPC。
- [x] Zone `NOT_OWNER` 时：
  - 失效该 Shard；
  - 刷新 Route；
  - 使用相同 `notification_id` 重试一次。
- [x] 第二次失败记录并丢弃。
- [x] 对 recipient 列表去重和稳定排序。
- [x] 请求数量设置上限，避免单次无限 fan-out。

## Task 3：好友成熟红点

- [x] Owner 地块每次 `GROWING -> MATURE` 只产生一次可偷事件。
- [x] 只有符合可偷配置的地块产生事件。
- [x] 查询 FriendSvr 获取好友 IDs。
- [x] 为好友生成：

```text
category = FRIEND_FARM
source_player_id = ownerID
operation = SET
```

- [x] 同一次成熟转换使用稳定 notification ID。
- [x] 不发送 CLEAR；点击好友按钮后 H5 本地清除。
- [x] 真实是否可偷继续由 Owner Actor 判断。

## Task 4：部署和验证

```bash
cd server
go test -race ./internal/info ./internal/gateway ./internal/player -count=1
go test ./... -count=1
go vet ./...
```

E2E：

```text
InfoSvr收到邮件红点
-> Route到Recipient Owner Zone
-> Zone Dispatcher
-> Gate
-> H5

Shard迁移
-> 旧Zone返回NOT_OWNER
-> InfoSvr刷新
-> 新Zone完成推送
```

创建：

```text
docs/archive/evidence/historical/2026-08-12-infosvr-red-dot.md
```

## 完成检查

- [x] InfoSvr 独立运行；
- [x] 不保存红点；
- [x] 不维护 Gate 映射；
- [x] RouteCache 不逐请求回源；
- [x] `NOT_OWNER` 只重试一次；
- [x] 邮件和好友红点可推送；
- [x] 离线或失败不影响业务；
- [x] Evidence 和 `CURRENT.md` 更新。