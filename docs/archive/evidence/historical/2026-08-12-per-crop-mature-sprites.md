---
status: active
updated: 2026-08-12
---

# 每种作物独立的成熟贴图

## 目标

成熟的地块要显示"种下去的那种作物"，而不是所有作物共用一张金色的通用贴图。

## 画法选择

美术走仓库既有的确定性像素管线（`frontend/src/assets/art/tools/generate_placeholders.py`
逐像素绘制 + 固定调色板 + `validate_art.py` 校验 + `manifest.json` 登记），没有用
图像生成模型：

- 运行时基线是 16×16 / nearest，AI 出图缩到 16 px 会糊，且每张的风格、光向、描边
  都对不齐；
- `licenses/SOURCES.md` 要求登记每个运行时衍生物的来源，脚本产出的图是项目自有、
  可复现、可 diff 的，不引入第三方训练/再分发限制。

## 改了什么

### 美术

- 新增 10 张 16×16 成熟贴图：`crop.{carrot,white-radish,corn,tomato,potato,
  eggplant,strawberry,pumpkin,watermelon,grape}-mature`，对应配置里的作物
  2002–2011；2001 演示作物继续用 `crop.demo-mature`。每种作物靠轮廓 + 色相区分
  （16 px 下只有这两样能读出来），光向统一左上。
- `plot.mature` 底图去掉了原来画死的三株金色作物，只保留土床和四个金色"可收"角标。
  否则一块成熟的地会同时出现"通用金色作物"和"真作物"两套东西。
- `source/palette.gpl` 增加了 14 个作物色；`inventory.md` 登记了新资产和 crop_id
  映射；`licenses/SOURCES.md` 的 AI 记录补了这次扩充。
- `references/crop-mature-6x.png` 是新的 6 倍审阅图，只有作物，方便肉眼比对轮廓。

### H5

- 新增 `web/src/lib/crop-art.ts`：`matureCropSprite(cropId)` 按 crop_id 取图，未知
  crop_id 回落到 demo 贴图——服务端加作物时不会让成熟地块变空。
- `FarmDashboard.vue` 和 `FriendFarmDashboard.vue` 的 `MATURE` 分支改用它；成长期仍
  共用 `demo-growing`（本次只改成熟态）。

## 验证

```bash
python3 frontend/src/assets/art/tools/validate_art.py   # 40 assets / 40 PNG，通过
cd web && npm run typecheck && npm test && npm run build # 全绿（3 文件 / 14 条）
```

新增测试 `web/src/__tests__/farm-dashboard.spec.ts`：成熟地块 3（crop 2010）渲染
`watermelon-mature`，地块 7（crop 9999，客户端没有映射）回落到 `demo-mature`。

## 仍是假设 / 待人工确认

- 只有单测和 6 倍审阅图，没有浏览器截图（本机 Playwright 的 chromium 缺
  `libasound.so.2`）。需要 owner 在真机上看一眼 16 px 贴图缩放到地块里的可读性。
- 种子图标、仓库里的作物道具图标仍是通用的 `item.demo-seed` / `item.demo-crop`，
  没有按作物区分。
- 成长中/接近成熟两个阶段仍所有作物共用一张图。
