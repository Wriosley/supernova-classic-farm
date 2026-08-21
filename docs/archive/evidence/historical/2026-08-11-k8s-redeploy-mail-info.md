---
status: passed-with-limits
date: 2026-08-11
scope: kind classic-farm redeploy with Mail/Info + 2026-08-11 server/H5 fixes
---

# kind 集群重新部署（八服务）

## 范围

把仓库当前 HEAD 对应的后端镜像重建并加载进既有
`kind-classic-farm` 集群，清单从六服务扩到八服务（新增 InfoSvr、
MailSvr），并滚动重启全部 Deployment。

不含 Ingress/TLS、不含 H5 前端进集群（H5 仍走本机 Vite）、不含
04-3 stage E2E。

## 执行

```text
docker build login/gate/coordinator/zone/friend/info/mail :dev   PASS
kind load docker-image … --name classic-farm                    PASS
kubectl apply -k deploy/k8s                                     PASS
  (ConfigMap 增 INFO_RPC_URL/MAIL_RPC_URL/邮件表名；
   Service/Deployment 增 info、mail)
classic-farm-internal-rpc += MAIL_ADMIN_TOKEN                   PASS
  (Mail 首次 CrashLoop：MAIL_ADMIN_TOKEN is required；补上后 Ready)
rollout restart ×8 + rollout status                             PASS
readyz via port-forward ×8                                      PASS
```

最终：

```text
deployment.apps/{coordinator,login,zone-a,zone-b,gate,friend,info,mail}  1/1
```

## 清单侧同步

- `deploy/k8s/README.zh-CN.md`：服务列表改为八个；补上
  `MAIL_ADMIN_TOKEN` 的创建/合并 patch 与 `:dev` 标签必须
  `rollout restart` 的说明。
- `docs/context/CURRENT.md`：集群目标改为八 Deployment，并记入
  H5 外壳后续修复与 Outbox revision 激活 bug 修复。

## 非生产限制

- 同标签 `:dev` + `IfNotPresent`，不重启就不会吃到新层；
- Zone 无 drain，滚动重启会打断活跃玩家；
- `MAIL_ADMIN_TOKEN` 只存在于集群 Secret，未写入仓库。
