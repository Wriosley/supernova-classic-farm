# Steal Direct-Success Path Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` or `superpowers:subagent-driven-development` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 `STEAL_FRIEND_CROP` 从当前的交互 Saga 简化为“访客直连 owner、owner 立即扣菜并 dirty 落库、访客直接成功返回”的单跳路径。

**Architecture:** 访客 Zone 只做请求校验、当前 visit/route 校验和把请求转给 owner 的 `ApplyVisitorAction`；owner Zone 继续复用现有 `player.Runtime.ApplyStealOnOwner`，它已经在 owner Actor mailbox 内完成扣菜、写 OWNER receipt、标脏并在后续由 Dirty flush 持久化。这个计划只收敛偷菜，不改 `APPLY_PEST_TO_FRIEND` / `CATCH_PEST_FOR_FRIEND` / `HELP_CLEAN_FRIEND_PLOT`，也不再为偷菜保留独立的 `FriendInteraction` 恢复链路。

**Tech Stack:** Go, gRPC, TcaplusDB, Player Actor mailbox, existing owner dirty writeback, kind E2E.

---

### Task 1: 让偷菜从访客 RPC 直接打到 owner

**Files:**
- Modify: `server/cmd/zone/friend_rpc.go`
- Modify: `server/cmd/zone/main.go`
- Create: `server/cmd/zone/friend_rpc_steal_direct_test.go`
- Modify: `server/cmd/zone/friend_rpc_steal_test.go`

- [ ] **Step 1: 写直接路径测试，先把新行为钉住**

```go
func TestExecuteFriendActionStealCallsOwnerDirectly(t *testing.T) {
	owner := &recordingOwnerFarmClient{
		response: &rpcv1.ApplyVisitorActionResponse{
			ResultPayload: []byte("ok"),
			FarmPatch:     &wsv1.FarmViewPatch{},
		},
	}
	server := newVisitorZoneRPCServer(mustVisitService(t), owner, localAuthorization{}, routing.DefaultZoneID)

	resp, err := server.ExecuteFriendAction(context.Background(), &rpcv1.ExecuteFriendActionRequest{
		CallerPlayerId:     1,
		OwnerPlayerId:      2,
		VisitId:            make([]byte, 16),
		GateId:             "local-gateway",
		RequestId:          "00112233-4455-6677-8899-aabbccddeeff",
		Action:             datav1.FriendInteractionAction_STEAL_FRIEND_CROP,
		PlotId:             1,
		ExpectedCropItemId: 1002,
		FarmViewEpoch:      make([]byte, 16),
		FarmViewSeq:        7,
	})
	if err != nil || resp.GetResult() == nil || owner.calls != 1 {
		t.Fatalf("resp=%+v err=%v owner_calls=%d", resp, err, owner.calls)
	}
}
```

- [ ] **Step 2: 运行定向测试，确认当前代码还没接上 direct owner client**

Run:

```bash
cd server && go test ./cmd/zone -run TestExecuteFriendActionStealCallsOwnerDirectly -count=1
```

Expected: FAIL，因为当前 `ExecuteFriendAction` 仍然通过 `interaction.StealSaga` 走旧路径，visitor server 还没有 direct owner-steal 依赖。

- [ ] **Step 3: 实现直连路径**

把 `visitorZoneRPCServer` 改成持有一个直接的 owner-steal client，沿用 `visit.ZoneOwnerFarmClient` 的 `ApplyVisitorAction` 能力，不再注入 `interaction.StealSaga`。`STEAL_FRIEND_CROP` 分支里直接构造 `ApplyVisitorActionRequest`，把 `RequestId` 作为 `InteractionId`，保留 `VisitId`、`PlotId`、`ExpectedCropItemId`、`FarmViewEpoch` 和 `FarmViewSeq`，然后把 owner 返回的 `ResultPayload` / `FarmPatch` 原样回给前端。

```go
type visitorZoneRPCServer struct {
	rpcv1.UnimplementedVisitorZoneServiceServer

	visits        *visit.Service
	owner         interaction.OwnerFarmClient
	authorization ownerAuthorization
	ownZoneID     string

	action *interaction.ActionSaga
}
```

`STEAL_FRIEND_CROP` 的成功语义变成：owner 侧 `ApplyStealOnOwner` 成功后，访客就直接收到成功响应；不再等待 `FriendInteraction` 记录、visitor reservation 或 release/reconcile。

- [ ] **Step 4: 运行回归测试，确认 owner 侧行为没有被改坏**

Run:

