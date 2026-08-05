---
status: completed
date: 2026-08-04
scope: TcaplusDB single-record PlayerCheckpoint POC
---

# Tcaplus PlayerCheckpoint POC

## 结论

已在真实 TcaplusDB 环境完成单条 `PlayerCheckpoint` 的连接和一致性
验证。该 POC 证明 `CheckpointStore` 的 Tcaplus 适配器可连接 PB 表，并
可使用 Tcaplus 物理记录版本和逻辑 `checkpoint_revision` 双重 CAS。

当前结论只覆盖单记录玩家 Checkpoint；Zone 生产路径仍使用 MySQL，
账号、Session、ShardFence、迁移进度及 Outbox 尚未迁移到 Tcaplus。

## 已验证

```text
SDK connected to directory server                 PASS
PB PlayerCheckpoint table metadata initialized    PASS
Create if absent                                  PASS
Load with physical record version                 PASS
CAS with expected record version + revision       PASS
Duplicate retry recognized as ALREADY_APPLIED     PASS
Stale token/revision write rejected               PASS
Reload returns the written checkpoint             PASS
```

实际 POC 输出：

```text
TCAPLUS_POC PASS player_id=90000001 checkpoint_revision=2
create_load=true cas=true duplicate=true stale_rejected=true reload=true
```

## 环境边界

- Tcaplus 接入 ID：224；
- 表格组 ID：8888；
- 表协议：PB Generic Table；
- 表：`PlayerCheckpoint`；
- SDK：`github.com/tencentyun/tcaplusdb-go-sdk@v0.2.3`，
  内部 API `3.55.0.000029`；
- 密码由本机未跟踪 `.env` 注入，未记录在本文或命令输出中。

## 尚未验证

- Zone 使用 Tcaplus 作为真实 CheckpointStore 后的完整主人环与重启恢复；
- Tcaplus `ShardFence`、`MigrationProgress`、`PlayerOutbox`；
- 跨记录补偿和 Outbox 对账；
- Kubernetes Secret 注入、readiness 与 preStop Drain。
