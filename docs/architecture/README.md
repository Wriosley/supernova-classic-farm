# 当前架构文档

- [整体架构](architecture.md)：当前服务拓扑、职责、命令链路、路由迁移和部署边界；
- [Stateful Zone V3](stateful-zone-v3-architecture.md)：Player Actor、Dirty写回、
  lease、epoch、恢复和迁移专题；
- [单人业务闭环](single-player-vertical-loop-business-architecture.md)：买种子至
  清理地块的业务规则。

旧V1/V2和整理前的重复长文位于`../archive/architecture-v1-v2/`及
`../archive/architecture-history/`，不属于当前架构。

精确DTO、表结构和错误语义属于`../contracts/`；重要取舍属于`../decisions/`；
性能数字属于`../evidence/`。
