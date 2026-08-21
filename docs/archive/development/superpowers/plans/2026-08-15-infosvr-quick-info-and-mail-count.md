---
status: deployed-startup-verified
date: 2026-08-15
scope: InfoSvr / Zone / FriendSvr / MailSvr / Gate / H5
---

# InfoSvr 快捷信息缓存、好友在线状态与邮件数量改造计划

## 目标

把 InfoSvr 从红点业务中转服务改造成纯内存的玩家快捷信息投影：

```text
Zone ConnectionRegistry ──在线租约──┐
Player Actor ──农场快捷摘要─────────┼→ InfoSvr 内存缓存
MailSvr ──新邮件计数投影────────────┘
                                      ↓
                           FriendSvr / Gate 快捷查询
```

产品结果：

- 好友列表由 FriendSvr 聚合 InfoSvr 信息；在线好友名字后显示绿色加粗
  “在线”，其他好友显示灰色“离线”。
- 好友列表打开时，根据缓存的成熟候选建立好友农场红点；实际能否偷仍由
  Owner Actor 权威校验。
- 邮箱导航继续显示红色徽标，但改为红底白字的具体新邮件数量。
- 在线收到私人邮件时立即推送最新数量；离线期间收到的邮件在登录查询后
  恢复数量。
- InfoSvr 不是权威存储，重启或 cache miss 时由原业务服务回源并修复缓存。

## 已确认的当前基线

- 在线状态当前由 Zone `ConnectionRegistry` 判断，不由 Actor 列表判断。
  Gate 注册连接、每 30 秒刷新，Zone connection lease 为 90 秒。
- Actor 断线后可能继续驻留约 60 秒；Actor 存在不等于玩家在线。
- `LIST_FRIENDS` 当前是 `Gate → FriendSvr`，FriendSvr 返回好友 ID、账号名和
  建交时间，不经过 Zone。
- Actor 内存调度只覆盖驻留 Actor；淘汰后 deadline 被取消，所以长时间离线
  成熟不会主动产生推送。
- `CHECK_MAILBOX_INDICATOR` 当前由 MailSvr 查询 Tcaplus
  `PlayerMailboxCursor` 和全部可见邮件，只返回 `has_new_mail` 布尔值。
- 当前邮件红点语义是“上次打开邮箱之后有新邮件”，不是严格逐封未读数。

## 业务语义冻结

### 在线状态

```text
online = InfoSvr presence_known && online_until_ms > now_ms
```

- 第一条有效 Gate 连接使玩家在线；最后一条连接注销使玩家离线。
- Gate 的 30 秒 refresh 通过 Zone 延长 InfoSvr `online_until_ms`。
- Zone/网络异常没有显式下线时，最长 90 秒由租约自动转为离线。
- 每 3 分钟 Zone 全量对账仅作为恢复手段；禁止把三分钟轮询作为主状态源。
- UI 将 `presence_known=false` 保守显示为灰色“离线”，协议仍保留 known 位，
  便于诊断缓存是否缺失。

### 农场快捷摘要

每个玩家缓存：

```text
player_id
owner_epoch
checkpoint_revision
has_growing_crop
earliest_mature_at_ms
has_mature_crop_candidate
updated_at_ms
farm_summary_known
```

好友列表的候选判断：

```text
may_have_stealable_crop =
  has_mature_crop_candidate ||
  (has_growing_crop && earliest_mature_at_ms > 0 && earliest_mature_at_ms <= now)
```

它只允许产生快捷提示，不能证明某个访客一定能偷。剩余偷取次数、该访客
是否偷过本轮、作物是否刚被别人偷完，继续由 Owner Actor 校验。

### 邮件数量

第一阶段数量定义为：

```text
new_mail_count = 上次成功打开邮箱之后新增的可见邮件数量
```

- 私人/礼物/系统私人邮件持久化成功后数量 `+1`。
- 成功打开邮箱并持久化 Cursor 后数量清零。
- `MarkMailRead` 不改变该数量。
- 公共邮件计入数量，但不能为全体玩家逐行 fan-out；使用全局公共邮件水位使
  玩家缓存失效，并在下次查询时由 MailSvr 回源计算。
