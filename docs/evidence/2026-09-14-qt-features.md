# Qt 多作物与好友功能验证

日期：2026-09-14。分支：`class-mid`。本次由 AI 辅助完成 Qt 代码、测试和文档，没有提交或推送。

## 范围与环境

- 沿用基础 Qt 控件、文字列表、成员变量和信号槽，增加六种作物商店/仓库及一个好友弹窗。
- 好友弹窗包含添加/查询好友、查看 16 块地、偷菜和发邮件。邮件显示系统或发送者用户名。
- 修正刷新丢失地块选择的问题；发送好友邮件的响应不覆盖自己的收件箱。
- 不改 Go 业务规则，不增加自动重试、样式表、图片或动画。
- Windows，Qt 6.11.2 Desktop MinGW 64-bit，MinGW 13.1.0，CMake/Ninja。
- 构建目录：`%TEMP%/classic-farm-qt-features-20260914`。
- 原 MySQL 密码无法取得，工作区没有可用 `.env`。本轮新建独立 MySQL 9.0.1 实例，监听 `127.0.0.1:3307`，数据目录 `%TEMP%/classic-farm-qt-mysql-20260914`，库名 `classicfarm_qt_test`。
- 仅在这个新目录执行初始化，未重置原账号、未改原数据、未写 `.env`。后端通过进程环境获得连接参数，运行当前 `server/cmd/game`，监听 `127.0.0.1:8080`。

这个临时实例用于本次本机联调，不是已安装的 Windows 服务。电脑重启后需重新启动它或改用配置好的本地数据库。Qt 只需游戏账号，不需要 MySQL 密码。

## 实际执行结果

1. 旧冒烟在后端未启动时退出 1，输出“无法连接服务器”。启动新版后端后再次运行旧断言，退出 1，输出 `unexpected initial snapshot`，确认旧初始值不再适用。
2. 更新 Qt 后，`cmake --build` 完成主程序和冒烟程序链接。配置时提示未找到可选 Vulkan 头文件，Widgets 构建成功。
3. `server` 下运行 `go test ./... -count=1`，退出 0：

```text
?    github.com/Wriosley/supernova-classic-farm/server/cmd/game [no test files]
ok   github.com/Wriosley/supernova-classic-farm/server/internal/game 0.118s
```

4. 新版 `classic_farm_smoke.exe 127.0.0.1:8080` 在临时库创建两个测试游戏账号，操作实际 Qt 控件并调用真实 HTTP/WS。`offscreen` 和 Windows 原生平台两次运行都退出 0：

```text
PASS register/login, 50 coins, 2 fertilizer, 16 plots, 6 crops
PASS six crops: UI buy/plant, plots 11-16, fertilizer, refresh selection
PASS friends, immature steal rejected, mail limits, Chinese mail, sender, mark read
Waiting for real crop growth (up to 61 seconds)...
PASS mature steal/repeat rejection, six crops harvest/sell/clean, chapter reward
PASS logout/relogin: coins, chapter, friends, stolen plot, read mail
qt feature smoke ok
```

测试等待作物自然成熟，没有通过修改数据库时间或赠送物品绕过规则。检查了六种作物价格、指定作物种植、产量和出售数量；好友偷菜增加完整产量和 1 金币；非法及重复操作不增加奖励。邮件只在新测试账号之间发送，测试输出不包含密码和 token。

5. 查看构建目录中的 `qt-farm.png`、`qt-friends.png`、`qt-mailbox.png`：Windows 原生平台中文、默认列表/输入框/按钮正常，邮件正文中的 `<b>` 保持普通文字。`offscreen` 的截图字体缺字，因此额外使用 Windows 平台完成界面检查，不把缺字截图算通过。

6. `git diff --check` 退出 0。`/healthz` 返回 `OK`，独立测试库查询到 9 张表和 6 种作物。工作区仍没有 `.env`。

7. 在用户 Windows 桌面启动新版 `classic_farm.exe`，从该桌面检查到进程 31304，窗口标题“农场”，窗口句柄 1572894，`Responding=True`。本地后端及临时数据库保留运行，方便直接注册新的游戏账号体验。

## 邮件发送者用户名修正

用户检查界面时发现好友邮件显示发送者数字 ID。沿数据链路确认：`class_mid_mails` 正常保存 `sender_id`，但邮箱查询只返回 ID，Qt 因此没有用户名可显示。

先修改双账号冒烟，要求邮件模型及界面显示发送者用户名。修复前重新构建按预期失败，编译器指出 `Mail` 没有 `senderName`。随后进行最小修改：后端邮件查询关联账号表并返回 `sender_username`，Qt 解析该字段，系统邮件仍显示“系统”。

修复后重新执行 Go 测试、Qt 构建和 Windows 平台完整冒烟，均退出 0。测试同时确认首次收信和注销重登后的邮件都保留发送者 ID 及用户名；`qt-mailbox.png` 中显示 `发送者：qta...`，不再显示数字 ID。

