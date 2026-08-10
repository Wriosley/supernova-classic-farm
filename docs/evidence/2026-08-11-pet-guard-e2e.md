---
status: verified
date: 2026-08-11
evidence_type:
  - code
  - test
---

# 宠物最小闭环（护主）

## 完成范围

实现宠物购买、出战、狗粮续时、偷菜护主罚款，以及 H5 文字宠物窗口。

```text
BUY_PET / DEPLOY_PET / BUY_PET_FOOD / FEED_PET / GET_PET_PANEL
-> Player Actor + Checkpoint.pet_state
-> 偷菜 ApplyStealOnOwner 冻结 StealGuardOutcome
-> CommitSteal 原子扣访客金币（最低 0，不转给主人）
```

未做：多宠物编队、战斗、换装、宠物交易、复杂成长。

## 配置（开发快照）

| 项 | 值 |
|---|---|
| 田园犬 pet_id=1 | 5 金币 / 1000 BPS / 罚款 2 |
| 牧羊犬 pet_id=2 | 10 金币 / 1000 BPS / 罚款 4 |
| 狗粮 item_id=1004 / shop=5004 | 5 金币 / 86400s |

概率使用 BPS 整数；`Runtime.randBPS` 可注入（测试强制触发）。

## Checkpoint

`PlayerCheckpointV1.pet_state = 20`（`PetStateRecord`）：

- `owned_pet_ids` 升序唯一
- `active_pet_id==0` 未出战
- `food_active_until_ms` 截止时间；无 Tick
- 新玩家为空（nil）

## 护主冻结

`FriendActionResponse.steal_guard`：

- owner apply：pet_id / config_version / food_active / bps / triggered / penalty_configured
- visitor commit：`guard_penalty_applied = min(coins, configured)`，写入 VISITOR receipt
- 同一 interaction 重试不重抽、不重复扣款

## H5

`web/src/components/PetPanel.vue`：商店条目、出战、狗粮、饱食/剩余时间（仅展示）、护主是否生效（服务端 `guard_buff_active`）。

## 验证

```bash
cd server
go test -race ./internal/player \
  -run 'Pet|PetFood|Feed|Deploy' -count=10
# ok

go test -race ./internal/player ./internal/interaction \
  -run 'PetGuard|Steal|Reconcile' -count=10
# ok

go test -race ./internal/player ./internal/interaction -count=1
go test ./... -count=1
go vet ./...
# ok

cd ../web
npm run typecheck
npm run build
# ok
```

覆盖：金币不足、重复购买、未拥有不能出战、换宠保留狗粮时间、喂食叠 24h、无狗粮失败、无宠/过期不抽奖、田园犬扣 2、牧羊犬扣 4、访客仅 3 金币扣到 0、Saga 重试稳定、flush 失败恢复只扣一次。

## 未重跑

- 完整 live/kind 好友 E2E（与 sprint 01–03 相同，可选本机 dual-zone）
- 服务进程重启后 H5 手工演示（单元已覆盖 Checkpoint round-trip）

## 主要文件

| 文件 | 作用 |
|---|---|
| `proto/.../data_model.proto` | `PetStateRecord` |
| `proto/.../ws.proto` | 宠物 Action / 消息 / `StealGuardOutcome` |
| `server/internal/player/config.go` | 宠物与狗粮配置 |
| `server/internal/player/pet.go` | 命令与护主判定 |
| `server/internal/player/steal_crop.go` | 冻结与扣款 |
| `web/src/components/PetPanel.vue` | 文字面板 |
