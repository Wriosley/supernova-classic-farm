# Actor 空闲回收与成熟调度设计

**状态：** proposed，等待实现前审核  
**日期：** 2026-08-13

## 1. 目标

- 农场主不在线、没有访客、没有请求处理时，连续空闲 3 分钟后安全回收 Player Actor。
- 驻留 Actor 自己决定下一次 Tick；Runtime 只维护注册表和时间调度，不执行农场业务。
- Actor 回收后，由 Redis 定时索引在下一块作物成熟时唤醒当前 Owner Zone。
- QuerySvr 在内存中提供 `actor_resident`、`player_online`、`has_stealable_crop` 快捷查询；数据丢失可接受。

## 2. 当前缺口

- Runtime 每秒遍历全部 `r.actors` 并在 Runtime 中结算成熟，成本随驻留 Actor 数线性增长。
- 普通断线不会回收 Actor；目前只有失败激活、迁移、进程关闭会删除 Actor。
- Actor 不驻留时没有成熟唤醒机制。
- `activateActor` 会结算并丢弃 `MaturityEvent`，离线成熟通知可能丢失。
- 好友农场红点只实时投递给在线好友，没有快捷状态查询。

## 3. Actor 空闲定义与回收

Actor 仅在以下条件同时持续 3 分钟后进入回收：

1. 农场主没有有效连接；
2. 没有好友访问会话；
3. 没有正在执行或排队的 Actor 请求；
4. 最近一次访问距今至少 3 分钟。

回收顺序固定为：阻止新任务进入旧 Actor → mailbox 内结算当前到期状态并计算最早 `next_tick_at` → 同步 SaveCAS → 若仍有成长中作物则成功写入 Redis 定时索引 → 更新 QuerySvr → 从 Runtime 删除 → 关闭 mailbox。

Tcaplus 刷盘失败时不得回收；存在成长中作物但 Redis 写入失败时不得回收。并发新请求必须复用仍有效的 Actor，或在旧 Actor 完成删除后创建新 Actor，不能同时存在两个可服务实例。

## 4. 驻留 Actor Tick

Actor 负责：

- 根据所有成长中地块计算最早 `next_tick_at`；
- Tick 时在 mailbox 内结算到期作物；
- 生成成熟事件和 DomainChanges；
- 返回 Dirty revision、快捷状态变化和新的 `next_tick_at`。

Runtime 负责：

- 维护 Actor 注册表；
- 使用共享最小堆维护 `(deadline, player_id, generation)`；
- 到期时只向目标 mailbox 投递 Tick；
- 消费 Tick 结果，调用 Dirty、广播、通知和 QuerySvr 基础设施；
- 不读取地块，不判断作物是否成熟。

不为每个 Actor 创建独立 `time.Ticker`。种植、施肥、害虫等改变成熟时间后增加 generation 并重排；旧堆节点到期时因 generation 不匹配而忽略。

## 5. Redis 离线成熟索引

使用分区 ZSet 保存非驻留 Actor 的最早成熟时间：

- `farm:maturity:scheduled:{partition}`：score 为 `next_mature_at_ms`；
- `farm:maturity:processing:{partition}`：保存已领取但未 ACK 的任务租约；
- 任务携带 `player_id`、`schedule_generation`、`expected_mature_at_ms`。

TimerSvr 原子领取到期任务，通过 Coordinator 查询当前 PlayerID → Zone 路由，再调用 `WakePlayerForMaturity`：

- `WOKEN`：Actor 原本不存在，完成 Load 和成熟结算，ACK；
- `ALREADY_RESIDENT`：Actor 已驻留，由本地 Actor Tick 负责，直接 ACK；
- `NOT_OWNER`：刷新 Coordinator 路由后重试；
- `RETRY_LATER`：Actor 正在回收、Shard 正在迁移或 Zone 暂不可用，保留重试。

TimerSvr 提供至少一次投递；generation 与 Actor 状态检查吸收重复和旧任务。Actor 激活后使用本地堆；Actor 回收后才使用 Redis，两者不同时承担同一 Actor 的常态调度。

## 6. 激活与好友访问

现有 `actorFor` 的“先注册 Loading Actor，再把 Load 作为 mailbox 第一个任务”保持不变。好友访问的 `BuildPublicFarmSnapshot` 已通过 `actorFor` 拉起农场主 Actor。

`activateActor` 调整为只 Load/初始化，不吞掉成熟事件。首次玩家命令、好友访问或 `WakePlayerForMaturity` 在 Ready Actor 的 mailbox 内统一执行到期结算。

## 7. QuerySvr

QuerySvr 只维护进程内 `PlayerQuickInfo`：

- `actor_resident`：Zone 在激活、回收、迁移时更新；
- `player_online`：连接注册、注销、租约过期时更新；
- `has_stealable_crop`：Actor 在成熟、收获、被偷至上限、清理时重新计算；
- 附带 `zone_id`、`zone_incarnation_id`、`state_version`、`updated_at`。

QuerySvr 不是权威数据源，不持久化。重启后未恢复的玩家返回 `found=false`，不能把未知误报为 `false`。真实偷菜操作仍由农场主 Actor 校验。当前 InfoSvr 的红点路由职责不并入 QuerySvr。

## 8. 验证标准

- 农场主在线、存在访客、mailbox 有工作三种情况都不能回收。
- 满足空闲 3 分钟后只回收一次，并在删除前完成 Dirty SaveCAS。
- 并发访问与回收竞争时只存在一个可服务 Actor。
- 驻留 Actor 只在最早 deadline 到期时 Tick，不再每秒全量扫描。
- 回收后 Redis 到期能在当前 Owner Zone 拉起 Actor并产生一次成熟状态转换。
- Redis 重复、旧 generation、Zone 迁移、TimerSvr 领取后崩溃均可恢复。
- Redis 写失败或 Tcaplus 刷盘失败时 Actor 保留。
- QuerySvr 重启后返回 unknown，并能由后续状态事件重新填充。

## 9. 实施边界

分三个阶段实施并分别回归：

1. Actor 空闲检测、安全刷盘回收、Actor 级内存调度；
2. Redis ZSet、TimerSvr、Zone 成熟唤醒接口；
3. 内存 QuerySvr 与好友列表快捷查询。

