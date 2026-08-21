# 性能报告交付材料清单

## 必交材料

1. `2026-08-19-classic-farm-performance-report.md`：全局性能评估报告，包含摘要、测试方法、核心结果、pprof分析、玩家到达模型、3000万DAU容量对照和结论。
2. `../delivery/assets/flamegraphs/`：4组与报告结论直接相关的PNG、SVG和pprof Flame Graph HTML；截图应使用同名`.html`页面，PNG/SVG用于静态插图和留档。
3. `evidence/`：支撑报告核心结论的 6 份证据摘要，覆盖 Zone、Gate、好友路由和偷菜 Await 对照。

## 证据摘要范围

- Zone 路由复制优化：证明 Gate 热点消除及吞吐提升。
- Zone 8k/12k pprof：证明 Zone 的 gRPC、H2C、认证和分配热点。
- Gate spread：证明三 Gate 均匀分流下的已观测吞吐边界。
- Friend SDK routing：证明 Coordinator 路由查询从热路径移除后的收益。
- Friend steal no-Await baseline：提供偷菜基线。
- Friend steal Await experiment：说明实验 Await 结果及其 A/B 限制。

## 不纳入主交付包的材料

原始`latency.csv`、完整`.pb.gz`、Pod资源采样CSV、压测脚本和全部历史raw目录保留在仓库`../../yace/`，用于复核和答辩追问，不进入主阅读路线。

## 路径约定

交付报告中的火焰图统一使用仓库相对路径`../delivery/assets/flamegraphs/`，移动或打包整个`docs`目录后仍可显示。
