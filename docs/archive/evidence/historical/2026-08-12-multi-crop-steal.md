---
status: verified
date: 2026-08-12
evidence_type:
  - code
  - test
  - build
---

# 04-5 好友偷菜规则收口

## 完成范围

全部 11 种作物可偷；偷菜请求携带 `expected_crop_item_id` + farm view 版本；
`FriendInteraction` 持久化作物/数量；Owner 权威校验作物一致、同访客本轮一次、
保底产量；H5 从公开视图发起偷菜。

```text
H5 STEAL_FRIEND_CROP(expected_crop_item_id, farm_view_epoch/seq)
  -> Gate -> Visitor Zone StealSaga
  -> ReserveSteal(crop=expected, qty=1)
  -> Owner ApplyStealOnOwner(crop match + visitor once + epoch)
  -> CommitSteal
  -> FarmViewPatch (steal_count / can_steal / crop_item_id)
```

## 规则

- 每次成功偷取数量固定 `1`
- `protected = ceil(base_yield/2)`，`max_steal_times = base_yield - protected`
- 演示作物 base=3 → protected=2、max=1（历史已种地块保留原冻结值）
- 同访客同地块同轮作物最多一次（`steal_visitor_player_ids`）

## 协议

- WS `StealFriendCropRequest`：`expected_crop_item_id`、`farm_view_epoch`、`farm_view_seq`
- RPC `ExecuteFriendAction` / `ApplyVisitorAction` 同步字段
- `PublicPlotView`：`crop_item_id`、`steal_count`
- Tcaplus `FriendInteraction`：`crop_item_id`、`quantity`、farm view 字段
- RequestDigest 纳入作物与视图版本

## 验证

```bash
cd server
go test ./internal/player ./internal/interaction ./cmd/zone
go test ./... -count=1
go vet ./internal/player ./internal/interaction ./cmd/zone ./internal/gateway
cd ../web && npm run typecheck && npm run build
# ok
```

覆盖：产量 3/4/5/6 保底表、11 作物可偷配置、作物不一致拒绝、同访客二次拒绝、
Saga/Reconciler、Gate 校验、H5 typecheck/build。

## 未重跑

- kind 多客户端 FriendInteraction E2E 脚本（需集群）
- 真实 Tcaplus 表结构变更后的建表/迁移

## 下一事项

继续交付冲刺剩余项（如 04-3 阶段 E2E，若尚未完成）。
