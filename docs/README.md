# Classic Farm 文档地图

## 推荐入口

- 负责人、评审和答辩：[最终交付文档](delivery/README.md)
- 开发维护：[当前项目状态](context/CURRENT.md)
- 系统设计：[当前整体架构](architecture/architecture.md)
- 性能结论：[综合性能报告](evidence/2026-08-19-classic-farm-performance-report.md)

## 当前事实来源

| 内容 | 权威位置 |
|---|---|
| 项目目标和边界 | `context/PROJECT.md` |
| 当前实现、部署和限制 | `context/CURRENT.md` |
| 当前整体架构 | `architecture/architecture.md` |
| Player Actor Zone V3专题 | `architecture/stateful-zone-v3-architecture.md` |
| 产品需求 | `requirements/README.md` |
| 模块职责 | `modules/README.md` |
| 精确协议和数据语义 | `contracts/` |
| 重要取舍 | `decisions/` |
| 实测结论 | `evidence/` |
| 启动与部署 | `project/deployment.md` |

发生冲突时，当前事实、已接受ADR、合同和实测证据优先于历史计划与过程记录。

## 目录职责

- `delivery/`：最终交付阅读入口和图片资产；
- `project/`：启动、部署和项目操作说明；
- `context/`：稳定项目事实与简洁当前状态；
- `requirements/`：确认需求、验收边界和非目标；
- `architecture/`：当前有效总体架构与专题设计；
- `modules/`：服务和业务模块职责、数据归属与不变量；
- `contracts/`：HTTP、WebSocket、gRPC、数据、事件、错误和幂等合同；
- `decisions/`：重要架构决策记录；
- `evidence/`：最终性能材料和代表性机制验收；
- `bugs/`：值得保留的根因复盘；
- `archive/`：旧架构、历史证据、开发计划、AI流水和个人工具配置。

## 阅读规则

- V3是唯一当前目标架构；V1/V2只用于解释演进；
- 3000万DAU是设计目标，不是本机实测能力；
- 规划值、实验值和完整业务实测必须明确区分；
- 归档材料不能覆盖当前合同、ADR或证据；
- 不在文档中提交凭据、Cookie、玩家数据或公司内部实现资料。
