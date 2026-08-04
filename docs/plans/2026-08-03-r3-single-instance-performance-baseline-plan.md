---
status: active
date: 2026-08-03
owner: project-owner
related:
  - 2026-08-03-remaining-roadmap-and-iterations.md
  - ../context/CURRENT.md
  - ../evidence/README.md
---

# R3 单机 Gate / Actor / Push / Dirty 性能基线计划

## 目标

在 Windows 本机、`static-dual-zone` 与 MySQL 检查点模式下建立可重复的
协议端到端基线。结果只说明该机器、该配置和该负载下的观察值；不得外推为
3000 万 DAU 或生产容量结论。

## 第一阶段范围

1. 新建根目录 `benchmark/`，保存脚本、配置、结果格式和运行说明。
2. 新建 `server/cmd/benchrunner` Go 协议客户端：
   - 为一个运行编号创建并认证至多 128 个 `bench_` 测试账号；
   - 建立真实 HTTP/CSRF/Ticket/Protobuf WebSocket 链路；
   - 用固定并发、闭环方式循环 `GET_PLAYER_SNAPSHOT`；
   - 输出请求数、成功/失败数、QPS、P50/P95/P99/最大延迟及原始 CSV。
3. 运行档位为 `1, 10, 25, 50, 100` 并发；默认预热 10 秒、测量 60 秒。
4. 保存机器和运行参数元数据，并提供结果 Markdown 模板。

## 后续阶段（不在本次首个工具提交中）

- 同一玩家与多玩家 Actor 对比；
- 成熟 Push 的端到端延迟样本；
- 从写命令成功响应到 MySQL 检查点版本变化的 Dirty 落盘延迟；
- 小型运行时观测：Actor 排队等待、Dirty 数量和 Flush 耗时。

现有运行时没有 `/metrics`、pprof、Actor 队列深度或 Dirty 批次统计。
在指标实现前，后续三项只能使用外部端到端时间和 MySQL 轮询观测。

## 负载模型

压测客户端在预热后让每个虚拟用户执行：

```text
发送 GET_PLAYER_SNAPSHOT
→ 等待相同 request_id 的 RESPONSE
→ 记录 t_receive - t_send
→ 发送下一次请求
```

同一轮不将请求速率设为无限制的开环速率；固定并发闭环模型使服务变慢时
QPS 自然下降。每个虚拟用户持有一个独立 WebSocket 与玩家账号。

## 非目标

- 不向常规游戏协议增加加金币、重置玩家等压测命令；
- 不把浏览器自动化当作吞吐发生器；
- 不实现生产监控、跨 Gate Push、背压或多机压测；
- 不在结果文件记录密码、Session、CSRF 或 Ticket。

## 验证和退出条件

1. `go test ./...`、`go vet ./...` 和 `go build ./cmd/benchrunner` 通过。
2. 未启动服务时，工具明确报连接/认证错误而不产生误导性结果。
3. 对已启动的本地堆栈，可完成至少一次 `concurrency=1` 的 smoke run，
   生成 `summary.json`、`latency.csv`、`report.md`。
4. 完整 1–100 并发 MySQL 测量必须由项目所有者在稳定的专用运行窗口执行；
   结果和机器信息写入 `docs/evidence/` 后才可在答辩使用。
