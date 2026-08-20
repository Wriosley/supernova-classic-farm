---
status: measured
date: 2026-08-19
---

# 三 Gate spread snapshot 校准

## 目的

在八个 Zone 资源充足的条件下校准三 Gate snapshot 转发能力，并验证流量确实均匀
进入三个 Gate。该阶段是短测，不是最终容量结论。

## 前置条件

- Coordinator map version 12317；
- `spread-800.csv` 共 800 个账号，八个 Zone 各 100 个；
- 三 Gate、八 Zone 全部 Ready、restart=0；
- Gate 使用已移除每请求 4096 路由复制的镜像；
- 四个固定 Login endpoint，setup workers 8；
- Zone 业务代码和部署未修改。

## 分流校准

单一 Gate NodePort 的短测 `gate_spread_open_cal_01` 出现 metrics-server 观测窗口内
Gate CPU 不均，且 30k 时服务资源尚低却 shed，不能据此判断三 Gate 容量。

benchrunner 因此增加逗号分隔 `-gate-url`，按账号 FNV 稳定选择 Gate。压测专用
Service `gate-0/1/2-loadtest` 分别使用 NodePort 32592/32593/32594，并各自只有一个
对应 Pod endpoint。它们不替换正式 Gate Service。

100 个连接的理论账号分配为 28/39/33。60 秒 10k 同窗 CPU profile 分别得到：

| Gate | 30 秒 CPU sample |
|---|---:|
| gate-0 | 18.50s |
| gate-1 | 21.56s |
| gate-2 | 19.84s |

三者约为平均值的 -7.3%/+8.1%/-0.5%，证明固定入口可以用于后续可归因测试。

## 800 连接固定分流短校准

Run `gate_direct_open_cal_02`，snapshot/open-loop，每档 warmup 10 秒、measurement
30 秒：

| offered | achieved | shed | P50 | P99 | errors |
|---:|---:|---:|---:|---:|---:|
| 20k | 19,996.59 | 0 | 7.118ms | 34.934ms | 0 |
| 25k | 24,986.55 | 0 | 16.150ms | 69.290ms | 0 |
| 30k | 27,553.10 | 70,519 | 25.485ms | 83.717ms | 0 |

全窗口资源峰值：三 Gate 为 1.522–1.590 核（limit 3），八 Zone 为
0.737–0.799 核（limit 2）。benchrunner 在 30k 档约使用 2.49–2.65 核，其中
约 0.94–1.02 核为 system CPU，并有约 31%–36% wait。Pod 均无 restart。

另用 1000 连接复测单 NodePort 30k 并未提高 achieved QPS，仍约 27.41k；但该 run
Gate 分流不可控，因此只用于排除“800 改为 1000 就能消除限制”，不用于 Gate
副本容量对比。

## 结论边界

已观测：三 Gate 在均匀固定分流下至少可以完整承接 25k snapshot QPS，P99
69.3ms、0 shed、0 服务错误；每 Gate 和每 Zone 都仍有 CPU 余量。

30k 档的 shed 不能归因给 Gate：服务资源未饱和，800 条连接每条串行执行请求，
同时压测机产生较高 system/wait。27.5k 是当前生成器、连接池和服务组合的已测平台，
不是 Gate 上限。

下一步先按计划执行 `connect_hold` 长连接容量；继续寻找请求吞吐上限前，需要让 open
模式跨多个 target 复用连接，并准备更多均匀 spread 账号/连接，或使用多进程压测源。
正式 Gate 容量值必须在生成器无 shed 且服务自身先出现拐点时才能给出。

## 材料

- `/data/workspace/yace/raw/gate_spread_open_cal_01/`
- `/data/workspace/yace/raw/gate_spread_pool1000_cal_01/`
- `/data/workspace/yace/raw/gate_direct_baseline_01/`
- `/data/workspace/yace/raw/gate_direct_open_cal_02/`
- `/data/workspace/yace/monitor/gate_direct_open_cal_02-pods.csv`
- `/data/workspace/yace/monitor/gate_direct_open_cal_02-benchrunner-pidstat.txt`
- `/data/workspace/yace/profiles/gate_direct_baseline_01/`
- `/data/workspace/yace/manifests/gate-direct-nodeports.yaml`

