---
status: accepted
date: 2026-08-15
decision-makers:
  - project-owner
supersedes:
  - 04-3C Mail Claim Saga 在线主链路
superseded-by:
---

# ADR-0013：邮件领取采用 Actor 内存优先的低延迟直连路径

## Context

原邮件领取在响应前同步执行 MailClaimSaga 创建/多次 CAS、Mail→Zone RPC、
PlayerCheckpoint SaveCAS、PlayerMailState/Saga 完成和未读数回写。远端 Tcaplus
往返和 Traverse 使一次领取明显变慢，不符合演示对交互延迟的要求。

项目负责人选择和普通农场/好友操作一致的 Actor Dirty 边界：正常运行的低延迟
优先于异常崩溃窗口内的零丢失、严格幂等和自动恢复。

## Considered options

1. 保留完整 Saga：崩溃可恢复且奖励严格幂等，但同步数据库往返最多。
2. Saga 只保证 Zone 奖励 SaveCAS，邮件完成异步：可靠性较强，但仍等待 Saga
   intent 和 PlayerCheckpoint SaveCAS。
3. 直接领取并异步 Dirty：数据库校验邮件后，Zone Actor 修改内存、记录 Receipt、
   标 Dirty并立即返回，邮件状态和 Info 投影后台更新。

## Decision

采用 Option 3：

- 在线主链路不创建或推进 MailClaimSaga；旧表、旧实现和 Reconciler 暂时保留，
  仅用于兼容旧版本遗留记录。
- 私人/礼物邮件优先按 `(player_id, mail_id)` 点查；公共邮件回退点查并校验注册
  时间；持久化 `claimed` 状态仍用于拒绝已经完成的领取。
- Zone 在 Actor mailbox 内一次性校验容量、增加附件/金币、追加 MailClaimReceipt、
  推进玩家版本，随后 `markDirty` 并立即返回，不同步 SaveCAS。
- MailSvr 收到 Zone 成功后立即返回 H5；后台写 `read=true/claimed=true` 并刷新
  InfoSvr 绝对未读数。
- 同一 Actor 生命周期内，按 `mail_id` 的 Receipt 阻止重复领取。

## Consequences

- Zone 在成功响应后、Dirty 刷盘前崩溃，奖励和 Receipt 可能一起丢失。
- Mail 状态后台写入前发生进程故障，重启重试可能重复领取；后台写失败只记录
  日志，不再由新 Saga 自动恢复。
- 正常路径不再等待 Saga CAS、Checkpoint SaveCAS、邮件完成写和 Info 回写。
- 旧 Saga 数据和恢复代码不能立即删除，需先确认不存在遗留未完成记录。
- MailSvr 不再启动 5 秒 ClaimReconciler；旧 Saga 只能通过显式维护工具恢复，
  避免它与在线邮箱查询争抢 Tcaplus Traverser。
- `PrivateMail(recipient_player_id,mail_id)` 和
  `PlayerMailState(player_id,mail_id)` 使用现有主键前缀的部分键查询，禁止为玩家
  邮箱执行全表 Traverse。
- `PlayerMailboxCursor` 不再参与在线列表或未读投影：打开邮箱不读写/CAS
  Cursor，权威边界统一为 `PlayerMailState.read`；表和协议字段暂留兼容。

## Validation

- Zone 测试确认奖励修改 Actor 内存并标 Dirty，响应前没有 SaveCAS。
- Mail 测试确认在线直连返回、异步 claimed/read 和未读缓存刷新。
- 保留容量校验、同 Actor 重复领取和状态版本回归。
- 本阶段只做单次链路计时，不声明 p95；压测单独进行。
