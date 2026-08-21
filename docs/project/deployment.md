# Classic Farm 本机启动与部署

## 1. 推荐方式

最终演示推荐使用kind/Kubernetes动态Zone部署。Linux `start-servers.sh` 仍可用于
回环地址下的快速调试，但它包含固定双Zone兼容模式，不代表当前部署架构。

## 2. 前置条件

- Docker和kind；
- 可用的`kubectl`上下文；
- Go 1.26和Node.js；
- 已创建Tcaplus访问Secret与内部RPC HMAC Secret。

不要把真实密码、token、cookie或内部地址提交到仓库。以
`deploy/k8s/secret.example.yaml`为模板在本机创建Secret。

## 3. 创建kind集群

如尚未创建集群：

```bash
kind create cluster --config deploy/kind-config.yaml
kubectl config use-context kind-classic-farm
```

kind配置固定映射宿主端口：Login `31238`、Gate `32591`。

## 4. 构建并加载镜像

后端可构建的服务为`login`、`gate`、`coordinator`、`zone`、`friend`、`info`和
`mail`。以Zone为例：

```bash
docker build --build-arg SERVICE=zone -t classic-farm/zone:dev .
kind load docker-image classic-farm/zone:dev --name classic-farm
```

共享协议或公共包发生变化时，需要重新构建所有受影响服务，而不是只更新一个Pod。

## 5. 部署动态Zone集群

```bash
kubectl apply -k deploy/k8s
kubectl -n classic-farm get pods -o wide
kubectl -n classic-farm rollout status statefulset/gate --timeout=300s
kubectl -n classic-farm rollout status statefulset/zone-pool --timeout=300s
kubectl -n classic-farm get service login gate zone-discovery zone-headless
```

当前清单默认副本为：Login 2、Gate 6、Coordinator 1、Zone 8、Friend 3、Mail 3、
Info 1。清单是事实来源，文档中的数字不覆盖实际YAML。

## 6. 动态扩缩Zone

```bash
kubectl -n classic-farm scale statefulset/zone-pool --replicas=8
kubectl -n classic-farm rollout status statefulset/zone-pool --timeout=300s
```

Coordinator会发现健康Zone并渐进放置、迁移Shard。缩容前必须执行Drain并确认目标
Pod已经没有Owner Shard、开放迁移任务和进度记录；不能直接删除仍持有Shard的Pod。

## 7. 启动H5

本机Vite开发服务器通过两条端口转发访问集群。每条命令必须在同一行执行，并保持
对应终端不退出：

```bash
kubectl -n classic-farm port-forward service/login 18080:8080
```

```bash
kubectl -n classic-farm port-forward service/gate 8081:8081
```

根目录`.env`应与端口一致：

```dotenv
LOGIN_PORT=18080
GATE_PORT=8081
```

然后启动前端：

```bash
cd web
npm install
npm run dev
```

访问`http://localhost:5173`。浏览器只访问Vite的`/v1`和`/ws`代理，不直接连接
Coordinator或Zone。

## 8. 更新服务

更新镜像后滚动对应工作负载：

```bash
kubectl -n classic-farm rollout restart statefulset/zone-pool
kubectl -n classic-farm rollout status statefulset/zone-pool --timeout=300s
```

Gate和Zone是StatefulSet；Login、Coordinator、Friend、Info和Mail是Deployment。
修改清单后先执行：

```bash
kubectl apply -k deploy/k8s
```

## 9. 快速检查

```bash
kubectl -n classic-farm get deployments,statefulsets,pods
kubectl -n classic-farm get endpointslices
kubectl -n classic-farm top pods
kubectl -n classic-farm logs statefulset/zone-pool --tail=100
```

常见问题：

- `port-forward`提示参数不足：端口映射被换行拆成了第二条Shell命令；
- `address already in use`：本机端口已有进程监听，先检查现有转发；
- Login出现CSRF拒绝：同一认证流程跨了不同Login实例；正式Service已配置ClientIP
  affinity，压测多Login时应让单账号固定到同一endpoint；
- Gate更新后仍调用旧Zone IP：检查路由SDK与gRPC连接状态，必要时滚动Gate；
- Zone扩缩容缓慢：查看Coordinator迁移任务和Drain状态，不要绕过Owner fencing。

## 10. 压测开关

以下开关只允许用于隔离诊断：

- `GATE_SKIP_AUTH=true`：跳过Ticket消费；
- `GATE_SKIP_CONNECTION_SYNC=true`：跳过连接注册、刷新和注销；
- `ZONE_QUICK_INFO_ENABLED=false`：关闭Zone到InfoSvr的QuickInfo/Presence旁路。

正式业务验证必须恢复默认值，否则结果不包含完整认证、Presence和精确Push语义。
