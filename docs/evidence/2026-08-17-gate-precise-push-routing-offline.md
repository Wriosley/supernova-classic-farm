# Gate 精确 Push 路由离线证据（2026-08-17）

## 已验证

- Gate 连接注册与好友访问租约携带 `gate_endpoint`。
- Gate 公网逻辑 ID 与 Pod UID 实例 ID 分离，避免破坏 Login 票据绑定。
- Zone Push 客户端按 `(gate_id, gate_endpoint)` 建池，不再使用
  `GATE_RPC_URL`/`service/gate`。
- owner Push、好友农场 patch、邮件/好友红点均从连接或访问租约解析精确 Gate。
- 精确 Gate 上没有目标订阅时，Push RPC 返回 `NOT_FOUND`，不再静默成功。
- Gate 清单为三副本 StatefulSet，并提供 `gate-headless` 与 `minAvailable: 2`
  PDB。

执行结果：

```text
go test ./internal/connection ./internal/visit ./internal/farmview ./internal/push ./cmd/zone
PASS

go test ./cmd/gate
PASS

go test ./... -run '^$'
PASS

kubectl kustomize deploy/k8s
PASS (983 lines rendered)
```

## 尚未验证

- 未构建/加载 Gate 与 Zone 镜像，未更新 kind。
- 未完成三 Gate 浏览器端成熟、好友同屏和邮件红点实测。
- 沙箱无法连接本机 kube-apiserver，因此未执行 API Server dry-run apply。
- 路由池空闲连接清理仍待实现。
