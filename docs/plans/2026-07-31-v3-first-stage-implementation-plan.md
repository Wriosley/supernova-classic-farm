---
status: completed
date: 2026-07-31
owners:
  - project-owner
related:
  - ../architecture/stateful-zone-v3-architecture.md
  - ../architecture/single-player-vertical-loop-business-architecture.md
  - ../contracts/http-api.md
  - ../contracts/websocket-protocol.md
  - ../contracts/idempotency-and-errors.md
  - ../contracts/data-model.md
  - ../contracts/event-contracts.md
---

# V3 第一阶段有界实施计划

## 1. 目标

在本地可重复证明：

```text
H5 注册或登录
→ HTTP Session 获取 bootstrap 和一次性 WS Ticket
→ GateSvr 完成 WebSocket AUTH
→ GET_PLAYER_SNAPSHOT 经 committed ACTIVE Shard 路由到 Player Actor
→ H5 收到相同 request_id 的 RESPONSE
```

该里程碑证明契约可以映射到代码，并证明最小请求链路存在。它不证明完整业务闭环、生产 Coordinator 高可用或 3000 万 DAU 容量。

## 2. 范围与非目标

范围：

- Go 与 Vue 3 可运行骨架；
- HTTP 注册、登录、Session、bootstrap 和一次性 WS Ticket 的最小安全实现；
- Protobuf WebSocket AUTH、PING 和 `GET_PLAYER_SNAPSHOT`；
- 单节点 Coordinator 兼容的 committed `ACTIVE` 路由；
- Player Actor Mailbox 串行执行和最小初始快照；
- MySQL 本地依赖、迁移入口和可替换的存储接口；
- Go/TypeScript Protobuf 生成与 round-trip smoke test；
- 可重复的端到端证据。

本阶段非目标：

- 完整购买、种植、施肥、成熟、收获、出售和任务闭环；
- Dirty Flusher 的完整性能实现与 Outbox Relay；
- Mail Service、好友和多人同步；
- 三节点共识、生产服务发现和跨机房部署；
- 任何 3000 万 DAU、QPS、延迟或高可用能力声明。

## 3. 代码布局和所有权

```text
proto/classicfarm/v1/       accepted 契约对应的唯一 Protobuf 源
server/cmd/login/           LoginSvr 入口
server/cmd/gate/            GateSvr 入口
server/cmd/zone/            ZoneSvr 入口
server/cmd/coordinator/     单节点 Coordinator 入口
server/internal/auth/       Session、Ticket 与认证存储
server/internal/gateway/    WebSocket、关联和连接状态
server/internal/routing/    ShardMap 缓存、hash 与 NOT_OWNER
server/internal/actor/      Mailbox 和 Actor 生命周期
server/internal/player/     最小玩家聚合与快照投影
server/internal/persistence/存储接口与 MySQL 实现
server/gen/                 生成的 Go Protobuf 类型
web/src/gen/                生成的 TypeScript Protobuf 类型
web/                        Vue 3 H5
deploy/                     本地依赖、迁移和启动配置
tests/e2e/                  跨进程验收
docs/evidence/              实测步骤、结果和限制
```

`proto/` 由协议任务独占；生成目录禁止手工修改。根构建文件和 `deploy/` 由基础设施任务独占。各服务任务不得复制共享 DTO。

## 4. 固定实现约定

