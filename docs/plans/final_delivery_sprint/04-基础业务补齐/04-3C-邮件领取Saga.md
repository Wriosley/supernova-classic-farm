---
status: verified
date: 2026-08-10
parent:
  - ./04-3-邮件与通知阶段总计划.md
depends_on:
  - ./04-3A-MailSvr数据与查询.md
---

# 邮件领取 Saga Implementation Plan

> **For agentic workers:** 附件领取跨 MailSvr 和 Recipient Player Actor。必须测试三个崩溃窗口，禁止用两个无幂等 RPC 拼接。

**Goal:** 玩家手动领取邮件附件；仓库和邮件状态最终一致，重试不丢奖励、不重复发奖。

**Architecture:** MailSvr `BeginClaim` 创建领取 Saga；Player Actor 使用 `mail_id + claim_id` 幂等入仓并同步持久化领取收据；MailSvr `CompleteClaim` 完成状态。超时 Reconciler 恢复中断流程。

## 规则

- 邮件不过期。
- 附件必须手动领取。
- 全部领取或全部失败。
- 任一附件放不下时不修改仓库。
- 每封邮件每名玩家只能领取一次。
- 公开邮件使用每玩家领取状态。
- 私人/礼物邮件校验 recipient。
- Player Actor 领取步骤必须同步持久化。

## Saga 状态

```text
INIT
-> CLAIMING
-> PLAYER_APPLIED
-> COMPLETED

CLAIMING
-> CANCELLING
-> AVAILABLE
```

建议记录：

```text
claim_id
mail_id
player_id
attachments
state
retry_at_ms
last_error
version
```

## 文件范围

```text
proto/classicfarm/v1/mail/
proto/classicfarm/v1/rpc/
proto/classicfarm/v1/data/data_model.proto
server/internal/mail/claim_saga.go
server/internal/mail/claim_reconciler.go
server/internal/player/mail_reward.go
server/internal/player/sync_persist.go
```

## Task 1：Player 领取收据

在 Player Checkpoint 追加：

```text
MailClaimReceipt {
    mail_id
    claim_id
    applied_at_ms
    attachments
}
```

- [x] 收据按 mail ID/claim ID 去重。
- [x] 校验所有附件合法。
- [x] 预检查仓库类型数和堆叠上限。
- [x] 所有附件一次性入仓。
- [x] 入仓和收据同一次 Actor 提交。
- [x] 使用同步 SaveCAS。
- [x] 相同 claim 重试返回第一次结果。
- [x] 不同附件复用同 claim 返回冲突。

## Task 2：MailSvr Begin/Complete/Cancel

- [x] `BeginClaim` 校验：
  - 邮件可见；
  - recipient 正确；
  - 公开邮件晚于注册时间；
  - 尚未领取；
  - 没有其他有效领取。
- [x] 创建或幂等返回 `CLAIMING`。
- [x] `CompleteClaim` 仅在 Player Applied 后标记已领取。
- [x] 仓库不足时 `CancelClaim` 恢复 AVAILABLE。
- [x] 重复 Complete 幂等。
- [x] 已领取邮件拒绝新 claim。

## Task 3：领取编排

```text
BeginClaim
-> Resolve Recipient Owner Zone
-> ApplyMailReward
-> CompleteClaim
```

- [x] Zone `NOT_OWNER` 时刷新 Route 并重试一次。
- [x] Player 返回仓库不足时取消领取。
- [x] Player 已应用但 Complete 失败时保留 Saga。
- [x] 不将网络超时当成仓库失败。
- [x] 使用稳定 claim ID。

## Task 4：Reconciler

- [x] 周期扫描到期的非终态 Saga。
- [x] CLAIMING：调用 Player Apply。
- [x] PLAYER_APPLIED：调用 Complete。
- [x] 仓库不足：取消并恢复 AVAILABLE。
- [x] 临时错误：更新 retry time，使用有界退避。
- [x] 多实例 Reconciler 使用 CAS，不能同时推进同一 Saga。

## Task 5：崩溃窗口测试

必须验证：

```text
窗口1：BeginClaim后、Player Apply前崩溃
窗口2：Player已入仓、MailSvr记录PLAYER_APPLIED前崩溃
窗口3：Player已入仓、CompleteClaim前崩溃
```

所有场景最终：

```text
附件只增加一次
邮件最终标记已领取
```

验证：

```bash
cd server
go test -race ./internal/mail ./internal/player -run 'Claim|MailReward|Reconcile' -count=10
go test ./... -count=1
go vet ./...
```

创建：

```text
docs/evidence/2026-08-12-mail-claim-saga.md
```

## 完成检查

- [x] 仓库不足全部失败；
- [x] 公开邮件每玩家只领一次；
- [x] 私人邮件只有收件人可领；
- [x] Player 收据同步持久化；
- [x] 三个崩溃窗口恢复；
- [x] 重试不重复发奖；
- [x] MailSvr 多实例 CAS 安全；
- [x] Evidence 和 `CURRENT.md` 更新。