- InfoSvr 丢缓存时禁止从 0 猜测；必须返回 `known=false`，由 MailSvr 使用
  现有邮件和 Cursor 计算绝对数量后回填。

## InfoSvr 接口边界

旧接口先保留、标记 deprecated，不删除代码：

```text
SetMailRedDot
NotifyOwnerPlotStealable
```

它们不再有主调用者。新增两类内部 RPC：

生产者更新接口（仅 Zone/MailSvr）：

```text
UpdatePresenceLease
BatchRenewPresenceLeases
UpdateFarmQuickInfo
ApplyPrivateMailEvent
SetMailboxQuickInfo
AdvancePublicMailWatermark
```

消费者查询接口：

```text
BatchGetPlayerQuickInfo
GetMailboxQuickInfo
```

“InfoSvr 只负责查询”指它不接受玩家业务命令、不修改权威数据；Zone 和
MailSvr 仍需向它提交投影更新。所有更新携带来源版本，禁止裸
`Increment(player_id)`。

## 一致性和版本规则

### Presence

- Zone 上报 `logical_zone_id + incarnation_id + owner_epoch + source_seq`。
- 同一 incarnation 只接受更大的 `source_seq`。
- Route epoch 更大时替换旧 Owner 数据；旧 epoch 更新拒绝。
- InfoSvr 根据 `online_until_ms` 自行过期，无需主动请求 Zone 才能下线。
- Zone 可每 3 分钟发送一次当前在线 ID 全量摘要用于对账，但使用 batch API，
  且不能覆盖更新 epoch 的记录。

### Farm summary

- 以 `owner_epoch + checkpoint_revision` 做单调版本。
- Actor mailbox 内重新计算摘要；仅在摘要变化时投入有界、合并更新队列。
- 队列按 player ID coalesce，只保留该玩家最新版本；InfoSvr 不可用不阻塞
  Actor 命令或淘汰。
- Actor 激活完成、所有可能改变成熟时间的操作完成、Actor 淘汰前均比较并
  更新，包括种植、自己/好友施肥、投虫、捉虫、成熟、收获和清理。

### Mail summary

- 私人邮件事件携带稳定 `mail_id + created_at_ms`，InfoSvr 在缓存周期内幂等
  去重；重复投递不重复增加。
- 缓存已有权威基线时，事件原子增加数量并返回新绝对值。
- 缓存未知时，InfoSvr 返回 `known=false`；MailSvr 回源 Tcaplus 计算绝对
  count，再用 `SetMailboxQuickInfo(count, cursor_ms, calculated_at_ms)` 修复。
- 打开邮箱成功后的清零带新的 `cursor_ms`。延迟到达且
  `mail.created_at_ms <= cursor_ms` 的旧事件必须忽略，避免“打开后又跳回 1”。
- 新邮件创建与清零按 player ID 串行更新缓存。

## 实施任务

### Task 1：冻结快捷信息协议和内存 Store

涉及文件：

```text
proto/classicfarm/v1/info/info.proto
server/gen/classicfarm/v1/info/*
web/src/gen/classicfarm/v1/info/*
server/internal/info/quick_store.go
server/internal/info/quick_store_test.go
server/internal/info/service.go
server/cmd/info/main.go
```

- [ ] 为 presence、farm summary、mail summary 定义 additive proto。
- [ ] `BatchGetPlayerQuickInfo` 限制批量数量、去重、稳定排序。
- [ ] 实现分片锁或每玩家串行的纯内存 Store。
- [ ] 测试版本倒退拒绝、重复 mail ID、cursor 清零竞态和 TTL 过期。
- [ ] 保留旧红点 RPC 和实现，但部署日志明确其 compatibility-only 状态。
- [ ] 运行 buf lint/generate，禁止手改 generated 文件。

### Task 2：ConnectionRegistry 事件化同步在线状态

涉及文件：

