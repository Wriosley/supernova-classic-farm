---
status: proposed
date: 2026-08-14
scope: gift-mail-red-dot-latency
---

# 好友赠礼邮件即时投递设计

## 目标

当前真机单次测量中，SEND_FRIEND_GIFT响应约65.52ms，邮箱红点总延迟约2.84s，
其中响应后等待约2.78s。Zone Outbox Relay当前只通过2秒Ticker执行全表
`Traverse(PlayerOutbox)`，构成主要固定等待。

本改动在不改变赠礼持久化、Mail去重和红点链路的前提下增加即时投递。完成门槛是
完整链路成功一次并记录单次响应/红点时间；百分位与容量压测另立任务。

## 设计

PlayerOutbox仍是唯一可靠证据。寄件人Actor完成Checkpoint CAS并确认对应PlayerOutbox
行存在后，Zone命令边界调用：

```text
relay.Notify(event_id)
```

Relay拥有容量为1的非阻塞wake channel和一个受Relay.Run串行控制的事件循环：

```text
event_id notify -> Store.Get(event_id) -> 校验PENDING/本Zone ownership -> deliverOne
2s ticker       -> RelayDue全表扫描，恢复丢失Notify、重启和失败任务
```

Notify只是低延迟提示，不是可靠队列。channel满、重复Notify或进程崩溃都允许丢弃提示；
durable Outbox保留PENDING，2秒Ticker最终恢复。Relay.Run必须串行即时投递和恢复扫描，
禁止并发处理同一event造成额外重复RPC。

Store增加按event ID读取单条Outbox的接口。即时路径不得为每封邮件执行全表Traverse。
读取后只接受`CREATE_GIFT_MAIL`且尚未DELIVERED的记录；ownership、retry_at和不可变摘要
校验继续复用现有规则。Mail端`source_event_id`去重与MarkDelivered保持不变。

## 接线

Zone命令处理仅在以下条件全部满足后Notify：

- SEND_FRIEND_GIFT成功；
- 响应包含合法outbox_event_id；
- Runtime同步SaveCAS已经返回成功，因此Outbox行已确认存在。

失败响应、幂等回放且Outbox已DELIVERED、非赠礼命令都不得错误创建任务。重复Notify必须
安全，最坏只产生Mail端幂等重放。

## 验证

- Notify后不等待Ticker即可调用Mail；
- 即时路径按主键Get，不Traverse；
- 重复Notify不创建第二封邮件；
- Notify丢失时Ticker仍能投递；
- 即时投递失败时Outbox保持PENDING并由Ticker重试；
- Relay关闭不会泄漏goroutine；
- 现有礼物Outbox、Mail、Info红点测试不回归；
- kind/Tcaplus完整链路执行一次，记录SEND_FRIEND_GIFT响应时间和红点Push时间，不声明
  p50/p95/p99。

## 范围

不新增Tcaplus表或字段，不修改邮件业务协议，不改变2秒恢复周期，不在本任务优化Mail、
Info或Gate剩余延迟，也不与好友互动异步副作用改造绑定发布。
