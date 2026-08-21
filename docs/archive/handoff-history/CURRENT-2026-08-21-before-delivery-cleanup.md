---
status: active
updated: 2026-08-15
---

# Current Handoff

## Resume here

V3 is the only current production-target strategy. A new AI should read, in order:

1. `../../AGENTS.md` and `../README.md`;
2. `PROJECT.md` and this file;
3. `../architecture/stateful-zone-v3-architecture.md`;
4. `../architecture/single-player-vertical-loop-business-architecture.md`;
5. the accepted contracts under `../contracts/`;
6. only the additional requirement, ADR, plan, or evidence files needed for the task.

Do not resume V1 or V2 as the implementation target. Do not read every ADR as if all decisions were simultaneously active. The ADR directory preserves how the design evolved; current truth comes from this handoff, the current architecture, and the accepted ADRs that the current architecture explicitly references.

## Snapshot at handoff

- 2026-08-21 文档交付整理完成两轮：`docs/delivery/README.md` 成为负责人、评审
  和答辩的精简入口；无状态 V1 与同步 Journal V2 已移至
  `docs/archive/architecture-v1-v2/`，Obsidian 个人配置移至归档工具目录。当前
  架构目录只展示 V3 与有效业务设计。开发计划、AI 流水、study 和 superpowers
  已物理移动到 `docs/archive/development/` 并修复有效文档引用，明确排除在最终
  交付阅读路线之外。`docs/evidence/` 已从 96 份收敛为最终性能材料与代表性机制
  验收共 20 份，其余 76 份移动到 `docs/archive/evidence/historical/` 并保留追溯。

- 2026-08-20 单 Zone 核心链路隔离实验已定位并绕开建连平台：阶段计时显示原
  1500/s 档的 `connect_auth` P99=911.977ms、worker queue P99=4815.710ms；升至
  800 worker 后 AUTH P99 接近 Gate→Zone 连接注册的 2 秒 deadline。新增默认关闭
  的压测开关 `GATE_SKIP_CONNECTION_SYNC`，显式启用时不做 register/refresh/
  unregister，因此结果不包含 Presence 与精确 Push 路由语义。相同 10000 热账号
  在 1500/3000/4500/6000 offered 下完整达成 1499.89/2998.94/4493.75/5994.57
  QPS，Snapshot P99 为 6.239/7.534/49.366/90.177ms，均 0 错误；延迟拐点位于
  3000--4500/s，当前可把 3000/s 作为 P99 明显低于 50ms 的保守 Zone 核心到达
  水位。短脉冲未被 Metrics Server 准确捕获，尚不能证明 2 CPU 饱和；下一步需
  延长 cohort/持续窗口并采集 cgroup 或 pprof。Evidence:
  `../evidence/2026-08-20-single-zone-connect-hold-baseline.md`。

- 2026-08-20 新 connect-and-hold 单 Zone 基线已开始：3 Gate、物理单
  `zone-pool-0`、Zone 2 CPU/1GiB、4096 Shard 全归 Zone 0，迁移 Worker/Planner
  已停。100/1000/3000 连接均完成一次 Snapshot 且业务错误为 0；1000 档 P99
  89.203ms 并稳定 hold 2 分钟，3000 档 P99 升至 449.950ms。3000 突发同时产生
  952 次 `presence quick-info queue full`，暴露固定 1024 队列和单消费者的旁路
  丢弃；此时 Zone 峰值仅约 158m CPU/114Mi，因此不是 CPU/内存瓶颈。修复或明确
  Presence 验收边界前，不把 3000 称为安全容量。Evidence:
  `../evidence/2026-08-20-single-zone-connect-hold-baseline.md`。

- 2026-08-20 按核心 Zone 隔离目标新增并部署 `ZONE_QUICK_INFO_ENABLED=false`
  开关（默认仍为 true），后续上限实验不调用 InfoSvr QuickInfo/Presence。3000
  冷态为 5,910.78 QPS/P99 492.382ms；同一批账号显式预热后立即复测为
  11,455.69 QPS/P99 254.909ms，均 0 错误并 hold 2 分钟。热态 Zone 峰值仅约
  224m CPU/109Mi。差异证明有限 Snapshot 突发对 Actor/Checkpoint 热状态敏感；
  后续上限必须固定并标明 warm 状态，且结果不代表包含 InfoSvr 的完整业务容量。
  Evidence: `../evidence/2026-08-20-single-zone-connect-hold-baseline.md`。

- 2026-08-20 InfoSvr 关闭的 5000 热态连接档完成：5000/5000 在 2.01 秒建连，
  初始 Snapshot 10,832.18 QPS、P99 450.125ms、0 错误并完成连接保持；2 秒
  `kubectl top` 采样中 Zone 峰值 302m CPU、平均 105.2m、峰值 159Mi，仍远低于
  2 CPU/1GiB。Metrics Server 可能遗漏亚秒 CPU 峰值，因此最终饱和档还需用
  cgroup/pprof 复核。Evidence:
  `../evidence/2026-08-20-single-zone-connect-hold-baseline.md`。

- 2026-08-20 benchrunner `connect_hold` 新增 `-connect-rate` 到达速率模型：每个
  玩家按计划到达，AUTH 后立即执行一次 Snapshot，不再等待所有连接后同窗释放；
  0 保留旧突发行为。两个不重叠的 1000 人 cohort 校验中，200/500 玩家每秒分别
  达成 199.09/493.06 QPS，P99 29.418/32.449ms，均 0 错误并保持 30 秒。
  下一步用 10000 人正式跑 500→1000→1500 玩家每秒阶梯，寻找 P99/完成速率拐点。
  Evidence: `../evidence/2026-08-20-single-zone-connect-hold-baseline.md`。

- 2026-08-20 单 Zone/InfoSvr 关闭的 10000 人冷态 paced 阶梯完成至 1500/s：
  offered 500/1000/1500 分别达成 470.12/687.37/812.89 QPS，Snapshot P99
  30.386/43.664/50.409ms，均 0 错误；Zone 峰值 1.190/1.110/1.146 核，内存
  231/223/239Mi，三个 Gate 峰值均低于 0.3 核。1500/s 的 10000 到达耗时
  12.302 秒而理论为 6.667 秒，但 Snapshot 延迟仍低且 Zone 未满，因此当前约
  813/s 是建连/AUTH/连接注册/单压测机组合平台，不能称为 Zone 上限。已停止
  2000/s，下一步先拆分阶段计时并采集生成器 CPU/socket。Evidence:
  `../evidence/2026-08-20-single-zone-connect-hold-baseline.md`。

- 2026-08-20 单 Zone 物理缩容前的七 Zone Drain 正在执行。Coordinator Migration
  Worker 新增保持默认 `8/2/2` 的正整数环境变量覆盖；本地压测集群使用
  `global=16/per-source=4/per-target=16`（从 8/2/4 继续试升），聚焦测试通过，
  启动日志确认配置生效，首次 4096 Task 索引恢复后迁移连续成功且未观察到新
  Tcaplus 超时、Placement/CAS 冲突；两个既有异常 Shard 仍分别以
  `FINAL_FLUSH_FAILED` 和 `NOT_OWNER` 重试。单节点 Coordinator
  滚动更新不能并存两个 active 实例，本次采用先停后启恢复持久化迁移。只有七个
  draining Zone 全部 `owner_shards/open_tasks/open_progress=0` 且
  `removable=true` 后才能把 `zone-pool` 缩为 1。Evidence:
  `../evidence/2026-08-20-zone-drain-concurrency.md`。

- 2026-08-20 新一轮容量模型已完成离线工具改造：benchrunner 新增
  `-auth-mode gate-skip`，可从 `account_name,player_id` cohort 直接连接 Gate，
  跳过 Login/CSRF/Session/Ticket；Gate 仅在 `GATE_SKIP_AUTH=true` 时接受
  `TargetPlayerId` 压测 AUTH，默认 ticket 校验保持不变。`connect_hold` 改为每个
  已建连接只执行一次 snapshot，随后仅保持连接和 PING，避免重复 snapshot QPS
  失真。3 Gate 下有效单 Zone hotspot 与三 Zone spread 的对照计划见
  `../archive/development/plans/2026-08-20-zone-scalability-loadtest.md`。全量 Go 测试和 benchrunner
  构建通过；尚未部署新 Gate/benchrunner，也未执行正式 1/3 Zone 对照。

- 2026-08-19 三 Gate spread snapshot 短校准完成。benchrunner 支持逗号分隔 Gate
  URL 并按账号固定哈希；三个 loadtest-only NodePort 分别直达 gate-0/1/2，同窗
  10k profile 的 30 秒 CPU 为 18.50/21.56/19.84 秒，证明分流均匀。spread-800
  固定分流下，20k/25k 均完整承接、0 shed/0 error，P99 34.934/69.290ms；30k
  只完成 27,553.10 QPS、shed 70,519、P99 83.717ms。但三 Gate 峰值仅
  1.522–1.590/3 核，八 Zone 峰值 0.737–0.799/2 核，benchrunner 约 2.5–2.65
  核且 system/wait 较高，因此 27.5k 是生成器/串行连接池与服务组合平台，不能称为
  Gate 上限。当前只可声称三 Gate 至少健康承载 25k snapshot QPS。下一步先测
  `connect_hold` 长连接容量，再扩充/复用生成器连接寻找 Gate 请求上限。Evidence:
  `../evidence/2026-08-19-gate-spread-calibration.md`。

- 2026-08-19 Zone 8k/12k 对照 profiling 已完成。真实 hotspot open-loop 8k 完成
  7,999.91 QPS、0 shed、P99 23.961ms；12k offered 只完成 10,511.14 QPS、
  shed 133,687、P99 25.712ms。30 秒 CPU 从 53.74 秒增至 57.89 秒（1.79→1.93
  核），Zone 峰值 1.944 核。CPU 主要消耗在 H2C/HTTP2 response syscall、gRPC
  unary transport、protobuf、分配/GC 与 rpcauth；Player/Actor 整体约 4.6%，
  `State.Snapshot` 和 `materializeDueMaturities` 各约 2%，不是第一瓶颈。首选待验证
  优化是将内部 gRPC 从 `grpc.Server.ServeHTTP`+H2C 改为原生 gRPC listener，
  health/pprof 分端口；其次是在不放松校验的前提下降低 rpcauth 和 Snapshot 分配。
  Evidence: `../evidence/2026-08-19-zone-bottleneck-pprof-comparison.md`。

- 2026-08-19 单 Zone hotspot 第一阶段与 Gate 路由热路径优化已完成。100 个固定
  账号全部命中 `zone-pool-6`；优化前 closed-loop 10/25/50/100 阶梯为
  5,096.91/6,542.10/7,396.34/8,194.05 QPS，均 0 错误。pprof 发现 Gate 每条
  游戏请求仅为读取 `MapVersion` 就复制 4096 条路由，约占 Gate 累计分配 97%。
  改为 atomic snapshot 的 O(1) `MapVersion()` 后，同参数 c100 从 8,194.05 提升
  至 10,662.80 QPS（+30.1%），P99 从 31.056 ms 降至 25.174 ms（-18.9%），
  仍 0 错误；新 Gate profile 中整表复制热点消失，三个 Gate 峰值均低于 0.83 核。
  热点 Zone 的 30 秒 profile 达 58.07 CPU 秒，已接近 2 CPU limit，说明当前
  snapshot 主瓶颈已转移到 Zone。修复 open 模式忽略 `-account-file` 的工具缺陷
  后，真实单 Zone open-loop 8k/10k/12k offered QPS 分别完成
  7,999.86/9,975.24/10,569.08 QPS；shed 为 0/1,354/85,493，服务错误均为 0，
  Zone 峰值 1.938 核而三个 Gate 峰值不超过 0.885 核。当前 snapshot 饱和平台约
  10.5k QPS，8k 可作为待长稳/混合场景复核的首版保守水位。下一步用 spread
  cohort 与资源充足 Zone 测 Gate。Evidence:
  `../evidence/2026-08-19-zone-hotspot-route-copy-optimization.md`。

