---
status: active
updated: 2026-08-11
---

# 16 块农田与草地化农田 UI

## 目标

1. 新玩家的初始农田从 4 块扩到 16 块，老账号登录后自动补齐；
2. 农田不再用大边框包起来：地块没有独立卡片框，文字直接压在地块美术上，
   整片农田显示为一块绿色草地。

## 改了什么

### 服务端

- `server/internal/player/state.go`：`InitialPlotCount 4 → 16`；新增
  `State.ensureInitialPlots()`，补齐缺失的 `plot_id`，已有地块（含生长中的作物）
  原样保留。
- `server/internal/player/runtime.go`：`activateActor` 在 owner epoch 采纳之后调用
  `ensureInitialPlots()`。补地会把 `CheckpointRevision +1` 并 `markDirty`，由既有的
  Dirty 刷盘写回 Tcaplus/MySQL。这是惰性迁移：不需要停机脚本，老账号下一次激活时补齐。
- 数据契约没有变化：`PlotStateRecord` 结构不变，只是数量变多；`ValidateCheckpoint`
  对地块数量本来就没有上限。

### H5

- `web/src/style.css`：`.plots-grid` 变成 4 列并自己画草地（原来草地画在
  `.farm-stage` 上），宽度取 `min(100%, 44rem, calc(100vh - 18rem))`，保证 4 行方形
  地块连同顶栏和两条底栏能一屏放下；720px / 440px 断点降到 3 列 / 2 列。
- `.plot-tile` 去掉边框、米色底和内边距框，改成正方形透明块；选中/可用态用
  `::before` 的内描边代替原来的边框变色。
- `.plot-stage` 从"带边框的舞台盒"变成铺满地块的绝对定位层，土地精灵直接落在草地上；
  `.plot-stage::after` 的沙土条删掉。
- 地块文字放进新的 `.plot-caption`（`FarmDashboard.vue` 与 `FriendFarmDashboard.vue`
  同步改模板），白字 + 深色描边压在美术上方，说明文字最多 2 行。
- 种子栏只显示仓库里真正有的种子：`seedCrops` 在 `seedShopEntryId > 0` 之上再按
  `quantity > 0` 过滤，去掉了置灰 chip 和 `:disabled`。目录没加载出来时仍显示
  "种子目录未加载，点此重试"；目录正常但一颗种子都没有时显示 "仓库里还没有种子"，
  两者不能混为一谈。

## 验证

```bash
cd server && go vet ./... && go test ./...     # 全绿
cd web && npm run typecheck && npm test        # 全绿（3 个测试文件 / 13 条）
```

新增测试：

- `server/internal/player/runtime_activation_test.go`
  `TestActivationBackfillsPlotsAddedByLaterBuild`：只有 4 块地的老存档激活后变成
  `InitialPlotCount` 块，原有地块对象未被替换，`CheckpointRevision` +1，
  `flushDirty` 写回一次。
- `web/src/__tests__/farm-dashboard.spec.ts`：16 块地全部渲染；地块文案位于
  `.plot-caption` 覆盖层内；种子栏只列出持有的种子，且没有种子时给出"仓库里还没有种子"
  而不是"目录未加载"。

真机链路验证（2026-08-11 21:3x，本机 `./start-servers.sh --dual-zone --tcaplus` 八服务全
Ready 后）：一次性 E2E 用例走 register → bootstrap → ticket → AUTH →
`GET_PLAYER_SNAPSHOT`，新账号 `player_id=89` 返回 `plots=16`，`plot_id` 连续 1..16 且全部
`EMPTY`。用例是临时的，验证完已删除。

注意：**必须重启后端**才能看到 16 块地。第一次改完没重启，跑的还是旧二进制，新账号仍然
只有 4 块。

`server/test/e2e/authenticated_snapshot_test.go` 里的地块断言已从写死的 4 改为
`player.InitialPlotCount`；该用例目前仍会失败，但原因与本次无关——它断言 `GET_SHOP` 只有
3 个条目，而多作物之后开发配置已有 24 个条目，属于既有的过期用例。

## 仍是假设 / 待人工确认

- 视觉效果没有截图证据：本机 Playwright 的 chromium 缺 `libasound.so.2`，未安装系统依赖。
  需要 owner 在浏览器里确认 4×4 草地排布、文字可读性和不同分辨率下的断点。
- 老账号补地只有单测证据：真机上没有可登录的 4 块地旧账号（密码在 owner 手里），需要
  owner 用自己的老号登录一次确认补到 16 块。
- 老账号补地依赖 Dirty 刷盘成功写回；如果玩家在补地后立刻断开且刷盘失败，下次激活会
  再补一次（幂等，可接受）。
- 16 块地会让章节任务和经济节奏变松（更多同时种植的地块），本次没有调整任何数值。
