---
status: measured
date: 2026-08-19
---

# Zone 8k/12k pprof 对照与瓶颈定位

## 问题

前一阶段已经证明：热点单 Zone 的 snapshot 吞吐在约 10.5k QPS 形成平台，
`zone-pool-6` 接近 2 CPU limit。本实验回答“Zone 内部具体耗在哪里”，不再只凭
`kubectl top` 判断。

## 方法

- cohort：`hotspot-100.csv`，当前 map version 12317，全部命中 `zone-pool-6`；
- 场景：benchrunner snapshot/open-loop，100 个连接；
- 8k 与 12k offered QPS 各 warmup 20 秒、测量 90 秒；
- 两档稳态中各抓 30 秒 CPU、heap、allocs、goroutine；
- 每 2 秒记录一次 Pod CPU/内存；
- Gate 使用已移除每请求 4096 路由复制的版本。

## 负载结果

| 档位 | achieved QPS | shed | P50 | P99 | 服务错误 |
|---:|---:|---:|---:|---:|---:|
| 8k | 7,999.91 | 0 | 1.715 ms | 23.961 ms | 0 |
| 12k | 10,511.14 | 133,687 | 8.859 ms | 25.712 ms | 0 |

8k profile 为 53.74 CPU 秒/30 秒（1.79 核），12k 为 57.89 CPU 秒/30 秒
（1.93 核）。资源采样峰值分别为 1.792 和 1.944 核。其余 Zone 保持约
0.031–0.038 核，说明 cohort 未分散。

## CPU 归因

30 秒 CPU profile 的主要事实：

- `linux.Syscall6`：8k 22.55%，12k 18.02%；调用链主要是 HTTP/2 response
  flush/write；
- gRPC `processUnaryRPC` cumulative：8k 19.91%，12k 23.15%；
- H2C/HTTP2 包含 frame read/write、HPACK、header 处理与每流 transport；
- `rpcauth` unary interceptor cumulative：8k 8.34%，12k 9.67%，其中 verifier
  约 4.08%/4.77%；
- `runtime.mallocgc` cumulative：8k 8.69%，12k 9.92%；12k 的 select、栈复制、
  扫描和调度占比也上升；
- Player Actor + Player Runtime 整体约 4.54%/4.59%；
- `State.Snapshot` cumulative 约 2.51%/2.18%；
- `materializeDueMaturities` cumulative 约 2.25%/1.93%。

因此 Actor mailbox、成熟计算和 Snapshot 业务构建都不是当前第一 CPU 瓶颈。
12k 相对 8k 的 CPU 增量主要分散在调度、分配、HPACK/header、protobuf
unmarshal 和认证路径，没有一个业务函数出现非线性放大。

## 分配与 goroutine

两次累计 allocs profile 之间增加约 9.65 GB。主要正增量包括：

- `Plot.View` 约 970 MB；
- `State.Snapshot` cumulative 约 1.59 GB；
- gRPC `NewServerHandlerTransport` 约 872 MB cumulative；
- gRPC `HandleStreams` 约 1.66 GB cumulative；
- HTTP/2 header/frame/data-buffer 与 metadata 合计数 GB；
- rpcauth sign/verifier 路径约数百 MB。

12k profile 时 goroutine 436，8k 为 214；12k 多出的主要是 select/runnable RPC
工作，而固定 100 个 Actor mailbox 的 `chan receive` 数量不变。与 shed 和接近
2 核同时出现，符合协议栈工作排队而不是 Actor 死锁。

注意：allocs 是进程启动以来累计值，两个 capture 的差分还包含两次实验间的 setup
和空档；它适合热点排序，不应当被当成精确的 12k 单档分配速率。

## 根因判断

当前 snapshot 压测的第一瓶颈是 Gate→Zone 每请求一次 unary gRPC 经过
`grpc.Server.ServeHTTP` + H2C/HTTP2 的传输、header、protobuf、response write
与对象分配组合成本。Zone 目前用一个 `http.Server` 和 `rpcnet.H2CHandler` 同端口
复用健康检查、pprof 与 gRPC；profile 中的 `serverHandlerTransport` 正是该服务
方式的每 RPC 热路径。

第二梯队是内部 RPC HMAC 认证（约 8%–10% cumulative）和 Snapshot/View 分配引发
的 GC。认证是当前安全边界，不应为得到更好数字而直接关闭；可做单独受控实验来
量化其成本，但正式容量仍必须保留认证。

这个结论只适用于高频完整 snapshot 读取。真实游戏应以命令响应和 push/delta 为
主，不会持续每玩家轮询完整 snapshot；因此它是 Zone RPC/序列化上限场景，不是
业务混合流量模型。

## 建议的优化顺序

1. **Proposed：原生 gRPC listener A/B。** 将内部 gRPC 改为
   `grpc.Server.Serve(net.Listener)`，HTTP health/pprof 使用独立端口或明确的连接
   分流。目标是移除 `serverHandlerTransport`/H2C ServeHTTP 每 RPC 成本。需要同步
   Kubernetes port、Zone endpoint 和 readiness，属于中等风险基础设施改动。
2. **保留安全语义地优化 rpcauth。** 预编译/减少 method 匹配和 metadata/string
   分配，复用 HMAC/hash buffer；不能跳过 replay、caller 和 body hash 校验。
3. **降低 Snapshot 分配。** 避免每次对稳定 plot/task ID 重建并排序临时 slice，
   评估 Actor 内版本化只读 view/cache；必须保证 protobuf response 不被后续状态
   修改并避免共享可变对象。
4. 用完全相同的 8k/10k/12k open-loop 做 A/B；验收同时看 achieved QPS、shed、
   P99、CPU profile 和分配，而不是只看平均延迟。

第一项预计最可能改变当前上限，但这是基于 profile 的待验证判断，不是已测收益。

## 材料

- `/data/workspace/yace/raw/zone_bottleneck_pprof_8k_01/`
- `/data/workspace/yace/raw/zone_bottleneck_pprof_12k_01/`
- `/data/workspace/yace/monitor/zone_bottleneck_pprof_8k_01-pods.csv`
- `/data/workspace/yace/monitor/zone_bottleneck_pprof_12k_01-pods.csv`
- `/data/workspace/yace/profiles/zone_bottleneck_compare_01/`

