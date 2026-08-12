---
status: passed-with-limits
date: 2026-08-11
scope: share-link auto friend + first-friend reward mails
---

# 分享链接自动加好友（04-4）

## 完成范围

- FriendSvr `PUBLIC_WEB_BASE_URL`（默认 `http://localhost:5173`）生成
  `{base}/invite/friend?code=...`，随 `CreateFriendCode` / `CreateShareCode`
  返回 `share_url`；有效期仍是好友码 `expires_at_ms`。
- H5 打开邀请路径时把 code 写入 `sessionStorage`，登录页提示
  「登录或注册后将自动添加好友」；WS AUTH + 快照完成后自动
  `REDEEM_FRIEND_CODE`。成功或终结失败清 pending；网络失败保留。
- `FirstFriendReward`（invitee PK）+ Friend Link Saga 新状态
  `FIRST_REWARD_CHECKED` / `REWARD_MAILS_CREATED`。仅 invitee 首次成功加好友
  时双方各收一封系统奖励邮件（10 金币 + 4 葡萄种子 `item_id=1014`），
  `source_event_id` 为 `first-friend:{invitee}:inviter|invitee`。
- MailSvr `CreateSystemRewardMail`（HMAC caller `friend`）+ 领取路径支持
  `coin_amount`（PrivateMail / ClaimSaga / ApplyMailReward / H5 邮箱展示）。

## 验证

```text
go test ./internal/friend ./internal/mail ./internal/player ./internal/gateway -count=1  PASS
  (含 TestFirstFriendRewardsBothPlayers / NonFirst / Concurrent / Retry)
go build ./cmd/friend ./cmd/mail ./cmd/zone ./cmd/gate                               PASS
cd web && npm run typecheck && npm test                                             PASS (9)
```

未跑：真实双浏览器 E2E（需本地 Vite + Friend/Mail/Zone/Gate + 新建
`FirstFriendReward` Tcaplus 表）。公网域名 / Nginx / HTTPS 按计划留给 05。

## 部署注意

- Tcaplus 控制台新建 PB 表 `FirstFriendReward`（见
  `deploy/tcaplus/schema/.../friend_tables.proto`）。
- ConfigMap / `start-servers.sh` 已含
  `PUBLIC_WEB_BASE_URL`、`TCAPLUS_FIRST_FRIEND_REWARD_TABLE`、`MAIL_RPC_URL`。
- FriendSvr 必须能连 MailSvr；Mail 不可用时好友关系仍 ACTIVE，Saga 重试发信。
