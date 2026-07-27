---
date: 2026-07-27
ai: Codex
task: Review owner feedback on module rules
status: recorded
---

# AI Work Record: Module Rules Review

## Goal

Preserve the owner's first-pass review of section 3 in `module-design-and-flows.md`, answer unclear terms, challenge correctness gaps, and convert confirmed product rules into the formal design.

## Owner feedback (original wording)

> 单设备，session的有效期指的是什么？指一次登录的有效时间吗？目前设置为1个小时，注册失败后的语义指的是什么？给我一些例子？

> 命名为资产，初始金币10个，仓库容量100个格子，存放100中不同物品，每个格子容量为300。

> 配置修改后，商店的商品按照新价格显示，第一版只能买卖作物。

> 收货后目前直接清空农田，种植历史保存，目前不支持好友操作，好友目前可以偷菜，好友偷的菜只获得成熟作物的30的收获，不会直接清空农田，被好友采集过的只能收获70%。

> 任务自动领取，不手动领取，完成条件的任务自动完成并标记完成，自动发送奖励，发送奖励的记录要记录下来让人可以点进任务栏查看。

> 链接可以多次点击，链接的有效时间是30分钟，任何人都可以通过同一个链接添加玩家为好友。好友农场可以互动，目前支持偷菜。

> 宠物暂时不用开发，但是要留一个记录。图鉴暂时不开发。邮件暂时不开发。

> 一个房间的访问人数不用专门设置限制，只要进入好友的菜田就可以看到，也可以同时操作，未来出现冲突我们再想办法优化，好友可以偷菜，偷菜的时候减少的相关数据可以写进对应农田的数据库。

## Clarifications and accepted rules

- Session validity means how long the login Token is accepted. First version: one device, fixed one-hour lifetime; a new login revokes the previous Session.
- Registration initialization uses one local transaction for account, player, wallet, farm, and initial plots. Any failure rolls back the whole registration.
- The module is named Asset. A new player starts with 10 coins. Inventory supports 100 occupied item types and 300 units per item type.
- First-version shop means buying seeds and selling mature crops. Submission always uses the current server price; an already-open page may need to refresh after a price change.
- Owner harvest clears the plot and a planting/harvest history is retained.
- A mature crop cycle can be stolen once. Current demonstration yield is 10: the thief receives 3 and the owner retains 7. Stealing does not clear the plot.
- Owner harvest and friend stealing must lock the same plot and commit plot state, thief assets, history, and version atomically. Correctness is required now; performance optimization can wait.
- First-version tasks automatically complete and grant coin rewards. Completion and reward are in the triggering transaction; a visible reward record is kept. Failure rolls back the triggering action.
- One invitation Token can be accepted by multiple different players for 30 minutes. Friendship uniqueness prevents duplicate relationships.
- Pet, collection, and mail remain documented final-scope modules but are not implemented in the current phase.
- Realtime rooms have no product-level fixed member limit; the current acceptance baseline remains three clients. Friends may observe and invoke the farm module's steal operation. Realtime code never writes plot data directly.

## Remaining costs and validation

- A full inventory makes operations that introduce a new item type or exceed a stack fail atomically. A failed harvest leaves the crop mature on the plot.
- Automatic task rewards are coin-only in the first phase, avoiding inventory-capacity ambiguity.
- Multi-use invitation links require rate limits and a friendship unique key; the invitation is active until expiry or revocation rather than consumed on first use.
- Steal tests must cover friend/owner concurrency, repeated stealing, immature crops, non-friends, full thief inventory, response loss, and service restart.
