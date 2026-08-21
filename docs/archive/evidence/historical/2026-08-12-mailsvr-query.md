---
status: verified
date: 2026-08-12
evidence_type:
  - code
  - test
---

# MailSvr 数据与查询

## 完成范围

独立 MailSvr（默认 `:8087`）：公开邮件单副本 + 注册时间过滤、运营私人邮件、邮箱游标、已读状态、Admin 内网+Bearer、私人邮件创建后 fail-open 通知 InfoSvr。

```text
Admin POST /internal/v1/admin/mails/public|private
  -> PublicMail / PrivateMail (+ MailSourceDedup)
  -> private only: InfoSvr.SetMailRedDot (failure does not roll back)

Gate (HMAC) -> MailService.OpenMailbox / MarkMailRead / CheckMailboxIndicator
  -> merge public(published_at_ms > registered_at_ms) + private
  -> sort (created_at_ms, mail_id) desc, page_size <= 50
  -> OpenMailbox updates last_mailbox_opened_at_ms
```

## 协议 / 表

- `classicfarm.mail.v1.MailService`
- Tcaplus：`PublicMail`、`PrivateMail`、`PlayerMailboxCursor`、`PlayerMailState`、`MailSourceDedup`
- `registered_at_ms` 由可信调用传入；缺省时读 `AccountByPlayer.created_at_ms`

## 行为约束

- 公开邮件不全服 Push
- 未领取旧附件不反复点亮红点（指示器只看打开游标之后的新邮件）
- Admin Token 不写日志；非本机/非 k8s RemoteAddr 拒绝
- MailSvr 要求 `STORAGE_MODE=tcaplus`（测试用 MemoryStore）

## 部署

- `server/cmd/mail`
- Dockerfile `mail`、k8s `mail.yaml`、ConfigMap `MAIL_RPC_URL` + mail 表名、`MAIL_ADMIN_TOKEN`（secret.example）、`start-servers.sh --tcaplus`

## 验证

```bash
cd server
go test -race ./internal/mail ./cmd/mail -count=1
go test ./... -count=1
go vet ./internal/mail ./cmd/mail
# ok
```

覆盖：注册时间过滤、私人隔离、Info 失败不回滚、游标/红点指示、分页、MarkRead、Admin 401。

## 未重跑

- kind / 真实 Tcaplus 联调（表需在集群建表）
- Gate WS 代理与 H5（04-3F）
- 好友赠礼 / 领取 Saga（04-3B / 04-3C）

## 下一子计划

`04-3B-好友赠礼与Outbox.md`
