---
status: verified
date: 2026-08-10
evidence_type:
  - code
  - test
  - contract
---

# 新玩家农田延迟初始化

## 完成范围

LoginSvr 注册只创建账号身份；Owner Zone 在 Actor 首次激活且 `Load` 明确返回
`ErrCheckpointNotFound` 时，经带 Fence 校验的 `CreateInitial` 同步持久化初始
农田后，Actor 才进入 `Ready`。

### Login / Auth

- `auth.NewTcaplusStore` 不再接收 checkpoint creator；注册不访问
  `PlayerCheckpoint` / `ShardFence`。
- MySQL 注册事务只插入 `accounts` + `auth_sessions`。
- `cmd/login` 纯 Tcaplus 模式不再打开 Checkpoint/Fence 表用于注册。

### Store

- 新增 `InitialCheckpointStore.CreateInitial`。
- Tcaplus / TcaplusDurable / MySQL 实现 Insert-if-absent：
  Applied / AlreadyApplied / CorruptConflict / Fenced / RetryableFailure。
- Durable / MySQL 创建前校验当前 Zone + Owner Epoch。

### Runtime

- `activateActor`：仅 `ErrCheckpointNotFound` 走初始化；其他 Load 错误直接失败。
- `CreateInitial` 成功（或不确定结果对账成功）后才 Ready。
- 不支持 `CreateInitial` 的 Store → `ErrInitialCheckpointUnsupported`，禁止退回内存默认农田。

### 契约

- `docs/contracts/http-api.md` / `.zh-CN.md`：201 不再要求注册时农田已持久化。

## 验证

```bash
cd server
go test -race ./internal/auth ./internal/player -count=1
# ok

go test -race ./internal/player \
  -run 'FirstActivation|InitialCheckpoint|CreatesInitial|InitializesNewPlayer|ReconcilesAmbiguous|NeverTreats' \
  -count=20
# ok

go test ./internal/player -run 'CreateInitial|InitialCheckpoint|Fenced' -count=1
# ok

go test ./... -count=1
go vet ./...
# ok
```

覆盖点：

- 注册后 `Load` → `ErrCheckpointNotFound`；
- 首次激活创建初始金币/仓库/四块空地/章节任务，并保存 Store token；
- 持久化完成前 lifecycle 保持 Loading；
- 100 并发首请求：一次 Load、一次 CreateInitial；
- 临时 Load 错误不会触发初始化；
- Create 响应丢失后对账恢复；
- Durable/MySQL Fence 拒绝错误 Owner。

## 未在本环境执行

| 项 | 原因 |
|---|---|
| 对真实 Tcaplus 的注册→首次快照→重启 E2E | 需本机 `--tcaplus` 栈或 kind 新镜像 |
| kind 双 Zone 初始 Owner 创建 E2E | 需 rebuild/load Zone+Login 镜像 |
| MySQL 全链路注册→激活→重启 | 适配器单测已覆盖；全链路未重跑 |

## 已知限制

- 初始配置版本仍来自 `NewInitialCheckpoint` 的 `ServerConfigVersion`（与改前一致）。
- 不确定 Create 结果只做一轮 Load 对账；对账失败则激活失败，可重试。
- 旧集群若仍跑“注册即写 Checkpoint”的 Login 镜像，行为与本代码不一致，需一起滚动 Login+Zone。
