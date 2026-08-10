---
status: verified
date: 2026-08-11
evidence_type:
  - code
  - test
---

# 用户生涯、作物图鉴与多作物配置

## 完成范围

实现公开用户生涯、农场主私有图鉴、11 种作物配置驱动，以及 H5 资料窗口/多作物商店与地块展示。

```text
HARVEST 净入仓
-> career.total_harvested_crop_quantity += net
-> crop_compendium unlock crop_id（去重升序）

STEAL visitor commit 净入仓
-> career.total_stolen_crop_quantity += net
-> 不解锁图鉴

GET_SHOP
-> entries + ActiveCropCatalog()（11 种）

PlayerSnapshot：career + crop_compendium
FarmVisitSnapshot：career only
```

未做：多作物偷菜（仍仅原作物可偷，交接 04-5）、作物专属美术、图鉴奖励。

## 配置（开发快照）

| 项 | 说明 |
|---|---|
| 原作物 crop_id=2001 | 种子 1001 / 收获物 1002 / 商店 5001 / 出售 5002；仍是唯一可偷作物 |
| 新增 10 种 2002–2011 | 种子 1005–1014、收获物 1015–1024、商店 5005–5014、出售 5015–5024 |
| `ActiveCropCatalog()` | 按 crop_id 升序；名称/成熟秒/产量/买卖价来自配置 |

校验拒绝重复 crop_id / item_id / shop_entry_id。业务仍走同一套 BUY_SEEDS / PLANT / HARVEST / SELL_CROP。

## Checkpoint

`PlayerCheckpointV1`：

- `career = 21`（`PlayerCareerRecord`）
- `crop_compendium = 22`（`CropCompendiumRecord.unlocked_crop_ids`）
- 旧 Checkpoint 缺字段按零值加载；不做历史回填

## 协议 / 快照

- `PlayerCareerView` / `CropCompendiumView` / `CropCatalogEntryView`
- 私有 `PlayerSnapshot.career` + `crop_compendium`
- 公开 `FarmVisitSnapshot.career`（无图鉴）
- `PlayerStatePatch` 可携带 career / crop_compendium
- `GetShopResponse.crops`

## H5

- `PlayerProfileModal.vue`：生涯；仅自己农场显示图鉴（配置完整列表 + 灰显未解锁）
- 用户名按钮：自己农场 / 好友农场均可打开资料
- 商店按 catalog 选种子购买；种植携带 `seedItemId`；出售按仓库中的作物 item
- 地块展示作物名 + `mm:ss` 倒计时（展示用，成熟权威在服务端）
- 复用同一套作物图片，无作物专用分支

## 验证

```bash
cd server
go test ./internal/player -run 'Career|Compendium|MultiCrop|HarvestAfterSteal|PublicFarmSnapshot' -count=1
# ok：收获累计与解锁、偷后净收获、偷菜不解锁、公开快照无图鉴、多作物买卖种植收获

go test -race ./internal/player ./internal/interaction ./internal/farmview ./internal/visit -count=1
go test ./... -count=1
go vet ./...
# ok

cd ../web
npm run typecheck
npm run build
# ok
```

## 交接给 04-5

`SoleStealableCrop` 仍只允许原作物（2001）进入可偷路径。新增 10 种作物可种植/收获/出售，但访客侧偷菜适配留给 `04-5-好友偷菜规则收口.md`（计划索引已有，正文尚未落地）。

## 未重跑

- kind / 真机双账号端到端演示（与 04-1 相同，以单测 + 构建为准）
