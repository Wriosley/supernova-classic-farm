---
status: active
updated: 2026-08-12
---

# 商店种子逐行展开 + 农田旁的护卫狗

## 目标

1. 商店去掉"先在上方白框里点名字选中一种种子、下面只买这一种"的交互，改成所有
   种子都列出来、各自展开各自购买。
2. 画狗：出战的宠物要出现在农田旁边，头顶显示品种，身上显示"xx护卫中（时间：
   xx:xx:xx）"；没喂狗粮时显示"xx现在很饿"并换成沮丧的表情。

## 商店（`web/src/components/ShopPanel.vue`）

- 删掉 `crop-picker` 选中态和全局的 `buyQuantity`/`selectedSeedCropId`。种子目录
  里的每种种子渲染成一行 `.seed-row`：折叠时显示图标、名称、单价、仓库存量，点一
  下展开出成熟时间、产量、数量步进器、合计和购买按钮。
- 数量按 `crop_id` 分别存（`seedQuantities`），默认 3，改一行不会串到另一行；
  可买判定（连接状态、报价 enabled、1–50、仓库上限 300、金币够不够）也按行算。
- 报价仍然优先用 `GetShopResponse` 里的条目，没有时回落到目录自带的
  `seed_unit_price` / `seed_price_version`，和改动前一致。
- 购买中态只压在被点的那一行（比对 `busyAction.seedItemId`），其余行仍可展开查看。
- 种子图标按要求没有换，仍是通用的 `item.demo-seed`。`style.css` 里
  `.crop-picker` / `.crop-chip` 已无人使用，一并删除。

## 狗（美术）

沿用确定性像素脚本（不是图像生成模型），`generate_placeholders.py` 新增
`dog(breed, mood)`，产出 4 张 32×32、bottom-center 锚点：

| 资产 | 对应 |
|---|---|
| `pet.village-dog` / `pet.village-dog-sad` | 田园犬（pet 1），黄褐毛 |
| `pet.shepherd-dog` / `pet.shepherd-dog-sad` | 牧羊犬（pet 2），黑白毛 + 白眉心 |

情绪不靠文字：吃饱是立耳、翘尾、张嘴吐舌；饿了是垂耳、垂尾、八字眉、`^ ^` 眼和一
滴眼泪。审阅图在 `references/pets-4x.png`，`inventory.md` 和
`licenses/SOURCES.md` 已登记。

## 狗（H5）

- 新增 `web/src/lib/pet-art.ts`：`petSprite(petId, hungry)`，未知 pet_id 回落到
  田园犬，服务端加宠物时不会渲染空图。
- `FarmDashboard.vue` 把 4×4 草地和狗一起放进 `.farm-yard`（flex，窄屏换行到草地
  下方），狗在草地右侧、不遮农田：头顶 `.farm-pet__breed` 是品种，身下
  `.farm-pet__status` 是状态文案。饿了整块牌子转成暖红色，点狗直接打开宠物抽屉。
- 饱食判定和倒计时都用 `food_active_until_ms` 与已有的 `nowMs` 每秒对比，所以
  倒计时会自己走；喂食/派出返回的面板会刷新 `petPanel`，狗即时跟着变。
- `App.vue` 用 `activePet` 计算属性把 `petPanel.activePetId` 映射成
  `{petId, name, foodActiveUntilMs}`。宠物面板本来就在快照之后随登录一起拉
  （`refreshPetPanel()`），所以没开过宠物抽屉也能看到狗。

## 验证

```bash
python3 frontend/src/assets/art/tools/validate_art.py    # 44 assets / 44 PNG，通过
cd web && npm run typecheck && npm test && npm run build  # 全绿（4 文件 / 20 条）
```

新增/扩充的单测：

- `web/src/__tests__/shop-panel.spec.ts`：没有 `.crop-picker`；两种种子各一行；展开
  第二行后买到的是土豆（`shopEntryId 5002`/`seedItemId 1003`/数量 3）；两行数量互不
  影响。
- `web/src/__tests__/farm-dashboard.spec.ts`：没有出战宠物时不渲染 `.farm-pet`；
  牧羊犬吃饱时文案是"牧羊犬护卫中（时间：01:02:05）"且用 `shepherd-dog.png`；田园犬
  过期后带 `hungry` 类、文案"田园犬现在很饿"、换 `village-dog-sad.png`。

## 仍是假设 / 待人工确认

- 仍然没有浏览器截图（本机 Playwright 的 chromium 缺 `libasound.so.2`），32 px 狗
  在真机上的可读性需要 owner 看一眼。
- 好友农场（`FriendFarmDashboard.vue`）通过 `FarmVisitSnapshot.pet` 显示对方狗；
  无出战宠物时显示空栏「尚未获得宠物」。详见
  `docs/archive/evidence/historical/2026-08-12-friend-farm-pet-badge.md`。
- 文案直接用服务端的宠物名（"田园犬"/"牧羊犬"），没有再拼一个"狗"字。
