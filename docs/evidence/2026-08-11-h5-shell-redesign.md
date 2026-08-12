---
status: verified
date: 2026-08-11
evidence_type:
  - code
  - build
  - live
---

# H5 外壳重构：导航栏 + 农田常驻 + 单表单登录

## 完成范围

H5 从"若干诊断卡片竖排"改成一个游戏外壳：登录页只剩标题与账号密码，
登录后农田常驻主区域，其余功能都在顶栏入口后的抽屉里，农田下方是常驻的
工具栏与种子栏。没有任何服务端改动。

```text
未登录  login-shell
  Grow! + 账号 + 密码 + 一个按钮

已登录  game-shell
  TopNav      用户名(图鉴) · 金币 · 账号/商店/宠物/好友/邮箱/任务/仓库
  FarmDashboard
    plots-grid          四块地，常驻可见
    farm-bars(sticky)   工具栏：手 / 铲子 / 杀虫剂 / 肥料
                        种子栏：目录内全部种子，0 数量置灰，悬浮显示成熟时间
  GameDrawer            右侧抽屉，无遮罩，农田保持可见可点
```

## 组件划分

原 `FarmDashboard.vue` 同时承担钱包、工具栏、农田、商店、仓库、章节六件事。
现在它只负责农田与两条底栏，其余拆成独立面板，由 `App.vue` 装进抽屉：

- `TopNav.vue`：用户名（打开图鉴）、金币、七个入口，邮箱/好友/任务带红点
- `GameDrawer.vue`：通用抽屉；**故意不加遮罩**，否则农田会被挡住且不可点
- `ShopPanel.vue` / `InventoryPanel.vue` / `TaskPanel.vue`：从旧侧栏拆出
- `AccountPanel.vue`：Session、重连/断开/退出，连接阶段与 Actor 诊断折叠在
  `<details>诊断</details>` 里
- `FriendsPanel.vue`：好友码、兑换、好友列表（兑换输入改为组件本地状态）
- `MailboxPanel.vue`：去掉自带 modal 外壳，改为抽屉内容
- 共享类型移到 `lib/farm-actions.ts`，面板 ID 与标题移到 `lib/panels.ts`

「杀虫剂」是旧「捉虫」的改名，动作仍是 `catch`；种子不再占工具位，点种子栏
即切到种植模式。种子栏保留 0 数量的种子（置灰不可点），因为它同时是玩家了解
「有哪些作物、要长多久」的地方。

## 登录：有则登、无则注

服务端不区分「账号不存在」和「密码错误」，两者都回 `INVALID_CREDENTIALS`，
所以前端先 login，失败再 register；register 撞上 `ACCOUNT_NAME_UNAVAILABLE`
说明账号已存在，即密码错误。CSRF token 不是一次性的（`ValidateCSRF` 只在过期
时删除），两次请求可复用同一个 token。

对运行中的 LoginSvr 实测：

```text
1) login  未注册账号        -> 401 INVALID_CREDENTIALS(201)
2) register 复用同一 csrf   -> 201 Created            # 新账号自动注册
3) login  错误密码          -> 401 INVALID_CREDENTIALS(201)
4) register 已存在账号      -> 409 ACCOUNT_NAME_UNAVAILABLE(204)  # 提示「密码错误」
5) login  正确密码          -> 200 OK
```

## 补丁：断线时外壳必须自己说出来

第一版把连接状态藏了起来，联调立刻踩到：WebSocket 断开后，农田仍然留在屏幕上，
七个抽屉照常打开，但里面全是空列表和禁用按钮，种子栏永远停在「加载中」，
错误只写在 `errorMessage` 里而它只在「账号」抽屉可见。表现出来就是「所有按钮
都点不动、导航栏没反应」。三处修正：

- `FarmWebSocket` 增加 `setConnectionHandler`。这个类刻意不是响应式的，
  `socket.connected` 只在别的状态变化触发重渲染时才被重新读取，socket 在两次
  渲染之间死掉就完全看不出来。现在 open/close/disconnect 回调写进一个
  `connected` ref，模板一律用它。
