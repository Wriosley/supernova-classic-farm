# 固定双 Zone Kubernetes 最小集群

该清单部署 Coordinator、Login、Gate、`zone-a`、`zone-b`、FriendSvr、InfoSvr
和 MailSvr。它不包含动态 Zone 发现、HPA、自动再均衡或生产入口。

## 构建并加载镜像

```bash
for service in login gate coordinator zone friend info mail; do
  docker build --build-arg SERVICE="${service}" \
    -t "classic-farm/${service}:dev" .
  kind load docker-image "classic-farm/${service}:dev" \
    --name classic-farm
done
```

标签始终是 `:dev` 且 `imagePullPolicy: IfNotPresent`，因此加载新镜像后必须
滚动重启已有 Deployment，否则 Pod 会继续用节点上的旧层。

## 创建 Secret

不要直接应用 `secret.example.yaml`。复制后替换占位值，或执行：

```bash
kubectl create namespace classic-farm --dry-run=client -o yaml | kubectl apply -f -
kubectl -n classic-farm create secret generic classic-farm-tcaplus \
  --from-literal=TCAPLUS_APP_ID='...' \
  --from-literal=TCAPLUS_ZONE_ID='...' \
  --from-literal=TCAPLUS_DIR_URL='...' \
  --from-literal=TCAPLUS_SIGNATURE='...'

kubectl -n classic-farm create secret generic classic-farm-internal-rpc \
  --from-literal=INTERNAL_GRPC_HMAC_KEY="$(openssl rand -hex 32)" \
  --from-literal=MAIL_ADMIN_TOKEN="$(openssl rand -hex 32)"
```

若 Secret 已存在但缺 `MAIL_ADMIN_TOKEN`，可合并补上（不会覆盖 HMAC key）：

```bash
kubectl -n classic-farm patch secret classic-farm-internal-rpc \
  --type merge \
  -p "{\"stringData\":{\"MAIL_ADMIN_TOKEN\":\"$(openssl rand -hex 32)\"}}"
```

Coordinator、Gate、两个 Zone、InfoSvr 和 FriendSvr 使用同一个最小原型
HMAC key 认证内部 gRPC（包括 Coordinator Route Watch）。MailSvr 用
`MAIL_ADMIN_TOKEN` 保护内网 Admin API。不要把真实 key 写入或提交到清单。

## 部署

```bash
kubectl apply -k deploy/k8s
kubectl -n classic-farm rollout restart \
  deploy/coordinator deploy/login deploy/zone-a deploy/zone-b \
  deploy/gate deploy/friend deploy/info deploy/mail
kubectl -n classic-farm rollout status deploy/coordinator
kubectl -n classic-farm rollout status deploy/login
kubectl -n classic-farm rollout status deploy/zone-a
kubectl -n classic-farm rollout status deploy/zone-b
kubectl -n classic-farm rollout status deploy/gate
kubectl -n classic-farm rollout status deploy/friend
kubectl -n classic-farm rollout status deploy/info
kubectl -n classic-farm rollout status deploy/mail
```

## 创建可供 CVM/CLB 访问的 kind 集群

首次创建或重建集群时使用仓库内的固定配置：

```bash
kind create cluster --config deploy/kind-config.yaml
```

该配置把宿主机 `31238/32591` 映射到 kind 节点的同名 NodePort；
`services.yaml` 也固定了 Login/Gate 的 NodePort，二者必须保持一致。腾讯云
CLB 监听器应绑定：

```text
CLB :8080 -> CVM :31238 -> login Service
CLB :8081 -> CVM :32591 -> gate Service -> 三个 Gate Pod
```

这种方式不需要 `kubectl port-forward`。已有 kind 节点无法追加 Docker 端口
映射，修改该配置后必须重建集群才能生效。重建前需在内存或安全的临时位置
保留 `classic-farm-tcaplus` 与 `classic-farm-internal-rpc` Secret；禁止把真实值
写入仓库。

本地验收使用端口转发：

```bash
kubectl -n classic-farm port-forward service/login 18080:8080
kubectl -n classic-farm port-forward service/gate 8081:8081
kubectl -n classic-farm port-forward service/coordinator 8083:8083
```