```text
server/internal/connection/registry.go
server/cmd/zone/connection_rpc.go
server/cmd/zone/main.go
server/cmd/zone/info_quick_client.go
server/cmd/zone/*_test.go
```

- [ ] Registry 能报告 `first connection`、`lease renewed`、`last connection`
  三种聚合变化，不能把多设备中的一条断线误判成玩家离线。
- [ ] Register/Refresh/Unregister/EvictExpired 后异步更新 InfoSvr。
- [ ] 批量续约而非每 30 秒每连接单 RPC。
- [ ] Zone 启动/Info 恢复后支持一次全量在线列表对账，之后每 3 分钟作为恢复
  校验；正常状态由事件和 lease 驱动。
- [ ] 测试多连接、异常断线、Zone 崩溃后 90 秒过期、旧 epoch/incarnation
  更新拒绝。

### Task 3：Actor 农场摘要同步

涉及文件：

```text
server/internal/player/quick_info.go
server/internal/player/runtime.go
server/internal/player/actor_tick.go
server/internal/player/actor_eviction.go
server/internal/player/*command*.go
server/cmd/zone/main.go
```

- [ ] 增加单一 `computeFarmQuickSummary`，禁止各命令复制计算规则。
- [ ] Actor 激活后发布完整摘要。
- [ ] mailbox 命令完成后统一比较 before/after，变化才进入 coalescing queue。
- [ ] 覆盖种植、自己/好友施肥、投虫、捉虫、成熟、收获、清理。
- [ ] Actor 淘汰必须在 SaveCAS 成功并删除前提交最终 best-effort 摘要。
- [ ] 验证 Actor 淘汰后 `earliest_mature_at_ms <= now` 仍能形成成熟候选。
- [ ] Info 更新失败不得回滚玩家命令或阻止 Actor 回收。

### Task 4：FriendSvr 聚合好友快捷信息

涉及文件：

```text
proto/classicfarm/v1/friend/friend.proto
proto/classicfarm/v1/ws/ws.proto
server/internal/friend/service.go
server/internal/friend/info_client.go
server/cmd/friend/main.go
server/internal/gateway/grpc_friend.go
```

- [ ] FriendSvr 先查权威 FriendList，再单次 batch 查询 InfoSvr。
- [ ] `FriendView` 增加 presence known/online/last seen、farm summary known、
  earliest maturity 和 `may_have_stealable_crop`。
- [ ] InfoSvr 超时或 miss 时仍返回好友关系，快捷字段标 unknown；不能让缓存
  故障导致好友列表不可用。
- [ ] FriendSvr 用自己的 server time 计算成熟候选，H5 不自行比较不可信时钟。
- [ ] Gate 继续直连 FriendSvr，不增加 Zone 跳转。

### Task 5：MailSvr 权威回源与 Info 邮件计数投影

涉及文件：

```text
proto/classicfarm/v1/mail/mail.proto
proto/classicfarm/v1/ws/ws.proto
server/internal/mail/service.go
server/internal/mail/store.go
server/internal/mail/info_quick_client.go
server/internal/mail/direct_notifier.go
server/cmd/mail/main.go
```

- [ ] 抽取 MailSvr 权威 `countNewVisibleMails(player, cursor)`，复用当前可见性
  规则并返回 exact count。
- [ ] `CheckMailboxIndicatorResponse` additive 增加 `new_mail_count`，保留
  `has_new_mail = count > 0` 兼容字段。
- [ ] 登录查询先查 Info；known=false、公共水位失效或缓存超时时回源 MailSvr
  Tcaplus，并回填绝对数量。
- [ ] 私人邮件持久化成功后 Apply event；拿到准确绝对 count 后再直接 Push
  Owner Zone。
- [ ] `RedDotChangedPush` additive 增加 `count`，旧客户端仍可只看 SET/CLEAR。
- [ ] `OpenMailbox` 只有在 Cursor 更新成功后才清零 Info；失败时不能清零。
- [ ] 公共邮件创建只推进全局 watermark，不做全玩家 fan-out。
- [ ] 测试 Info 重启/miss、重复 mail event、并发新邮件与打开邮箱、公共邮件、
  Push 失败不回滚邮件。

