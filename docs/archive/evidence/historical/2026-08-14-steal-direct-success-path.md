---
status: verified
date: 2026-08-14
evidence_type:
  - code
  - test
---

# 偷菜直连成功路径

## 完成范围

按 `docs/archive/development/superpowers/plans/2026-08-14-steal-direct-success-path.md`：

1. 访客 Zone `ExecuteFriendAction(STEAL_FRIEND_CROP)` 直接调用 owner
   `ApplyVisitorAction`（`visit.ZoneOwnerFarmClient`）；
2. owner 继续走 `player.Runtime.ApplyStealOnOwner`（mailbox 内扣菜、写
   OWNER receipt，再 settle/flush）；
3. 成功时把 owner 的 `ResultPayload` / `FarmPatch` 回给前端；
4. 删除偷菜专用 `StealSaga` 启动、`Reconciler` ticker 与
   `withStealSaga` wiring。

**未改：** `APPLY_PEST_TO_FRIEND` / `CATCH_PEST_FOR_FRIEND` /
`HELP_CLEAN_FRIEND_PLOT` 仍走 `ActionSaga`。

## 代码路径

```text
Gate → VisitorZone.ExecuteFriendAction(STEAL)
     → ownerFarmClient.ApplyVisitorAction
     → OwnerFarm.ApplyVisitorAction
     → Runtime.ApplyStealOnOwner (+ Dirty/settle)
     → FriendActionResponse{interaction_id, steal_guard, farm_patch}
```

不再创建 `FriendInteraction` 行，不再 `ReserveSteal` /
`CommitSteal` / release/reconcile。

Working tree relative to `525a02d`（本变更尚未单独 commit）。

## 验证

```bash
cd server
GOFLAGS=-mod=mod GOMODCACHE=/root/go/pkg/mod GOPROXY=off \
  go test -race ./cmd/zone ./internal/player ./internal/visit -count=1
# ok

GOFLAGS=-mod=mod GOMODCACHE=/root/go/pkg/mod GOPROXY=off \
  go test -race ./cmd/zone \
  -run 'TestExecuteFriendActionStealCallsOwnerDirectly|TestVisitorStealFastPathWiresOwnerClient|TestZoneStealFriendCrop' \
  -count=1
# ok
```

`./test/e2e` 中 `TestFriendInteractionE2E` 当前无匹配测试；本环境无法连接
Docker/kind，未采到新的真机单样本延迟。历史真机 Saga 路径样本见
`2026-08-14-friend-action-mail-red-dot-latency.md`（约 410ms）。

## 已知限制

- 本路径成功响应**不再**携带访客库存 `visitor_patch`；访客作物到账 /
  任务推进需后续异步副作用设计（见
  `specs/2026-08-14-asynchronous-friend-action-effects-design.md`）。
- owner 侧 `ApplyStealOnOwner` 仍走现有 sync settle；极端进程崩溃窗口
  与既有 Dirty/flush 语义一致，仍可能丢失未落盘变更。
- 偷菜不再依赖独立 FriendInteraction 恢复链路；遗留表行如存在，不会被
  本 Zone 再 reconcile。
