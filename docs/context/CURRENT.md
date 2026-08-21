---
status: active
updated: 2026-08-21
---

# 当前项目状态

## 1. 当前结论

Classic Farm 已形成可本地演示的 Go + Vue 3 农场游戏原型。当前唯一有效的目标
架构是 Stateful Player Actor Zone V3：同一玩家的命令在 Actor 内串行执行，普通
业务状态异步 Dirty 写回 Tcaplus；Coordinator 管理 4096 个逻辑 Shard、租约、
Owner epoch 和迁移；Gate 根据本地路由快照把 WebSocket 命令转发到权威 Zone。

本项目验证的是缩小版机制和单实例性能基线，不能表述为已经在本机承载
3000 万 DAU。

## 2. 已实现能力

- Login 注册、登录、CSRF、Session 和一次性 WebSocket Ticket；
- 买种子、种植、施肥、自然成熟、收获、出售、章节任务、奖励和清理地块；
- 16块农田、多作物、图鉴、宠物、商店、仓库和基础H5界面；
- FriendSvr 好友关系、分享链接、好友列表和好友农场访问；
- 好友进入、心跳、退出、偷菜、放虫、抓虫和帮忙清理；
- Owner状态与好友农场增量Push，按Gate实例精确投递；
- MailSvr、InfoSvr、好友及邮件红点和礼物邮件链路；
- 动态 `zone-pool`、Coordinator Kubernetes discovery、Shard放置、迁移、Drain、
  lease和epoch fencing；
- Tcaplus账号、Session、Checkpoint、ShardRoute/Fence、迁移、好友、邮件和Outbox
  持久化；MySQL仅保留为历史基线与适配器。

## 3. 当前部署基线

仓库清单 `deploy/k8s/` 当前声明：

| 服务 | 工作负载 | 默认副本 |
|---|---|---:|
| Login | Deployment | 2 |
| Gate | StatefulSet | 6 |
| Coordinator | Deployment | 1 |
| Zone | `zone-pool` StatefulSet | 8 |
| Friend | Deployment | 3 |
| Mail | Deployment | 3 |
| Info | Deployment | 1 |

Coordinator 单副本只用于本地机制验证，不具备生产控制面高可用。生产目标仍要求
多数派控制面、多可用区部署和故障接管验证。

## 4. 最新性能结论

完整口径见 `../evidence/2026-08-19-classic-farm-performance-report.md`。

- 单热点 Zone 重复 Snapshot：8k QPS 是首版保守水位；约10.5k QPS出现2核平台。
- 三 Gate 均匀分流：25k Snapshot QPS完整承接、0 shed、0服务错误；30k档受到
  生成器限制，不能称为Gate上限。
- Gate路由版本读取从复制4096条路由改为O(1)后，同场景QPS由8194提升至10663。
- 好友生命周期使用Coordinator SDK本地路由快照后，50对玩家由227提升至1227
  lifecycle/s。
- 真实“一批玩家到达、一次Snapshot、保持连接”模型中，同步连接注册是完整链路
  的首个瓶颈。关闭该旁路后的Zone核心隔离实验在3000/s时P99为7.534ms；4500/s
  P99为49.366ms，6000/s P99为90.177ms。该实验不包含Presence和精确Push语义。

## 5. 已知边界

- 尚无30分钟以上混合读写、Dirty backlog和Tcaplus压力的正式长稳结论；
- 尚未验证百万级WebSocket、Actor内存、重连风暴和跨可用区网络；
- 本地Coordinator不是三节点多数派；
- 偷菜Await是实验实现，测试工作量不等量，不能宣称相对普通实现性能提升；
- 压测绕过开关 `GATE_SKIP_AUTH`、`GATE_SKIP_CONNECTION_SYNC` 和
  `ZONE_QUICK_INFO_ENABLED=false` 只能用于隔离诊断，不是生产配置；
- 6000/s短脉冲未被Metrics Server可靠捕获，不能声称CPU已经打满。

## 6. 文档入口

- 最终交付阅读：`../delivery/README.md`
- 当前架构：`../architecture/architecture.md`
- V3专题：`../architecture/stateful-zone-v3-architecture.md`
- 当前合同：`../contracts/README.md`
- 当前证据：`../evidence/README.md`
- 历史交接全文：`../archive/handoff-history/CURRENT-2026-08-21-before-delivery-cleanup.md`

## 7. 后续工作

若继续做生产化验证，优先级为：混合读写长稳与pprof、连接生命周期异步化、
Actor/连接内存测量、多机压测生成器、三节点Coordinator和多可用区故障演练。
