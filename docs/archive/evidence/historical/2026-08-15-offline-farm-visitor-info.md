---
status: deployed-startup-verified
date: 2026-08-15
---

# 离线农场红点与访客提醒证据

已实现 InfoSvr 离线农场已看 revision、去重访客集合和版本 ACK；FriendSvr
仅对离线好友应用 InfoSvr 红点决定；Zone 在成功进入后异步记录访问；Player
Runtime 已移除激活、命令和成熟调度期间的 FarmQuickInfo 发布，仅保留 Actor
回收发布。H5 登录查询访客名单、显示一次性提示并确认版本，进入失败不再提前
清除好友红点。

验证：

```text
buf lint
buf generate --template buf.gen.yaml
PASS

go test -race -count=1 ./internal/info ./internal/friend ./internal/player \
  ./internal/gateway ./cmd/info ./cmd/friend ./cmd/zone ./cmd/gate
PASS

npm run typecheck
npm test -- --run
npm run build
PASS (5 files / 26 tests)
```

关键新增覆盖包括相同 revision 访问后红点抑制、新 revision 重新点亮、访客
去重、旧版本 ACK 保留新到访玩家，以及在线农场主不累计离线访客。

Info、Friend、Gate、zone-a、zone-b 和四副本 zone-pool 已使用新镜像滚动；最终
11 个 Pod Ready、零重启，相关启动日志未发现应用 ERROR/WARN。

限制：InfoSvr 投影重启后会丢失访客提醒和已看状态，但不影响权威农场数据；
浏览器双账号功能验证尚未完成。

## 空闲窗口调整

手工验证发现仅在 Actor 回收时发布离线摘要会让断线后的红点出现时间受回收
窗口约束。经确认，`actorIdleTimeout` 已从 3 分钟缩短为 60 秒；无本人连接、
无访客、mailbox 空闲且连续 60 秒无外部访问时 SaveCAS 后回收。Zone 仍每 10 秒
扫描，因此离线摘要通常在 60～70 秒内发布。

Player/Zone race 回归通过；新 Zone 镜像已滚动到 zone-a、zone-b 和四副本
zone-pool。