- 2026-08-19 Zone 压测 cohort 已建立：benchrunner snapshot 支持安全导出
  `account_name/player_id/shard_id` 及从 CSV 固定加载账号。复用 reg1000 账号、四
  Login 固定 endpoint、4 setup worker 和正常 HMAC ticket 后，1000/1000 在
  3m2.98s 建连。按 Coordinator `map_version=12317` 生成 `spread-800`（8 Owner
  各 100）与 `hotspot-100`（全部 zone-pool-6），完整材料在
  `/data/workspace/yace/cohorts/`。2 秒 hotspot 消费检查为 7988.12 QPS、P99
  31.651ms、0 错误；zone-pool-6 为 213m CPU，其余 Zone 31–32m，证明流量集中
  生效，但不是容量结论。正式测试前若 map version 改变必须重建 cohort。当前 live
  Gate 已关闭不可用的 `GATE_SKIP_AUTH` 绕过并恢复正常 ticket 路径。
  Evidence: `../archive/evidence/historical/2026-08-19-zone-loadtest-cohorts.md`。

- 2026-08-19 压测前 Gate 三副本健康基线已恢复：live StatefulSet 模板显式设置
  `GATE_PORT=""`，不再误读 Kubernetes 注入的 `GATE_PORT=tcp://...`。三 Pod
  滚动完成后 `gate-0/1/2` 均 Ready、restart=0、镜像统一，EndpointSlice 有三个
  Ready 地址；每个实例以独立 UID 和 StatefulSet DNS endpoint 正常监听。延迟复查
  的空闲用量为 1–2m CPU、35–45Mi 内存。该证据只确认可以开始压测校准，不是容量
  结论。Evidence: `../archive/evidence/historical/2026-08-19-gate-three-replica-recovery.md`。

- 2026-08-19 benchrunner 的 50 连接失败已定位为认证 setup 问题而非
  Gate/Zone 的 50 并发容量结论：实际 Login Service 有 4 个 Ready Pod 但
  `sessionAffinity: None`，短复现明确得到 register `HTTP 403 / CSRF_REJECTED
  (203)`；此前的 `HTTP 500 / code 501` 表示 Login 内部错误，不能解释为
  CSRF。同期 `gate-2` 因部署模板未覆盖 Kubernetes 注入的 `GATE_PORT=tcp://...`
  而 CrashLoop，Gate Service 只有两个 Ready endpoint。benchrunner 现会安全输出
  认证阶段、HTTP path 和 request-id；Go 聚焦测试/构建通过。压测必须先修复
  Login 请求亲和与三 Gate 健康，再把认证 setup 与 Gate/Zone 稳态测量分开。
  benchrunner 另已支持逗号分隔的多 Login URL，并按账号稳定哈希选择 endpoint：
  单个虚拟用户的 CSRF/Session/Ticket 请求不跨 Login，不同用户则可均摊 Login，
  避免单压测源使用 Service `ClientIP` 时把所有认证压到一个 Pod。四 Login 短复现
  进一步确认 16 个 setup worker 会在新账号注册的共享 Tcaplus
  `PlayerIdCounter` 八次无退避 CAS 上得到 register `500/501`；复用账号并降为 4 个
  setup worker 后，50/50 连接在 9.581 秒完成，5 秒 snapshot 得到 22,465 成功、
  0 错误、4482.15 QPS、P99 25.421ms。该结果使用 Service port-forward、仅 5 秒且
  第三个 Gate 未 Ready，只证明原失败不是 Gate/Zone 的 50 并发容量边界，不是正式
  多 Gate 容量结论。完整压测计划、采集脚本与原始结果在
  `/data/workspace/yace/`。
  Evidence: `../archive/evidence/historical/2026-08-19-benchrunner-auth-setup-diagnosis.md`。

- Eight-Zone pool/static A/B retirement has migrated all 4096 authoritative
  routes to eight pool owners. At live `map_version=12317`, all routes were
  ACTIVE and A/B each owned zero Shards before deletion. A final recovery race
  left 13 OPEN Progress rows at TARGET_READY even though their exact target
  ACTIVE routes were committed; Planner now recognizes complete committed-task
  Route evidence and preserves those Tasks for idempotent Worker cleanup. The
  Kubernetes A/B Deployments/Services were deleted, but the coordinator drain
  snapshot still reports one stale `zone-b` progress row (`shard 1623`,
  `TARGET_READY`, task already `CANCELLED`). Offline migration/placement and
  complete Coordinator tests pass. Evidence:
  `../evidence/2026-08-17-eight-zone-pool-drain-offline.md`.

- Gate 三副本精确 Push 路由已完成离线主体实现：Gate 使用 StatefulSet、Pod
  UID 实例 ID 和 `gate-N.gate-headless` 直连地址；Zone 的玩家连接及跨 Zone
  访客租约保存 `(gate_id, gate_endpoint)`，所有 owner state/presence、好友
  farm patch 与 red-dot fan-out 均通过按精确目标建池的 Router 发送，不再通过
  `service/gate` 随机落 Pod。聚焦测试、Gate 测试、全 Go 编译门槛与 kustomize
  渲染通过；Gate 零投递会显式返回 `NOT_FOUND`。尚未构建镜像/更新 kind，
  空闲连接清理仍待补。
  Evidence: `../evidence/2026-08-17-gate-precise-push-routing-offline.md`。

- MailSvr/FriendSvr 三副本与 gRPC 客户端负载均衡已通过离线实现门槛：所有
  Mail/Friend 调用方支持 Headless Service `dns:///` 地址并使用
  `round_robin`；只有只读查询和幂等 Ack 对 `UNAVAILABLE` 重试一次，好友码
  写入、邮件创建和 `ClaimMail` 禁止透明重试。Kubernetes 清单声明各 3 副本、
  Ready-only Headless Service、`maxUnavailable: 0`、软拓扑分布及
  `minAvailable: 2` PDB。带真实 HMAC 防重放的多后端 failover 测试连续 5 次
  race 通过，相关包 race 回归及清单 dry-run 通过。尚未构建镜像或更新 kind，
  三 Pod DNS/流量分布/删 Pod 实测待手工部署后完成。Evidence:
  `../evidence/2026-08-16-mail-friend-three-replica-grpc-balancing.md`。

- FriendSvr 已停止启动 5 秒一次的 `FriendLinkSaga` 全表恢复扫描，避免持续
  Traverse 和未来三副本下的重复扫描。在线好友码兑换及 Saga 写入保持不变；
  FriendSvr 在多行好友绑定中途故障后不再自动补偿，玩家需要重新添加好友，
  这是明确接受的一致性降级。Reconciler 代码仅保留作手工/历史恢复工具，
  不在生产启动路径运行。Evidence:
  `../archive/evidence/historical/2026-08-16-friend-saga-scan-disabled.md`。

- 两章任务与奖励已完成离线实现：新玩家配置版本提升为 2，第一章奖励改为
  10 金币、1 肥料、3 南瓜种子；第二章三个好友任务已有中文名称，奖励为
  10 金币、5 肥料、10 西瓜种子。第二章作为终章可正常领取并以 `CLAIMED`
  保留，H5 支持 1/2 章翻页、终章提示，以及按玩家/章节记录的“可领奖待查看”
  任务红点（打开任务面板即清除，不改变服务端领奖状态）。不迁移或回填旧玩家。
  Player 包、前端 34 项测试及生产构建通过；真实 kind/Tcaplus 新玩家链路待部署
  后手测。Evidence: `../archive/evidence/historical/2026-08-16-two-chapter-task-rewards.md`。

- 2026-08-16 kind 集群已用 `deploy/kind-config.yaml` 重建，宿主机固定映射
  `31238 -> login NodePort`、`32591 -> gate NodePort`；Login/Gate Service 同步
  固定对应 `nodePort`。7 个当前源码镜像和两个运行 Secret 已恢复，8 个
  Deployment、4 副本 `zone-pool` 全部 Ready。宿主机两个 `/readyz` 均成功，
  Gate Endpoint 包含 3 个 Pod；不再需要 `kubectl port-forward`。腾讯云 CLB
  仍需在控制台把临时后端端口 `18080/18081` 改为 `31238/32591`。
  Evidence: `../archive/evidence/historical/2026-08-16-kind-clb-nodeport-rebuild.md`。

- H5 邮箱“无未读仍显示 1”已修复：旧代码把 Mail `SET + count=0` 当作未知
  数量并强制保留 1；在绝对未读数协议下现在直接接受权威零值。新增 Push
  回归后前端 30 项测试及生产构建通过，仅需 Vite 热更新。

- 邮件领取在线主链路已按 ADR-0013 改为低延迟直连：不创建/推进
  MailClaimSaga，私人/礼物邮件优先点查后调用 Owner Zone；Actor 修改内存、追加
  Receipt、标 Dirty 即返回，不同步 SaveCAS；claimed/read 和 Info 未读数在响应后
  异步更新。旧 Saga/Reconciler 仅保留处理遗留记录。明确接受响应后的 Dirty
  崩溃窗口可能丢奖励，以及邮件状态落库前故障可能导致重试重复奖励。Player/Mail
  race 聚焦测试通过；真实 Tcaplus 单次计时待部署后验证。Evidence:
  `../archive/evidence/historical/2026-08-15-direct-mail-claim-fast-path.md`。

- 公共邮件打开语义已改为“先展示、后异步回写已读”：`OpenMailbox` 会把当前可见
  的公共未读邮件先在响应里标为已读，并立即刷新 Info 里的未读数；后台再异步
  将对应 `PlayerMailState.read` 回写到 Tcaplus。这样打开邮箱本身就会消除公共
  邮件红点，不再依赖后续手动点击。Evidence:
  `../archive/evidence/historical/2026-08-15-public-mail-open-auto-read.md`。

- 邮箱列表的私人数据查询已从全表 Traverse 改为玩家二级索引查询：
  `PrivateMail` 按 `recipient_player_id`、`PlayerMailState` 按 `player_id` 一次
  读取，既不扫描其他玩家私人邮件，也消除逐封状态 N+1。MailSvr 不再启动
  5 秒 ClaimReconciler/`MailClaimSaga` Traverse。`mail_tables.proto` 已新增
  `idx_recipient` 和 `idx_player`；旧表缺少索引时会兼容回退 Traverse，避免
  MailSvr 中断，但只有重建 `PrivateMail`/`PlayerMailState` 后才走快路径。
  Mail Store/cmd race 回归通过，真实 Tcaplus 单次延迟待部署验证。

- `PlayerMailboxCursor` 已退出邮箱运行时热路径：打开邮箱不再执行 Cursor
  DoGet/CAS，登录未读校准、标记已读和 Claim 后刷新也不再读取它；未读权威
  统一为 `PlayerMailState.read`，Info 使用权威计算时间作为事件水位。表和协议
  字段暂留兼容，无 schema 变化；Mail/Info race 回归通过。

