---
status: deployed-startup-verified
date: 2026-08-15
---

# InfoSvr 快捷信息与邮件数量：实现证据

## 已实现

- InfoSvr 新增纯内存 QuickStore，缓存 presence lease、农场快捷摘要和邮件
  `new_mail_count` 投影；旧红点 RPC/实现保留，但 Mail/Zone 主调用链不使用。
- 在线状态来自 Zone ConnectionRegistry：注册/30 秒 refresh/注销/90 秒过期
  产生异步更新，Zone 每三分钟发送一次在线快照作为恢复对账。
- Actor 在 mailbox 内计算 earliest maturity、growing/mature candidate；激活、
  命令、成熟 Tick 和淘汰路径统一比较发布，Info 故障不阻塞业务。
- FriendSvr 查完权威 FriendList 后一次批量查询 Info，返回 online、last seen、
  earliest maturity 和 may-have-stealable；Info 失败时好友关系仍返回。
- MailSvr/Tcaplus 继续作为邮件和 Cursor 权威。Info 命中返回快捷绝对数量；
  miss/公共邮件水位失效时 MailSvr 回源可见邮件并修复缓存。私人邮件事件按
  mail_id 幂等，打开邮箱成功后按 cursor 清零，延迟旧事件不会重新加一。
- H5 好友姓名后显示绿色加粗“在线”或灰色“离线”；邮箱徽标显示红底白字
  绝对数量，超过 99 显示 `99+`，打开邮箱失败不会提前清零。
- 无新增 Tcaplus 表或字段。

## 验证结果

```text
buf lint
buf generate --template buf.gen.yaml
PASS

go test -race -count=1 ./internal/info ./internal/connection \
  ./internal/player ./internal/friend ./internal/mail ./internal/gateway \
  ./internal/push ./cmd/info ./cmd/zone ./cmd/friend ./cmd/mail ./cmd/gate
PASS

npm run typecheck
npm run test -- --run
npm run build
PASS (5 files / 26 tests; production bundle built)
```

关键测试覆盖：

- presence source sequence 倒退拒绝和 90 秒时间语义；
- mail event 幂等、未知缓存不猜数、cursor 后延迟事件忽略；
- Actor 摘要最早成熟时间和成熟可偷候选；
- 摘要计算保持 mailbox 串行，race 回归通过；
- FriendSvr 聚合在线与离线成熟候选；
- MailSvr known cache 返回具体 count。

## kind 部署

已重新构建/load Info、Zone、Friend、Mail、Gate，按 Info-first 顺序滚动；
zone-a、zone-b 和四副本 zone-pool 均使用新 Zone 镜像。最终 11 个 Pod 全部
Ready、零重启，Info/Mail/Friend 均正常监听且聚焦日志无 ERROR/WARN。

## 尚未完成的运行态 E2E

- 双连接关闭一个仍在线、关闭全部后离线的 UI 实测；
- 强杀 Zone 后 90 秒 presence 过期实测；
- Actor 淘汰后的离线成熟候选 UI 实测；
- 离线三封 + 在线一封显示 `3 → 4`、Info 重启回源、邮箱成功/失败清零实测；
- 公共邮件 watermark 回源实测。

因此本证据只声明代码、race、前端构建和部署启动通过，不声明完整功能 E2E
或性能分位数。

## 手工测试修复：礼物邮件零计数

手工测试发现好友礼物写入后可能没有可见徽标。原因是 InfoSvr 投影冷启动或
调用失败时，`mailCountAfterPrivateInsert` 会把尚未计算的零值作为绝对数量
推给 H5，H5 随即按 `count=0` 隐藏徽标。

MailSvr 现会在快捷投影未知或失败时回源权威邮件与 Cursor 计算绝对数量，并
记录各级失败日志。H5 同时兼容 `SET + count=0`：先保留一个可见徽标，再异步
查询精确数量。`TestCreateGiftMailDedup` 已覆盖冷缓存第一封礼物推送 count 1。

登录流程原本已在认证快照完成后调用 `loadFriends()`；它通过 FriendSvr 批量
查询 InfoSvr，并依据 `may_have_stealable_crop` 初始化好友农场红点，不要求先
打开好友抽屉。

本次通过 Mail race 测试、前端 typecheck、5 文件 26 测试及生产构建；Mail
镜像已重建滚动，替换 Pod Ready、零重启。浏览器礼物/重新登录可见性仍需用户
复测。InfoSvr 重启后，从未重新激活的离线好友摘要仍会暂时 cold，直至 Zone
再次发布该 Actor 摘要；本修复没有改变这个恢复边界。
## 2026-08-15 登录未读数量字段修复

登录后的 H5 已调用 `CHECK_MAILBOX_INDICATOR`，MailSvr 也会返回权威
`new_mail_count`，但 Gate 的 Mail gRPC 适配层曾只复制 `has_new_mail`，遗漏
数量字段，导致 H5 收到 Protobuf 默认值 `0`。现已同时转发
`new_mail_count`，并增加 Gate 字段映射测试和 H5 首次登录显示数量测试。

验证结果：Gateway 新增聚焦测试通过；前端 5 个测试文件共 29 项测试、类型
检查和生产构建通过。完整 Gateway 包测试在受限沙箱中因 `httptest` 无权监听
本地端口而未运行完，这不是业务断言失败。

手工复测继续发现：字段转发修复后，登录仍可能显示 0。根因是原计数语义为
“上次打开邮箱后新增”，`OpenMailbox` 会直接清零 Info 投影，未逐封阅读的
`PlayerMailState.read=false` 邮件也会被隐藏。现已收紧为真正的未读语义：

- 登录查询从权威可见邮件及 `PlayerMailState.read` 重新计算并修复 Info 缓存，
  不允许遗留的缓存零值抑制未读邮件；
- 打开邮箱只更新 Cursor，不清零未读数；
- `MARK_MAIL_READ` 成功后按权威状态刷新快捷缓存，重复请求不会重复扣减；
- Claim Saga 成功把邮件置为已读后，也按权威状态刷新快捷缓存，避免下一封
  在线邮件在过期基线上递增；
- H5 只在阅读或领取一封原本未读的邮件后将本地数字减一。

MailSvr/Gateway 聚焦回归通过；前端仍为 5 个文件、29 项测试通过，类型检查和
生产构建成功。该修复部署需要更新 MailSvr；Gate 必须至少包含上述数量字段
转发修复，Zone/Info 无需滚动。

后续手工测试发现邮箱已无未读但 H5 仍显示 `1`。根因是旧兼容逻辑把
`RED_DOT SET + count=0` 当成“生产者不知道数量”，强制保留至少 1；当前 MailSvr
已经发布绝对未读数，因此该兼容规则失效。H5 现将 `SET + 0` 视为权威零并清除
徽标。新增零计数 Push 回归后，前端 5 个文件共 30 项测试、类型检查和生产构建
通过；仅前端变化，无需滚动集群服务。
