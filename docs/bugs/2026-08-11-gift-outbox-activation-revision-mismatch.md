---
status: fixed
date: 2026-08-11
severity: blocker
affects:
  - login
  - player-actor-activation
  - friend-gift-outbox
---

# 送过好友礼物的账号永久登不进（checkpoint revision mismatch）

## 现象

- H5 登录流程走到快照阶段后失败，前端提示：
  `快照请求失败：WebSocket 错误 200`（`SERVICE_UNAVAILABLE`）。
- 从未送过好友礼物的新号完全正常；送过礼物的旧号（例如 player 46 / 47 / 52）
  无论重试多少次都登不进。
- 服务重启后问题依旧，不是瞬时网络故障。

## 出现原因

两条本意正确的路径叠在一起，形成了永久激活失败。

### 1. Load 时修剪已投递的 Outbox

`TcaplusDurableCheckpointStore.Load` 在读出存档后，会对照
`PlayerOutbox` 表剔除 Relay 已经投递成功的 `PendingOutbox` 条目，并把
内存里的 `CheckpointRevision` +1，表示「这份状态比库里的行新，待 Dirty
回写」：

```111:120:server/internal/player/tcaplus_durable_store.go
		if row.RelayStatus == tcaplusOutboxDelivered {
			changed = true
			continue
		}
		retained = append(retained, pending)
	}
	loaded.State.PendingOutbox = retained
	if changed {
		loaded.State.CheckpointRevision++
	}
```

`PersistedRevision` 仍是表行上的原 revision；状态领先一行是设计行为。

### 2. 送礼会在存档里留下 PendingOutbox

`SEND_FRIEND_GIFT` 在寄件人 Actor 同一次提交里扣库存并追加
`CREATE_GIFT_MAIL` Outbox。内存态里这条 pending **不会**在 Relay 成功后
清掉——只在下次 Load 时由上面的修剪逻辑处理。

因此：只要某个账号送过礼物，且 Relay 已经把那条 Outbox 标成 delivered，
下次 Actor 冷激活（卸载 / Zone 重启 / 进程重启）就一定走到「状态 revision
= 表 revision + 1」。

### 3. 激活处用了严格相等校验

```496:499:server/internal/player/runtime.go
			// 修复前：
			if persistedRevision != state.CheckpointRevision {
				fail(errors.New("loaded checkpoint revision does not match state"))
				return
			}
```

激活末尾本来就有 `if state.CheckpointRevision > persistedRevision { markDirty }`
来承接这种「领先一行待回写」的情况，但严格相等校验让它永远执行不到。
每次重试都重复同一条修剪、同一个 +1、同一次拒绝——账号被永久锁死。

### 触发条件（充分）

1. 玩家成功送过至少一次好友礼物；
2. Zone Relay 已把对应 `PlayerOutbox` 标为 delivered；
3. 该玩家的 Actor 之后被卸载或 Zone 重启（冷激活）。

未送过礼物、或 Outbox 尚未 delivered 的账号不受影响。

## 排查方法

1. **从前端错误码入手。** `WebSocket 错误 200` 是 `SERVICE_UNAVAILABLE`，
   说明 Gate 已把 Zone 的失败映射成了业务错误，不是协议 / Origin / 代理问题。
2. **看 Zone 日志里被拒的命令。**
   ```bash
   grep -h '"level":"ERROR"' /tmp/classic-farm-servers.*/zone*.log \
     | grep GET_PLAYER_SNAPSHOT
   ```
   会看到稳定复现的：
   ```text
   game command rejected
     action=GET_PLAYER_SNAPSHOT
     player_id=<受害账号>
     error="loaded checkpoint revision does not match state"
   ```
3. **用时间线证明与本次改动无关。** 对比多份 `classic-farm-servers.*`
   目录：同样的错误在邮件修复重启之前（例如 15:24）就已经出现在送礼账号上，
   新注册号没有。
4. **顺着错误字符串回代码。** 全仓搜索
   `checkpoint revision does not match state`，唯一命中在
   `Runtime.activateActor`。紧挨着看 `PersistedRevision` 的赋值来源，落到
   `TcaplusDurableCheckpointStore.Load` 的 Outbox 修剪分支——那里在
   `changed` 时主动 `CheckpointRevision++`，而返回的 `PersistedRevision`
   仍是表行值。
5. **核对送礼路径是否会留下 PendingOutbox。** `gift.go` /
   `claim_reward.go` 都是「提交时 append，Relay 成功后不在内存里删」。
   这解释了为什么「送过礼物的号」和「新号」的命运完全不同。
6. **确认激活末尾已有 dirty 回写。** `activateActor` 末尾的
   `state.CheckpointRevision > persistedRevision → markDirty` 说明原作者
   预期过「Load 修出领先 revision」的场景，只是中间的 `!=` 守卫把它堵死了。

## 解决

### 代码

把严格相等改成「只拒绝落后于表行的状态」：

```501:504:server/internal/player/runtime.go
			if state.CheckpointRevision < persistedRevision {
				fail(errors.New("loaded checkpoint state is behind the persisted revision"))
				return
			}
```

领先的 revision 继续交给激活末尾已有的 `markDirty` 回写。落后才是真正的
不一致，仍然 fail-closed。

### 回归测试

`server/internal/player/runtime_activation_test.go`：

- `TestActivationAcceptsRepairedCheckpointRevision` —
  Load 返回 `state.rev = persisted + 1` 时激活必须成功。
- `TestActivationRejectsStaleCheckpointState` —
  Load 返回 `state.rev < persisted` 时仍须拒绝。

把守卫临时改回 `!=`，Accepts 测试立刻失败，确认测试咬住了这个洞。

### 验证

```bash
cd server
go test -count=1 -run 'TestActivation' ./internal/player/
go test ./...
# 重启 --dual-zone --tcaplus 后，用之前登不进的送礼账号重新登录
```

预期：快照成功；Zone 日志不再出现
`loaded checkpoint revision does not match state` /
`behind the persisted revision`。

## 相关文件

- `server/internal/player/tcaplus_durable_store.go` — Load 时修剪 Outbox
- `server/internal/player/runtime.go` — `activateActor` 校验
- `server/internal/player/gift.go` — 送礼追加 PendingOutbox
- `server/internal/player/runtime_activation_test.go` — 回归
- `docs/evidence/2026-08-12-friend-gift-outbox.md` — 赠礼 Outbox 设计背景

## 教训

- 「Load 可修状态并抬高 revision」和「激活要求 revision 严格相等」是一对
  隐性矛盾；写完 dirty 回写分支后，要用「修过 / 没修过」两条路径各测一次。
- 永久锁号类故障优先看冷激活路径，而不是命令执行路径：重试永远重复同一条
  Load，所以错误会稳定复现、且只打中满足前置条件的账号。
- 前端 `SERVICE_UNAVAILABLE` 本身信息量不够，必须落到 Owner Zone 的
  `game command rejected` 日志才能区分「服务挂了」和「存档激活失败」。