- 首次登录邮箱未读数量显示已修复：H5 原本就会在快照后调用
  `CHECK_MAILBOX_INDICATOR`；第一处根因是 Gate Mail gRPC 适配层遗漏
  `new_mail_count`，第二处根因是旧实现按“上次打开邮箱后新增”计数并在打开
  邮箱时清零。现在登录按权威 `PlayerMailState.read` 校准并修复 Info 缓存，
  打开不清零，逐封阅读/领取才减一；普通阅读和 Claim Saga 成功都会把最新
  绝对未读数写回 Info，保证后续在线新邮件在正确基线上递增。部署需更新
  Mail 和包含字段修复的 Gate，
  Zone/Info 不需滚动，前端本地 Vite 自动热更新。Evidence:
  `../archive/evidence/historical/2026-08-15-infosvr-quick-info-mail-count.md`。

- H5 图鉴新解锁红点已完成离线验证：快照/Patch 中新增的解锁作物会同时点亮
  顶部图鉴按钮和对应图鉴条目；关闭图鉴或切换抽屉后才按玩家写入浏览器
  已读基线并清除。首次使用不会把历史解锁误报为新增，无服务端协议或表变化。
  5 个前端测试文件共 28 项测试及生产构建通过；真实浏览器收获后的视觉验收
  待执行。Evidence: `../archive/evidence/historical/2026-08-15-h5-compendium-red-dot.md`。

- 好友红点语义已拆分：顶部“好友”按钮只表示好友面板有未查看的成熟通知，
  打开好友列表后立即清除；列表内每个好友农场的成熟红点继续跟随成熟状态，
  进入某个好友农场不再把该农场红点抹掉。前端单测已覆盖按钮态清除与列表态
  保留。Evidence: `../archive/evidence/historical/2026-08-15-friend-red-dot-separation.md`。

- All four visitor farm mutations (steal/apply pest/catch pest/help clean) use
  the direct visitor→owner path and no runtime FriendInteraction Saga. Both
  Owner farm mutation/receipt and Visitor coin/task/receipt commit in Actor
  memory, `markDirty`, and return without waiting for Checkpoint SaveCAS. A
  crash inside either Dirty window may lose an already-acknowledged effect and
  its receipt, and a retry may execute it again; this weaker boundary is
  explicitly accepted. Evidence:
  `../archive/evidence/historical/2026-08-15-friend-action-owner-async-dirty.md`.

- Actor idle eviction **phase 1** is implemented: Actor-owned maturity
  deadlines replace the Runtime one-second full scan; idle Actors with no
  owner connection, no live visitors, idle mailbox, and no external access
  for 60 seconds are SaveCAS'd then removed; Zone sweeps every 10s. Redis
  offline maturity wake, TimerSvr, and QuerySvr remain phase 2/3. Evidence:
  `../archive/evidence/historical/2026-08-13-actor-idle-eviction-local-tick.md`.

- Final delivery sprint **07/06 normal migration worker** has passed its
  offline implementation gate through Tasks 1–5, a successful live Tcaplus
  migration/restart smoke gate, and one real process crash after persisted
  `SOURCE_DRAINING`. Persistent MigrationTask
  claim/retry/fail/complete, expanded MigrationProgress recovery, the
  durable-first one-Shard Executor, bounded Scheduler and transition-bound
  Zone lifecycle are implemented. The existing manual move endpoint now only
  creates a DRAIN-priority task and returns `202 + task_id`; it cannot execute
  the retired synchronous path. `COORDINATOR_MIGRATION_WORKER_ENABLED` is
  wired and remains `0` in Kubernetes. The full nine-boundary live Tcaplus
  crash/restart matrix is explicitly deferred; offline injection covers all
  boundaries, while the real crash moved Shard 10 from zone-b epoch 1 to
  zone-a epoch 2 and observed one automatic container restart. The earlier
  live gate moved Shard 1 from zone-a
  epoch 1 to zone-b epoch 2 (`route_version 1 -> 3`, `map_version 4117 ->
  4119`) and restored that exact durable ACTIVE route after Coordinator
  restart. It also found that live `MigrationTask` Traverse omits rows, so the
  Store now recovers the fixed 4096 primary-key space once at startup and then
  maintains an in-process open-task index. A development/E2E-only persisted-
  boundary crash hook and reusable matrix runner are present; production
  rejects crash injection. No new Tcaplus table or field is required. The focused
  Coordinator/Zone/Player/migration race suite passes. Evidence:
  `../archive/evidence/historical/2026-08-13-normal-migration-worker.md`.

- The next implementation target is final delivery sprint **07/07 Zone failure
  evidence and Failover**, beginning with validated/deduplicated transport
  failure evidence. It must reuse Phase 04 membership and the Phase 06 worker;
  SUSPECT may only pause routing, and ownership/Fence changes are forbidden
  until the owner is confirmed DEAD.

- Final delivery sprint 07/05 Placement and Rebalance Queue has passed its
  offline implementation gate: exact existing Rendezvous bytes produce a
  deterministic Desired map; `MigrationTask` has generated Tcaplus types plus
  memory/Tcaplus CAS stores; the disabled-by-default Planner consumes HEALTHY
  membership and persists only Current/Desired differences without granting
  ownership. The Tcaplus field-name limit was captured by a descriptor test and
  field 13 is `planned_availability_version`. The live kind/Tcaplus gate remains
  pending creation of the corrected `MigrationTask` table. Evidence:
  `../archive/evidence/historical/2026-08-13-placement-rebalance-queue.md`.

- Final delivery sprint 07/04 Zone identity and Kubernetes discovery is
  complete. The kind cluster runs `zone-pool-0` as a discovered HEALTHY
  candidate with stable UUIDv5 logical identity and zero owned Shards;
  replacement changes incarnation and advances availability without changing
  any of the 4096 durable routes (map version remains 4117). The live gate
  found and fixed ACTIVE Fence over-validation, cross-object resource-version
  comparison, stable-DNS probing and stale-event Controller exit defects.
  Evidence:
  `../evidence/2026-08-13-zone-kubernetes-discovery.md`.

- The single-player owner loop is complete through `player_seq=8`.
- Pure Tcaplus is green for auth, checkpoint, Fence, migration, Outbox and
  complete process restart.
- The fixed dual-Zone kind cluster now runs **eight** Ready Deployments
  (Login, Gate, Coordinator, zone-a, zone-b, FriendSvr, InfoSvr, MailSvr)
  after the 2026-08-11 rebuild/load/`kubectl apply -k` + rollout. Evidence:
  `../archive/evidence/historical/2026-08-11-k8s-redeploy-mail-info.md`. Earlier friend
  interaction / restart-recovery E2Es remain
  `../evidence/2026-08-07-friend-interaction-e2e.md`.
- Dynamic Zone discovery and automatic scaling are outside the prototype.
- Friend plan phases 0–7 are complete: contracts, gRPC+HMAC, FriendSvr,
  visit sessions, FarmViewPatch/Presence, steal Saga, pest/catch/help,
  H5 wiring, kind FriendSvr deploy, and multi-client WS E2E with full
  stack restart recovery.
- Final delivery sprint **01 Actor register-before-load** is complete:
  cold activation now creates a `Loading` Actor + mailbox under
  `Runtime.mu`, `Mailbox.Submit`s Load/init as the first job, then
  publishes the Actor so concurrent callers share one mailbox and one
  `Store.Load`. Failed activation is removed via `removeActorIfSame` and
  can retry; Loading actors participate in Drain/Close. Evidence:
  `../archive/evidence/historical/2026-08-10-actor-register-before-load.md`.
- Final delivery sprint **02 lazy farm init** is complete: LoginSvr
  registration creates account identity only (no PlayerCheckpoint /
  ShardFence). Owner Zone on first Actor activation treats clear
  `ErrCheckpointNotFound` as new-player init via fenced
  `CreateInitial`, and only then marks the Actor `Ready`. Evidence:
  `../archive/evidence/historical/2026-08-10-zone-initial-player-checkpoint.md`.
- Final delivery sprint **03 broadcast / business decoupling** is
  complete: plot commands report `DomainChanges`; the owner Actor
  builds ordered `FarmViewPatch` inside the mailbox; a bounded
  `farmview.Dispatcher` fans out via the existing Broadcaster. Broadcast
  remains online best-effort; H5 uses `decideFarmViewPatch` for
  contiguous apply / duplicate ignore / gap+epoch resync. Evidence:
  `../archive/evidence/historical/2026-08-10-farm-broadcast-separation.md`.
- Final delivery sprint **04-1 pet minimum loop** is complete: players
  start with no pets; buy/deploy 田园犬/牧羊犬, buy/feed dog food
  (24h stackable `food_active_until_ms`), and steal guard rolls once in
  `ApplyStealOnOwner` then freezes `StealGuardOutcome` into the Saga
  receipt; visitor commit deducts `min(coins, penalty)` without paying
  the owner. H5 `PetPanel.vue` is text-only. Evidence:
  `../archive/evidence/historical/2026-08-11-pet-guard-e2e.md`.
- Final delivery sprint **04-3A MailSvr** is complete: independent MailSvr
  (`:8087`) with Public/Private mail tables, mailbox cursor, read state,
  intranet Admin Bearer APIs, registration-time public-mail filtering, and
  fail-open InfoSvr red-dot notify on private create. Evidence:
  `../archive/evidence/historical/2026-08-12-mailsvr-query.md`.
- Final delivery sprint **04-3B friend gift** is now implemented as direct
  synchronous MailSvr submission: Gate checks mutual friendship, the sender
  Actor deducts crop inventory only after `CreateGiftMail` returns, and the
  result is recorded as a normal idempotent player command response rather than
  an Outbox relay event. Evidence:
  `../archive/evidence/historical/2026-08-12-friend-gift-outbox.md`.
- Friend-gift delivery no longer wakes a Zone Outbox relay. `zone` injects a
  direct `MailSvr.CreateGiftMail` client into the Player Runtime, and the
  sender Actor now uses mailbox await/continue to suspend while MailSvr
  returns before committing the sender state. The direct Tcaplus read retries
  a bounded 500 ms only while an inserted row is temporarily not visible. A
  live single sample reduced post-response mail red-dot latency from 2.779 s
  to 1.160 s; this is not a percentile claim.
  Evidence: `../archive/evidence/historical/2026-08-14-friend-action-mail-red-dot-latency.md`.
- Mail/friend-farm red-dot routing has passed its offline cutover gate:
  MailSvr now owns a Coordinator SDK subscriber and sends mail indicators
  directly to the recipient Owner Zone; Zone queues maturity events, queries
  FriendSvr and sends friend-farm indicators directly to each friend's Owner
  Zone. Both call sites no longer depend on InfoSvr, while the old Info RPCs
  remain for compatibility. Delivery groups by full Shard Route and retries
  `NOT_OWNER` once. No Tcaplus schema change is required. The four affected
  images are deployed and all 11 Pods are Ready; the InfoSvr-down functional
  E2E remains pending. Evidence:
  `../archive/evidence/historical/2026-08-15-direct-red-dot-routing.md`.
- InfoSvr quick-info projection is implemented and deployed: Zone connection
  leases publish online state (30s refresh / 90s expiry plus 3m reconcile),
  Actor mailbox summaries publish earliest maturity and mature candidates,
  FriendSvr batch-enriches FriendList, and MailSvr caches absolute
  `new_mail_count` while retaining Tcaplus/Cursor authority and cold-cache
  repair. H5 shows green bold 在线 / grey 离线 and a red numeric mail badge
  (`99+` cap). No Tcaplus schema change was needed. Race, Vue tests/build and
  11-Pod rollout are green; destructive/restart and user-visible functional
  E2Es remain pending. Evidence:
  `../archive/evidence/historical/2026-08-15-infosvr-quick-info-mail-count.md`.
