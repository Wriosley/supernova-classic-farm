# Classic Farm · class-mid

中期课设简化分支：**一个 Go 游戏服务器 + Vue 3 + JSON + MySQL**。
只有 `server/cmd/game` 一个后端入口。

## 环境与启动

- Go 1.26.x，Windows amd64；Node.js 24+，npm；MySQL 8.0.46/8.4（实际验证范围见证据）。】
- PowerShell 脚本被限制时用 `powershell -NoProfile -ExecutionPolicy Bypass -File ...`。

首次下载依赖（项目根目录）：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\dev.ps1 -Action install
```


MySQL 模式：新建数据库 `classicfarm`，给本地应用账号该库的读写和建表权限。
本机 MySQL 已有该库/账号时，直接复用。将 `.env.example` 复制为 `.env`（已有 `.env` 不要覆盖），在本地填密码。
启动脚本读取 `.env` 的 MYSQL_* 字段，或使用当前环境中的 MYSQL_DSN：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\start-servers.ps1
```

服务自动创建三张新表 `class_mid_accounts`、`class_mid_states`、`class_mid_mails`；不修改旧版表，也不需要重建数据库。
**请在新版本重新注册账号。** 内存模式退出丢失全部数据；MySQL 模式保留账号与农场，服务重启后需要重新登录。

另开终端运行前端：

```powershell
cd web
npm.cmd run dev
```

访问 http://localhost:5173。开发代理把 `/api` 和 `/ws` 转发给 8080。
`npm.cmd run build` 只生成静态文件，不自动提供后端代理；正式部署需另行配置同源反向代理。

Windows 桌面客户端在 `qt/`。用 Qt Creator 打开 `qt/CMakeLists.txt`，Kit 选 **Desktop Qt 6.11.2 MinGW 64-bit**。`moc` 不能写到含中文的构建目录，请把构建目录设到纯英文路径，例如 `C:/build/classic-farm-qt`。先启动 Go 后端，再运行 `classic_farm`，默认连接 `127.0.0.1:8080`。

## 当前玩法

4 块地、胡萝卜一种作物。初始 10 金币、1 份肥料。
胡萝卜种子 2 金币，肥料 2 金币，胡萝卜卖价 5 金币；种植 100 秒成熟，收获 3 个。
一份肥料立即缩短 30 秒剩余时间，每株仅一次。仓库总数量上限 200。
每章完成买 3 种子、种植、施肥、收获、出售五项任务，领取 10 金币和 3 种子，再进入相同目标的下一章。
新注册玩家会收到一封欢迎邮件；已有账号不补发。邮箱支持查询正文和标记已读。满仓时操作失败，成熟由服务器时间推导，离线仍会生长。

## 代码阅读顺序

1. `server/internal/game/model.go`：数据、消息、配置。
2. `rules.go`：种植到领奖的业务规则与重复请求处理。
3. `store.go` / `mysql.go`：内存存储、三张表和 MySQL 行锁事务。
4. `password.go` / `http.go`：密码派生、账号登录、Session。
5. `websocket.go`：连接认证及游戏指令。
6. `server/cmd/game/main.go`：启动配置。
7. `web/src/game/`、`web/src/App.vue`：JSON 网络层和界面。

## 验证与文档

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\dev.ps1 -Action test
```

[文档地图](docs/README.md) · [架构说明](docs/architecture.md) · [JSON 协议](docs/contracts/json-api.md) · [Qt 接入](docs/qt-client-guide.md) · [代码 Review](docs/code-review.md) · [测试说明](docs/testing.md) · [中期答辩](docs/midterm-defense.md)

旧分布式实现和历史文档在 main 等原分支中保留，不是本分支运行依赖。
本分支包含 AI 辅助改造，开发记录见证据文档；课程使用应遵循教师要求并如实说明。
