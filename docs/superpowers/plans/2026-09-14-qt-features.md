# Qt 功能对接实施计划

**目标：** 在当前基础 Qt 客户端中补齐多作物和好友功能。

**结构：** 沿用 models、FarmApiClient、FarmWindow、MailboxDialog，新增一个 FriendDialog。不增加请求框架或复杂控件模型。

**环境：** Qt 6.11.2 Widgets / Network / WebSockets，C++17，MinGW 13.1.0，CMake / Ninja。

在当前已获授权的 class-mid 工作区内按 executing-plans 顺序执行；不另建分支、不提交。

- [x] 数据与回归基线：运行旧 Qt 冒烟确认旧初始值断言；扩展 qt/src/models.h 解析 shop、inventory、crop_id、crop_name、sender_id。
- [x] 主窗口：修改 qt/src/farmwindow.h/.cpp，增加作物框、仓库列表和好友入口。请求示例 `api->performWrite("PLANT", {{"plot_id", id}, {"crop_id", crop->currentData().toInt()}})`，保留刷新前的选择。
- [x] 网络：修改 qt/src/farmapiclient.h/.cpp，增加五个好友 HTTP 方法及响应处理。Authorization 只放头部，发送好友邮件不覆盖本人 mailbox；同一时间只处理一个请求。
- [x] 好友弹窗：新增 qt/src/frienddialog.h/.cpp，在 qt/CMakeLists.txt 加入源文件。默认列表、按钮、输入框；好友响应到达后显示 16 块地，偷取按钮根据选中地块 MATURE 状态启用。
- [x] 邮箱：修改 qt/src/mailboxdialog.cpp 显示发送者，显示查询/已读失败，断线关闭弹窗。
- [x] 验证：更新 qt/src/smoketest.cpp 的 50 金币/2 肥料/16 地块断言，按实际价格验证六种作物，双账号验证好友、偷菜和中文邮件，注销重登确认服务端存档。测试输出不含 token/密码。
- [x] 构建与检查：使用 E:/QT/Tools/CMake_64/bin/cmake.exe 配置新临时构建目录，执行 cmake --build；server 中运行 go test ./... -count=1；执行新版 classic_farm_smoke.exe，检查基础界面。
- [x] 文档：更新 docs/context/CURRENT.md、docs/testing.md 和 Qt 指南，新增 docs/evidence/2026-09-14-qt-features.md，明确验证范围与限制。
