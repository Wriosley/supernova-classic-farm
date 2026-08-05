---
status: passed-with-limits
date: 2026-08-05
scope: fixed dual-Zone kind cluster with pure Tcaplus persistence
---

# Kubernetes 固定双 Zone 最小集群

## 范围

集群固定运行 Coordinator、Login、Gate、`zone-a` 和 `zone-b` 各一个
Deployment。没有动态 Zone 注册/发现、第三个 Zone、HPA、PDB 或自动
缩容。

## 实现

- 一个多阶段 `Dockerfile` 构建四个非 root distroless 镜像；
- `deploy/k8s` 提供 Namespace、ConfigMap、Secret 示例、五个
  Deployment、五个 ClusterIP Service 和 Kustomize 入口；
- `INTERNAL_NETWORK_MODE=kubernetes` 显式允许 Pod 网络监听和内部
  HTTP 调用；未配置时仍严格限制 loopback；
- Tcaplus 凭据仅从 `classic-farm-tcaplus` Secret 注入。

## 验证

以下检查通过：

```text
go test ./...                                      PASS
go vet ./...                                       PASS
kubectl kustomize deploy/k8s                       PASS
kubectl create --dry-run=client ...                PASS
docker build login/gate/coordinator/zone            PASS
kubectl wait deployment --all                       PASS
```

真实 kind 集群最终状态为五个 Pod 全部 `1/1 Running`。通过端口转发运行
双 Zone E2E：

```text
DUAL_ZONE zone_a_player=34 shard=4095
zone_b_player=35 shard=3660
migrated_player=36 migrated_shard=3225 migrated_epoch=2
snapshot_lookups=154 shard_lookups=7
PASS
```

测试覆盖 Tcaplus 注册、Session/Ticket、两个 Owner 路由、游戏写入、
非活跃与活跃 Shard 迁移，以及错误 Owner 拒绝。

## 真实部署中修复的问题

1. Kubernetes Service 自动注入的 `LOGIN_PORT`、`GATE_PORT` 和
   `COORDINATOR_PORT` 值是 `tcp://...`，与旧兼容端口变量冲突。清单
   显式将三者置空并使用 `HTTP_ADDRESS`。
2. Login 的 Ticket consume 接口原先只接受 loopback，Gate Pod 调用被
   拒绝。它现在与其他内部接口统一受显式 internal-network policy
   控制，默认本地策略没有放宽。

## 非生产限制

- 集群内网模式信任隔离集群中的 Pod 网络，不是生产身份认证方案；
- Login 和 Gate 仅通过 `kubectl port-forward` 暴露，没有 Ingress/TLS；
- Zone 没有 Zone 级 Drain/preStop，不能安全滚动重启有活跃玩家的 Zone；
- 固定双 Zone 不支持扩容、缩容、故障替补或自动再均衡；
- 本次没有执行 Kubernetes Pod 重启后的玩家恢复专项测试。
