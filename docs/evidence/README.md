# Evidence

本目录只保留最终交付直接引用的性能材料和少量代表性机制验收。按功能日期产生的
细粒度实验已移至 `../archive/evidence/historical/`，仍可追溯但不进入默认阅读路线。

## 最终性能材料

- [综合性能报告](2026-08-19-classic-farm-performance-report.md)
- [性能交付清单](2026-08-19-performance-delivery-manifest.md)
- [单 Zone 最新建连与核心链路基线](2026-08-20-single-zone-connect-hold-baseline.md)
- [Zone Drain 与迁移并发](2026-08-20-zone-drain-concurrency.md)

## 性能专题证据

- [Zone热点与Gate路由复制优化](2026-08-19-zone-hotspot-route-copy-optimization.md)
- [Zone瓶颈pprof对照](2026-08-19-zone-bottleneck-pprof-comparison.md)
- [三Gate均匀分流校准](2026-08-19-gate-spread-calibration.md)
- [好友SDK路由优化](2026-08-19-zone-friend-sdk-routing.md)
- [跨Zone好友交互压测](2026-08-19-cross-zone-friend-loadtest.md)
- [偷菜无Await基线](2026-08-19-friend-steal-no-await-baseline.md)
- [偷菜Await实验](2026-08-19-friend-steal-await-experiment.md)

## 代表性机制验收

- [纯Tcaplus运行门槛](2026-08-05-pure-tcaplus-runtime-gate.md)
- [好友交互E2E](2026-08-07-friend-interaction-e2e.md)
- [持久化当前Shard路由](2026-08-12-durable-current-shard-route.md)
- [Kubernetes动态Zone发现](2026-08-13-zone-kubernetes-discovery.md)
- [Mail/Friend三副本负载均衡](2026-08-16-mail-friend-three-replica-grpc-balancing.md)
- [八Zone迁移及静态Zone退役](2026-08-17-eight-zone-pool-drain-offline.md)
- [Gate精确Push路由](2026-08-17-gate-precise-push-routing-offline.md)
- [同步礼物邮件直达链路](2026-08-18-sync-gift-mail-direct.md)

## 证据规则

Evidence records observed results and limitations. Store reproducible evidence
used to support project claims, including:

- Test commands and results.
- Load models and environment descriptions.
- Latency, throughput, error, CPU, memory, and connection results.
- Screenshots or diagrams that support a report.
- Links to the exact commit tested.

Distinguish:

1. Code evidence: a mechanism exists.
2. Runtime evidence: one execution behaved a certain way.
3. Statistical evidence: repeated measurements under a defined load.

Never present code evidence as proof of performance improvement.

Evidence does not silently convert a proposed design into an accepted decision.

历史证据索引见 [归档说明](../archive/README.md)。
