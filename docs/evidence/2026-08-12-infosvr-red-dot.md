---
status: verified
date: 2026-08-12
evidence_type:
  - code
  - test
---

# InfoSvr 红点通知

## 完成范围

独立 InfoSvr：持有与 Gate 同语义的 Player RouteCache，将邮件/好友成熟红点路由到 Recipient Owner Zone，再经 `push.Dispatcher` → Gate → H5。不保存红点，不维护 Gate 映射。

```text
MailSvr/测试 → SetMailRedDot → InfoSvr
Owner Zone 成熟且 CanSteal → NotifyOwnerPlotStealable → InfoSvr
InfoSvr → ListFriends(FriendSvr) / RouteCache
       → ZoneNotificationService.DispatchRedDot
Zone → push.Dispatcher → PublishRedDotChanged → Gate PushHub → RED_DOT_CHANGED
```

## 协议

- WS：`Action.RED_DOT_CHANGED`、`RedDotChangedPush`（MAIL / FRIEND_FARM，SET/CLEAR）
- `classicfarm.info.v1.InfoService`：`SetMailRedDot`、`NotifyOwnerPlotStealable`
- `ZoneNotificationService.DispatchRedDot`
- `GatePushService.PublishRedDotChanged`

## 行为约束

- 命中路径不逐请求回源 Coordinator（Warm + CachedRouteResolver）
- `NOT_OWNER`：InvalidateIfVersion → 刷新 → 同 `notification_id` 重试一次
- 收件人去重、升序、单次上限 256
- 离线/投递失败只打日志，不回滚业务
- 好友成熟：仅 `CanSteal` 地块；稳定 ID `stealable:{owner}:{plot}:{player_seq}`；不发 CLEAR

## 部署

- `server/cmd/info`（默认 `:8086`）
- Dockerfile / k8s `info.yaml` / ConfigMap `INFO_RPC_URL` / `start-servers.sh`

## 验证

```bash
cd server
go test -race ./internal/info ./internal/gateway ./internal/player ./internal/push -count=1
go test ./... -count=1
go vet ./...
# ok

cd ../web && npm run typecheck
# ok
```

## 未重跑

- kind 现场联调（邮件红点需 04-3A MailSvr；H5 展示需 04-3F）

## 下一子计划

`04-3A-MailSvr数据与查询.md`