Login 返回的 URL 使用本机 `localhost`，因此适用于该端口转发方式。Gate 用
`CLIENT_CONFIG_URL`（集群内 `http://login:8080/...`）在启动时算配置摘要，
但用 `CLIENT_CONFIG_PUBLIC_URL` 下发给客户端；后者必须和 Login 的
`CLIENT_CONFIG_URL` 完全一致，否则 H5 会以为配置变更并去下载一个浏览器
解析不了的集群内地址。

Gate → Zone 游戏命令、Zone → Gate Player Push 和 Coordinator Route
Watch 使用内部 gRPC；Coordinator 的 HTTP 兼容路由和 gRPC 共享 8083 端口，
由 h2c 分流。kind 默认使用 Tcaplus durable RouteStore 与 Coordinator SDK。
Gate、Info、Zone 可分别通过 `GATE_ROUTE_SOURCE=http`、
`INFO_ROUTE_SOURCE=http`、`ZONE_ROUTE_SOURCE=http-poll` 回滚到 Phase 02；
`COORDINATOR_ROUTE_STORE=legacy-fence` 是独立的存储回滚开关。

如果要把 `login` 和 `gate` 都对外给浏览器直连，使用腾讯云 TKE 的
`LoadBalancer` Service 暴露它们，并把下面这些值改成你的公网/内网 CLB 地址：

```bash
kubectl -n classic-farm patch configmap classic-farm-runtime --type merge -p '{
  "data": {
    "H5_ORIGIN": "http://<你的前端域名或IP>",
    "GATEWAY_URL": "ws://<CLB域名或IP>:8081/ws",
    "CLIENT_CONFIG_URL": "http://<CLB域名或IP>:8080/v1/client-config/1",
    "CLIENT_CONFIG_PUBLIC_URL": "http://<CLB域名或IP>:8080/v1/client-config/1"
  }
}'
kubectl -n classic-farm patch service login --type merge -p '{
  "metadata": {
    "annotations": {
      "service.kubernetes.io/tke-existed-lbid": "lb-n9maz47a"
    }
  },
  "spec": {
    "type": "LoadBalancer"
  }
}'
kubectl -n classic-farm patch service gate --type merge -p '{
  "metadata": {
    "annotations": {
      "service.kubernetes.io/tke-existed-lbid": "lb-n9maz47a"
    }
  },
  "spec": {
    "type": "LoadBalancer"
  }
}'
kubectl -n classic-farm rollout restart deploy/login
```

前端不需要自己发现 gate。它先访问 Login，Login 在 `/v1/bootstrap`
和 `/v1/gateways` 里下发 `GatewayEndpoint.websocketUrl`；只要这里配置成
CLB 地址，H5 就会自动连到 `gate` 的 3 个副本。前端的 HTTP 请求打到
`login` 的 CLB 地址，WebSocket 打到 `gate` 的 CLB 地址；如果两者复用同一
个 CLB，那么只需要区分端口 `8080/8081`。

如果你暂时还想保留本机端口转发，只要不改 `GATEWAY_URL`，前端仍然会按
`ws://localhost:8081/ws` 工作。

本机开发时，Vite 代理也可以直接指向这两个 LB：

```bash
export LOGIN_PROXY_TARGET=http://21.214.142.172:8080
export GATE_PROXY_TARGET=ws://21.214.142.172:8081
pnpm -C web dev
```

这样浏览器访问本机 Web 页面时，`/v1/*` 和 `/ws` 都会被开发服务器转到
对应的 LB，不需要再手工做 `kubectl port-forward`。

## Gate 三副本精确 Push 路由

Gate 已由 Deployment 改为三副本 StatefulSet。公网 WebSocket 仍访问 `gate`
LoadBalancer；Zone 内部 Push 使用玩家连接租约登记的 Pod UID 和精确 Pod DNS：

```text
http://gate-N.gate-headless.classic-farm.svc.cluster.local:8081
```

首次从旧清单切换时必须删除旧 Deployment，避免两种控制器同时选中 `gate`
Service：

