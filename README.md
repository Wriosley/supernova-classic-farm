# Supernova Classic Farm

经典农场小游戏：个人完成的超新星后台课题。

## 当前状态

当前已完成可运行的单玩家农场闭环、固定双 Zone 路由和纯 Tcaplus
持久化原型：

- H5 注册/登录、一次性 WS Ticket、Protobuf WebSocket 和完整单玩家农场
  循环已通过浏览器与 E2E；
- Coordinator 管理 4096 个逻辑 Shard，Gate 使用本地 RouteCache，
  Zone 以 Player Actor 串行执行命令；
- Login、Zone、Coordinator 的账号、Session、Checkpoint、Fence、
  MigrationProgress 和 Outbox 均已接入 Tcaplus，不需要 MySQL 运行时；
- 固定 `zone-a`/`zone-b` 支持非活跃和活跃 Shard 迁移、Fence 拒绝、
  Checkpoint CAS 和完整进程重启恢复；
- kind 集群已运行 Coordinator、Login、Gate、Zone A、Zone B 五个
  Deployment，纯 Tcaplus 双 Zone E2E 通过。

MySQL 实现仍保留为历史基线和回退适配器；不带存储参数的启动仍使用开发
内存模式。当前 Kubernetes 原型不包含动态 Zone 发现、自动扩缩容、
Ingress/TLS 或 Zone 级 preStop Drain。

好友功能尚未编码。业务、gRPC、持久化、跨 Actor Saga、验收清单和
8 阶段实施方案已完成评审，下一次开发从
`docs/plans/friend_design_plan/06-分阶段实施方案.md` 的阶段 0 开始。
权威进度与限制见 `docs/context/CURRENT.md`。

## 文档入口

- `AGENTS.md`：所有 AI 和开发者共同遵守的工作规则。
- `docs/README.md`：文档地图、阅读顺序和事实来源规则。
- `docs/context/PROJECT.md`：稳定的项目目标、边界与事实。
- `docs/context/CURRENT.md`：当前进度、问题和下一步。
- `docs/architecture/`：系统总览与跨模块设计。
- `docs/modules/`：业务模块所有权、能力和不变量。
- `docs/contracts/`：HTTP、WebSocket、事件、数据和幂等契约。
- `docs/decisions/`：架构决策记录。
- `docs/plans/`：开放问题看板和实施计划。
- `docs/evidence/`：测试、压测和故障实验证据。
- `docs/plans/friend_design_plan/`：下一阶段好友业务设计、验收与分阶段方案。

## 项目目录

- `server/`：Go 后端。
- `web/`：Vue H5 客户端。
- `tests/`：跨模块和端到端测试。
- `loadtest/`：压测脚本与负载模型。
- `deploy/`：本地部署及后续演进配置。

## 本地启动

### Linux

首次启动先创建本地配置并填写密码、端口：

```bash
cp .env.example .env
```

后端和 Vite H5 都读取仓库根目录这份 `.env`。可通过 `WEB_PORT`、
`LOGIN_PORT`、`GATE_PORT`、`ZONE_PORT`、`ZONE_B_PORT`、
`COORDINATOR_PORT` 和 `MYSQL_PORT` 统一调整端口。

填写 Tcaplus 配置后，启动固定双 Zone 纯 Tcaplus 运行时：

```bash
./start-servers.sh --dual-zone --tcaplus
```

Kubernetes 最小集群的构建、Secret、部署和排错命令见：

```text
docs/study/tasks/18-Kubernetes固定双Zone部署入门/00-固定双Zone集群部署与查看.md
```

MySQL 仅作为保留的基线和回退路径。需要运行该基线时，启动 MySQL
并应用迁移：

```bash
docker compose -f deploy/docker-compose.yml up -d mysql
./deploy/migrate.sh
```

启动 Linux 双 Zone + MySQL 基线：

```bash
./start-servers.sh --dual-zone --mysql
```

脚本优先读取进程中的 `MYSQL_DSN`；未提供时，使用仓库根目录 `.env`
里的 `MYSQL_HOST`、`MYSQL_PORT`、`MYSQL_DATABASE`、`MYSQL_USER` 和
`MYSQL_PASSWORD` 构造本地连接。脚本不会打印 DSN 或密码。按 `Ctrl+C`
会向后端发送正常终止信号，并等待 Zone 最终刷 Dirty。

另开一个终端启动 H5：

```bash
cd web
npm install
npm run dev
```

### Windows

启动全部 Go 后端（Login、Gate、Zone、Coordinator）：

```powershell
.\start-servers.ps1
```

不传 `MYSQL_DSN` 时脚本使用开发内存适配器。按 `Ctrl+C` 会停止全部后端进程。

使用 Docker 时，可启动 MySQL 并应用迁移：

```powershell
Copy-Item .env.example .env
.\dev.ps1 -Action migrate
```

MySQL 模式下，注册会在一个事务内提交账号、Session 和初始
`PlayerCheckpointV1`；Zone 首次激活 Actor 时从该 Checkpoint 加载，
后续 Actor 命令通过异步 Dirty flusher 写回；奖励溢出的 checkpoint
与 `player_outbox` 行在同一个 MySQL 事务提交。

已安装本机 MySQL 并执行迁移后，可运行会安全提示输入应用密码的 E2E：

```powershell
.\tests\e2e\run-mysql-authenticated-snapshot.ps1
.\tests\e2e\run-mysql-restart-recovery.ps1
```

另开一个 PowerShell 启动 H5：

```powershell
cd web
npm install
npm run dev
```

浏览器访问 `http://localhost:5173`。

运行可自动清理进程的协议端到端验证：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\tests\e2e\run-authenticated-snapshot.ps1
```
