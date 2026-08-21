---
status: verified
date: 2026-08-06
evidence_type:
  - code
  - test
  - contract
---

# 好友功能阶段 2：FriendSvr 与好友关系

## 完成范围

- 控制台已创建好友相关 Tcaplus PB 表：
  `FriendCodeCurrent`、`FriendCodeLookup`、`FriendRelation`、
  `FriendList`、`FriendLinkSaga`、`FriendInteraction`；
- 新增独立 `FriendSvr`（默认 `127.0.0.1:8085`），实现
  `CreateShareCode`、`RedeemShareCode`、`ListFriends`、
  `CheckMutualFriend`；
- `FriendRelation` 是双向好友权威记录；`FriendList` 是带
  `link_id` reservation 的可修复投影；
- `FriendLinkSaga` 完成名额预留、关系激活、双方列表投影和任务推进；
- Zone 新增 `PlayerSocialService.ApplyFriendTaskCredit`，按
  `relation_id` 幂等推进 `TASK_ADD_FRIEND`，并同步 SaveCAS；
- Player Checkpoint 支持 schema v1→v2 懒迁移（好友行动次数与任务回执）；
- 本机 `--tcaplus` 启动脚本与 kind `deploy/k8s/friend.yaml` 已接入。

## 明确未执行

- 未接入 Gate WebSocket 好友码/列表 action 路由（仍属后续阶段）；
- 未实现农场访问、公开快照、互动 Saga；
- 未跑真实 Tcaplus 双玩家 FriendSvr 联机 E2E（本阶段以单元测试与
  进程接线验收为主）。

## 质量门

以下命令均通过：

```text
cd server && go test ./...
cd server && go vet ./...
kubectl kustomize deploy/k8s
```

覆盖验收清单第 1 节对应的单测：代码 TTL、自兑、重复兑换、100 人上限、
Saga 中断后任务推进幂等重试、`CheckMutualFriend` 仅认 ACTIVE 关系。

## 启动方式

```bash
./start-servers.sh --dual-zone --tcaplus
```

FriendSvr 监听 `http://127.0.0.1:8085`，要求 `STORAGE_MODE=tcaplus` 与
`INTERNAL_GRPC_HMAC_KEY`。Gate 调用 Friend 码/列表 RPC 的 allowlist 为
`gate`；Zone 调用 `CheckMutualFriend` 的 allowlist 为
`zone-local`/`zone-a`/`zone-b`。