- 外壳顶部常驻一条状态条：断线显示「实时连接已断开」+「重新连接」，
  连接中显示当前阶段，失败显示原因。
- `establishSnapshot` 拿到快照后先 `phase='ready'`，再加载商店/宠物/好友/红点；
  这些副加载失败不再往外抛（否则快照已写入、外壳已进入，异常却没人看得见）。
  商店失败写进状态条，种子栏空时给出「点此重试」。

顺带去掉了「任务」入口上的红点：那是第一版自作主张加的，需求里只有邮箱和好友
两个红点，而且它把「章节可领奖」当成了通知。

## 验证

```bash
cd web && npm run typecheck && npm run build
# ok；顺带清掉旧布局遗留的死 CSS（26.5 kB -> 23.7 kB）

cd web && npm test
# vitest + happy-dom，挂载真正的 App.vue（http/ws 两个模块用假实现替掉），
# 走完登录握手进入外壳，然后逐个点七个导航按钮：
#   - 每次点击都要出现 .drawer 且 aria-label 等于对应标题，再点一次要关闭
#   - 种子目录要加载出作物，不能停在「种子目录未加载」
#   - socket 断开后要出现「实时连接已断开」状态条
# 4/4 通过。
```

## 补丁二：BigInt 崩溃，以及一个从未生效的 typecheck

红色横幅上线后第一次登录就抓到真错误：`Cannot mix BigInt and other types`。
`CropCatalogEntryView.maturity_seconds` 是 uint64，protobuf-es 生成 `bigint`，
而种子栏把它传进了 `formatDuration(seconds: number)`，`Math.floor(bigint / 60)`
直接抛。种子栏每帧都会为每个作物算一次，所以一登录就死。

真正该被追问的是：`vue-tsc` 为什么没拦住。答案是 **`npm run typecheck` 一直在检查
0 个文件**（`--listFiles` 输出 0 行）。根 `tsconfig.json` 是 `"files": []` + `references`，
而 references 只有 `--build` 模式才会跟进，普通 `--noEmit` 等于什么都没检查。
指向 `tsconfig.app.json` 后立刻暴露 44 个存量错误，其中包括：

- `MailboxPanel.vue` 用了 `MailKind.MAIL_KIND_PUBLIC/PRIVATE/GIFT`——protobuf-es
  会裁掉枚举前缀，正确写法是 `MailKind.PUBLIC/PRIVATE/GIFT`。这和之前 `RedDotCategory`
  那个红点 bug 是同一个坑，邮箱的筛选和类型标签一直是坏的。
- `ws.ts` 里 31 处 `sendGameRequest(..., { case, value: {...} })`：参数类型写成了
  `WsEnvelope['payload']`（构造完成的消息），但调用方传的是 init 对象，应为
  `MessageInitShape<typeof WsEnvelopeSchema>['payload']`。
- `hash.ts` 把 `Uint8Array<ArrayBufferLike>` 交给要求 `BufferSource` 的 WebCrypto。
- 三个既有 `*.test.ts` 因为缺 `types: ["node"]` 而报错。

44 个已全部修完，`npm run typecheck` 现在真的会检查（app + node 两个工程都跑）。

## 为什么加这套测试

排查「点导航没反应」时，第一版测试里我把 `downloadClientConfig` 的假返回值写漏了
一个字段，于是「账号」抽屉渲染时抛 `Cannot read properties of undefined`。Vue 打印
两行 warning 之后就再也不 patch 了——农田停在最后一帧，七个按钮全部失灵。这正是
用户看到的现象，而当时控制台之外没有任何提示。

生产代码那行是安全的（`ClientConfigPackage` 是解码后的 protobuf 消息，标量字段一定
存在），但这类「渲染抛错 → 整个应用静默僵死」的失败模式必须能被看见，所以
`main.ts` 加了 `app.config.errorHandler`：出错时用原生 DOM（不能再依赖 Vue）在页面
顶部压一条红色横幅，并把堆栈打到控制台。

## 未重跑

- 浏览器人工烟雾（登录、七个抽屉、底栏种植/收获、320/375/430 宽度）
- 好友串门视图下的底栏行为（串门用 `FriendFarmDashboard`，未改）
