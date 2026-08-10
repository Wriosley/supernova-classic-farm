---
status: proposed
date: 2026-08-10
parent:
  - ./04-3-邮件与通知阶段总计划.md
depends_on:
  - ./04-3D-InfoSvr红点通知.md
---

# MailSvr 数据与查询 Implementation Plan

> **For agentic workers:** 本计划只实现 MailSvr 存储、运营邮件、邮箱查询和打开游标；好友赠礼与领取 Saga 分属后续计划。

**Goal:** 建立独立 MailSvr，支持公开邮件、运营私人邮件和玩家邮箱查询。

**Architecture:** 公开邮件只保存一份，查询时根据玩家注册时间过滤。私人邮件按 recipient 保存。玩家邮箱游标记录上次打开时间，用于判断是否产生新邮件红点。

## 业务规则

- 公开邮件只保存一份。
- 玩家只能看到注册后发布的公开邮件。
- 公开邮件不全服 Push。
- 私人邮件创建后通知 InfoSvr。
- 邮件不设置过期时间。
- 邮件记录时间、奖励、寄信人和收信人。
- Admin API 只允许内网和密钥鉴权。
- 打开邮箱后更新 `last_mailbox_opened_at_ms`。
- 未领取旧附件不反复点亮红点。

## 建议数据表

```text
PublicMail
PrivateMail
PlayerMailboxCursor
PlayerMailState
```

建议键：

```text
PublicMail:
  primary key = mail_id

PrivateMail:
  primary key = recipient_player_id + mail_id

PlayerMailboxCursor:
  primary key = player_id

PlayerMailState:
  primary key = player_id + mail_id
```

具体 Tcaplus 索引必须通过查询测试验证。

## 邮件字段

```text
mail_id
mail_type
created_at_ms
published_at_ms
sender_type
sender_player_id
sender_display_name
recipient_type
recipient_player_id
title
content
attachments
source_event_id
```

附件：

```text
item_id
quantity
```

## 注册时间

MailSvr 查询请求必须从可信内部调用获得：

```text
player_id
registered_at_ms
```

`registered_at_ms` 不能由 H5 自由填写。

优先由 Gate 从已认证 Session/Account 上下文传入内部 MailSvr RPC。

## 文件范围

```text
server/cmd/mail/
server/internal/mail/
proto/classicfarm/v1/mail/
proto/classicfarm/v1/rpc/
deploy/tcaplus/schema/
deploy/k8s/
```

## Task 1：存储模型

- [ ] 定义公开邮件、私人邮件、附件、游标和玩家邮件状态 Protobuf。
- [ ] 建立 Tcaplus Store 接口和 fake Store。
- [ ] 实现公开邮件 Insert/Get/ListSince。
- [ ] 实现私人邮件 Insert/ListByRecipient。
- [ ] 实现邮箱 Cursor Load/SaveCAS。
- [ ] 实现 PlayerMailState 的已读字段。
- [ ] `source_event_id` 唯一去重。
- [ ] 所有列表按 `(created_at_ms, mail_id)` 稳定倒序。
- [ ] 增加分页和单页上限。

## Task 2：运营 Admin API

```text
POST /internal/v1/admin/mails/public
POST /internal/v1/admin/mails/private
```

- [ ] 验证 Admin Token/HMAC。
- [ ] 默认只监听或允许内网来源。
- [ ] 校验标题、正文、附件数量和数量上限。
- [ ] 公开邮件只写一条。
- [ ] 私人邮件写入指定 recipient。
- [ ] 私人邮件创建成功后调用 InfoSvr。
- [ ] InfoSvr 失败不回滚邮件创建。
- [ ] Admin Token 不写日志。

## Task 3：邮箱查询

内部/玩家接口：

```text
OpenMailbox
MarkMailRead
CheckMailboxIndicator
```

- [ ] `OpenMailbox` 合并：
  - 注册后发布的公开邮件；
  - 当前玩家私人邮件；
  - 后续 GiftMail。
- [ ] 公开邮件过滤：

```text
published_at_ms > registered_at_ms
```

- [ ] 打开成功后更新 `last_mailbox_opened_at_ms`。
- [ ] `CheckMailboxIndicator` 查询上次打开后的新内容。
- [ ] 查询本身不领取附件。
- [ ] 已读状态按玩家保存。
- [ ] 注册前公开邮件不可见、不可领取。

## Task 4：启动和验证

```bash
cd server
go test -race ./internal/mail ./cmd/mail -count=1
go test ./... -count=1
go vet ./...
```

E2E：

```text
发布公开邮件
-> 老玩家可见
-> 后注册玩家不可见

发布私人邮件
-> 指定玩家可见
-> 其他玩家不可见
-> 在线玩家收到红点
```

创建：

```text
docs/evidence/2026-08-12-mailsvr-query.md
```

## 完成检查

- [ ] 公开邮件只保存一份；
- [ ] 注册时间过滤正确；
- [ ] 私人邮件隔离正确；
- [ ] 邮箱游标正确；
- [ ] Admin API 有鉴权；
- [ ] 邮件不过期；
- [ ] 查询支持分页；
- [ ] InfoSvr 失败不回滚邮件；
- [ ] Evidence 和 `CURRENT.md` 更新。