- Manual testing found and fixed a cold/error-path gift indicator bug: MailSvr
  now repairs an unknown InfoSvr count from authoritative mail/cursor data
  instead of pushing an accidental zero; H5 keeps a `SET + count=0` fallback
  badge visible while fetching the exact count. Login already performs the
  InfoSvr-enriched FriendList query and seeds friend-farm indicators without
  opening the Friends drawer. The Mail image is rolled out; browser-visible
  revalidation remains pending. Evidence:
  `../archive/evidence/historical/2026-08-15-infosvr-quick-info-mail-count.md`.
- Offline farm indicators and login visitor reminders are deployed and startup-verified.
  Player Actors now publish the offline maturity summary only at eviction;
  online maturity remains an active Zone push. InfoSvr compares each offline
  farm's checkpoint revision with the viewer's seen revision, records at most
  50 distinct visitors while the owner is offline, and uses versioned ACK so
  a concurrent new visit is not cleared. H5 queries and acknowledges the list
  after login. No Tcaplus schema change is required; live cluster/browser E2E
  has rolled through Info/Friend/Gate/all Zones with 11 Pods Ready; browser
  dual-account E2E remains pending. Evidence:
  `../archive/evidence/historical/2026-08-15-offline-farm-visitor-info.md`.
- Final delivery sprint **04-3C mail claim Saga** is complete: MailSvr
  orchestrates `BeginClaim → Zone ApplyMailReward → CompleteClaim`; Player
  Actor grants attachments all-or-nothing with sync SaveCAS
  `MailClaimReceipt`; ClaimReconciler recovers the three crash windows.
  Evidence: `../archive/evidence/historical/2026-08-12-mail-claim-saga.md`.
- Final delivery sprint **04-3F H5 mailbox + red dots** is complete: Gate
  proxies `OPEN_MAILBOX`/`MARK_MAIL_READ`/`CLAIM_MAIL`; H5 shows mailbox
  modal, friend gift panel, mail/friend-farm red dots (local-only, cleared on
  click). Evidence: `../archive/evidence/historical/2026-08-12-h5-mail-red-dot.md`.
- Mail claim now carries the recipient version end to end: Zone
  `ApplyMailRewardResponse` reports `owner_epoch` beside `player_seq`, MailSvr
  `ClaimMailResponse` forwards a `state_version`, and Gate stamps it on the
  response envelope. Without it H5 rejected every successful claim with
  "写命令响应缺少 patch 或 state_version". The version is deliberately absent
  when an earlier attempt already applied the reward, and H5 then reloads a
  snapshot instead of sequencing the patch.
- `CHECK_MAILBOX_INDICATOR` (Action 328) closes the offline red-dot hole:
  `RED_DOT_CHANGED` only reaches players connected at delivery time and public
  mail never pushes at all, so H5 queries the indicator once after
  authentication. A failure there leaves the dot untouched and never blocks
  login. Evidence: `../archive/evidence/historical/2026-08-12-h5-mail-red-dot.md`.
- H5 is now a game shell instead of a stack of diagnostic cards: the login page
  is only `Grow!` + account/password (one button that logs in, or registers when
  the account is unknown), and after login a top nav (username/coins/账号·商店·
  宠物·好友·邮箱·任务·仓库) opens backdrop-less right drawers over a permanently
  visible farm, with a sticky tool bar (手/铲子/杀虫剂/肥料) and seed bar
  (all catalog seeds, unowned ones greyed, hover shows maturity time) below it.
  Connection timeline and Actor diagnostics moved into 账号 → 诊断. No server
  change. Evidence: `../archive/evidence/historical/2026-08-11-h5-shell-redesign.md`.
- H5 shell follow-ups after first live use: reactive `connected` + shell
  reconnect banner (dead sockets no longer look like "buttons do nothing");
  seed-bar `maturity_seconds` formatted via `Number(bigint)` (uint64 was
  crashing the whole render with "Cannot mix BigInt and other types");
  `MailKind.PUBLIC/PRIVATE/GIFT` enum names (same protobuf-es prefix trap as
  earlier red-dot bug); Vue `errorHandler` fatal banner outside the app;
  `npm run typecheck` now points at `tsconfig.app.json` (the root config was
  checking **zero** files). Evidence updated in the same redesign note.
- The starting farm is now **16 plots** (`InitialPlotCount`), and accounts
  created against the 4-plot build are backfilled lazily: `activateActor` calls
  `State.ensureInitialPlots()`, bumps `CheckpointRevision`, and lets the dirty
  flusher persist the new empty plots. H5 renders the plots frameless on one
  green lawn (`.plots-grid` paints the grass, `.plot-caption` overlays the text
  on the soil sprite) and the seed bar now lists only seeds the player owns.
  A fresh account on the restarted local stack returns `plots=16`. Evidence:
  `../archive/evidence/historical/2026-08-11-farm-16-plots-grass-ui.md` (backfill of pre-existing
  accounts and the visual pass remain owner checks).
- Mature plots now show the crop that actually grew there: ten new 16×16
  sprites (crops 2002–2011) come from the existing deterministic pixel script,
  `plot.mature` lost its baked-in generic crop, and `web/src/lib/crop-art.ts`
  maps `crop_id` to a sprite with a demo fallback. Evidence:
  `../archive/evidence/historical/2026-08-12-per-crop-mature-sprites.md`.
- The shop lists **every** seed as its own expandable row (per-crop quantity,
  total, and buy button) instead of a name picker that drove one shared buy
  form, and the deployed pet now sits beside the lawn: four new 32×32 dog
  sprites (田园犬/牧羊犬 × fed/hungry), breed above the head, and
  "xx护卫中（时间：hh:mm:ss）" / "xx现在很饿" driven by
  `food_active_until_ms`. Evidence:
  `../archive/evidence/historical/2026-08-12-shop-seed-rows-and-guard-dog.md`.
- Friend-farm visits now carry a public `FarmVisitSnapshot.pet` so visitors see
  the owner's deployed dog (or an empty "尚未获得宠物" slot). Evidence:
  `../archive/evidence/historical/2026-08-12-friend-farm-pet-badge.md`.
- Actor activation no longer treats "checkpoint revision ahead of persisted
  after Outbox prune" as corruption: `activateActor` only fails when
  `CheckpointRevision < persistedRevision`. That unblocked accounts that had
  sent friend gifts and then could not reload snapshots
  (`SERVICE_UNAVAILABLE`). Postmortem:
  `../bugs/2026-08-11-gift-outbox-activation-revision-mismatch.md`.
- Final delivery sprint **04-4 share-link auto friend** is implemented:
  FriendSvr returns `share_url` from `PUBLIC_WEB_BASE_URL`; H5 stores pending
  invite codes and auto-redeems after AUTH; `FirstFriendReward` + Saga steps
  grant both players a system mail (10 coins + 4 grape seeds) only on the
  invitee's first successful friendship. Evidence:
  `../archive/evidence/historical/2026-08-12-local-friend-invite-link.md` (unit-tested; dual-
  browser E2E and Tcaplus `FirstFriendReward` table creation remain owner).
- Final delivery sprint **04-5 multi-crop steal** is complete: all 11 crops
  freeze steal limits from `ceil(base_yield/2)`; steal requests carry
  `expected_crop_item_id` + farm-view version; FriendInteraction persists
  crop/qty; same visitor once per crop round; H5 sends plot `crop_item_id`.
  Evidence: `../archive/evidence/historical/2026-08-12-multi-crop-steal.md`.
- Steal (`STEAL_FRIEND_CROP`) now uses a **direct visitor→owner success
  path**: visitor Zone validates the request and calls owner
  `ApplyVisitorAction`; owner mutates via `ApplyStealOnOwner` and returns
  `ResultPayload`/`FarmPatch` immediately. Steal no longer creates or
  reconciles a `FriendInteraction` Saga row, and no longer waits on
  visitor reservation/commit. Pest / catch-pest / help-clean remain on
  `ActionSaga` and are unchanged by this cutover. Evidence:
  `../archive/evidence/historical/2026-08-14-steal-direct-success-path.md`.
- Farm visits return a public snapshot on `ENTER_FRIEND_FARM` and now also
  receive incremental `FarmViewPatch` pushes: public plot mutations (plant,
  fertilize, harvest, clean, natural maturity) report `DomainChanges`, bump
  an Actor-local `farm_view_seq` inside the same mailbox call, and
  `farmview.Dispatcher` → `Broadcaster` fans the resulting patch out through
  Gate to the owner plus every currently registered visitor. H5 replaces the
  full snapshot on an epoch change or a seq gap and merges in place on
  `seq == local + 1`.
- Historical note: the cross-Actor `FriendInteraction` Saga was previously
  live for `STEAL_FRIEND_CROP` (`INIT → … → COMPLETED` with a 5s reconciler).
  That steal recovery path is retired; `ReserveSteal`/`CommitSteal`/
  `ReleaseSteal` remain in `player.Runtime` for compatibility and for
  pest/catch/help ActionSaga steps that still share the package. Gate and
  H5 still route `STEAL_FRIEND_CROP` like other visit actions; after the
  direct path, H5 should treat steal success from owner result +
  `FarmViewPatch` rather than expecting an immediate visitor inventory
  patch on this hop.
- Final delivery sprint **07/01 Coordinator contract baseline** is complete:
  existing route records gained additive assignment-version/endpoint fields;
  the generated Coordinator unary/Watch/failure-evidence gRPC contract and
  retryable routing error numbers 204–207 are frozen. Runtime wiring, durable
  ShardRoute, Kubernetes discovery and failover remain later phases. Evidence:
  `../archive/evidence/historical/2026-08-12-coordinator-contract-baseline.md`.
- Final delivery sprint **07/02 durable Current ShardRoute** is live-verified:
  opt-in Tcaplus `ShardMapMeta` + 4096 `ShardRoute` rows, self-contained pending
  intent recovery, exact Map restore, durable-first manual migration and a
  binding-checked runtime lease-expiry overlay pass fake-Tcaplus/process/unit
  regression. Live Tcaplus now completes a Fence-aligned 4096-row Meta-last
  bootstrap in about six minutes with a configurable 10-minute budget; normal
  Load avoids per-row `DoGet`, and a clean restart restores the same durable
  Current in about two seconds (`bootstrapped=false`). The kind default remains
  `legacy-fence` pending an explicit deployment-mode change. Evidence:
  `../evidence/2026-08-12-durable-current-shard-route.md`.

## Current accepted direction

- ADR-0012 is the accepted Coordinator evolution target: Kubernetes discovers
  Zone membership and elects one Coordinator Leader; Tcaplus persists Current
  ShardRoute/Fence/MigrationProgress; embedded SDKs receive committed route
  updates. This replaces ADR-0008's production 2/3 Route Log implementation
  choice, while retaining its single-Owner, epoch, Fence and cached ordinary
  request-path principles. None of ADR-0012's dynamic discovery, Watch,
  automatic failover or Leader Election is implemented yet.

- The 30-million-DAU production target uses stateful Player Actors in Zone processes.
- One logical shard has exactly one write-authorized Active Zone Owner at a time; one Zone owns many logical shards.
- Player IDs map to 4096 versioned logical shards. Placement may use Rendezvous Hashing and load correction, but only the production Coordinator's majority-committed route grants ownership.
- GateSvr routes from a local cache of committed `ACTIVE` routes; ordinary commands do not call the Coordinator.
- The current prototype uses exactly two static Owners, `zone-a` and `zone-b`.
  Coordinator materializes versioned Rendezvous candidates into 4096 committed
  routes; Gate warms a complete immutable Snapshot and Zones atomically refresh
  read-only authorization Snapshots.
- Pure Tcaplus is the current persistence target. Account, Session,
  PlayerCheckpoint, ShardFence, MigrationProgress and PlayerOutbox adapters
  pass unit, live owner-loop, migration and restart checks without MySQL.
  MySQL remains a tested rollback adapter.
