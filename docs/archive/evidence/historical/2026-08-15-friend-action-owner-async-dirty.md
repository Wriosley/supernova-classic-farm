---
status: offline-verified
date: 2026-08-15
---

# 好友互动 Owner/Visitor 异步 Dirty

偷菜、投虫、捉虫和帮忙清理仍走统一的 visitor→owner 直接 RPC，但 Owner Actor
不再创建 syncPending 或等待 Checkpoint SaveCAS。mailbox 内完成地块修改、Owner
Receipt、player/checkpoint revision 后立即 `markDirty`、生成 FarmViewPatch 并
返回。Visitor 的金币、任务、狗罚款和 Visitor Receipt 也在 mailbox 内提交后
`markDirty` 返回，不再等待 SaveCAS。

接受的故障边界：任一 Zone 在 Dirty flush 前崩溃时，对应农场修改、奖励/罚款、
任务和 Receipt 可能丢失；客户端已经看到的成功不会回滚，同 request 重试也可能
重新执行。后台 Dirty flush 冲突保留 dirty revision 并按现有机制重试。

验证：

```text
go test -race -count=1 ./internal/player ./cmd/zone ./internal/interaction
PASS
```

测试覆盖 Owner/Visitor 操作响应前零 Checkpoint 写、显式 Dirty flush 后持久化、
并发偷菜上限，以及第一次异步 CAS 冲突后第二次 flush 成功且 FarmPatch 只广播
一次。
