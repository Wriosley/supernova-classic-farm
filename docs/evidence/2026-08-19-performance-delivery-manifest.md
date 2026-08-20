# 性能报告交付材料清单

## 必交材料

1. `report.md`：全局性能评估报告，包含摘要、测试方法、核心结果、pprof 分析、3000 万 DAU 容量对照和结论。
2. `flamegraphs/`：4 组与报告结论直接相关的 PNG、SVG 和 pprof Flame Graph HTML；截图应使用同名 `.html` 页面，PNG/SVG 仅作静态图和原始图形留档。
3. `evidence/`：支撑报告核心结论的 6 份证据摘要，覆盖 Zone、Gate、好友路由和偷菜 Await 对照。

## 证据摘要范围

- Zone 路由复制优化：证明 Gate 热点消除及吞吐提升。
- Zone 8k/12k pprof：证明 Zone 的 gRPC、H2C、认证和分配热点。
- Gate spread：证明三 Gate 均匀分流下的已观测吞吐边界。
- Friend SDK routing：证明 Coordinator 路由查询从热路径移除后的收益。
- Friend steal no-Await baseline：提供偷菜基线。
- Friend steal Await experiment：说明实验 Await 结果及其 A/B 限制。

## 不纳入主交付包的材料

原始 `latency.csv`、完整 `.pb.gz`、Pod 资源采样 CSV、压测脚本和全部历史 raw 目录保留在 `/data/workspace/yace/`，用于复核和答辩追问，不放入主阅读包，以控制包体积并避免将中间实验误认为最终结论。

## 路径约定

交付报告中的火焰图引用统一使用 `/workspace/bechreport/flamegraphs/`。将本目录挂载或解压到该路径后，Markdown 插图可直接显示。
