---
status: verified
date: 2026-08-10
evidence_type:
  - code
  - test
---

# Actor 先创建后 Load

## 完成范围

将 Player Runtime 冷激活从“Load 完成后再插入 Actor”改为：

1. 在 `Runtime.mu` 下创建 `Loading` Actor 与 mailbox；
2. 通过 `Mailbox.Submit` 把 Load/初始化作为 **第一个** mailbox 任务入队；
3. 再把 Actor 发布到 `Runtime.actors`；
4. 所有调用方（含创建者）经 `waitForActorReady` 屏障等待 `Ready` / `Failed` / `Closing`；
5. 失败路径：`Failed` → `removeActorIfSame` → 异步 `mailbox.Close` → 允许重试。

未改协议、业务规则、广播、幂等或 FriendInteraction Saga。Checkpoint `NotFound` 新玩家农田初始化留给 sprint 02。

## 代码

| 文件 | 变更 |
|---|---|
| `server/internal/player/runtime.go` | lifecycle（Loading/Ready/Failed/Closing）、`getOrCreateLoadingActor` 先 Submit 再发布、`activateActor`、`waitForActorReady` 循环屏障、`removeActorIfSame`；maturity / dirty / Drain / Close 跳过或取消非 Ready |
| `server/internal/actor/mailbox.go` | 新增非阻塞 `Submit` |
| `server/internal/player/runtime_activation_test.go` | 并发冷激活、命令排队、失败重试、背压、Drain/Close while Loading |
| `server/internal/actor/mailbox_test.go` | `Submit` 早于后续 `Do` |

## 修改前行为（已用测试钉死）

旧 `actorFor` 在 Store Load 之后才插入 map。阻塞 Load 的并发测试会观察到多次 `Store.Load`。  
中间实现若“先插入 map、再由创建者 `Do(activate)`”，并发 waiter 可能把空屏障排到激活任务之前，出现 `player actor activation failed` 竞态；修复为 **Submit 激活任务后再发布 Actor**。

## 验证命令与结果

### Task 1–3（race 重复）

```bash
cd server
go test -race ./internal/player \
  -run 'TestRuntimeRegistersActorBeforeCheckpointLoad|TestRuntimeQueuesCommandsBehindActorInitialization' \
  -count=20
# ok

go test -race ./internal/player \
  -run 'FailedActivation|ActivationRejectsStaleOwner' \
  -count=20
# ok

go test -race ./internal/actor ./internal/player \
  -run 'LoadingBackpressure|DrainWhileActorIsLoading|CloseWhileActorIsLoading' \
  -count=10
# ok

go test -race ./internal/player \
  -run 'TestRuntimeRegistersActorBeforeCheckpointLoad|TestRuntimeQueuesCommandsBehindActorInitialization|FailedActivation|ActivationRejectsStaleOwner|LoadingBackpressure|DrainWhileActorIsLoading|CloseWhileActorIsLoading' \
  -count=50
# ok
```

断言覆盖：

- 100 路并发冷激活：`actors` 仅 1 个、同一 mailbox、`Store.Load` 恰好 1 次；
- 业务命令不越过初始化；
- Load 失败后 map 清理、后续可重新激活；
- 旧 Failed Actor 的 `removeActorIfSame` 不删除替换 Actor；
- stale Owner Epoch → `ErrNotOwner`；
- mailbox 容量 64，第 65 个等待者吃 context deadline；
- Loading 期间 Drain / Close 可结束且不遗留 Loading Actor。

### Task 4 回归

```bash
cd server
go test -race ./internal/actor ./internal/player -count=1
# ok

go test ./internal/interaction ./internal/visit ./internal/farmview -count=1
# ok

go test ./... -count=1
# ok（含 cmd/zone、routing、auth、test/e2e 包内单元测试）

go vet ./...
# ok
```

迁移相关单元测试随 `./internal/player` 一并通过（含
`TestRuntimeDrainShardFlushesAndEvictsActiveActor` 等）。

## 未在本环境重跑的项

| 项 | 原因 |
|---|---|
| kind 双 Zone / Friend 多进程 E2E（`run-friend-*.sh`） | 本机 kubectl API `127.0.0.1:40247` connection refused，集群未就绪 |
| 完整注册→WS→写命令→Dirty→重启冷加载冒烟 | 依赖上述进程栈；本阶段以 Runtime 单元 + 全量 `go test ./...` 验收 |

既有好友 E2E 证据仍见 `2026-08-07-friend-interaction-e2e.md`；本变更不触及 Friend/Gate 协议路径。

## 已知限制

- 初始化失败时在 mailbox worker 内 `go mailbox.Close()`，避免在 worker 内同步 Close 死锁；
- `Mailbox.Do` 在队列满时仍会在持有 `RLock` 的情况下阻塞发送；与同步 `Close` 叠加时理论上可能互相等待（背压测试用 context deadline，未改 Do 语义）；
- Checkpoint `NotFound` → Zone 内建新农田仍属 sprint 02，本阶段 Load 失败仍按错误返回。
