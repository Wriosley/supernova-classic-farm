# R3 单机性能基线

本目录保存压测模型、运行脚本和本地结果；Go 协议客户端位于
`server/cmd/benchrunner`，因为它必须使用后端的共享 Protobuf 模块。

## 首个场景：`snapshot`

每个虚拟用户完成真实的注册、CSRF、Ticket、WebSocket AUTH 后，在一个独立
连接上闭环请求 `GET_PLAYER_SNAPSHOT`：

```text
send request -> await correlated response -> record latency -> repeat
```

默认运行档位是 `1,10,25,50,100` 并发，预热 10 秒，测量 60 秒。首次运行
会创建最多 128 个带本次 `run-id` 前缀的 `bench_` 账号；后续并发档位会
复用同一批账号，不会按档位重复创建。它们只存在于本地测试数据库中。

## 前置条件

1. 使用 MySQL 启动当前双 Zone 栈：

   ```powershell
   . .\tests\e2e\_mysql-env.ps1
   $c = Resolve-MySQLConnection -AllowPrompt
   .\start-servers.ps1 -MySQLDSN $c.Dsn -DualZone
   ```

2. 在另一终端运行：

   ```powershell
   cd server
   go run ./cmd/benchrunner `
     -scenario snapshot `
     -concurrency 1,10,25,50,100 `
     -warmup 10s `
     -duration 60s
   ```

默认结果目录为 `benchmark/results/<run-id>/`。它包含：

- `environment.json`：机器、Go、参数和服务地址；
- `summary.json`：每档的聚合指标；
- `latency.csv`：每个样本延迟（微秒）；
- `report.md`：供审阅的本次结果摘要。

`results/` 是本机生成数据，禁止提交密码、Cookie、CSRF 或 Ticket。

## 结果口径

- `QPS`：测量窗口内的成功响应数 / 秒；
- `P50/P95/P99`：从发送一个 Protobuf WebSocket 请求到收到其关联响应的
  客户端端到端延迟；
- `error_count`：超时、连接、协议或业务错误总数；
- `max_latency`：测量窗口中最大的成功请求延迟。

这是单机本地基线，不是生产吞吐、连接容量或 3000 万 DAU 能力声明。