### Task 6：H5 在线标签和数字邮件徽标

涉及文件：

```text
web/src/App.vue
web/src/components/FriendsPanel.vue
web/src/components/TopNav.vue
web/src/lib/ws.ts
web/src/**/*.test.ts
```

- [ ] 好友名字后显示状态：在线为绿色加粗“在线”，离线/unknown 为灰色
  “离线”；同时保留文字而不是只靠颜色表达。
- [ ] 好友列表成功刷新后，用 `may_have_stealable_crop` 重建当前好友红点集合；
  列表刷新之后到达的实时成熟 Push 仍可追加。
- [ ] `mailRedDot: boolean` 改为 `newMailCount: number`。
- [ ] 顶部邮箱徽标显示红底白字数量；大于 99 显示 `99+`，0 时隐藏。
- [ ] 登录查询使用 `new_mail_count`；在线 Push 使用 Push 中的绝对 count，
  禁止前端盲目 `+1`。
- [ ] 只有 `OpenMailbox` 成功后清零徽标；打开失败保留原数量。
- [ ] 增加 typecheck、组件测试和无障碍 label。

### Task 7：部署与端到端验证

- [ ] 不新增 Tcaplus 表；InfoSvr 内存丢失由 MailSvr/Zone 回源或重放修复。
- [ ] 构建并滚动 Info、Zone、Friend、Mail、Gate 镜像，最后部署 H5。
- [ ] 验证同一玩家两个连接，关闭一个仍显示在线，全部关闭后显示离线。
- [ ] 强杀 Zone，确认 90 秒内 Info 在线租约过期。
- [ ] Actor 淘汰后等待 earliest maturity，好友列表出现成熟候选红点。
- [ ] 离线收 3 封私人邮件，登录显示 `3`；在线再收 1 封立即显示 `4`。
- [ ] 重启 InfoSvr 后登录，MailSvr 回源恢复正确数量。
- [ ] 打开邮箱成功显示 `0`；模拟打开失败时数量不清零。
- [ ] 创建公共邮件，确认下次查询通过 watermark 回源后计入数量。
- [ ] 创建 `docs/evidence/2026-08-xx-infosvr-quick-info-mail-count.md` 并更新
  `docs/context/CURRENT.md`。

## 验证命令

```bash
buf lint
buf generate --template buf.gen.yaml

cd server
go test -race -count=1 \
  ./internal/info ./internal/connection ./internal/player ./internal/friend \
  ./internal/mail ./internal/gateway ./cmd/info ./cmd/zone ./cmd/friend ./cmd/mail

cd ../web
npm run typecheck
npm run test -- --run
```

## 完成标准

- InfoSvr 主职责只有快捷信息投影和查询，旧红点代码保留但无主调用者。
- 在线状态来自 ConnectionRegistry 事件 + 90 秒 TTL，Actor 存活不参与判断。
- 好友列表仍由 FriendSvr 聚合，并返回在线状态和成熟候选。
- Actor 淘汰后 InfoSvr 仍能根据 earliest maturity 提供离线成熟候选。
- MailSvr/Tcaplus 保持邮件与 Cursor 权威，Info 只缓存新邮件数量。
- 在线 Push 和登录查询都返回绝对邮件数量，重试不会重复加一。
- H5 在线/离线文字样式与数字邮件徽标符合产品要求。
- InfoSvr 重启、Zone 崩溃和缓存 miss 不会产生永久错误状态。

## 暂不处理

- 删除 InfoSvr 旧红点 proto/RPC/实现。
- 将 InfoSvr 改为 Redis 或持久化服务。
- 严格逐封“未读邮件数”；本阶段是“上次打开邮箱后的新邮件数”。
- 为公共邮件向所有玩家逐个推送或更新缓存。
- 用 InfoSvr 的成熟候选绕过 Owner Actor 的偷菜权威校验。
