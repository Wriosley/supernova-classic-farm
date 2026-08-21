# Classic Farm 最终交付文档

本目录是面向负责人、评审和答辩的阅读入口。这里仅组织当前有效设计、最终结果
和可复核结论；开发计划、AI 过程记录、旧架构及细粒度实验不属于默认阅读范围。

## 建议阅读顺序

1. [项目介绍](../context/PROJECT.md)：项目目标、范围和交付边界。
2. [当前项目状态](../context/CURRENT.md)：已经实现的服务、业务能力、部署和限制。
3. [最终系统架构](../architecture/architecture.md)：服务拓扑、Player Actor、
   动态Zone、Dirty持久化、Shard、租约和epoch fencing。
4. [业务闭环设计](../architecture/single-player-vertical-loop-business-architecture.md)：
   农场主单人核心流程及业务规则。
5. [接口合同](../contracts/README.md)：HTTP、WebSocket、内部 gRPC、数据与错误语义。
6. [性能报告](../evidence/2026-08-19-classic-farm-performance-report.md)：Gate、Zone、
   好友交互、profiling 和 3000 万 DAU 容量分析。
7. [关键架构决策](../decisions/README.md)：当前方案的选择、代价和替代方案。
8. [启动与演示](../project/deployment.md)：本机及Kubernetes动态多Zone启动方法。

## 交付口径

- 3000 万 DAU 是容量设计目标，不是本机实测承载结论。
- 性能数字必须来自 `docs/evidence/` 中可复核的实测记录。
- V3 是唯一当前生产目标架构；V1/V2 仅保存在历史归档中。
- `archive/development/` 中的 plans、AI workflow、study 和 superpowers 是内部
  开发材料，不作为最终方案或性能结论。

## 深入阅读

- [文档事实来源与完整地图](../README.md)
- [当前项目状态](../context/CURRENT.md)
- [性能交付材料清单](../evidence/2026-08-19-performance-delivery-manifest.md)
- [历史资料归档说明](../archive/README.md)