```bash
cd server && go test -race ./cmd/zone ./internal/player -run 'TestExecuteFriendActionStealCallsOwnerDirectly|TestApplyStealOnOwnerMutatesOnceAndDedupesRetry|TestApplyStealOnOwnerRejectsWhenNotStealableWithoutMutating' -count=1
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add server/cmd/zone/friend_rpc.go server/cmd/zone/main.go server/cmd/zone/friend_rpc_steal_direct_test.go server/cmd/zone/friend_rpc_steal_test.go
git commit -m "feat(friend): direct steal success through owner rpc"
```

---

### Task 2: 去掉偷菜专用的 interaction 启动与恢复 wiring

**Files:**
- Modify: `server/cmd/zone/main.go`
- Modify: `server/cmd/zone/friend_rpc.go`
- Modify: `server/cmd/zone/friend_rpc_steal_test.go`

- [ ] **Step 1: 写启动 wiring 测试，确保 visitor server 能直接拿到 owner client**

```go
func TestVisitorStealFastPathWiresOwnerClient(t *testing.T) {
	owner := &recordingOwnerFarmClient{}
	server := newVisitorZoneRPCServer(mustVisitService(t), owner, localAuthorization{}, routing.DefaultZoneID)
	if server.owner == nil {
		t.Fatal("owner client must be wired for direct steal")
	}
}
```

- [ ] **Step 2: 运行 package 测试，确认新构造还没接上**

Run:

```bash
cd server && go test ./cmd/zone -run TestVisitorStealFastPathWiresOwnerClient -count=1
```

Expected: FAIL，直到 `main.go` 把 `ownerFarmClient` 直接传给 visitor RPC server，而不是再创建 steal 专用 Saga。

- [ ] **Step 3: 删除 steal 专用 startup wiring**

在 `server/cmd/zone/main.go` 里保留 `ownerFarmClient`，把它传给 visitor RPC server；删除 `interactionStore`、`stealSaga`、`stealResolver`、steal 专用 `Reconciler` 和对应的 `runInteractionReconcileLoop` 启动。`ActionSaga` 相关代码如果未来还要给 Phase 6 用，可以保留，但这次计划不要求它参与偷菜。

```go
visitorZoneServer := newVisitorZoneRPCServer(visitorService, ownerFarmClient, authorization, ownerZoneID)
```

`friend_rpc.go` 里删掉 `withStealSaga` 和 `steal` 字段，或者把它们降到只服务未来的 Phase 6，不再被 `STEAL_FRIEND_CROP` 使用。

- [ ] **Step 4: 运行 package 回归**

Run:

```bash
cd server && go test -race ./cmd/zone ./internal/player ./internal/visit -count=1
```

Expected: PASS，并且 `ExecuteFriendAction(STEAL_FRIEND_CROP)` 只走 owner 直连。

- [ ] **Step 5: 提交**

```bash
git add server/cmd/zone/main.go server/cmd/zone/friend_rpc.go server/cmd/zone/friend_rpc_steal_test.go
git commit -m "refactor(friend): drop steal saga startup wiring"
```

---

### Task 3: 记录结果并同步项目状态

**Files:**
- Create: `docs/archive/evidence/historical/2026-08-14-steal-direct-success-path.md`
- Modify: `docs/context/CURRENT.md`

- [ ] **Step 1: 跑一条真机偷菜样本，确认端到端结果**

Run:

```bash
cd server && go test -run TestFriendInteractionE2E -count=1 ./test/e2e
```

如果需要 kind/Tcaplus 复现，则在现有本机集群和端口转发保持不变的前提下，执行一次 `STEAL_FRIEND_CROP`，记录一次响应时间和访客实际到账时间。这里只记录单样本，不声明 p50/p95/p99。

- [ ] **Step 2: 写证据**

证据文件应记录：

```text
- commit hash / image tag
- 代码路径：visitor 直连 owner，owner 用 ApplyStealOnOwner + Dirty 落库
- 单次响应时间
- 单次到账时间
- 没有再依赖 steal 专用 FriendInteraction 恢复链路
- 已知限制：owner Dirty flush 仍然是异步持久化，极端进程崩溃窗口仍可能丢失未落盘变更
```

- [ ] **Step 3: 更新 CURRENT**

把 `docs/context/CURRENT.md` 里好友互动那段改成“偷菜已切到访客直连 owner 的成功路径；偷菜不再依赖独立恢复 Saga”，并明确这次改造不影响 `APPLY_PEST_TO_FRIEND` / `CATCH_PEST_FOR_FRIEND` / `HELP_CLEAN_FRIEND_PLOT` 的后续计划。

- [ ] **Step 4: 提交**

```bash
git add docs/archive/evidence/historical/2026-08-14-steal-direct-success-path.md docs/context/CURRENT.md
git commit -m "docs(friend): record direct steal success path"
```
