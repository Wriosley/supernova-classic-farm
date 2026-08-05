# 固定双 Zone 集群部署与查看

本文说明 Classic Farm 当前 Kubernetes 最小集群是如何部署的、部署了哪些
代码、使用了哪些配置，以及如何查看和排查集群。

## 1. 先理解几个对象

- **镜像（Image）**：打包好的程序和运行环境。
- **容器（Container）**：镜像启动后的进程。
- **Pod**：Kubernetes 运行容器的最小单位。
- **Deployment**：负责创建 Pod，并在进程退出后自动重启。
- **Service**：给 Pod 提供稳定名称和地址，例如 `http://login:8080`。
- **ConfigMap**：保存非敏感配置。
- **Secret**：保存 Tcaplus 凭据等敏感配置。
- **Namespace**：隔离一组资源；本项目使用 `classic-farm`。

本项目的关系是：

```text
Dockerfile -> 4 个镜像
            -> 5 个 Deployment/Pod
            -> 5 个 Service
            -> TcaplusDB
```

Zone 镜像只有一个，但用不同的 `OWNER_ZONE_ID` 启动两次，成为
`zone-a` 和 `zone-b`。

## 2. 集群中部署了什么

部署了五个后端进程：

- `server/cmd/coordinator`：维护 4096 个 Shard 的路由和迁移状态；
- `server/cmd/login`：注册、登录、Session 和 WebSocket Ticket；
- `server/cmd/gate`：接受 WebSocket，查询本地路由缓存并转发命令；
- `server/cmd/zone`：玩家 Actor 和游戏逻辑；
- 同一个 Zone 程序分别作为 `zone-a`、`zone-b` 运行。

没有部署：

- `web/`、`frontend/` 等前端代码；
- MySQL；
- 动态 Zone 发现、第三个 Zone；
- Ingress、TLS、HPA、PDB 和自动扩缩容。

请求链路：

```text
客户端 -> Login 获取 Session/Ticket
客户端 -> Gate WebSocket
Gate -> Coordinator 路由缓存
Gate -> zone-a 或 zone-b
Login / Zone / Coordinator -> TcaplusDB
```

## 3. 关键代码和配置文件

### 镜像

- `Dockerfile`
  - 使用 Go 1.26 编译 `login`、`gate`、`coordinator`、`zone`；
  - 最终使用非 root 的 distroless 镜像；
  - 通过 `SERVICE` 参数选择要编译的程序。
- `.dockerignore`
  - 构建时只发送 `server/`，避免把前端和文档放进镜像。

### Kubernetes 清单

- `deploy/k8s/namespace.yaml`：创建 `classic-farm` Namespace；
- `deploy/k8s/configmap.yaml`：公共非敏感配置；
- `deploy/k8s/secret.example.yaml`：Tcaplus Secret 示例，不能填写真实值后提交；
- `deploy/k8s/coordinator.yaml`：Coordinator Deployment；
- `deploy/k8s/login.yaml`：Login Deployment；
- `deploy/k8s/gate.yaml`：Gate Deployment；
- `deploy/k8s/zone.yaml`：`zone-a`、`zone-b` 两个 Deployment；
- `deploy/k8s/services.yaml`：五个 ClusterIP Service；
- `deploy/k8s/kustomization.yaml`：汇总以上清单，供 `kubectl apply -k` 使用。

### 重要配置

`ConfigMap` 中主要包含：

```text
STORAGE_MODE=tcaplus
INTERNAL_NETWORK_MODE=kubernetes
ROUTING_MODE=static-dual-zone
COORDINATOR_URL=http://coordinator:8083
TCAPLUS_*_TABLE=<表名>
```

Coordinator 固定配置两个 Zone：

```text
ZONE_A_ID=zone-a
ZONE_A_ENDPOINT=http://zone-a:8082
ZONE_B_ID=zone-b
ZONE_B_ENDPOINT=http://zone-b:8082
```

Secret 中包含：

```text
TCAPLUS_APP_ID
TCAPLUS_ZONE_ID
TCAPLUS_DIR_URL
TCAPLUS_SIGNATURE
```

`server/internal/platform/internalnet/policy.go` 控制网络策略：

- 本地模式只允许 loopback；
- 只有显式设置 `INTERNAL_NETWORK_MODE=kubernetes` 时，才允许 Pod 间通信。

这是最小原型策略，不是生产级服务认证。

## 4. 从零部署

在仓库根目录执行。

### 4.1 检查工具

```bash
docker version
kind version
kubectl version --client
```

没有集群时创建：

```bash
kind create cluster --name classic-farm
kubectl config use-context kind-classic-farm
```

