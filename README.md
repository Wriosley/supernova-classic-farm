# Classic Farm · class-mid

这是用于课程中期演示的简化农场项目：一个 Go 游戏服务器、一个 MySQL 数据库，以及 Qt/Vue 演示客户端。后端入口只有 `server/cmd/game`，HTTP 和 WebSocket 都传 JSON。

## 当前功能

- 注册、登录、注销和内存 Session。
- 每位玩家 16 块地，支持种植、施肥、成熟、收获和清理。
- 商店提供胡萝卜、玉米、土豆、番茄、草莓、南瓜 6 种作物。
- 独立仓库记录每种作物的种子数和成品数。
- 任务、章节奖励、欢迎邮件和邮件已读。
- 添加好友、好友列表、查看好友农场、偷取成熟作物和发送自定义邮件。
- 所有业务数据存入 9 张 `class_mid_*` 关系表，写操作使用 MySQL 事务。

当前 Qt 主界面可以登录和运行旧农场操作，但按钮默认只操作胡萝卜，尚未实现多作物商店、仓库和好友页面。需要新增的 Qt 功能见 [Qt 功能升级与接口对接](docs/qt-feature-upgrade.md)。

## 环境

- Go 1.26.x，Windows amd64。
- MySQL 8.0 或 8.4，数据库名默认 `classicfarm`。
- Qt 6.11.2 Desktop MinGW 64-bit，包含 Network、WebSockets 和 Widgets。
- Vue 演示客户端需要 Node.js 24+ 与 npm；后端开发不依赖 Vue。

把 `.env.example` 复制为 `.env`，填写自己的 MySQL 配置。不要覆盖已有 `.env`，也不要提交数据库密码。

首次或需要清空旧账号时，在 MySQL 客户端执行：

```sql
source F:/workspace/supernova-classic-farm/server/sql/reset_class_mid.sql;
```

该脚本会删除并重建本分支的 `class_mid_*` 表，原账号会被清空。

## 启动

项目根目录启动后端：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\start-servers.ps1
```

默认地址：

```text
HTTP:      http://127.0.0.1:8080
WebSocket: ws://127.0.0.1:8080/ws
```

Qt：用 Qt Creator 打开 `qt/CMakeLists.txt`，Kit 选择 Desktop Qt 6.11.2 MinGW 64-bit。构建目录使用纯英文路径，例如 `C:/build/classic-farm-qt`，然后运行 `classic_farm`。

Vue：

```powershell
cd web
npm.cmd install
npm.cmd run dev
```

浏览器访问 `http://localhost:5173`。

## 初始数据与玩法

新玩家有 50 金币、2 份肥料、16 块空地、6 条空仓库记录和一封欢迎邮件。6 种作物成熟时间为 30–60 秒；完整价格和产量见 [JSON 接口契约](docs/contracts/json-api.md)。施肥缩短 10 秒。

偷菜要求双方已经是好友且目标作物成熟。成功后偷取者获得该作物完整产量和 1 金币，对方地块变为 `NEED_CLEANUP`。

没有 Qt 好友界面时，可运行演示脚本：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\demo-friends.ps1 `
  -MailTitle "测试标题" `
  -MailContent "这是自定义邮件正文"
```

只验证好友和邮件、不等待作物成熟：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\demo-friends.ps1 `
  -MailOnly -MailTitle "测试标题" -MailContent "测试正文"
```

出现 `B received mail` 表示服务端插入邮件后，从 B 的邮箱中读回了相同标题和正文。

验证六种作物的购买、种植、成熟、收获和出售：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\demo-all-crops.ps1
```

最后出现 `ALL_SIX_CROPS_OK` 表示六种作物全流程均通过。

## 后端代码阅读顺序

1. `server/cmd/game/main.go`：连接 MySQL 和启动 HTTP 服务。
2. `server/internal/game/model.go`：JSON 请求、响应和业务数据结构。
3. `server/internal/game/http.go`：HTTP 路由、注册登录和 Session。
4. `server/internal/game/websocket.go`：WebSocket 认证及消息循环。
5. `server/internal/game/game.go`：购买、种植、施肥、收获、出售和任务。
6. `server/internal/game/friend.go`：好友、查看农田、偷菜和好友邮件。
7. `server/internal/game/mysql.go`：建表、注册初始化、快照和邮箱查询。
8. `server/sql/reset_class_mid.sql`：完整关系表结构和初始作物数据。

## 验证

```powershell
cd server
$env:GOCACHE="$PWD\..\.cache\go-build"
go test ./... -count=1
go vet ./...
```

详细文档见 [文档地图](docs/README.md)、[架构与 ER 图](docs/architecture.md)、[接口契约](docs/contracts/json-api.md)、[中期答辩讲解稿](docs/midterm-defense.md)和[真实验证记录](docs/evidence/2026-09-14-relational-backend.md)。

旧分布式实现保存在其他分支，不是 `class-mid` 的运行依赖。本分支的 AI 辅助记录如实保留。