## 地块剩余秒数修正

用户检查主农场时发现地块只显示“生长中”。服务端响应一直带有 `mature_at_ms`，但简化 Qt 时删除了该字段的解析和本地文字刷新。

先在冒烟中要求自己和好友的生长中地块包含“剩余”和“秒”。修复前完整构建成功，冒烟在种下第一块地后按预期退出 1，输出 `FAIL: own farm remaining seconds missing`。随后让 `Plot` 读取 `mature_at_ms`，主农场和好友窗口各使用一个普通 `QTimer` 每秒更新列表文字，到零显示“已成熟”。客户端不修改服务器时间和作物状态，操作仍由服务端校验。

修复后重新构建并运行完整冒烟，退出 0。六种作物和好友邮件阶段先通过，等待 61 秒后成熟偷取、重复偷取拒绝、六种作物收获/出售/清理、任务领奖及注销重登也全部通过，最后输出 `qt feature smoke ok`。

## Qt 代码再次简化

用户要求进一步减少函数拆分、缩短函数名，并删除测试代码。清理时先保留冒烟保护行为：把注册和登录合成 `enter()`，农场 WebSocket 操作直接使用 `game()`，删除只转发一行的邮箱读取、邮件已读和快照刷新函数；`setServerHostPort` 改为 `setHost`，好友农场、偷菜和发信分别改为 `friendFarm`、`steal`、`sendMail`。登录成功后的 WebSocket 打开逻辑直接放回 `enter()`。

好友 HTTP 不再经过按操作名分支的统一完成函数。五个请求各自在自己的回调中读取结果，因此有少量重复，但单个函数可以从请求一直读到界面信号。删除只有肥料价格仍在使用的 `Config` 结构，改用一个整数；网络实现由 402 行降为 377 行，头文件由 81 行降为 72 行。

重命名和合并后再次运行完整 Qt 冒烟，退出 0，最后输出 `qt feature smoke ok`。确认结果后删除 `qt/src/smoketest.cpp`、`classic_farm_smoke` CMake 目标，以及只被测试使用的玩家 ID 缓存。此后仓库不再提供 Qt 自动回归测试，最终只验证主程序构建和手动启动；历史冒烟结果仍如实保留在本记录中。

## Qt 无用逻辑清理

继续删除 Qt 中没有读取位置的 `Snapshot::seeds`、`Snapshot::crops` 和 `Plot::cropId`。邮件不再保存只用于判断系统邮件的发送者 ID，改用是否存在 `sender_username` 判断；玩家邮件仍显示用户名，系统邮件仍显示“系统”。只调用一次的邮件解析函数并回 WebSocket 回调，并删除两个信号没有实际用途的参数和未使用的应用元数据。

使用 MinGW 13.1.0 和 Qt 6.11.2，以额外的 `-Wall -Wextra` 选项重新配置并构建，9 个构建步骤全部成功且没有编译警告。`/healthz` 返回 `OK`。新程序启动成功，进程号 20636，窗口标题为“农场”。本次只做删除和等价简化，没有恢复已删除的 Qt 测试源码。

随后给 Qt 源码补充中文说明，内容集中在登录连接顺序、JSON 转换、信号槽、列表附加数据和倒计时。注释修改后再次完成 9 个步骤的主程序构建，退出 0；`git diff --check` 退出 0。

最后继续做删除优先的简化：客户端数据类型缩短为 `Farm`、`Crop`、`Bag` 和 `Friend`；自定义接口及信号使用 `user()`、`online()`、`working()`、`farm()`、`getList()`、`add()`、`visit()`、`send()`、`entered()` 和 `back()` 等短名称。删除只使用一次的 `getHost()`、重复的退出信号、重复 HTTP 状态判断、额外 WebSocket 二进制及错误分支、无作用的 JSON/UUID 格式选项和同目录头文件路径设置。Qt JSON、请求头、邮件字节数及列表数据仍保留实际需要的类型转换。

使用 `-Wall -Wextra -Wpedantic -Wconversion -Wsign-conversion -Wunused` 全量清理构建，9 个步骤全部成功，没有编译警告。`git diff --check` 退出 0，`/healthz` 返回 `OK`。最终程序启动成功，进程号 35160，窗口标题“仿QQ农场”，`Responding=True`。

## 验证限制

- Go 当前仅有两个密码单元测试；本次业务证据主要来自 Qt 与真实 MySQL 的完整操作链。
- 注销重登验证了存档和已读状态；没有把它当作服务端重启恢复的测试。
- 没有完整自动覆盖 HTTP/WS 超时、服务停止、登录失效及旧响应到达的故障场景。
- 没有验证 MSVC、Linux、macOS、公网安全或并发压力。
- 本次没有修改 Vue，也没有把 Vue 作为新功能的验收证据。

复现命令与检查项目见 [测试说明](../testing.md)。
