# Supernova Classic Farm

经典农场小游戏：个人完成的超新星后台课题。

## 当前状态

项目处于需求与设计阶段。第一阶段优先完成农场主单人的核心业务闭环，再逐步引入好友、多人实时同步、弱网恢复和性能验证。

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

构建和启动命令将在代码骨架建立后补充。
