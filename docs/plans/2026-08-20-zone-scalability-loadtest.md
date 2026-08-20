---
status: proposed
date: 2026-08-20
---

# 3 Gate 下单 Zone/三 Zone 扩展性压测

## 目标

在 3 个 Gate 数量和资源不变、每个 Zone 固定 2 CPU/1GiB 的条件下，对比有效单 Zone 承压与三 Zone 均匀承压的首次业务链路吞吐、延迟、连接稳定性和扩展效率。

## 固定条件

- Gate：3 个，测试期间副本数和资源不变；
- Zone：每 Pod `limits.cpu=2`、`limits.memory=1Gi`；
- 身份：benchrunner `-auth-mode gate-skip`，从 cohort CSV 读取 `player_id`；
- 链路：WebSocket AUTH → 一次 `GET_PLAYER_SNAPSHOT` → 保持连接 → 周期 PING；
- 阶梯：500、1000、2000、3000、5000，先以 10 分钟 hold 验证；
- 两组使用相同连接建立并发、超时、hold 时间、Gate URL 和压测机。

## 对照组

| 组 | Zone 部署 | cohort | 解释 |
|---|---|---|---|
| A | 保留 3 Pod | hotspot，全部测试玩家命中一个 Zone | 有效单 Zone 承压，避免物理缩容引入迁移噪声 |
| B | 保留 3 Pod | spread，玩家均匀命中三个 Zone | 三 Zone 均匀承压 |

若必须物理缩容为一个 Zone，必须先完成 Shard 迁移并确认所有测试玩家路由 ACTIVE；不能直接删除仍持有 Shard 的 Pod。

## 压测认证

Gate 已有 `GATE_SKIP_AUTH`，仅压测时启用：

```bash
kubectl -n classic-farm patch configmap classic-farm-runtime \
  --type merge -p '{"data":{"GATE_SKIP_AUTH":"true"}}'
kubectl -n classic-farm rollout restart statefulset/gate
kubectl -n classic-farm rollout status statefulset/gate --timeout=300s
```

压测结束立即恢复：

```bash
kubectl -n classic-farm patch configmap classic-farm-runtime \
  --type merge -p '{"data":{"GATE_SKIP_AUTH":"false"}}'
kubectl -n classic-farm rollout restart statefulset/gate
```

该模式只跳过 Login/CSRF/Session/Ticket；Gate 路由、内部 HMAC、Owner epoch、Zone fencing、Connection register 和 Player Actor 均保留。

## 命令模板

```bash
./server/benchrunner \
  -scenario connect_hold \
  -auth-mode gate-skip \
  -account-file /data/workspace/yace/cohorts/COHORT.csv \
  -gate-url ws://172.18.0.2:32592/ws,ws://172.18.0.2:32593/ws,ws://172.18.0.2:32594/ws \
  -concurrency 500,1000,2000,3000,5000 \
  -connect-workers 100 \
  -warmup 0s \
  -duration 10m \
  -ping-interval 30s \
  -timeout 15s \
  -run-id RUN_ID \
  -output /data/workspace/yace/raw/RUN_ID
```

`connect_hold` 报告的 QPS 和延迟来自所有连接建立后，每个玩家恰好一次的 `GET_PLAYER_SNAPSHOT`；`duration` 是随后的连接保持窗口，PING 不计入业务 QPS。

## 验收与扩展效率

每档记录总 QPS、P50/P95/P99、错误率、异常断线、Gate/Zone CPU 和内存、CFS throttling、goroutine、Actor 和 Dirty backlog。有效安全档要求 hold 期间无持续错误、内存不超过 80%、CPU 无持续 throttling、连接数不下降、Dirty 可追平。

```text
三 Zone 扩展效率 = QPS(三 Zone) / [3 × QPS(单 Zone)]
```

只有 Gate 和压测机均未先饱和时，才能把差异归因于 Zone 扩展。
