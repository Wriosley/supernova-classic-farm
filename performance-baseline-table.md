| 场景 | QPS | 延时 | 结论 |
|---|---:|---:|---|
| Gate 资源充足、单 Zone snapshot（0 shed） | 7,999.86 | P99 23.153 ms | 单 Zone 在 8k QPS 下稳定运行，可作为保守运行水位 |
| Gate 资源充足、单 Zone snapshot（有 shed） | 10,569.08 | P99 25.403 ms | 单 Zone 实测吞吐平台约 10.5k QPS，12k offered 后出现 shed |
| Zone 资源充足、三 Gate snapshot（0 shed） | 24,986.55 | P99 69.290 ms | 三 Gate 均匀分流已完整承接约 25k QPS |
| Zone 资源充足、三 Gate snapshot（有 shed） | 27,553.10 | P99 83.717 ms | 30k offered 下出现 shed；压测生成器存在 CPU wait，不能作为 Gate 极限 |
| Friend 进入农场交互（SDK 路由，200 对） | 2,528.24 cycles/s（约 7,585 command/s） | P99 124.930 ms/cycle | 当前 Friend 生命周期最大实测吞吐；Info 达到 CPU limit，不能视为 Zone 极限 |
| Friend 偷菜（普通，无 Await） | 5,589.34 | P99 62.396 ms | 610 次尝试全部成功，作为无 Await 基线 |
| Friend 偷菜（实验 Await） | 6,057.59 | P99 51.716 ms | 1,600 次尝试全部成功；目标数量不同，仅作实验结果，不能直接宣称性能提升 |
