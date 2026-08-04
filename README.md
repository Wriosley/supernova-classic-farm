# Supernova Classic Farm

经典农场小游戏：个人完成的超新星后台课题。

## 当前状态

项目已完成第一阶段认证快照技术链路：H5 注册/登录、一次性 WS Ticket、Gate 路由、单节点 Coordinator-compatible ShardMap、Zone Player Actor 和关联快照响应。

默认启动仍使用开发内存适配器。MySQL 8.4.11 下的账号、Session、原子注册事务和 Player Checkpoint 已通过真实多进程 E2E；`BUY_SEEDS`、`PLANT` 和 `APPLY_FERTILIZER` 验证了幂等重放、异步 Dirty 写回、checkpoint CAS、本地数据库 Fence 成功路径，以及四个服务全量重启后的 `player_seq=3` 肥料效果恢复。基础/效果区间定点成长、Actor 激活离线成熟和在线成熟扫描已有自动化测试；另一个真实四进程测试验证了 `player_seq=4` 的自然成熟 Push，以及全量收获入仓、收获任务推进和 `player_seq=5` 的 `NEED_CLEANUP` 地块。H5 已支持增量 Patch 和版本缺口快照恢复。收获后 MySQL 重启恢复脚本已扩展但尚待输入本机密码实跑；生产级 Push 重试/跨 Gate 路由、旧 Owner Fence 拒绝、完整单玩家业务闭环和容量验证尚未完成。权威进度与限制见 `docs/context/CURRENT.md`。

Zone 还实现了最小不可变版本化配置快照；`GET_SHOP` 返回当前启用报价，`BUY_SEEDS` 使用同一固定快照推导物品与权威价格。独立 ConfigSvr 和 H5 商店界面尚未实现。

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
后续 `BUY_SEEDS` 通过异步 Dirty flusher 写回。

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
