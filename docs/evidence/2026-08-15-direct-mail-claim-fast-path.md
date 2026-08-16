---
status: verified-offline
date: 2026-08-15
---

# 邮件领取低延迟直连路径

在线领取已退出 MailClaimSaga：MailSvr 点查并校验邮件后调用 Owner Zone；Zone
Actor 在 mailbox 内增加奖励和 Receipt，标 Dirty 后返回，不同步 SaveCAS；
MailSvr 在响应后异步写 claimed/read 并刷新 Info 未读数。接受的崩溃丢失/重复
窗口记录在 ADR-0013。

验证命令与结果：

```text
go test -race -count=1 ./internal/player \
  -run '^(TestApplyMailRewardAllOrNothingAndReplay|TestApplyMailRewardCapacity)$'
PASS

go test -race -count=1 ./internal/mail
PASS

go test -count=1 ./cmd/mail ./cmd/zone ./internal/gateway \
  -run '^(TestNonExistent|TestToWSMailboxIndicatorPreservesUnreadCount)$'
PASS
```

尚未做真实 Tcaplus 单次领取计时和浏览器验收，不声明 p95。

## 玩家邮箱索引查询与停止 Saga 扫描

Tcaplus 不能仅凭复合主键前缀执行部分键查询，因此 schema 在
`PrivateMail` 上新增 `idx_recipient(recipient_player_id)`，在
`PlayerMailState` 上新增 `idx_player(player_id)`。Mail Store 通过 SDK
`DoGetByPartKey` 分别按玩家读取，
不再 Traverse 全部私人邮件，也不再对每封邮件串行点查状态。新增适配器测试用
一个拒绝 Traverse 的客户端验证两条查询仍只返回目标玩家记录。

真实旧表曾返回 `TXHDB_ERR_INDEX_NO_EXIST (-34565)`，使登录期权威未读查询
失败并最终表现为前端 WebSocket 错误 200。Store 现仅对该明确错误兼容回退
Traverse 并再次按玩家过滤，保证迁移窗口可服务；重建 `PrivateMail` 和
`PlayerMailState` 后使用二级索引才是正常快路径。

MailSvr 启动流程已停止 5 秒 `MailClaimReconciler`，因此不再周期 Traverse
`MailClaimSaga`；旧表和手动恢复代码仍保留。公共邮件仍读取 `PublicMail`，因为
它们本来就是所有符合注册时间玩家的共享邮件，不会混入其他玩家私人邮件。

```text
go test -race -count=1 ./internal/mail ./internal/testtcaplus \
  ./internal/platform/tcaplusdb ./cmd/mail
PASS
```

需要用最新 `mail_tables.proto` 重建 `PrivateMail` 和 `PlayerMailState`；这会
清空现有私人邮件与已读状态。真实 Tcaplus 索引查询延迟仍待部署后单次测量。

`PlayerMailboxCursor` 也已从运行时热路径退役：打开邮箱、登录校准、标记已读和
领取后的缓存刷新均不再读取或 CAS Cursor；Info 快捷投影使用本次权威计算时间
作为事件水位。测试确认 `OpenMailbox` 返回后没有创建 Cursor。Cursor 表和响应
字段仅为兼容保留，没有 schema 变更。Mail/Info race 测试通过。