- The prototype implements Coordinator-compatible route, lease, epoch,
  state-transition and fencing semantics with one Coordinator process.
  Production consensus and dynamic Zone membership are not implemented.
- Commands for one player enter one Actor mailbox and execute serially.
- A successful ordinary write follows `validate -> apply Actor memory -> update task if matched -> player_seq++ -> save idempotency result/outbox -> checkpoint_revision++ -> mark Dirty -> reply`.
- `checkpoint_revision` orders persistence CAS and is not client-visible. Saving a terminal business failure, pruning idempotency results, or reconciling Outbox increments it without incrementing `player_seq`.
- A shared Zone flusher asynchronously persists Dirty checkpoints through
  `CheckpointStore`. Tcaplus is the current recovery store; active Actor memory
  remains online truth.
- V3 accepts that an abnormal Zone exit may roll back the latest unflushed ordinary game state. It does not use V2's Kafka Journal or MySQL `journal_events` path.
- Normal shutdown, Actor eviction, and controlled migration must drain the mailbox and flush Dirty state before ownership changes.
- WebSocket is established between Client and GateSvr and carries game commands, responses, snapshots, and pushes. The client does not connect directly to Zone.
- Current chapter-task state belongs to the Player Actor and is updated by successful server-side business actions. Rewards are claimed manually.

## Current business-design state

The first accepted vertical loop is:

```text
buy seed -> plant -> fertilize -> grow/mature -> harvest -> sell
-> update task -> claim reward -> clean plot
```

Important rules already recorded in the business architecture:

- ConfigSvr is the configuration authority; Zone uses an atomically replaced, versioned local snapshot.
- A command pins one configuration snapshot for its whole execution.
- Planting freezes the crop's maturity threshold, base growth rate, and base yield.
- Growth is derived from elapsed server time and the effective rate; it is not persisted by ticking every second.
- Shop price versions prevent purchases or sales from silently using stale prices.
- Farm, inventory, coins, current chapter task, recent idempotency results, and pending Outbox belong to one Player Actor checkpoint.
- Full warehouse makes an ordinary harvest fail atomically; task reward items that do not fit use a mail Outbox fallback.
- Client-visible configuration is an immutable versioned Protobuf package delivered over HTTP and verified by SHA-256; it never becomes transaction authority.
- A pending reward-mail Outbox is recorded atomically in Actor state but becomes database-durable only after the asynchronous checkpoint/Outbox transaction commits.
- Friend phases 0–5 are accepted: contracts, internal gRPC/HMAC migration,
  FriendSvr share-code/relation/list Saga, Zone `ApplyFriendTaskCredit`,
  farm-visit sessions (`ENTER`/`HEARTBEAT`/`EXIT_FRIEND_FARM` plus a
  one-shot public snapshot), and incremental public-Patch broadcast. Steal
  still uses the existing FriendInteraction Saga today; a separate plan now
  targets a direct visitor→owner success path for steal only. Pest/catch-pest/
  help-clean still use ActionSaga / remain the Phase 6 follow-on track.

## Current architecture and decision map

Current architecture:

- `../architecture/stateful-zone-v3-architecture.md`: current distributed target.
- `../architecture/single-player-vertical-loop-business-architecture.md`: accepted first-slice business design.

Accepted first-stage contracts:

- `../contracts/http-api.md`: registration, Session, one-time WS ticket, Gateway discovery and client-config bootstrap.
- `../contracts/websocket-protocol.md`: Protobuf game connection, commands, responses, snapshots, patches, Push and reconnect.
- `../contracts/idempotency-and-errors.md`: request identity, retained results, retry and error behavior.
- `../contracts/data-model.md`: Player checkpoint, `checkpoint_revision`, ShardMap, fences, Dirty batches and Outbox storage.
- `../contracts/event-contracts.md`: reward-mail event, relay and consumer deduplication.
- `../contracts/internal-grpc.md`: internal unary services, HMAC identity,
  deadlines, errors and retry boundaries.
- Complete Chinese reading mirrors use the `.zh-CN.md` suffix.

Current supporting decisions referenced by V3:

- ADR-0003: stateful Player Actor Zone foundation. Its V2 Journal-specific text is historical where V3/ADR-0006 conflicts.
- ADR-0006: asynchronous Dirty writeback supersedes ADR-0005's Journal write path.
- ADR-0008: V3 retains majority-authorized Shard ownership, replacing ADR-0004 as the current V3 statement.
- ADR-0009: current chapter-task progress belongs to Player Actor.
- ADR-0010: local prototype keeps unused WS tickets and CSRF nonce records
  process-local; Login restart drops them even when MySQL Sessions survive.
- ADR-0011: Player Runtime depends on one `CheckpointStore` contract. Logical
  checkpoint revision and opaque physical Store Token are kept separate so
  MySQL and Tcaplus can expose the same Load/CAS semantics.

Historical design evidence:

- V1 and ADR-0002: stateless target.
- V2, ADR-0004, and ADR-0005: synchronous-Journal-era routing and persistence design.
- ADR-0001: earlier modular-monolith implementation decision; useful history, not the current distributed architecture definition.

## Capacity planning values

These remain planning assumptions, not measured claims:

- 30 million DAU; 1.25 million average online; 3.75 million normal peak online/WebSocket connections.
- 4.5 million connection/reconnection pressure capacity.
- About 5 million peak resident Actors.
- About 69,444 peak game application messages per second.
- 4096 logical player shards.
- About 60 Zone instances as a pre-benchmark midpoint.

All values must be revised from reproducible prototype measurements before being presented as capability.

## Completed historical milestone: first stage by 2026-08-02

Goal:

```text
freeze the minimum protocol and data model
-> establish the Go backend and Vue 3 H5 runnable skeleton
-> login from H5
-> authenticate one Protobuf WebSocket
-> send one command through GateSvr and Player Actor
-> receive the correlated response
```

Current milestone status:

- WebSocket connection, Protobuf envelope, commands, responses, Push, snapshot, state-patch and reconnect semantics: frozen.
- Error codes, `request_id`, idempotency retention and retry behavior: frozen.
- Client-facing minimum player, farm, inventory and chapter-task views: frozen.
- `(owner_epoch, player_seq)` state-version and resynchronization semantics: frozen.
- HTTP registration, login, Session, one-time WS ticket and Gateway discovery: frozen in `../contracts/http-api.md`.
- Versioned client-config delivery and publication: frozen in `../contracts/http-api.md`.
- Internal Player checkpoint, recent idempotency results and Outbox persistence shape: frozen in `../contracts/data-model.md`.
- ShardMap, Dirty batch, `checkpoint_revision` and database fence: frozen in `../contracts/data-model.md`.
- Reward-mail Outbox event, relay and consumer deduplication: frozen in `../contracts/event-contracts.md`.
- Cross-contract review aligned the HTTP bootstrap/WS AUTH field set and ticket boundary, and clarified replayed-error versions, committed-lineage sequence monotonicity, event-version scalar types, idempotency, Outbox and abnormal-recovery meanings.
- Bounded first-stage implementation plan: completed in `../archive/development/plans/2026-07-31-v3-first-stage-implementation-plan.md`.
- Shared HTTP/WS/data/event Protobuf generates Go and TypeScript types; both round-trip smoke tests pass.
- Login, Gate, Zone and the single-node Coordinator-compatible process compile; the complete Go test suite and `go vet ./...` pass.
- The Vue 3 H5 implements register/login, CSRF, bootstrap/config hash, Ticket, WS AUTH, snapshot/Shop reads, all seven owner-loop commands, `PLAYER_STATE_CHANGED` patch application and version-gap snapshot recovery. It presents four authoritative plots in a responsive 2x2 farm, with seed/fertilizer/shovel/hand tool selection, per-tool desktop cursors, state-aware plot targets, 1–50 seed purchases, quantity/all crop sales, inventory and chapter tasks.
- A repeatable four-process protocol client proves `register -> ws_ticket -> AUTH -> PING -> GET_PLAYER_SNAPSHOT -> RESPONSE` and Ticket replay rejection. Browser UI automation and MySQL persistence are not part of that evidence.
- The owner manually completed the same H5 registration-to-snapshot flow in a browser after an authenticated-CSRF binding defect was found and fixed. This is a manual smoke result, not automated browser evidence.
- `start-servers.ps1` builds and starts Login, Zone, Coordinator and Gate in dependency order, checks readiness and stops all child processes on exit.
- An optional `MYSQL_DSN` path provisions the account, initial deterministic `PlayerCheckpointV1` and first HTTP Session in one MySQL transaction, and loads the checkpoint when Zone first activates the Actor. Mocked-SQL tests, live registration-to-snapshot and fresh-process login/checkpoint-recovery E2Es pass on MySQL 8.4.11.
- `BUY_SEEDS` is the first implemented Actor write command. It validates the local versioned quote, mutates coins/inventory/task progress atomically, increments `player_seq`, retains terminal idempotency results, marks Dirty and returns a state patch. Same-ID replay does not apply the purchase twice.
- Zone now holds an immutable versioned local configuration snapshot behind atomic replacement. `GET_SHOP` returns active buy and sell quotes in stable order without activating a Player Actor; `BUY_SEEDS` and `SELL_CROP` derive authoritative prices and versions from the same pinned snapshot. This is the Zone-side bootstrap boundary, not a standalone ConfigSvr.
- `PLANT` is implemented in the Player Actor. It validates plot/config/inventory state, consumes one seed, freezes crop identity, maturity/rate/yield and timestamps, changes the plot to `GROWING`, advances the planting task, retains idempotency results and marks Dirty.
- Base crop growth now uses checked fixed-point settlement. Actor activation, each Actor request and a local one-second online scan materialize due plots in stable `plot_id` order; each `GROWING -> MATURE` transition increments both versions once and marks Dirty.
- Online maturity emits one unsolicited `PLAYER_STATE_CHANGED/MATURED` Push per transitioned plot. Zone forwards it to Gate over a loopback-only internal Protobuf endpoint; Gate keeps authenticated per-player subscriptions, buffers while a snapshot is in flight, drops versions not newer than the snapshot and flushes newer Pushes in version order.
- `APPLY_FERTILIZER` is implemented. It settles the old rate, validates the active slot/config/inventory, consumes fertilizer, freezes a deterministic timed effect, recomputes maturity, advances the task and retains idempotency. Growth settlement splits exact intervals across effect boundaries.
- `HARVEST` is implemented as an all-or-nothing Actor command. It requires `MATURE`, checks the 100-type and 300-per-stack warehouse limits before mutation, adds the complete frozen yield, advances the harvest task, changes the plot to `NEED_CLEANUP`, retains the result for replay and marks Dirty. Live in-memory and MySQL four-process runs reached `player_seq=5`.
- The MySQL Zone path flushes Dirty checkpoints asynchronously, checks the exact local `shard_fences` owner and epoch, and updates `player_checkpoints` using checkpoint-revision CAS. An owner-run two-stack E2E recovered `player_seq=1`, 4 coins and three seed items after all four services restarted.
- The extended two-stack E2E coalesced `BUY_SEEDS` and `PLANT` into a checkpoint and recovered `player_seq=2`, 4 coins, two seeds and a `GROWING` crop after all four services restarted.
- The latest extension also persisted and recovered `APPLY_FERTILIZER` at `player_seq=3`, including empty fertilizer inventory and the timed effect identity.
- The owner-run MySQL harvest extension observed online maturity at `player_seq=4`, harvested three crop items at `player_seq=5`, stopped all four processes, and recovered two seeds, three crops and the `NEED_CLEANUP` plot from a fresh stack.
- `SELL_CROP` supports an explicit positive quantity or `sell_all`, validates the active sell rule and `price_version`, removes inventory, adds checked integer-price coins, advances the sell task and changes the chapter to `CLAIMABLE` when all five tasks are complete. Same-ID `sell_all` replay returns the first resolved quantity. A live in-memory run reached `player_seq=6`, 19 coins, no crop stack and `CLAIMABLE`.
- Chapter status is now preserved between the data checkpoint enum and client enum; previously checkpoint load/write forced `IN_PROGRESS`, which would have lost `CLAIMABLE` after restart.
- The owner-run MySQL sell extension stopped all four processes after `SELL_CROP` and recovered `player_seq=6`, 19 coins, two seeds, no crop stack, the `NEED_CLEANUP` plot and `CLAIMABLE` from a fresh stack.
- `CLAIM_CHAPTER_REWARD` now validates the frozen chapter identity and status, credits 10 coins, allocates one fertilizer and three next-chapter seeds under warehouse limits, activates development chapter two, retains the exact result and advances to `player_seq=7`. Same-ID replay does not grant twice.
- Reward overflow is deterministic by `item_id`. A full warehouse keeps the fitting quantities in Actor state and records one `CreateRewardMailV1` pending Outbox event for all remaining items; the response says `items_pending_mail`, not that a mail was delivered.
- `deploy/migrations/000004_player_outbox.up.sql` adds the relational relay table. A Dirty flush validates pending event payloads and atomically inserts or immutable-compares each `player_outbox` row in the same MySQL transaction as checkpoint CAS. The relay, Mail Service and delivered-event reconciliation are not implemented.
- Live in-memory and owner-run MySQL four-process flows completed claim at `player_seq=7`, 29 coins, one fertilizer, three next-chapter seeds and chapter two `IN_PROGRESS`. After all four MySQL-backed services stopped, a fresh stack recovered the same `player_id=9` checkpoint, including the `NEED_CLEANUP` plot.
- `CLEAN_PLOT` is implemented as an idempotent Actor command. It requires `NEED_CLEANUP`, consumes and grants nothing, advances no task, clears every frozen crop/growth/effect field and returns the plot to `EMPTY`. The H5 no longer blocks cleaning until the chapter-one reward is claimed.
- The development shop now returns three active entries in stable entry-ID order: seed sale (`1001`, 2 coins), crop buyback (`1002`, 5 coins), and basic fertilizer sale (`item_id=1`, 2 coins). `BUY_FERTILIZER` has its own idempotent Actor command, shares the 1–50 quantity and 300-stack rules, and does not advance the seed-purchase task.
- Live in-memory and owner-run MySQL four-process flows completed the server-side owner loop at `player_seq=8` and replayed cleanup without applying twice. After all four MySQL-backed services stopped, a fresh stack recovered the same `player_id=10`, 29 coins, expected inventory, chapter two and the `EMPTY` plot.
- A browser-driven in-memory H5 run registered `player_id=1`, bought, planted, fertilized, received one natural maturity Push, harvested, sold, claimed and cleaned through `state_version=1/8`. The final UI showed 29 coins, two old seeds, one fertilizer, three next-chapter seeds, chapter two and an empty plot; gap recovery remained zero. A 320 CSS-pixel viewport check reported no horizontal overflow.
- New development Player state and registration checkpoints now contain four stable `EMPTY` plots (`plot_id=1..4`). Commands still patch only their requested plot; snapshot and checkpoint ordering remain stable. Existing development checkpoints are not migrated online and must be reset/re-registered locally.
- A browser-driven four-plot run used plot 2 for plant, fertilizer, natural maturity Push, harvest and cleanup while plots 1/3/4 remained empty. It also exercised an explicit one-crop sale followed by `sell_all`, verified the 1/50 purchase boundaries and tool cursor URL, completed at 29 coins with all four plots empty, and reported no horizontal overflow at 320 CSS pixels.
- An owner-run MySQL 8.4 two-stack E2E registered `player_id=11`, completed the command loop to `player_seq=8`, stopped all four services, then recovered the same checkpoint from fresh processes. The updated snapshot assertions validated four ordered plots and kept plots 2–4 `EMPTY`.
- `start-servers.ps1 -DualZone` starts Coordinator, Login, Zone A on 8082,
  Zone B on 8084 and Gate in dependency order. With `MYSQL_DSN`, Coordinator
  requires explicit bootstrap authorization and aligns all Fences before Login
  accepts registrations.
