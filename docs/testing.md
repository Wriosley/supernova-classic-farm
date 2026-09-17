# class-mid 测试说明

本文描述当前关系型后端和 Qt 客户端的验证。旧版 MemoryStore、sqlmock、4 块地和请求去重测试已经随对应代码删除。

## Go 自动测试

当前保留两个简单的密码测试：

- `TestEncodePassword`：验证大小写字母、数字循环右移 3 位。
- `TestValidateCredentials`：验证用户名及字母数字密码格式。

运行：

```powershell
cd server
$env:GOCACHE = Join-Path $env:TEMP 'classic-farm-go-cache'
go test ./... -count=1
go vet ./...
```

当前业务验证主要使用真实 MySQL 接口链，而不是大量模拟 SQL 测试。

## 真实 MySQL 双玩家验证

先启动服务器，再在另一个终端运行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\demo-friends.ps1
```

脚本验证：

1. 注册并登录 A、B 两个临时玩家。
2. A 添加 B，好友列表为 1，好友农场返回 16 块地。
3. B 通过 WebSocket 买胡萝卜、种植、施肥。
4. 等待 21 秒成熟后，A 偷菜。
5. 检查 A 增加 1 金币和 3 个胡萝卜。
6. A 给 B 发邮件，并从服务端返回的 B 邮箱中核对标题与正文。

只验证好友与自定义邮件：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\demo-friends.ps1 `
  -MailOnly -MailTitle "测试标题" -MailContent "测试正文"
```

成功输出包含：

```text
Friend added: 26; friends: 1; plots: 16
B received mail: title=[测试标题] content=[测试正文]
```

## 六种作物验证

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\demo-all-crops.ps1
```

脚本为同一玩家分别购买并种植 1–6 号作物，等待 61 秒且每 20 秒发送心跳，然后逐种收获、核对产量并出售 1 个。全部成功时最后输出 `ALL_SIX_CROPS_OK`。

## Qt 验证说明

为减少课设中的额外代码，Qt 冒烟源码和 CMake 测试目标已按要求删除。当前只构建主程序，以下命令在项目根目录执行，路径按本机 Qt 安装位置调整：

```powershell
$env:PATH = 'E:/QT/6.11.2/mingw_64/bin;E:/QT/Tools/mingw1310_64/bin;' + $env:PATH
$qtBuild = Join-Path $env:TEMP 'classic-farm-qt-features-20260914'
& 'E:/QT/Tools/CMake_64/bin/cmake.exe' -S qt -B $qtBuild -G Ninja `
  -DCMAKE_MAKE_PROGRAM=E:/QT/Tools/Ninja/ninja.exe `
  -DCMAKE_CXX_COMPILER=E:/QT/Tools/mingw1310_64/bin/g++.exe `
  -DCMAKE_PREFIX_PATH=E:/QT/6.11.2/mingw_64 -DCMAKE_BUILD_TYPE=Release
& 'E:/QT/Tools/CMake_64/bin/cmake.exe' --build $qtBuild -j 4
```

构建后启动 `classic_farm.exe`，使用两个新游戏账号手动检查：

- 6 位字母数字密码注册、登录、AUTH；初始 50 金币、2 肥料、16 块地、6 种作物。
- 在实际 Qt 下拉框选择六种作物，逐种购买并种在 11–16 号地；买肥料和施肥。
- 自己和好友的生长中地块显示“剩余 X 秒”，到零后显示已成熟。
- 刷新保留作物和地块选择，商店与仓库不重复追加。
- 空好友列表、添加好友、双向列表、重复添加失败、非好友访问失败。
- 好友农田的玉米名称和 16 块地；未成熟偷取失败，成熟后奖励及地块清理状态正确，重复偷取失败。
- 空邮件、标题和正文字节超限被界面阻止；中文和换行发送正确；发送响应不覆盖发件人的收件箱。
- 邮箱显示系统/玩家用户名，点击标记已读。
- 六种作物收获、按数量出售、清理，完成任务领奖。
- 注销清空本地账号数据，重新登录后金币、章节、好友、被偷地块和已读邮件仍保存。

代码简化前曾用完整 Qt 冒烟覆盖以上流程并通过，真实输出保留在 [Qt 功能记录](evidence/2026-09-14-qt-features.md)。该记录是当时结果，不代表当前仍有测试程序。

## Vue 验证

如果修改 Vue，再运行：

```powershell
cd web
npm.cmd test
npm.cmd run build
```

本轮后端关系型改造没有修改 Vue，因此没有把旧 Vue 界面视为多作物和好友功能的验收证据。

## 未覆盖范围

- 没有并发压力测试、网络故障自动重试测试或公网安全测试。
- 本轮 Qt 未验证服务端重启、HTTP/WS 超时及登录失效的完整自动故障流程，也未验证 MSVC、Linux 或 macOS 构建。
- 当前没有 Qt 自动回归测试。
- 没有好友申请确认、偷菜次数限制和邮件分页。
- 凯撒密码只验证编码正确性，不代表真实密码安全。

实际执行结果见 [关系型后端记录](evidence/2026-09-14-relational-backend.md) 和 [Qt 功能记录](evidence/2026-09-14-qt-features.md)。
