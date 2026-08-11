---
status: verified
date: 2026-08-12
evidence_type:
  - code
  - build
---

# H5 邮箱与红点窗口

## 完成范围

H5 增加可点击邮箱窗口、邮箱/好友农场红点、好友赠礼入口；Gate 代理
`OPEN_MAILBOX` / `MARK_MAIL_READ` / `CLAIM_MAIL` 到 MailSvr。

```text
RED_DOT_CHANGED Push
  -> mailRedDot / friendFarmRedDots(Set)
  -> 点击邮箱 / 进入农场：仅清本地红点

OPEN_MAILBOX / MARK_MAIL_READ / CLAIM_MAIL
  -> Gate GRPCMailCommander -> MailSvr

SEND_FRIEND_GIFT
  -> Gate mutual-friend check -> Zone Actor
```

## 协议

- WS：`OPEN_MAILBOX=326`、`MARK_MAIL_READ=327`，以及 `MailView` / `MailKind`
- 既有：`CLAIM_MAIL`、`SEND_FRIEND_GIFT`、`RED_DOT_CHANGED`

## 补丁：领取版本号与离线红点

联调发现两个缺口，已修复：

- **领取必失败**。`CLAIM_MAIL` 服务端成功（附件已入库）但响应 envelope 不带
  `state_version`，H5 的写命令校验要求 patch 与 state_version 同时存在，于是
  报「写命令响应缺少 patch 或 state_version」。版本号在链路上本就断了：Zone
  只回 `player_seq`，MailSvr 的 `ClaimMailResponse` 没有版本字段。现在
  `ApplyMailRewardResponse` 增加 `owner_epoch`，MailSvr 回传
  `state_version`，Gate 盖到 envelope 上。仅当更早的尝试已经发放过奖励时故意
  不带版本（无可复现的版本），此时 H5 重拉快照而不是拒绝领取。
- **离线收件永远没有红点**。`RED_DOT_CHANGED` 只推给投递时在线的玩家，公告
  邮件根本不推。新增 `CHECK_MAILBOX_INDICATOR=328`（payload 78/79），H5 认证
  完成后查询一次；查询失败只保留当前红点状态，不影响登录。

真机验证（`--dual-zone --tcaplus`，新注册账号）：

```text
A. 离线投递邮件 -> 登录后 CHECK_MAILBOX_INDICATOR has_new_mail=true
B. 领取前 owner_epoch=1 player_seq=0
   CLAIM_MAIL error=<nil> patch=true items_added=1
   CLAIM_MAIL state_version = owner_epoch=1 player_seq=1   # 连续递增
C. 打开邮箱后 has_new_mail=false
```

## UI

- `web/src/components/MailboxPanel.vue`：分栏、分页、标记已读、领取
- `web/src/components/FriendGiftPanel.vue`：作物 1–10、固定模板预览
- 好友列表：进入农场红点 + 赠送礼物按钮

## 验证

```bash
cd server && go test ./... -count=1
cd web && npm run typecheck && npm run build
# ok
```

## 未重跑

- kind / 多端真实联调（需 Mail/Info/Zone/Gate 同集群）
- 浏览器 320/375/430 人工烟雾
- 阶段 E2E → `2026-08-12-mail-notification-e2e.md`

## 下一子计划

阶段 E2E：`docs/evidence/2026-08-12-mail-notification-e2e.md`