- Linux now has executable `start-servers.sh`, `deploy/migrate.sh` and
  `tests/e2e/run-mysql-restart-recovery.sh`. On TencentOS Server 4.4 with
  Go 1.26.5 and Docker MySQL 8.4, the dual-Zone five-process stack completed
  the full owner loop to `player_seq=8`, stopped normally, restarted and
  recovered 29 coins, two old seeds and an `EMPTY` plot. A separate live
  dual-Zone check routed players to both Owners, migrated one active Shard
  from Zone A epoch one to Zone B epoch two, persisted the post-migration
  write and rejected a delayed old-Zone writer. See
  `../archive/evidence/historical/2026-08-04-linux-dual-zone-mysql-baseline.md`.
- The first ADR-0011 implementation is complete. Runtime no longer injects
  separate checkpoint Loader/Writer interfaces; it carries
  `PersistedRevision` plus an opaque `StoreToken`, and consumes normalized CAS
  outcomes. `MySQLCheckpointStore` preserves the existing Fence,
  revision-CAS and Checkpoint/Outbox transaction. Full Go regression and the
  live Linux five-process restart/active-migration/Fence E2E pass after the
  refactor.
- The Tcaplus `PlayerCheckpoint` POC is complete against a real PB table using
  the official Go SDK module `v0.2.3` (API 3.55). It proves Create, Load,
  record-version plus logical-revision CAS, duplicate-commit reconciliation,
  stale-write rejection and reload. This was the single-table checkpoint;
  the pure-Tcaplus runtime described below supersedes its earlier limitation. See
  `../archive/evidence/historical/2026-08-04-tcaplus-player-checkpoint-poc.md`.
- The owner selected immediate pure-Tcaplus runtime work on 2026-08-05.
  PlayerIdCounter, account provisioning Saga, durable Session generation,
  ShardFence, MigrationProgress, fenced Checkpoint CAS, PlayerOutbox and
  activation reconciliation now have adapters and hermetic tests. Login, Zone,
  Coordinator and `start-servers.sh --dual-zone --tcaplus` are wired to reject
  `MYSQL_DSN`. The live table group now has all eight runtime tables. The
  no-MySQL five-process gate registered players, routed both Owners, persisted
  gameplay, migrated inactive and active Shards, and passed a complete
  post-migration restart. Fence bootstrap uses one Traverse plus bounded
  parallel inserts and preserves advanced epochs for route hydration. See
  `../evidence/2026-08-05-pure-tcaplus-runtime-gate.md`.
- The owner explicitly excluded dynamic Zone discovery and selected exactly
  two static Kubernetes Zones. A kind cluster now runs Coordinator, Login,
  Gate, `zone-a` and `zone-b` as five Deployments with pure Tcaplus storage.
  All Pods reached Ready and the live dual-Zone owner loop plus inactive/active
  migration E2E passed. `INTERNAL_NETWORK_MODE=kubernetes` is an explicit
  non-production Pod-network exception; local mode remains loopback-only.
  Zone-level Drain/preStop, HPA, PDB and replica scaling are not implemented.
  See `../archive/evidence/historical/2026-08-05-k8s-fixed-dual-zone.md`.
- Friend functionality was reviewed on 2026-08-05 but has no product code yet.
  The accepted design uses an authoritative `FriendRelation`, repairable
  FriendList projections, Tcaplus Sagas, activation-scoped public-farm epochs,
  full game-internal gRPC migration and HMAC-authenticated Metadata. Chapter
  two will contain add-friend, steal-crop and apply-pest-to-friend tasks;
  successful friendship advances both players. See
  `../archive/development/plans/friend_design_plan/01-FriendSvr详细设计.md` through
  `../archive/development/plans/friend_design_plan/06-分阶段实施方案.md`.
- Assignment algorithm V1 uses deterministic SHA-256 Rendezvous scoring over
  `shard_id` and stable `zone_id`. Gate and Zone do not treat that calculation
  as authority; only the Coordinator's committed Route with Zone, endpoint,
  epoch, route version, state, Lease and map version is routable.
- Gate forwards trusted Shard/Zone/epoch/version metadata. Zone recomputes the
  target player's Shard and rejects wrong Shard, wrong Zone, stale epoch,
  non-`ACTIVE` state or expired Lease before Actor activation.
- A five-process dual-Zone E2E routed `player_id=2/shard=1631` to Zone A and
  `player_id=1/shard=2066` to Zone B, kept the other player isolated after one
  purchase, rejected a direct wrong-Zone command with `409 NOT_OWNER`, and
  observed zero Coordinator single-Shard lookups during ordinary commands.
- Gate cache tests cover immutable Snapshot warmup, concurrent miss collapse,
  conditional route-version invalidation and one same-`request_id` retry after
  `NOT_OWNER`.
- The dual-Zone prototype now supports loopback-triggered migration of an
  inactive Shard. Zone takes an exclusive per-Shard execution gate, blocks new
  commands, rejects migration if any Player Actor is active, and otherwise
  allows Coordinator to commit `PREPARING` and `ACTIVE` with epoch increment.
- Actor epoch is activation-scoped and now flows through snapshots, command
  results, idempotency records, pending Outbox, checkpoints and maturity Push.
  The migration E2E moved `player_id=7/shard=3552` from Zone A epoch 1 to Zone B
  epoch 2; Gate refreshed exactly one stale cached Route and the snapshot
  completed on B. An active Shard migration was rejected without changing its
  Route.
- MySQL mode now supports controlled active-Shard migration. Zone first blocks
  new commands; Coordinator commits `PREPARING`; the old Zone excludes
  command, maturity and background-flush races, settles and final-flushes every
  active Actor, returns a durable manifest and evicts only after all succeed.
  Coordinator then advances the exact transition-bound MySQL Fence, the target
  rewrites and validates those checkpoints at the new epoch, and only then
  commits `ACTIVE`.
- Fence CAS and target preparation are idempotent. MySQL mode persists
  per-Shard migration progress through drain, Fence and target preparation.
  Coordinator restart rebuilds `ACTIVE` routes from fences, overlays open
  `PREPARING` fail-closed, and exposes loopback inspect/continue/abandon.
  Abandon before Fence restores the source Owner and burns the prepared
  epoch; abandon after Fence is refused. Before `PREPARING`, failure resumes
  the old Owner; after it, failure remains non-routable and never reuses the
  epoch.
- R3 now has a first local protocol benchmark scaffold:
  `server/cmd/benchrunner` creates isolated `bench_` accounts, establishes the
  actual HTTP/CSRF/Ticket/Protobuf WebSocket path, and measures closed-loop
  `GET_PLAYER_SNAPSHOT` latency for configurable 1–100 virtual users. It
  writes JSON, CSV and Markdown under ignored `benchmark/results/`, persists
  each completed stage, classifies errors and stops a virtual user after its
  first failure.
- The MySQL-backed snapshot baseline used 10-second warmup and 60-second
  samples. Successful QPS for 1/10/25/50/100 virtual users was respectively
  3,094.83 / 13,250.00 / 15,046.84 / 16,080.84 / 13,846.43, with zero errors
  after the fix. P99 was 1.029 / 2.090 / 4.523 / 9.225 / 23.256 ms. The
  observed throughput knee was 50 users on this host.