### 4.2 构建并加载镜像

```bash
for service in login gate coordinator zone; do
  docker build --build-arg SERVICE="${service}" \
    -t "classic-farm/${service}:dev" .
  kind load docker-image "classic-farm/${service}:dev" \
    --name classic-farm
done
```

### 4.3 创建 Tcaplus Secret

先创建 Namespace：

```bash
kubectl apply -f deploy/k8s/namespace.yaml
```

再创建 Secret，命令中的值需要替换：

```bash
kubectl -n classic-farm create secret generic classic-farm-tcaplus \
  --from-literal=TCAPLUS_APP_ID='...' \
  --from-literal=TCAPLUS_ZONE_ID='...' \
  --from-literal=TCAPLUS_DIR_URL='...' \
  --from-literal=TCAPLUS_SIGNATURE='...'
```

不要把真实凭据写入 Git。

### 4.4 应用清单

```bash
kubectl apply -k deploy/k8s
kubectl -n classic-farm wait \
  --for=condition=Available deployment --all --timeout=180s
```

## 5. 查看集群状态

确认当前上下文：

```bash
kubectl config current-context
kubectl cluster-info
```

查看本项目全部主要资源：

```bash
kubectl -n classic-farm get deployments,pods,services
```

正常结果应满足：

```text
coordinator  1/1
login        1/1
gate         1/1
zone-a       1/1
zone-b       1/1
```

持续观察 Pod：

```bash
kubectl -n classic-farm get pods -w
```

查看更详细的信息：

```bash
kubectl -n classic-farm describe pod <Pod名称>
kubectl -n classic-farm get events --sort-by=.lastTimestamp
```

## 6. 查看日志和排查错误

查看当前日志：

```bash
kubectl -n classic-farm logs deployment/coordinator
kubectl -n classic-farm logs deployment/login
kubectl -n classic-farm logs deployment/gate
kubectl -n classic-farm logs deployment/zone-a
kubectl -n classic-farm logs deployment/zone-b
```

持续查看日志：

```bash
kubectl -n classic-farm logs -f deployment/zone-a
```

容器重启后查看上一次崩溃日志：

```bash
kubectl -n classic-farm logs deployment/zone-a --previous
```

常见状态：

- `Running`：容器正在运行；
- `Pending`：通常是调度、镜像或资源问题；
- `CrashLoopBackOff`：程序反复崩溃，先看 `--previous` 日志；
- `ImagePullBackOff`：节点找不到镜像，需要重新执行 `kind load docker-image`；
- `0/1 Running`：进程已启动，但健康检查尚未通过。

## 7. 在本机访问服务

服务默认只有集群内部地址。开发机使用端口转发，每条命令单独占一个终端：

```bash
kubectl -n classic-farm port-forward service/login 18080:8080
kubectl -n classic-farm port-forward service/gate 8081:8081
kubectl -n classic-farm port-forward service/coordinator 8083:8083
```

检查健康状态：

```bash
curl http://127.0.0.1:18080/readyz
curl http://127.0.0.1:8081/readyz
curl http://127.0.0.1:8083/readyz
```

端口转发只在命令运行期间有效，按 `Ctrl+C` 停止。

## 8. 修改代码后如何更新

以 Login 为例：

```bash
docker build --build-arg SERVICE=login \
  -t classic-farm/login:dev .
kind load docker-image classic-farm/login:dev \
  --name classic-farm
kubectl -n classic-farm rollout restart deployment/login
kubectl -n classic-farm rollout status deployment/login
```

查看更新是否成功：

```bash
kubectl -n classic-farm get pods
kubectl -n classic-farm logs deployment/login
```

当前 Zone 没有 Zone 级 Drain。存在活跃玩家时，不要随意执行 Zone 的
`rollout restart` 或缩容。

## 9. 停止和删除

删除 Classic Farm 清单和 Namespace：

```bash
kubectl delete -k deploy/k8s
```

删除整个 kind 集群：

```bash
kind delete cluster --name classic-farm
```

删除集群不会删除远端 Tcaplus 表和数据。

## 10. 最常用命令

```bash
# 看状态
kubectl -n classic-farm get deployments,pods,services

# 看事件
kubectl -n classic-farm get events --sort-by=.lastTimestamp

# 看日志
kubectl -n classic-farm logs deployment/<服务名>

# 看上次崩溃
kubectl -n classic-farm logs deployment/<服务名> --previous

# 等待部署完成
kubectl -n classic-farm rollout status deployment/<服务名>

# 查看实际生效配置
kubectl -n classic-farm get deployment/<服务名> -o yaml
kubectl -n classic-farm get configmap/classic-farm-runtime -o yaml
```
