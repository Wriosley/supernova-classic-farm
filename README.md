# Supernova Classic Farm

Classic Farm 是一个使用 Go 后端和 Vue 3 H5 实现的经典农场游戏原型，覆盖注册
登录、种植成长、商店仓库、任务奖励、好友农场、多人状态同步、偷菜、邮件和红点
等核心链路。

后端采用 Stateful Player Actor Zone V3：同一玩家的命令在 Actor 内串行执行，
普通状态异步写回 Tcaplus；Coordinator 管理4096个逻辑Shard、lease、epoch和
动态迁移；Gate使用本地路由快照把WebSocket命令转发到当前Owner Zone。

项目视频展示：[农场游戏展示备份](https://www.bilibili.com/video/BV1bU8T6mE41/?share_source=copy_web&vd_source=cd107d1d2700f531f354277cf09bc86e)

## 文档入口

- [最终交付文档](docs/delivery/README.md)：负责人、评审和答辩的推荐入口；
- [当前项目状态](docs/context/CURRENT.md)：已实现能力、部署基线和已知限制；
- [系统架构](docs/architecture/architecture.md)：当前V3整体设计；
- [性能报告](docs/evidence/2026-08-19-classic-farm-performance-report.md)：实测结果与
  3000万DAU容量评估；
- [接口合同](docs/contracts/README.md)：HTTP、WebSocket、内部gRPC和数据语义；
- [完整部署教程](docs/project/deployment.md)：本地H5、kind集群和服务更新方法；
- [历史归档](docs/archive/README.md)：旧架构、开发计划、AI流水和中间实验。

## 项目目录

```text
server/        Go服务、Player Actor和压测工具
web/           Vue 3 H5客户端
deploy/        Kubernetes、kind和本地依赖配置
tests/         跨模块和端到端测试
docs/          当前交付文档、证据和历史归档
yace/          压测原始结果、监控采样和profiling材料
```

## 快速启动

推荐使用Kubernetes动态多Zone方式：

```bash
kubectl apply -k deploy/k8s
kubectl -n classic-farm rollout status statefulset/zone-pool --timeout=300s
kubectl -n classic-farm get pods
```

分别在两个终端转发Login和Gate：

```bash
kubectl -n classic-farm port-forward service/login 18080:8080
kubectl -n classic-farm port-forward service/gate 8081:8081
```

启动H5：

```bash
cd web
npm install
npm run dev
```

浏览器访问 `http://localhost:5173`。首次部署所需的Tcaplus和内部RPC Secret、
镜像构建、动态Zone扩缩容及常见故障处理见[完整部署教程](docs/project/deployment.md)。

## 交付边界

项目验证了面向3000万DAU设计所需的关键机制和本地单实例性能基线，但没有在本机
实际承载3000万DAU。所有容量结论必须连同测试场景、资源规格和限制一起引用。