- R3 exposed a Gate-to-Zone HTTP pool defect: Go's default client retained too
  few idle connections and could select connections after Zone's shorter
  server idle timeout, producing six repeatable `SERVICE_UNAVAILABLE` results.
  Gate now allows 64 idle connections per host and retires them at 20 seconds,
  before Zone's 30-second close. Local failure counters remained zero across
  the post-fix matrix. This remains a single-host read-path baseline, not a
  production or 30-million-DAU claim. Actor contention, Push, Dirty, CPU and
  memory measurement remain next. See
  `../archive/evidence/historical/2026-08-03-r3-snapshot-read-baseline.md`.

Work order for this milestone:

1. HTTP, WebSocket, error/idempotency, data-model and minimum reward-mail event contracts are frozen and have one bounded implementation baseline.
2. Materialize the contracts as shared Protobuf and generated Go/TypeScript types.
3. Build the smallest Go + Vue 3 skeleton and prove `register/login -> ws_ticket -> AUTH -> GET_PLAYER_SNAPSHOT -> RESPONSE`.
4. Record repeatable evidence; do not expand friends, multiplayer or full mail UI.

## Actual prototype data ownership

With neither `STORAGE_MODE=tcaplus` nor `MYSQL_DSN`, the runnable code
deliberately uses development-only in-memory adapters:

- LoginSvr stores accounts, Argon2id password hashes, Sessions, CSRF records and one-time tickets in process-local Go maps. Registration allocates a sequential `player_id`; restarting LoginSvr loses all of these records.
- Registration does not yet create a durable Player checkpoint and does not call Zone.
- ZoneSvr stores one lazily created Player Actor per `player_id` in a process-local map. The first player command creates the development state with 10 coins, one basic fertilizer, four empty plots and chapter-one tasks.
- `GET_PLAYER_SNAPSHOT` is routed by Gate through its locally cached committed
  Route, executes on the selected Zone's Player Actor mailbox and projects the
  snapshot from current Actor memory. The default mode still uses one local
  Zone; `static-dual-zone` uses two independent Actor runtimes, backed either
  by process memory alone or by assigned-Fence MySQL checkpoints.
- Gate keeps authenticated player subscriptions in process memory. Online
  maturity travels from Zone to Gate through HMAC-authenticated Unary gRPC and
  is forwarded as a Protobuf Push; reconnect or any detected version gap uses a
  fresh snapshot rather than replaying Push history.
- `GET_SHOP` is routed to Zone and reads the pinned global configuration snapshot without activating a Player Actor.
- Coordinator route state is also process-local. In this mode, Dirty
  writeback, database Fences and restart recovery are not implemented.
- `deploy/migrations/000001_platform.up.sql` creates the migration ledger.

With `STORAGE_MODE=tcaplus`:

- Login uses durable PlayerIdCounter CAS, account provisioning Saga and
  Session generation;
- Zone loads and saves Player checkpoints with physical-version plus logical
  revision CAS, exact ShardFence validation and PlayerOutbox reconciliation;
- Coordinator persists ShardFence and MigrationProgress and hydrates advanced
  routes after restart;
- no `MYSQL_DSN` is accepted;
- the live eight-table environment passes pure-Tcaplus five-process and kind
  fixed-dual-Zone owner-loop/migration checks.

With `MYSQL_DSN`, the new code path:

- uses `deploy/migrations/000002_auth_player_checkpoint.up.sql` for account, HTTP Session and Player checkpoint envelopes;
- uses `deploy/migrations/000003_local_shard_fences.up.sql` to bootstrap 4096
  epoch-one Fence rows; static dual-Zone startup can atomically align those
  untouched rows to the committed Zone A/B assignment;
- uses `deploy/migrations/000005_shard_migration_progress.up.sql` for open or
  abandoned migration progress; completed migrations delete the OPEN row;
- makes registration externally atomic by locking the player's assigned Fence
  and committing the account, first Session and initial checkpoint in one
  local MySQL transaction;
- stores only the Session digest, not the raw cookie value;
- validates the deterministic checkpoint blob, SHA-256 and relational envelope before activating a Zone Actor;
- fails Actor activation instead of silently creating default state when a configured checkpoint load fails;
- executes `BUY_SEEDS`, `PLANT`, `APPLY_FERTILIZER`, `HARVEST`, `SELL_CROP`, `CLAIM_CHAPTER_REWARD` and `CLEAN_PLOT` inside the Player Actor mailbox, retains their idempotency results and marks the aggregate Dirty;
- asynchronously writes the whole checkpoint under exact process-Zone and
  epoch Fence validation plus checkpoint-revision CAS, including atomic
  relational Outbox creation when a reward overflows inventory.

The single-Zone path has live MySQL registration-to-Actor-load and fresh-process
restart evidence for the complete server owner loop at `player_seq=8`. The
static dual-Zone path has live active migration evidence: `player_id=14`,
Shard 3371 moved from Zone A epoch one to Zone B epoch two, preserved its first
write and persisted a second write on B at `player_seq=2`. A direct old-Zone
command and a delayed Zone-A checkpoint write were rejected. Unused WS tickets
and CSRF nonce records remain process-local by ADR-0010 even when MySQL stores
accounts and Sessions; Login restart requires a new CSRF bootstrap and ticket
(or a fresh login). Online Actors remain process-local. Coordinator routes are
rebuilt from fences plus durable migration progress on MySQL restart. Live
reward-overflow Outbox insertion, abnormal termination inside the Dirty-loss
window, restart while still crossing an active effect/maturity boundary and
multi-Actor batching remain unverified.

The static bootstrap is deliberately mode-specific: after `zone-local` Fences
are converted to Zone A/B, that database must not be reused for local
single-Zone MySQL writes without a separately designed safe conversion.

The auth DDL and local values `AUTO_INCREMENT player_id`, `db_shard_id = 0`, initial `checkpoint_revision = 1`, `owner_epoch = 1`, four empty plots, seed quote `(shop_entry_id=5001, item_id=1001, unit_price=2, price_version=8)`, crop `(crop_id=2001, crop_item_id=1002, maturity=100, rate=1, base_yield=3)`, and fertilizer `(item_id=1, modifier=+0.5, duration=60s)` are proposed implementation conventions, not accepted contract decisions.

## Product code and evidence state

- The backend and H5 are complete for the bounded single-player/fixed-dual-Zone
  prototype, but are not production-complete.
- The architecture and first single-player business loop have initial accepted documents.
- `frontend/src/assets/art/` now contains an engine-neutral 16x16 pixel-art
  workspace, a business-mapped inventory, source/license ledger, 30
  project-owned runtime placeholder PNGs, a manifest, contact sheets, and a
  standard-library validator. These are development placeholders, not accepted
  final art.
- First-stage HTTP, WebSocket, idempotency/error, logical data-model and reward-mail event contracts are frozen under `../contracts/`, with complete `.zh-CN.md` reading mirrors.
- The bounded first-stage implementation plan and MT progress-sync outline are present.
- Shared `.proto`, generated Go/TypeScript types, platform helpers, four Go processes, the H5 snapshot client and initial local deployment files exist.
- `../archive/evidence/historical/2026-07-30-protobuf-toolchain.md` records generation/round-trip evidence. `../archive/evidence/historical/2026-07-31-authenticated-snapshot-e2e.md` records one passing loopback multi-process command path using explicit in-memory development adapters.
- `../archive/evidence/historical/2026-07-31-browser-manual-smoke.md` records the owner-confirmed browser smoke and the CSRF defect found during that run.
- `../archive/evidence/historical/2026-07-31-mysql-registration-checkpoint-unit.md` records mocked-SQL atomic-registration, rollback, checkpoint-integrity and Actor-activation tests.
- `../archive/evidence/historical/2026-07-31-mysql-authenticated-snapshot-e2e.md` records one live MySQL 8.4.11 registration transaction and cross-process checkpoint load.
- `../archive/evidence/historical/2026-07-31-mysql-restart-recovery-e2e.md` records passing command replays through `CLAIM_CHAPTER_REWARD`, online maturity, Dirty flush and fresh-process recovery at `player_seq=7`.
- `../archive/evidence/historical/2026-07-31-zone-config-get-shop-e2e.md` records atomic local snapshot replacement and a passing four-process `GET_SHOP` path.
- `../archive/evidence/historical/2026-07-31-growth-and-maturity-tests.md` records exact fixed-point, clock-rollback, large-intermediate, activation-time and online maturity tests.
- `../archive/evidence/historical/2026-07-31-maturity-push-e2e.md` records a passing 72-second four-process run through buy, plant, fertilizer and natural `MATURED` Push at `player_seq=4`, plus Gate snapshot-buffer unit coverage.
- `../archive/evidence/historical/2026-07-31-harvest-e2e.md` records all-or-nothing warehouse-limit tests, checkpoint serialization and a passing live `MATURED -> HARVEST` flow at `player_seq=5`.
- `../archive/evidence/historical/2026-07-31-sell-crop-e2e.md` records quantity/sell-all, stale-price, inventory, chapter-status checkpoint tests and fresh-process MySQL recovery at `player_seq=6`.
- `../archive/evidence/historical/2026-07-31-claim-chapter-reward-e2e.md` records normal reward claim, full-warehouse pending mail Outbox, atomic MySQL writer unit coverage and the live in-memory `player_seq=7` flow.
- `../archive/evidence/historical/2026-07-31-clean-plot-e2e.md` records cleanup preconditions, complete field reset, retained replay and live in-memory/MySQL server-loop completion with fresh-process recovery at `player_seq=8`.
- `../archive/evidence/historical/2026-07-31-h5-farm-loop-browser.md` records the browser-driven H5 owner loop, maturity Push, final state and 320-pixel layout check.
- `../archive/evidence/historical/2026-08-03-four-plot-tools.md` records the four-plot, tool-driven and quantity-control implementation plus its static and browser verification.
- `../archive/evidence/historical/2026-08-03-dual-zone-routing.md` records deterministic placement,
  Gate cache behavior, wrong-Owner rejection and the passing five-process
  memory-only dual-Zone E2E.
- `../archive/evidence/historical/2026-08-03-manual-inactive-shard-migration.md` records
  per-Shard drain exclusion, epoch-two stale-cache recovery and active-Shard
  migration refusal.
- `../archive/evidence/historical/2026-08-03-static-dual-zone-mysql-fence.md` records the
  verified epoch-one Fence alignment and one persisted write through each
  Zone.
- `../archive/evidence/historical/2026-08-03-active-shard-mysql-migration.md` records final Actor
  flush, epoch-two Fence transfer, Gate recovery, target persistence and stale
  old-Owner rejection.
- `../archive/evidence/historical/2026-08-03-coordinator-preparing-recovery.md` records durable
  migration progress, fail-closed PREPARING overlay, continue/abandon controls
  and post-migration Coordinator Fence hydration after restart.
- `../archive/evidence/historical/2026-08-03-ws-ticket-restart-boundary.md` and ADR-0010 freeze
  unused WS tickets/CSRF as process-local across Login restart.
- Automated browser behavior in MySQL mode, distributed/retriable Push delivery, abnormal Dirty-window loss, availability and performance remain unverified.
- A loopback-only local test platform exists under `tests/catalog.json` and
  `server/cmd/testrunner`. It wraps existing Go/PowerShell checks with tiered
  safety controls; platform history does not replace `docs/evidence/`.
- `../evidence/2026-08-05-pure-tcaplus-runtime-gate.md` records the no-MySQL
  account, checkpoint, migration and restart acceptance gate.
- `../archive/evidence/historical/2026-08-05-k8s-fixed-dual-zone.md` records the five-Deployment
  kind cluster and passing live dual-Zone owner-loop/migration E2E.
- `../archive/evidence/historical/2026-08-06-friend-phase-1-grpc.md` records HMAC interceptor
  rejection tests plus passing local and kind gRPC dual-Zone E2Es.
