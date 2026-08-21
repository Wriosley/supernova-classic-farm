---
status: verified
date: 2026-08-12
evidence_type:
  - code
  - test
---

# 好友赠礼与 Outbox

## 完成范围

寄件人 Actor 同一次提交扣除作物并写 `CREATE_GIFT_MAIL` Outbox；Gate 校验互为好友；Zone Relay 最终投递 MailSvr；MailSvr 按 `source_event_id` 去重并 fail-open 通知 InfoSvr。

```text
H5 SEND_FRIEND_GIFT
  -> Gate CheckMutualFriend
  -> Owner Zone Actor: validate crop/qty, deduct inventory, append Outbox, Dirty
  -> flush PlayerOutbox (Tcaplus)
  -> Zone Relay -> MailSvr.CreateGiftMail (MAIL_TYPE_GIFT)
  -> InfoSvr.SetMailRedDot (failure does not recreate mail)
```

## 协议

- WS：`Action.SEND_FRIEND_GIFT`、`SendFriendGiftRequest/Response`
- Outbox：`OutboxEventType.CREATE_GIFT_MAIL`、`event.CreateGiftMailV1`
- Mail：`MailService.CreateGiftMail`（Zone caller allowlist）

## 行为约束

- 只能赠送作物 Item，数量 1–10，不能赠自己
- 扣除与 Outbox 同一次 Actor 提交；相同 request_id 幂等回放
- MailSvr 不可用不阻塞寄件人提交；Relay 保留 pending 重试
- 重放 `source_event_id` 返回 `already_applied`，不建第二封

## 验证

```bash
cd server
go test -race ./internal/player ./internal/friend ./internal/mail ./internal/outbox ./internal/gateway -count=1
go test ./... -count=1
# ok
```

## 未重跑

- kind / 真实 Tcaplus 联调（需 MailSvr + Zone relay 同集群）
- H5 赠礼入口（04-3F）
- 领取 Saga（04-3C）

## 下一子计划

`04-3C-邮件领取Saga.md`