- Go 使用一个 `server/go.mod`，避免第一阶段过早拆分多模块。
- H5 使用 Vue 3、Vite 和 TypeScript；应用位于 `web/`，现有 `frontend/src/assets/art/` 保持为只读占位资产源。
- Protobuf 源位于 `proto/classicfarm/v1/`，生成 Go 和 TypeScript 类型。
- `player_id`、epoch、sequence 和 version 在 TypeScript 中使用 `bigint` 或十进制字符串，禁止转成 JavaScript `number`。
- 本地端口固定为：H5 `5173`、LoginSvr `8080`、GateSvr `8081`、ZoneSvr `8082`、Coordinator `8083`、MySQL `3306`。
- 浏览器公开边界严格使用 accepted HTTP/WS Protobuf 契约。第一阶段内部服务边界使用小接口封装；本地可使用进程内或 loopback HTTP 适配器，调用方不得依赖具体传输。
- `shard_id = stable_hash64(player_id) % 4096`。只有 committed、`ACTIVE` 且租约有效的路由可以写；本地初始地图把目标 Shard 指向一个 Zone。
- 首条 Actor 命令固定为 `GET_PLAYER_SNAPSHOT`。它是读请求，不进入写命令幂等窗口，但必须携带用于关联的 `request_id`。
- MySQL 不可用时可以用显式开发配置启用内存适配器以排查链路，但正式 8 月 2 日证据必须说明实际使用的适配器，不能把内存结果描述成持久化证据。

## 5. 实施顺序

### Wave 0：契约与计划

1. 复核 HTTP bootstrap 与 WS `AuthResponse` 的七个字段。
2. 复核 Ticket 原子消费、Session 撤销、版本、幂等、Outbox 与异常回退边界。
3. 只在无冲突后生成 `.proto`；冲突必须先由唯一契约维护者修订。

### Wave 1：共享基础

1. 建立 Go、Vue、Protobuf 和本地 MySQL 工具链。
2. 将 accepted 字段和枚举编码为 `.proto`。
3. 建立配置、日志、健康检查和优雅退出。
4. 提供生成检查与 Go/TypeScript round-trip smoke test。

### Wave 2：并行服务

1. LoginSvr：注册、登录、Session、bootstrap、Ticket 签发与原子消费接口。
2. Coordinator/Router：单节点 committed ShardMap、租约、epoch 和 ACTIVE 查询。
3. ZoneSvr：最小 Actor、Mailbox、初始化/加载接口和快照投影。
4. GateSvr：AUTH、PING、64 KiB 限制、路由、关联响应和 `NOT_OWNER` 同 ID 重试。
5. H5：HTTP Session 流程、配置校验、Ticket、AUTH、snapshot 请求和响应展示。

### Wave 3：集成与证据

1. 用一条命令启动依赖和服务。
2. 执行浏览器端主路径。
3. 执行 Ticket 重放、未认证请求、错误 epoch、重复 request 和重连最小失败场景。
4. 在 `docs/evidence/` 记录环境、命令、观察结果和未验证项。

## 6. 8 月 2 日验收清单

- 注册只在玩家初始状态和 Session 均可用后返回成功。
- 同一 Ticket 的并发消费最多一个成功。
- AUTH 是连接建立后十秒内第一个非心跳请求。
- PING 在 GateSvr 内处理，不激活 Actor。
- H5 可发送多个在途请求，并按 `request_id` 关联响应。
- GateSvr 只使用 committed ACTIVE 路由；内部 `NOT_OWNER` 刷新后复用同一 ID。
- 同玩家 Actor 命令串行；snapshot 来自 Actor 内存投影而不是直接读旧检查点。
- `owner_epoch` 变化触发客户端完整替换快照。
- 64 KiB 限制在无界分配前执行。
- 端到端脚本可在干净本地环境重复运行。

## 7. 风险和停止条件

- 共享 Proto 未冻结前，服务实现只能依赖手写接口 stub，不能各自发明网络 DTO。
- 若 8 月 1 日中午仍未打通 AUTH，暂停完整 Coordinator、Dirty 和 UI 扩展，保留相同接口改用单进程或内存适配器，优先完成认证到 Actor 的链路。
- 若浏览器安全策略阻塞联调，先保持 accepted CSRF、cookie 和 loopback 限制，不通过关闭安全校验绕过问题。
- 若 MySQL migration 阻塞主链路，内存适配器只能作为临时诊断手段；证据必须标记限制。
- 不为赶里程碑加入 V2 Journal、本地 WAL 或未经决策的同步持久化例外。

## 8. 后续工作

第一阶段通过后，再按 accepted 业务架构依次实现写命令、Dirty 合并写回、任务、奖励 Outbox、双 Zone 迁移/fencing 和分层压测证据。
