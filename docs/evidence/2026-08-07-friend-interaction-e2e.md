---
status: verified
date: 2026-08-07
evidence_type:
  - code
  - test
  - runtime
  - kubernetes
---

# 好友功能阶段 7：完整 H5、Kubernetes 与恢复 E2E

## 完成范围

| 项 | 结果 |
|---|---|
| H5 好友码 / 列表 / 串门 / 投虫·捉虫·清理·偷菜 UI | 阶段 3–6 已可用；本阶段未再改前端核心逻辑 |
| FriendSvr 镜像 + kind 六 Pod | `deploy/k8s/friend.yaml`；六 Deployment Ready |
| 三客户端公开视图一致性 | `TestFriendInteraction`（owner + 2 visitors WS） |
| FriendSvr / Gate / Zone 完整停启恢复 | `TestFriendRestartRecovery` + `run-friend-restart-recovery.sh` |
| Gate 客户端配置 URL（kind） | `CLIENT_CONFIG_PUBLIC_URL`，避免 AuthResponse 与 bootstrap 不一致 |

### 1. 多进程 E2E

新增：

- `server/test/e2e/friend_interaction_test.go`
- `server/test/e2e/friend_interaction_saga_recovery_test.go`
- `tests/e2e/run-friend-interaction.sh`
- `tests/e2e/run-friend-restart-recovery.sh`

覆盖验收清单第 6 节：

1. 两名访客兑换农场主好友码并出现在列表；
2. 访客 `ENTER_FRIEND_FARM` 后农场主收到 `FARM_VISITOR_ENTERED`；
3. 两名访客同时在线，公开快照一致；
4. 访客 A 投虫 → 访客 B 捉虫 → 成熟后偷菜 → 收割后帮忙清理；
5. 投虫后 owner / 两名访客的 `FarmViewPatch.pest_active` 一致；
6. 访客 A 退出后，owner 再种植时仅访客 B 收到 `FARM_VIEW_CHANGED`；
7. 完整停栈再启后，三人可登录、互为好友、可再次进入农场。

运行：

```bash
./tests/e2e/run-friend-interaction.sh
./tests/e2e/run-friend-restart-recovery.sh
```

本地实测（2026-08-07）：

```text
RESULT friend_interaction=PASS owner=fo_260807025024_1880
RESULT friend_restart_recovery=PASS owner=fo_260807025207_8900
```

### 2. kind 六 Pod

```text
coordinator  1/1 Running
friend       1/1 Running
gate         1/1 Running
login        1/1 Running
zone-a       1/1 Running
zone-b       1/1 Running
```

本机 H5 连集群需保持：

```bash
kubectl -n classic-farm port-forward service/login 18080:8080
kubectl -n classic-farm port-forward service/gate 8081:8081
```

### 3. 过程中修掉的阻塞问题

- Gate 对浏览器下发 `CLIENT_CONFIG_URL=http://login:8080/...`，与 Login
  bootstrap 的 `localhost` 不一致 → 登录卡在「客户端配置」。
  新增 `CLIENT_CONFIG_PUBLIC_URL`（见 `server/cmd/gate/main.go`、
  `deploy/k8s/gate.yaml`）。
- Zone `ExecutePlayerCommand` 把真实错误压成 `invalid game command` →
  增加 `game command rejected` 日志。
- `visitCommandTimeout=3s` 在共享 Tcaplus 负载下不够跑完偷菜 Saga →
  调至 `15s`（`friendCommandTimeout=5s`）。
- Gate 推送在 `GET_PLAYER_SNAPSHOT` 完成前缓冲 → E2E 必须先拉快照再等
  Presence / FarmViewPatch。

## 质量门

- `go test ./test/e2e -run 'TestFriend'`（无 `E2E_RUN` 时 skip）
- `./tests/e2e/run-friend-interaction.sh` PASS
- `./tests/e2e/run-friend-restart-recovery.sh` PASS
- `go test ./internal/testcatalog`（catalog 含 bash kind）
- kind 六 Pod Ready

## 明确限制 / 未执行

- 三个真实浏览器窗口的人工同屏验收仍可作为演示检查，但协议级三客户端
  一致性已由 Go E2E 覆盖；
- 未在 E2E 中注入 Saga 中途杀进程（崩溃窗口仍由阶段 5 单测覆盖）；
  本阶段验证的是「完整停启后关系与可进入性」；
- kind 与本机 `--tcaplus` 栈共用同一套 Tcaplus 表，并行压测可能互相抢
  延迟；跑 E2E 时建议不要同时对集群打重负载；
- 仍无 Ingress / TLS / 生产日志栈；日志默认 stdout + `kubectl logs`。

## 启动方式

```bash
# 本机六进程
./start-servers.sh --dual-zone --tcaplus

# 或 kind
kubectl apply -k deploy/k8s
```
