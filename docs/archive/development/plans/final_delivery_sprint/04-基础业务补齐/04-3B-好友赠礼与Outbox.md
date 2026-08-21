---
status: verified
date: 2026-08-10
parent:
  - ./04-3-邮件与通知阶段总计划.md
depends_on:
  - ./04-3A-MailSvr数据与查询.md
---

# 好友赠礼与 Outbox Implementation Plan

> **For agentic workers:** 赠礼扣除和 Outbox 必须在寄件人 Player Actor 的同一次提交中完成。禁止直接从 Gate 调 MailSvr 后再扣仓库。

**Goal:** 玩家可以向好友赠送仓库作物，寄件人立即扣除，Outbox 最终创建礼物邮件。

**Architecture:** Gate/FriendSvr 校验好友关系，寄件人 Actor 原子扣除作物并写 `CreateGiftMail` Outbox。Relay 使用稳定 event ID 投递 MailSvr；MailSvr 去重并通知 InfoSvr。

## 规则

- 只能赠送作物 Item。
- 每封只能赠送一种作物。
- 数量 1–10。
- 允许赠送给离线好友。
- 不设置每日次数。
- 标题和正文使用服务端固定模板。
- 不能赠送给自己。
- 发送时立即扣除。
- 重复请求不重复扣除或建信。
- 收件人之后手动领取。

## 文件范围

```text
proto/classicfarm/v1/ws/
proto/classicfarm/v1/events/
server/internal/player/gift.go
server/internal/player/gift_test.go
server/internal/friend/
server/internal/mail/
server/internal/outbox/
client/src/
```

实际 Outbox 位置以当前代码为准。

## Task 1：协议和 Outbox

建议命令：

```text
SEND_FRIEND_GIFT {
    recipient_player_id
    crop_item_id
    quantity
    request_id
}
```

新增事件：

```text
CreateGiftMail {
    event_id
    sender_player_id
    sender_display_name
    recipient_player_id
    crop_item_id
    quantity
    created_at_ms
}
```

- [x] 事件 ID 在重试中稳定。
- [x] 事件 payload 使用确定性 Protobuf。
- [x] Outbox type 不复用奖励邮件的错误语义。
- [x] 重新生成 Go/TypeScript 类型。

## Task 2：寄件人 Actor

- [x] FriendSvr 校验双方为好友。
- [x] Actor 校验：
  - recipient 非自己；
  - Item 是作物；
  - quantity 为 1–10；
  - 仓库数量足够。
- [x] 同一次 Actor 提交：
  - 扣除作物；
  - 写入 `CreateGiftMail` Outbox；
  - 保存幂等结果；
  - 增加版本并标 Dirty。
- [x] 相同 request ID 返回第一次结果。
- [x] 相同 ID 不同内容返回冲突。
- [x] MailSvr 不可用不阻塞本次 Actor 内存提交。

## Task 3：Relay 和 MailSvr 去重

- [x] Relay 扫描 `CreateGiftMail`。
- [x] 使用 `event_id` 调用 MailSvr。
- [x] MailSvr 按 `source_event_id` 去重。
- [x] 创建私人 GiftMail。
- [x] 创建成功后通知 InfoSvr。
- [x] InfoSvr 失败不重复创建邮件。
- [x] Relay 不确定结果时重新查询或幂等重投。
- [x] 成功后按现有 Outbox 语义标记已投递。

## Task 4：验证

```bash
cd server
go test -race ./internal/player ./internal/friend ./internal/mail -count=1
go test ./... -count=1
go vet ./...
```

E2E：

```text
A和B成为好友
-> A赠送3个作物
-> A仓库立即减少3
-> MailSvr暂时不可用
-> Outbox保留
-> MailSvr恢复
-> B收到一封GiftMail
-> 重放event不产生第二封
-> B收到邮箱红点
```

创建：

```text
docs/archive/evidence/historical/2026-08-12-friend-gift-outbox.md
```

## 完成检查

- [x] 非好友不能赠送；
- [x] 不能赠送给自己；
- [x] 只能赠送作物；
- [x] 数量范围正确；
- [x] 扣除和 Outbox 原子；
- [x] 重试不重复扣除；
- [x] Relay 最终投递；
- [x] MailSvr 去重；
- [x] 离线好友可接收；
- [x] Evidence 和 `CURRENT.md` 更新。