```bash
kubectl -n classic-farm delete deployment gate
kubectl apply -k deploy/k8s
kubectl -n classic-farm rollout status statefulset/gate
kubectl -n classic-farm get pods -l app.kubernetes.io/name=gate -o wide
```

后续 Gate 更新使用 `rollout restart statefulset/gate`，不再使用
`rollout restart deploy/gate`。

## Mail/Friend 三副本

MailSvr 和 FriendSvr 各运行 3 个 API 副本。内部调用方使用：

```text
dns:///mail-headless.classic-farm.svc.cluster.local:8087
dns:///friend-headless.classic-farm.svc.cluster.local:8085
```

Headless Service 只发布 Ready Pod，gRPC `round_robin` 为每个地址维护
SubConn。安全查询和幂等 Ack 遇到 `UNAVAILABLE` 时最多重试一次；好友码
写入、邮件创建和邮件领奖不做透明重试。普通 `mail`、`friend` ClusterIP
Service 继续保留用于健康检查、调试和旧客户端回滚。

两个 Deployment 使用 `maxUnavailable: 0`，并各有 `minAvailable: 2` 的
PDB。当前 kind 是单节点环境，因此这些设置能保护滚动更新和主动驱逐，不能
抵御宿主机或整个 kind Node 故障。FriendLinkSaga 和 MailClaimSaga 均不运行
周期性全表恢复扫描。

若要在非生产环境显式重建整张 Route 表（例如把另一套环境遗留的 endpoint
替换为当前 kind Service），临时设置
`COORDINATOR_ROUTE_REINITIALIZE=true` 并只滚动 Coordinator。它会先删除
`ShardMapMeta` 提交锚点，再逐条 CAS 覆盖 4096 条 `ShardRoute`，最后重建
Meta；已有 Route 行不会先删除，因此中断后可重试。等待 Coordinator ready
后必须立即把开关恢复为 `false` 并再次滚动。reinitialize 会生成新的完整
bootstrap route identity，而 map version 可能与旧快照相同；严格的 Zone
authorization 会拒绝这种同版本身份变化，所以该维护动作完成后还必须滚动
重启两个 Zone。生产环境进程会拒绝此开关。

## 八 Zone Pool 与 A/B 退役

清单将 `zone-pool` 扩到8副本，并通过以下 Coordinator 配置持久表达 A/B
退役意图：

```text
COORDINATOR_DRAIN_ZONE_IDS=zone-a,zone-b
COORDINATOR_PLANNER_MIN_HEALTHY_ZONES=8
COORDINATOR_PLANNER_ENABLED=1
COORDINATOR_MIGRATION_WORKER_ENABLED=1
```

必须先构建并加载新的 Coordinator 镜像，再应用清单。Planner 会等8个 pool
均通过健康探测后才创建任务；Migration Worker 保持全局8、每来源2、每目标2
的并发限制。

迁移期间通过端口转发查询删除门槛：

```bash
kubectl -n classic-farm port-forward service/coordinator 18083:8083
curl http://127.0.0.1:18083/internal/v1/zones/drain
```

只有 `zone-a` 和 `zone-b` 都同时满足以下结果才可删除：

```json
{"owner_shards":0,"open_tasks":0,"removable":true}
```

不要仅凭 Pod 数量或 Planner 日志删除 A/B。当前清单仍保留两者，完成实时迁移
验收后再从 kustomization、Deployment 和 Service 中移除。

## 明确限制

- `zone-pool` 使用动态身份和 Kubernetes 发现；旧 A/B 只在迁移验收完成前
  保留；
- 当前支持配置驱动的正常 DRAIN/Rebalance，不提供自动故障转移或
  Coordinator Leader Election；
- `INTERNAL_NETWORK_MODE=kubernetes` 允许 Pod 网络调用内部 HTTP/gRPC，
  只能用于隔离的非生产集群；
- 不要直接缩容仍持有 Current Shard 的 Zone；必须先通过 Drain 删除门槛；
- 不提供 Ingress、TLS、NetworkPolicy 或 HPA；PDB 目前覆盖 Gate/Mail/Friend；
- Tcaplus 凭据和内部 gRPC HMAC key 只从各自 Secret 注入，示例文件不得
  填写或提交真实值。
