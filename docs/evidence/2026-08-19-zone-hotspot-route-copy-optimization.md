---
status: measured
date: 2026-08-19
---

# 单 Zone hotspot 基线与 Gate 路由复制优化

## 目的与边界

本实验先让 Gate 三副本有足够资源，把 100 个固定账号全部路由到
`zone-pool-6`，测量单个 2 CPU Zone 的 snapshot 闭环吞吐；随后根据同步采集的
pprof 做一个局部优化，并用相同 100 并发、30 秒 warmup、120 秒 measurement
做 A/B 复测。

这是本地 kind 单节点、单场景结果，不代表混合业务容量，也不代表 3000 万 DAU。

## 固定条件

- 场景：benchrunner `snapshot`，closed-loop；
- cohort：`/data/workspace/yace/cohorts/hotspot-100.csv`，100 个账号均属于
  `zone-pool-6`；
- 认证：4 个 Login direct endpoint，账号稳定哈希选择 Login，正常 HMAC ticket；
- Gate：3 个 Ready Pod，3 CPU limit/Pod；Zone：8 个 Pod，2 CPU limit/Pod；
- Gate NodePort：`ws://172.18.0.2:32591/ws`；
- 每操作 timeout 15 秒，setup workers 4；
- Coordinator route map version：12317。

## 优化前阶梯基线

Run：`zone_hotspot_closed_01`。

| 并发 | QPS | P50 | P95 | P99 | 错误 |
|---:|---:|---:|---:|---:|---:|
| 10 | 5,096.91 | 1.473 ms | 5.177 ms | 8.016 ms | 0 |
| 25 | 6,542.10 | 3.150 ms | 8.895 ms | 13.001 ms | 0 |
| 50 | 7,396.34 | 5.849 ms | 14.423 ms | 19.411 ms | 0 |
| 100 | 8,194.05 | 11.062 ms | 23.309 ms | 31.056 ms | 0 |

全程 133 个 `kubectl top` 采样中，热点 Zone 平均 1.234 核、峰值 1.749 核；
其余 Zone 平均约 0.037 核。100 并发稳态 pprof 中，热点 Zone 30 秒累计
40.36 CPU 秒。Gate 三副本在 25 并发以上均有流量，100 并发时约为
1.68/1.75/1.69 核。

吞吐从 50 到 100 并发只增加 10.8%，P99 增加 60.0%，已经出现明显收益递减，
但此时仍没有错误，不能把 8,194 QPS 直接称为硬容量上限。

## profiling 定位

优化前 Gate 的 cumulative allocs profile 显示：

- 25 并发：累计分配 512.44 GB，其中
  `coordinatorclient.(*routeCache).getSnapshot` 为 497.34 GB（97.05%）；
- 100 并发：累计分配 953.25 GB，其中该函数为 925.38 GB（97.08%）；
- 100 并发 Gate heap 约 19.5 MB，其中 `getSnapshot` 在用 8.48 MB。

调用链确认 `CoordinatorRouteResolver.Resolve` 先用 `ResolveShard` O(1) 读取单条
路由，又为了取得 `MapVersion` 调用 `Snapshot()`；后者每次复制全部 4096 条
`RouteEntry`。因此每条游戏请求产生一次与分片总数成正比的无效复制。

## 修改与验证

`coordinatorclient.Client` 新增只读 `MapVersion()`，直接从 atomic snapshot 指针
读取版本；Gateway 热路径不再调用 `Snapshot()`。路由条目、失效重试和 map version
语义不变。以下回归通过：

```text
go test ./internal/coordinatorclient ./internal/gateway ./cmd/gate
```

仅重建 `supernova-gate:latest` 并滚动 Gate StatefulSet；`gate-0/1/2` 均 Ready、
restart=0。

## 同参数 A/B 结果

优化后 Run：`zone_hotspot_routecopy_opt_01`。

| 指标 | 优化前 c100 | 优化后 c100 | 变化 |
|---|---:|---:|---:|
| QPS | 8,194.05 | 10,662.80 | +30.1% |
| P50 | 11.062 ms | 8.671 ms | -21.6% |
| P95 | 23.309 ms | 16.820 ms | -27.9% |
| P99 | 31.056 ms | 25.174 ms | -18.9% |
| errors | 0 | 0 | 不变 |

