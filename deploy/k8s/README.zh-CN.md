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

Gate、两个 Zone 和 FriendSvr 使用同一个最小原型 HMAC key 认证内部
gRPC（FriendSvr 同时作为 gRPC 服务端接受 Gate/Zone 调用，也作为客户端
向 Owner Zone 发起好友任务积分调用）。MailSvr 用 `MAIL_ADMIN_TOKEN` 保护
内网 Admin API。不要把真实 key 写入或提交到清单。

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

Gate → Zone 游戏命令和 Zone → Gate Player Push 使用 Unary gRPC，并与
现有 HTTP 健康检查、Coordinator 生命周期接口共享 8081/8082 端口。共享
端口由 h2c 分流；Login、Ticket consume 和 Coordinator 路由仍使用 HTTP。

## 明确限制

- 两个 Zone 是固定身份和固定 Service，不注册、不发现；
- `INTERNAL_NETWORK_MODE=kubernetes` 允许 Pod 网络调用内部 HTTP/gRPC，
  只能用于隔离的非生产集群；
- Zone 收到 SIGTERM 时会停止 HTTP 服务，但当前没有 Zone 级 drain，
  因此不要直接缩容或滚动重启有活跃玩家的 Zone；
- 不提供 Ingress、TLS、NetworkPolicy、PDB 或 HPA；
- Tcaplus 凭据和内部 gRPC HMAC key 只从各自 Secret 注入，示例文件不得
  填写或提交真实值。
