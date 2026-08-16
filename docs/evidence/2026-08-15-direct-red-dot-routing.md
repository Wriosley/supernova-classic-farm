---
status: deployed-startup-verified
date: 2026-08-15
---

# Mail 与好友农场红点直达 Zone：离线验证

## 结果

- MailSvr 新增 `SUBSCRIBER_KIND_MAIL` Coordinator SDK subscriber；启动时先
  获取完整 committed Route Snapshot，再对外服务。
- 邮件创建后的红点由 MailSvr 根据 SDK Route 直接投递 Recipient Owner Zone，
  不再调用 InfoSvr。Mail claim 也复用同一个 SDK RouteResolver。
- Zone 成熟事件进入有界非阻塞队列，由固定 worker 查询 FriendSvr，然后按
  好友当前 Shard Route 直接投递 Owner Zone；不再调用 InfoSvr。
- 直达投递严格按 shard/owner/epoch/route-version/endpoint 分组，修复旧
  InfoSvr 仅按 endpoint 合批可能混合多个 Shard 的问题。
- InfoSvr RPC 和部署未删除；Zone 与 Mail 的主调用点已脱离 InfoSvr。
- 没有新增 Tcaplus 表或字段。

## 验证

协议生成：

```text
buf lint
buf generate --template buf.gen.yaml
PASS
```

聚焦 race 回归：

```text
go test -race -count=1 ./internal/reddot ./internal/coordinatorclient \
  ./internal/coordinator/publisher ./internal/mail ./internal/info \
  ./cmd/mail ./cmd/friend ./cmd/zone
PASS

go test -race -count=1 ./internal/player ./cmd/coordinator
PASS
```

部署清单：

```text
kubectl kustomize deploy/k8s
kubectl apply --dry-run=client -f /tmp/classic-farm-red-dot.yaml
PASS
```

覆盖的关键断言：

- 同 endpoint、不同 Shard 产生两个 `DispatchRedDot` 请求；
- `NOT_OWNER` 只 invalidates/resync 并重试一次；
- 成熟通知队列已满时调用者不会阻塞；
- Coordinator Publisher 接受 MAIL subscriber；
- 原有 Mail、Info、Player、Zone 和 Coordinator 回归通过。

## 限制

- 尚未执行“停止 InfoSvr 后分别触发邮件和自然成熟”的实时 E2E。
- 没有记录端到端耗时样本，因此不作 p50/p95/p99 声明。

## kind 部署验证

已构建并 load `coordinator`、`mail`、`friend`、`zone` 四个镜像，应用
Kustomize 并滚动 Coordinator、Mail、Friend、zone-a、zone-b 和四副本
zone-pool。最终 11 个 Pod 全部 Ready，Coordinator diagnostics 报告
`active_subscribers=8`、`queue_overflows=0`、`resyncs=0`。

Mail 在并行 rollout 的启动窗口重启两次：旧 Coordinator Pod 尚未包含
`mail` allowlist，首次 Watch 返回 `PermissionDenied`；新 Coordinator Ready
后 Mail 自动重启并成功建立 SDK Watch，此后保持 Ready。这是 rollout 顺序
证据，不是稳定态鉴权失败。生产/后续脚本应先等待 Coordinator rollout
完成，再滚动新增 subscriber 类型的消费者。
