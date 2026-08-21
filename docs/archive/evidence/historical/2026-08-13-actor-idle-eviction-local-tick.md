---
status: verified
date: 2026-08-13
evidence_type:
  - code
  - test
---

# Actor 空闲回收与本地 Deadline Tick（阶段 1）

## 完成范围

按 `docs/archive/development/superpowers/plans/2026-08-13-actor-idle-eviction-and-local-tick.md` Task 1–4：

1. Actor 拥有 `nextTickAt` / `tick`，返回 `ActorTickResult`；
2. Runtime 共享最小堆按 deadline 调度，删除每秒全量 `runMaturityScheduler`；
3. 暴露 `Mailbox.Idle`、`connection.Registry.Has`、`visit.Registry.HasVisitors`，Runtime 注入 `PlayerPresence` / `FarmObservers`；
4. 3 分钟空闲后 SaveCAS-before-delete 安全回收；Zone 每 10s sweep。

**未做（阶段 2/3）：** Redis 离线成熟索引、TimerSvr、`WakePlayerForMaturity`、QuerySvr。

## 代码

| 文件 | 变更 |
|---|---|
| `server/internal/player/actor_tick.go` | `ActorTickResult`、`nextTickAt`、`tick` |
| `server/internal/player/actor_scheduler.go` | 共享堆、`runDeadlineScheduler`、`deliverActorTick` |
| `server/internal/player/actor_eviction.go` | `actorIdleTimeout`、`EvictIdleActors`、`lastAccessAt` |
| `server/internal/actor/mailbox.go` | `Idle()` via inflight |
| `server/internal/connection/registry.go` | `Has` |
| `server/internal/visit/registry.go` / `owner_service.go` | `HasVisitors` |
| `server/cmd/zone/main.go` | presence/observers 注入 + 10s idle sweep |

## 验证命令与结果

```bash
cd server
GOFLAGS=-mod=mod GOMODCACHE=/root/go/pkg/mod GOPROXY=off \
  go test -race ./internal/player ./internal/actor ./internal/connection ./internal/visit ./cmd/zone -count=1
# ok

GOFLAGS=-mod=mod GOMODCACHE=/root/go/pkg/mod GOPROXY=off \
  go test ./... -count=1
# ok
```

断言覆盖：

- 最早 deadline 先触发；reschedule 使旧 generation 失效；cancel 阻止投递；
- 在线 Tick 结算成熟并推送；不再依赖秒级全扫；
- 连接在线 / 有访客 / mailbox 忙 / 3 分钟内访问 → 不回收；
- Dirty SaveCAS 成功后删除；SaveCAS 失败保留 Actor 并恢复 Ready；
- 调度 Tick 不延长 `lastAccessAt`。

## 已知限制

- 离线作物成熟仍依赖下次访问/激活拉起 Actor（阶段 2 Redis/TimerSvr）；
- 空闲 sweep 失败会打日志并在下一轮重试；单 Actor flush 失败会返回错误并中断当轮剩余扫描。
