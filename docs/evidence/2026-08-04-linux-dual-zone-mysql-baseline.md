---
status: completed
date: 2026-08-04
scope: Linux dual-Zone MySQL baseline before Tcaplus work
---

# Linux 双 Zone + MySQL 基线

## 结论

TencentOS Server 4.4、Go 1.26.5、Docker MySQL 8.4 上，Linux 五进程
双 Zone 基线通过：

- Coordinator、Login、Gate、Zone A、Zone B 均启动并通过 readiness；
- 一个合成测试玩家完成完整主人环到 `player_seq=8`；
- 所有写命令使用同一 `request_id` 重放时均未重复生效；
- 五进程正常停止并重新启动后，从 MySQL 恢复到
  `player_seq=8`、29 金币、2 个旧种子和空地块；
- 双 Zone 测试分别创建路由到 Zone A 与 Zone B 的玩家；
- 一个活跃 Shard 从 Zone A 迁移到 Zone B，epoch 从 1 增加到 2；
- 迁移后的 Checkpoint 持久化到 `player_seq=2`；
- 延迟的旧 Zone A Checkpoint writer 被 Fence 拒绝。

该结果只证明本机 Linux、单 MySQL、两个 Zone 和单节点 Coordinator
原型，不证明生产高可用、Raft、自动扩缩容或 3000 万 DAU。

## 环境

```text
OS: TencentOS Server 4.4
Go: 1.26.5 linux/amd64
MySQL: mysql:8.4 Docker image
Routing: static-dual-zone
Coordinator: single process, no consensus
```

本机 8080 被已有进程占用，因此 Login 使用根目录 `.env` 中配置的
18080；其他服务端口仍由同一 `.env` 提供。凭据和 DSN 未写入输出。

## 执行命令

```bash
./deploy/migrate.sh
./tests/e2e/run-mysql-restart-recovery.sh
```

脚本执行：

1. 应用五个 migration；
2. 启动双 Zone + MySQL 五进程栈；
3. 注册合成账号并执行主人环；
4. 正常停止五进程；
5. 启动全新五进程栈并登录同一账号；
6. 验证恢复快照；
7. 验证双 Zone 路由、活跃迁移、epoch 和旧 Owner Fence。

## 主人环结果

```text
初始快照             player_seq=0, coins=10, plot=EMPTY
BUY_SEEDS             player_seq=1, coins=4, seeds=3
PLANT                 player_seq=2, seeds=2, plot=GROWING
APPLY_FERTILIZER      player_seq=3, fertilizer=0
MATURED Push          player_seq=4, plot=MATURE
HARVEST               player_seq=5, crops=3, plot=NEED_CLEANUP
SELL_CROP             player_seq=6, coins=19, chapter=CLAIMABLE
CLAIM_CHAPTER_REWARD  player_seq=7, coins=29, fertilizer=1
CLEAN_PLOT            player_seq=8, plot=EMPTY
```

重启后的登录快照：

```text
owner_epoch=1
player_seq=8
coins=29
old_seed_quantity=2
plot=EMPTY
```

## 双 Zone 与 Fence 结果

```text
Zone A synthetic player: shard=3552
Zone B synthetic player: shard=326
migrated owner: zone-b
migrated owner_epoch=2
migrated persisted player_seq=2
old Zone A delayed writer: rejected
```

所有 player/account 标识均由本次本地 E2E 临时生成，不是真实玩家数据。

## 测试中发现的问题

首次执行在 `GET_SHOP` 断言处失败。服务正确返回当前三项开发商店配置：
种子、作物回收和肥料；E2E 仍断言历史的两项配置。测试已更新为校验三项，
随后完整流程通过。这是过期测试断言，不是服务返回错误。

## CheckpointStore 改造后回归

ADR-0011 接受并完成第一轮实现后，再次执行同一组命令，结果仍为
`PASS`：

- Player Runtime 只依赖统一的 `CheckpointStore`，不再分别注入
  Loader 和 Writer；
- MySQL 适配器继续使用原有 Fence 检查、Checkpoint Revision CAS
  和 Checkpoint/Outbox 单事务边界；
- 五进程主人环完成到 `player_seq=8`，重启后恢复为 29 金币、2 个
  旧种子和空地块；
- 双 Zone 活跃迁移从 epoch 1 前进到 epoch 2，迁移后
  `player_seq=2`，延迟旧 Owner 写入仍被拒绝；
- `go test ./...` 和新增的 Store Token/CAS 结果契约测试通过。

这次回归只验证“抽象没有改变 MySQL 已有行为”。Tcaplus SDK、建表、
单记录条件更新及重启恢复仍属于下一步 POC。

## 未验证边界

- 尚未验证 Linux 浏览器自动化，仅验证真实 HTTP、Ticket、Protobuf
  WebSocket 和五进程服务链路；
- 尚未验证 Tcaplus；
- 尚未验证第三个动态注册 Zone；
- 尚未验证 Kubernetes preStop/HPA/PDB；
- 尚未验证生产级异常故障切换或多节点 Coordinator。