- `../archive/evidence/historical/2026-08-06-friend-phase-2-friendsvr.md` records FriendSvr,
  FriendLinkSaga, Zone task credit and friend-table deploy wiring.
- `../archive/evidence/historical/2026-08-06-friend-phase-3-visit.md` records Zone/Gate visit-
  session wiring, the new gRPC gateway adapters, `friend_rpc_test.go`
  RPC-argument/authorization coverage, gateway routing tests and the
  minimal H5 friends/visit panel.
- `../archive/evidence/historical/2026-08-06-friend-phase-4-farmview.md` records the
  `farmview.Broadcaster` fan-out, the Runtime `farm_view_seq` hook on public
  plot mutations only, the Gate `PublishFarmViewPatch`/`push_hub.go`
  validation and the H5 epoch/seq-gap recovery logic in `App.vue`.
- `../archive/evidence/historical/2026-08-06-friend-phase-5-steal-saga.md` records the frozen
  per-plot steal fields, `player.CanSteal`, the synchronous
  Reserve/Apply/Commit/Release Actor steps, the new
  `server/internal/interaction` Saga/Tcaplus-store/reconciler package (with
  all three crash-window recovery tests), the Zone
  `ExecuteFriendAction`/`ApplyVisitorAction` RPC wiring, Gate routing and
  the minimal H5 steal button.
- `../archive/evidence/historical/2026-08-13-coordinator-sdk-route-publish.md` records the Phase
  03 authenticated Watch publisher/shared SDK rollout, four live subscribers,
  Coordinator restart recovery, explicit 4096-row endpoint reinitialize and a
  passing kind dual-Zone active-migration E2E. Gate/Info/Zone now default to
  the SDK in kind while retaining HTTP/poll rollback switches.
- `../evidence/2026-08-19-cross-zone-friend-loadtest.md` records the balanced
  cross-Zone friend visit lifecycle benchmark. Throughput plateaued near
  223--229 complete enter/heartbeat/exit cycles per second while latency
  doubled from 50 to 100 pairs. Coordinator reached its 500m CPU limit because
  every visitor command synchronously performs a per-Shard HTTP route lookup;
  Gate, Friend and Zone CPU remained below their limits. The next performance
  step is an isolated route-cache/control-plane experiment, not a higher pair
  count or a Zone Actor optimization.
- `../evidence/2026-08-19-zone-friend-sdk-routing.md` records the code-level
  fix that makes `ZoneOwnerFarmClient` reuse the Zone's existing versioned
  Coordinator SDK/cached resolver. Normal friend visit/action commands no
  longer perform per-command Coordinator HTTP lookup; stale Owner rejection
  invalidates the exact route version and permits one fallback re-resolution.
  Full Go tests pass. The Zone-only image rollout is live on all eight pool
  Pods with zero restarts. A/B verification improved 50-pair lifecycle QPS
  from 227.07 to 1227.26 and reduced P99 from 388.59ms to 52.65ms; at 100
  pairs it reached 1881.51/s with 77.43ms P99. Coordinator remained at
  42--56m rather than its former 499m/500m saturation. At 200 pairs the run
  reached 2528.24 cycles/s with zero errors, but Info reached 501m/500m while
  the busiest Zone was 822m/1000m, so this is not a Zone capacity ceiling.
- `../archive/development/plans/2026-08-19-friend-steal-actor-await-plan.md` remains the proposed
  production design. A deliberately small experimental `AwaitFriendOwnerCall`
  path is now live on all eight Zone Pods for performance testing only; it
  releases the Visitor Actor mailbox during the Owner RPC but has no durable
  pending receipt, UNKNOWN reconciliation, duplicate protection, or
  migration/eviction gate.
- `../evidence/2026-08-19-friend-steal-no-await-baseline.md` records the first
  real steal benchmark. `friend_steal` prepares mature farms outside the timed
  section and reports attempt QPS, success QPS, business rejects, and system
  errors separately. The no-Await baseline used 610 finite targets: 610
-  successes, 0 rejects/errors, 5,589.34 attempt and success QPS, successful
  P99 62.40ms. The experimental Await run used the same 100-pair cohort but
  prepared 1,600 targets: 1,600 successes, 0 rejects/errors, 6,057.59 QPS,
  successful P99 51.72ms. Target counts differ, so this is validation and a
  directional signal only, not a controlled performance claim. Details are in
  `../evidence/2026-08-19-friend-steal-await-experiment.md`.

## Next actions

- 2026-08-19 全局性能报告已生成：`../evidence/2026-08-19-classic-farm-performance-report.md`，汇总 Zone/Gate/Friend/偷菜压测、pprof 结论和 3000 万 DAU 规划边界；独立交付目录为 `/data/workspace/yace/reports/classic-farm-performance-2026-08-19/`，包含 Markdown 报告与 SVG 火焰图。报告结论是“架构方向可继续支撑目标，但当前原型尚不能声称已实际支撑 3000 万 DAU”。
- 最终交付包已整理为 `/data/workspace/yace/bechreport-2026-08-19.zip`，解压目录 `/data/workspace/yace/bechreport/`，含正式总报告、4 张 SVG 火焰图、6 份核心证据摘要和材料清单；报告插图路径统一为 `/workspace/bechreport/flamegraphs/`。

The eight-pool Zone drain is currently **paused** in the live Deployment. Its
first run found a `MigrationProgress` comparison bug caused by historical
Source `transition_id` fields that the progress schema intentionally does not
store. The code fix and regression tests pass offline; the live authority was
checked after pausing at `map_version=4127` with 2015 Shards on zone-a, 2080 on
zone-b and 1 on the pool. Next: rebuild/load Coordinator, retain both migration
switches at `0`, then perform the controlled resume documented in
`../archive/development/plans/2026-08-17-eight-zone-pool-drain-static-zones.md`. A/B were deleted
after `/internal/v1/zones/drain` reported `zone-a` removable and `zone-b`
reduced to one stale progress row that then cleared.
The first Planner-only resume also exposed a same-intent Task deduplication bug:
restart-time map/availability observation versions must not replace or conflict
with an otherwise identical PLANNED task. That fix and both memory/Tcaplus fake
backend tests now pass; Coordinator must be rebuilt once more before retrying
Planner-only recovery.
The subsequent run also found a legitimate RUNNING task at Shard 1593 stopped
between Claim and its first Progress write. Planner now preserves a matching
open task instead of attempting to overwrite it; Worker owns its recovery.
Worker resume subsequently advanced Current to map version 4319, then was
stopped after concurrent same-table Tcaplus Traverses caused retriable SDK
errors and startup rejected the valid Shard 1593 SOURCE_FLUSHED/source-Fence
boundary. Route commit critical sections are now serialized and new worker
step names are covered by Fence validation. The live Coordinator is scaled to
zero pending rebuild; restore it to one only with the fixed image.
The fixed image has now restored successfully at map version 4380 with 1825
OPEN Progress rows and no Fence mismatch. Planner/Worker may resume under the
serialized RouteStore commit critical section; A/B remain required until the
drain endpoint reports removable.
Later ownership reached zero on A/B and all 4096 routes became ACTIVE at map
version 12317. Thirteen OPEN Progress tails remained at TARGET_READY (4 from
A, 9 from B) with exact committed target ACTIVE routes. Planner preserved
these recovered Tasks based on complete Route migration evidence instead of
cancelling them as CURRENT_MATCHES_DESIRED. The A/B Deployments/Services were
deleted from Kubernetes, but the coordinator drain snapshot still keeps one
stale `zone-b` progress row visible until the migration store is cleaned up.

Final delivery sprint **01**–**03**, **04-1**, **04-2**, **04-3A–F**, and
**04-4** (code + unit tests) are done. Remaining:

1. Create Tcaplus table `FirstFriendReward`, redeploy Friend/Mail, then run
   the dual-browser invite E2E checklist in
   `../archive/development/plans/final_delivery_sprint/04-基础业务补齐/04-4-分享链接自动加好友.md`
   Task 5;
2. Stage E2E for mail/notification → `docs/evidence/2026-08-12-mail-notification-e2e.md`
   (read `../archive/development/plans/final_delivery_sprint/04-基础业务补齐/04-3-邮件与通知总阶段.md`).

Friend prototype vertical (phases 0–7) remains complete for the frozen
scope in `../archive/development/plans/friend_design_plan/06-分阶段实施方案.md`.

1. Owner: Tcaplus `FirstFriendReward` + dual-browser invite E2E;
2. Next sprint coding task: **04-3 stage E2E** (mail + gift + claim + red-dot)
   unless invite E2E surfaces bugs first;
3. Keep `go test ./...` green when touching Gate/Zone/Friend/Info/Mail paths;
4. Keep `cd web && npm run typecheck && npm test` green when touching H5
   (typecheck must use `tsconfig.app.json`).

Evidence:

- `../archive/evidence/historical/2026-08-12-friend-farm-pet-badge.md`
- `../archive/evidence/historical/2026-08-12-shop-seed-rows-and-guard-dog.md`
- `../archive/evidence/historical/2026-08-12-per-crop-mature-sprites.md`
- `../archive/evidence/historical/2026-08-11-farm-16-plots-grass-ui.md`
- `../archive/evidence/historical/2026-08-12-local-friend-invite-link.md`
- `../archive/evidence/historical/2026-08-11-k8s-redeploy-mail-info.md`
- `../bugs/2026-08-11-gift-outbox-activation-revision-mismatch.md`
- `../archive/evidence/historical/2026-08-12-multi-crop-steal.md`
- `../archive/evidence/historical/2026-08-12-h5-mail-red-dot.md`
- `../archive/evidence/historical/2026-08-12-mail-claim-saga.md`
- `../archive/evidence/historical/2026-08-12-friend-gift-outbox.md`
- `../archive/evidence/historical/2026-08-12-mailsvr-query.md`
- `../archive/evidence/historical/2026-08-12-infosvr-red-dot.md`
- `../archive/evidence/historical/2026-08-12-zone-connection-push.md`
- `../archive/evidence/historical/2026-08-11-h5-shell-redesign.md`
- `../archive/evidence/historical/2026-08-11-career-compendium-multi-crop.md`
- `../archive/evidence/historical/2026-08-11-pet-guard-e2e.md`
- `../archive/evidence/historical/2026-08-10-farm-broadcast-separation.md`
- `../archive/evidence/historical/2026-08-10-zone-initial-player-checkpoint.md`
- `../archive/evidence/historical/2026-08-10-actor-register-before-load.md`
- `../evidence/2026-08-07-friend-interaction-e2e.md`

Manual owner checkpoints:

- rebuild/redeploy **all eight** images into kind when exercising cluster
  demos (`login` `gate` `coordinator` `zone` `friend` `info` `mail`;
  `CLIENT_CONFIG_PUBLIC_URL` must stay aligned with Login's browser URL);
- ensure `classic-farm-internal-rpc` includes `MAIL_ADMIN_TOKEN` before Mail
  pods become Ready;
- run `./tests/e2e/run-friend-interaction.sh` after friend/Gate/Zone changes.

## AI memory and handoff rule

- This file is the short, authoritative mutable handoff. Update it only when current state or next actions materially change.
- `PROJECT.md` stores stable scope and constraints.
- `docs/decisions/` stores chronological decision evolution; it is not the current-state summary.
- `docs/archive/development/ai-workflow/` stores traceability about what AI and the owner did; it cannot override current architecture, contracts, or evidence.
- Obsidian and `ai-context` may keep learning notes and pointers, but must not override repository truth.
- When switching among Codex, CodeBuddy, or Claude, provide the reading order above and only task-specific supporting files.
