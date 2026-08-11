---
status: verified
date: 2026-08-12
evidence_type:
  - code
  - test
---

# 邮件领取 Saga

## 完成范围

MailSvr 编排 `BeginClaim → Zone ApplyMailReward → CompleteClaim`；Player Actor 按
`(mail_id, claim_id)` 幂等入仓并同步 SaveCAS 收据；5s Reconciler 恢复非终态
Saga。仓库容量不足时 Cancel 回 `AVAILABLE`，网络错误只延期重试。

```text
H5 CLAIM_MAIL (request_id = claim_id UUID)
  -> Gate GRPCMailCommander
  -> MailSvr.ClaimMail
       BeginClaim (MailClaimSaga CLAIMING)
       -> Coordinator route + Zone ApplyMailReward (sync SaveCAS receipt)
       -> CompleteClaim (PlayerMailState.claimed + COMPLETED)
  -> ClaimReconciler resumes CLAIMING / PLAYER_APPLIED
```

## 协议与表

- WS：`Action.CLAIM_MAIL`、`ClaimMailRequest/Response`（envelope 72/73）
- Mail：`MailService.ClaimMail`（Gate caller allowlist）
- Zone：`PlayerSocialService.ApplyMailReward`（Mail caller allowlist）
- Checkpoint：`MailClaimReceipt`
- Tcaplus：`MailClaimSaga`（`TCAPLUS_MAIL_CLAIM_SAGA_TABLE`）

## 崩溃窗口

| 窗口 | 状态 | 恢复 |
| --- | --- | --- |
| 1 | Begin 后、Apply 前 | Advance/Reconciler 调 Zone → Complete |
| 2 | Zone 已入仓、仍 CLAIMING | 再调 Zone（收据幂等）→ Complete |
| 3 | PLAYER_APPLIED、Complete 前 | Complete 标记 claimed，不重入仓 |

## 验证

```bash
cd server
go test -race ./internal/mail ./internal/player -run 'Claim|MailReward|Reconcile' -count=10
go test ./... -count=1
go vet ./internal/mail ./internal/player ./internal/gateway ./cmd/mail ./cmd/zone ./cmd/gate
# ok
```

## 未重跑

- kind / 真实 Tcaplus 联调（需 MailSvr + Zone + Gate 同集群）
- H5 邮箱领取 UI（04-3F）
- 阶段 E2E（总阶段收尾）

## 下一子计划

`04-3F-H5邮箱与红点窗口.md`
