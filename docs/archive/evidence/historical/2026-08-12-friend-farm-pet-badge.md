---
status: active
updated: 2026-08-12
---

# 好友农场可见护卫狗

## 目标

访客进入好友农场时，也能在农田旁看到对方出战宠物的状态；若对方没有出战狗，
仍保留宠物栏，文案为「尚未获得宠物」。

## 改动

### 协议

`FarmVisitSnapshot` 新增公开字段 `PublicPetView pet`：

- `active_pet_id` / `pet_name` / `food_active_until_ms`
- 不含狗粮库存、已购未出战列表等私有信息
- `active_pet_id = 0` 表示空栏（客户端显示「尚未获得宠物」）

### 后端

`player.publicFarmSnapshot` / `BuildPublicFarmSnapshot` 从 `PetState` + 配置解析
品种名写入 `pet`。新增回归：`TestBuildPublicFarmSnapshotExposesDeployedPetOnly`。

### 前端

- 抽出共用 `FarmPetBadge.vue`（自家可点开宠物抽屉，好友只读）
- `FriendFarmDashboard` 用 `snapshot.pet` 渲染宠物栏；无狗也显示空栏
- 自家农田同样始终显示宠物栏（无狗时「尚未获得宠物」）

## 验证

```bash
cd server && go test ./internal/player/ -run BuildPublicFarmSnapshot -count=1
cd web && npm run typecheck && npm test
```

## 仍是假设

- 访问中对方喂食/换狗不会实时推送到访客（`FarmViewPatch` 只带地块）；需重新进入
  或另做宠物公开推送才刷新。
- 「尚未获得宠物」覆盖「有狗但未派出」的情况，与访客视角一致（农田旁没有狗）。
