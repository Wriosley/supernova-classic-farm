# 固定双 Zone Kubernetes 最小集群

该清单只部署一个 Coordinator、Login、Gate、`zone-a` 和 `zone-b`。
它不包含动态 Zone 发现、HPA、自动再均衡或生产入口。

## 构建并加载镜像

```bash
for service in login gate coordinator zone; do
  docker build --build-arg SERVICE="${service}" \
    -t "classic-farm/${service}:dev" .
  kind load docker-image "classic-farm/${service}:dev" \
    --name classic-farm
done
```

## 创建 Tcaplus Secret

不要直接应用 `secret.example.yaml`。复制后替换占位值，或执行：

```bash
kubectl create namespace classic-farm --dry-run=client -o yaml | kubectl apply -f -
kubectl -n classic-farm create secret generic classic-farm-tcaplus \
  --from-literal=TCAPLUS_APP_ID='...' \
  --from-literal=TCAPLUS_ZONE_ID='...' \
  --from-literal=TCAPLUS_DIR_URL='...' \
  --from-literal=TCAPLUS_SIGNATURE='...'
```

## 部署

```bash
kubectl apply -k deploy/k8s
kubectl -n classic-farm rollout status deploy/coordinator
kubectl -n classic-farm rollout status deploy/login
kubectl -n classic-farm rollout status deploy/zone-a
kubectl -n classic-farm rollout status deploy/zone-b
kubectl -n classic-farm rollout status deploy/gate
```

本地验收使用端口转发：

```bash
kubectl -n classic-farm port-forward service/login 18080:8080
kubectl -n classic-farm port-forward service/gate 8081:8081
kubectl -n classic-farm port-forward service/coordinator 8083:8083
```

Login 返回的 URL 使用本机 `localhost`，因此适用于该端口转发方式。

## 明确限制

- 两个 Zone 是固定身份和固定 Service，不注册、不发现；
- `INTERNAL_NETWORK_MODE=kubernetes` 允许 Pod 网络调用内部 HTTP 接口，
  只能用于隔离的非生产集群；
- Zone 收到 SIGTERM 时会停止 HTTP 服务，但当前没有 Zone 级 drain，
  因此不要直接缩容或滚动重启有活跃玩家的 Zone；
- 不提供 Ingress、TLS、NetworkPolicy、PDB 或 HPA；
- Tcaplus 凭据只从 Secret 注入，示例文件不得填写或提交真实值。