优化后 1,279,588 次成功请求，QPS/错误计数来自完整计数；延迟样本上限为
1,000,000，因此丢弃 279,588 个延迟样本，延迟分位数不是全量直方图。

优化后资源/profile：

- 三个 Gate 在完整采集窗口的平均 CPU 为 0.428/0.461/0.458 核，峰值
  0.759/0.818/0.826 核；
- Gate 的新 allocs/heap profile 中 `routeCache.getSnapshot` 已消失；Gate 30 秒
  CPU 为 22.02 CPU 秒；
- 热点 Zone 峰值 1.946 核，30 秒 CPU 为 58.07 CPU 秒，即约 1.94 核；
- 其他 Zone 仍约 0.033 核，证明热点 cohort 未漂移。

## 结论

已观测事实：移除 Gate 每请求整表复制后，同参数吞吐提升 30.1%，尾延迟下降，
Gate CPU 大幅下降；热点 Zone 在 profile 窗口已接近其 2 CPU limit。

据此推断：本次 snapshot 场景当前主要约束已从 Gate 的路由复制转移至单个 Zone。
约 10.7k QPS 是这个本地环境、这个场景在 100 closed-loop 并发下的已测点，不是
最终安全容量。下一步应保持 Gate 充足，做 Zone 的 offered-load/open-loop 阶梯，
用 P99、错误率、实际 QPS 与 CPU throttling 一起确定拐点和安全水位；随后用分散
cohort 和资源充足的 Zone 单独压 Gate。

## Open-loop 单 Zone 校准

第一次 open-loop 校准暴露了 benchrunner 缺陷：`runSnapshotOpen` 忽略
`-account-file` 并生成新账号，导致 8 个 Zone 分散承压。Run
`zone_hotspot_open_cal_01/02` 因此明确排除在单 Zone 容量证据之外。修复为 open
和 closed 共用 `accountNamesForRun`，新增账号数量回归并通过
`go test ./cmd/benchrunner`，重建 benchrunner 后重新执行。

修复后 Run `zone_hotspot_open_fixed_01`，连接池 100，每档 warmup 20 秒、测量
60 秒：

| offered QPS | achieved QPS | shed | shed/offered | P50 | P99 | 服务错误 |
|---:|---:|---:|---:|---:|---:|---:|
| 8,000 | 7,999.86 | 0 | 0% | 1.707 ms | 23.153 ms | 0 |
| 10,000 | 9,975.24 | 1,354 | 0.226% | 6.738 ms | 24.925 ms | 0 |
| 12,000 | 10,569.08 | 85,493 | 11.874% | 8.818 ms | 25.403 ms | 0 |

153 个 2 秒资源采样中，`zone-pool-6` 平均 1.193 核、峰值 1.938 核；其余
Zone 平均约 0.034 核。三个 Gate 峰值 0.798–0.885 核。因此 12k 档的 shed 与
吞吐平台发生在单 Zone 接近 2 CPU limit 时，不是 Gate 资源不足。

当前可操作结论：对这个 snapshot 单一读场景，约 10.5k QPS 是已观测饱和平台；
8k QPS（约平台的 76%）可作为首版保守运行水位，因为该档 60 秒内无 shed、无
错误。它还不是生产容量：需至少做 30 分钟长稳、读取 CFS throttling 指标，并用
真实读写混合场景复核后才能冻结安全水位。

## 原始材料

- `/data/workspace/yace/raw/zone_hotspot_closed_01/`
- `/data/workspace/yace/monitor/zone_hotspot_closed_01-pods.csv`
- `/data/workspace/yace/profiles/zone_hotspot_closed_01/`
- `/data/workspace/yace/raw/zone_hotspot_routecopy_opt_01/`
- `/data/workspace/yace/monitor/zone_hotspot_routecopy_opt_01-pods.csv`
- `/data/workspace/yace/profiles/zone_hotspot_routecopy_opt_01/`
- `/data/workspace/yace/raw/zone_hotspot_open_fixed_01/`
- `/data/workspace/yace/monitor/zone_hotspot_open_fixed_01-pods.csv`

排除项（保留用于说明工具缺陷，不作为单 Zone 结果）：

- `/data/workspace/yace/raw/zone_hotspot_open_cal_01/`
- `/data/workspace/yace/raw/zone_hotspot_open_cal_02/`
