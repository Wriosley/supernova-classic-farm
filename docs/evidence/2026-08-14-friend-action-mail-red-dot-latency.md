---
status: verified
date: 2026-08-14
evidence_type:
  - live-e2e
  - code-trace
---

# 好友偷菜与赠礼红点延迟

## 环境

- 本机 kind `classic-farm` 集群，所有 Coordinator/Gate/Info/Mail/Friend/Zone Pod Ready
- 后端连接真实 Tcaplus，Login/Gate 通过本地 port-forward 访问
- Go E2E 客户端从写入 WebSocket 请求开始计时，到收到匹配的 Response/Push 为止

## 结果

```text
STEAL_FRIEND_CROP end-to-end latency=410.381926ms
SEND_FRIEND_GIFT response latency=65.524211ms
SEND_FRIEND_GIFT red-dot latency=2.844581302s
SEND_FRIEND_GIFT post-response red-dot latency=2.779057181s
```

完整 FriendInteraction E2E 通过，总测试时间 78.78 秒；其中等待作物成熟不计入
偷菜的 410.38 ms。

## 链路证据

偷菜成功路径同步执行 FriendInteraction Get/Insert、多次状态 CAS、访客预留、
Owner ApplyVisitorAction 和访客提交。客户端的“处理中”会持续到整个同步 Saga 完成。

赠礼先在寄件人 Actor 提交中扣库存并写 PlayerOutbox，命令响应很快；Zone Outbox
Relay 默认每 2 秒运行一次，并通过 `Traverse(PlayerOutbox)` 找待投递记录。随后串行
调用 MailSvr（source dedup + PrivateMail）、InfoSvr、收件人 Zone 和 Gate。因此本次
2.78 秒的响应后等待发生在异步 Outbox/红点投递链路，而非 H5 或赠礼命令本身。

## Zone Outbox 即时唤醒验证

改造后，成功的 `SEND_FRIEND_GIFT` 在 durable SaveCAS 返回后，用响应中的
`outbox_event_id` 唤醒同一 Zone 的 Relay；Relay 直接读取该行并投递，2 秒
`Traverse(PlayerOutbox)` 只保留为丢 hint、进程崩溃和临时失败的恢复兜底。

首次上线实测发现 Tcaplus `DoInsert` 成功后紧接的 `DoGet(event_id)` 会短暂返回
not found，而稍后的 Traverse 可以看到该行。即时路径因此增加了仅针对 not found
的最多 500 ms 短重试；其他错误不重试。回归测试覆盖“前两次不可见、第三次可见”并
确认无需全表扫描。

同一 kind + 真实 Tcaplus 环境的修复后单次结果：

```text
SEND_FRIEND_GIFT response latency=61.513314ms
SEND_FRIEND_GIFT red-dot latency=1.221125574s
SEND_FRIEND_GIFT post-response red-dot latency=1.15961234s
```

本次没有出现 `immediate outbox relay failed`，说明即时路径在 500 ms 可见性窗口内
完成；端到端响应后等待由 2.779 秒降到 1.160 秒。剩余时间包含 Tcaplus 行可见、
MailSvr 写入、Info/Zone/Gate 推送，不应仅凭该单样本归因或宣称百分位指标。

另外，修复后重新运行完整 FriendInteraction E2E 通过，偷菜单次链路为
`369.34558ms`，总用例时间 `78.36s`（大部分为作物成熟等待）。

## 限制

- 当前仅为单次功能环境测量，不代表 p50/p95/p99。
- 现有服务日志没有 request_id/interaction_id 贯穿各阶段的耗时字段，暂时不能把
  410 ms 和红点剩余约 0.8 秒精确归因到每一个 RPC/Tcaplus 操作。
- 下一步应先增加分段耗时观测并重复 20--50 次，再确定优化目标与验收阈值。
