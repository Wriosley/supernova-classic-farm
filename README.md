# Supernova Classic Farm

经典农场小游戏：个人完成的超新星后台课题。

## 当前状态

项目已完成第一阶段认证快照技术链路：H5 注册/登录、一次性 WS Ticket、Gate 路由、单节点 Coordinator-compatible ShardMap、Zone Player Actor 和关联快照响应。

默认启动仍使用开发内存适配器。MySQL 8.4.11 已验证从注册到 `CLEAN_PLOT` 的完整服务端单玩家链路和 `player_seq=8` 重启恢复：29 金币、2 个旧种子、1 个肥料、3 个下一章种子、`EMPTY` 地块和第二章 `IN_PROGRESS`。满仓奖励会原子记录待发送邮件 Outbox，当前没有 Relay、Mail Service 或邮件 UI。H5 已提供商店、地块、仓库和章节任务交互；浏览器实测完成购买到清理的整条内存链路，收到一次成熟 Push，最终到达 `player_seq=8`，320 像素宽度无横向溢出。生产级 Push 重试/跨 Gate 路由、旧 Owner Fence 拒绝和容量验证尚未完成。权威进度与限制见 `docs/context/CURRENT.md`。

Zone 还实现了最小不可变版本化配置快照；`GET_SHOP` 返回当前启用的买入/卖出报价，`BUY_SEEDS` 和 `SELL_CROP` 使用同一固定快照推导权威价格。独立 ConfigSvr 和 H5 商店界面尚未实现。

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

## 计划中的目录

- `server/`：Go 后端。
- `web/`：Vue H5 客户端。
- `tests/`：跨模块和端到端测试。
- `loadtest/`：压测脚本与负载模型。
- `deploy/`：本地部署及后续演进配置。

## 本地启动

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
