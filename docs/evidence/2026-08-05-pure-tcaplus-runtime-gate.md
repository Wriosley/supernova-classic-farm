---
status: passed
date: 2026-08-05
scope: pure Tcaplus five-process runtime acceptance
---

# 纯 Tcaplus 运行时验收门

## 已通过

- 真实 `PlayerCheckpoint` POC：Create、Load、物理版本与逻辑 Revision
  双重 CAS、重复提交识别、旧版本拒绝和 Reload；
- Tcaplus 认证注册 Saga、Session generation、Fence、迁移进度、
  Checkpoint/Outbox 补偿写入和激活对账的 hermetic tests；
- Login、Zone、Coordinator 的纯 Tcaplus 启动分支；
- Linux `--dual-zone --tcaplus` 参数校验；
- 真实五进程注册、Session/Ticket、双 Zone 路由、Checkpoint 写入、
  非活跃与活跃 Shard 迁移；
- 迁移后关闭并重启五进程，Coordinator 从高 epoch Fence 恢复路由；
- 全量 `go test ./...` 和 `go vet ./...`。

## 真实验收结果

Tcaplus 表格组返回全部 8 张运行时表：

```text
AccountByName AccountByPlayer MigrationProgress PlayerCheckpoint
PlayerIdCounter PlayerOutbox Session ShardFence
```

首次运行暴露出 4096 个 Fence 逐条 `Get` 无法在 30 秒服务就绪期限内完成。
实现改为先 Traverse 一次现有 Fence，再用 32 个受控 worker 并发补齐缺失行；
幂等重启不再对 4096 个稳定行执行逐条网络读取。

第一次真实迁移后重启又暴露出高 epoch Fence 被静态 bootstrap 错误拒绝。
bootstrap 现在只补齐或校准 epoch-one 行，保留迁移推进后的 Fence，
再由 `LoadFences` 恢复 ACTIVE 路由。对应 hermetic 回归测试覆盖该行为。

验收命令：

```bash
./start-servers.sh --dual-zone --tcaplus --run-seconds 60
E2E_RUN=1 E2E_DUAL_ZONE=1 E2E_SUITE=dual-zone \
  E2E_LOGIN_URL=http://127.0.0.1:18080 \
  go test ./test/e2e -run '^TestDualZoneRoutingAndCache$' -count=1 -v
```

结果：

```text
DUAL_ZONE zone_a_player=22 shard=2755 zone_b_player=25 shard=218
migrated_player=26 migrated_shard=3879 migrated_epoch=2
snapshot_lookups=18 shard_lookups=2
PASS
```

随后完整停止服务并再次执行 15 秒五进程启动，Coordinator、Login、
Zone A、Zone B 和 Gate 均通过 ready gate，输出
`Data mode: pure TcaplusDB.`。

Checkpoint 恢复另用固定测试账号完成两个进程生命周期：第一轮注册
`player_id=27` 并购买种子，得到 `player_seq=1 / coins=4 /
seed_item_1001=3`；完整停止五进程后，第二轮通过 Tcaplus Session 登录
同一账号，Snapshot 恢复出完全相同的状态。纯 Tcaplus 运行时门已解除，
可以进入 Kubernetes 最小集群阶段。
