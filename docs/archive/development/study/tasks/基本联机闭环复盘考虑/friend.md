## 一、好友码兑换
```mermaid
flowchart LR
    A["INIT"] --> B["reserveSlot(Low) 预留A的好友位"]
    B --> C["reserveSlot(High) 预留B的好友位"]
    C --> D["createRelation 建双向关系记录"]
    D --> E["projectBothSides 把预留位变成双向好友列表条目"]
    E --> F["creditBothSides 给双方发'加好友'任务奖励"]
    F --> G["COMPLETED"]
    C -. 任一方好友满100 .-> H["RELEASING 释放两边预留位"]
    H --> I["ABORTED"]
```

**兑换好友时序图**
saga：先添加好友数量（预留位置）->添加好友关系->修改好友列表->发奖励->完成
```mermaid
sequenceDiagram
    autonumber
    participant C as 客户端 (Unity)
    participant G as Gateway<br/>(grpc_friend.go)
    participant S as FriendSvr.Service<br/>(service.go:125)
    participant L as FriendLinker<br/>(link_saga.go:58)
    participant SM as Saga状态机<br/>advance()
    participant DB as Tcaplus 存储

    Note over C,DB: 正常兑换路径（兑换者 caller 输入好友码 code）

    C->>G: REDEEM_FRIEND_CODE {code}
    G->>S: RedeemShareCode{CallerPlayerId, code}<br/>[gRPC, 身份=gate]
    Note right of S: ① 前置校验
    S->>S: normalizeCode + 校验 caller!=0
    S->>DB: GetCodeLookup(code)
    DB-->>S: lookup(OwnerPlayerId, Status, ExpiresAtMs)
    S->>S: 检查码存在 / 未过期 / Owner≠caller

    Note right of S: ② 交给 Linker
    S->>L: EstablishFriendship(Owner, caller, code, now)
    L->>L: sorted → low, high
    L->>DB: GetRelation(low, high)
    alt 已是 ACTIVE 好友
        DB-->>L: relation (ACTIVE)
        L-->>S: 直接返回 (幂等 no-op)
    else 非好友 → 走 Saga
        L->>DB: GetSaga(linkID=code+caller)
        alt 无 Saga 记录
            L->>DB: InsertSaga(INIT)  [createSaga]
        end
        L->>SM: advance(saga, version)

        rect rgb(235,245,255)
        Note over SM,DB: ③ Saga 步骤逐步落盘
        SM->>DB: reserveSlot(Low) → FriendList[Low].Reservations+1
        SM->>DB: reserveSlot(High) → FriendList[High].Reservations+1
        SM->>DB: InsertRelation(low,high,ACTIVE) → FriendRelation[1条]
        SM->>DB: consumeReservation(Low) → FriendList[Low].Entries+High, Reservations-1
        SM->>DB: consumeReservation(High) → FriendList[High].Entries+Low, Reservations-1
        SM->>DB: creditBothSides → ApplyFriendTaskCredit(Low/High) [发任务奖励, 可跨服务]
        SM->>DB: UpdateSaga → COMPLETED
        end
    end

    L-->>S: (relationID, newlyCreated=true)
    S->>DB: AccountName(Owner)  [取对方昵称]
    DB-->>S: name
    S-->>G: RedeemShareCodeResponse{Friend{Owner,name}, NewlyCreated}
    G-->>C: 领域响应 (WsEnvelope_RedeemFriendCodeResponse)

    Note over C,DB: 后续查看好友
    C->>G: LIST_FRIENDS
    G->>S: ListFriends(caller)
    S->>DB: GetFriendList(caller)  → 读 FriendList[caller].Entries
    S-->>C: 好友列表
```
## 二、tcaplus 好友关系存储
**关系表：用于验证 A 与 B 是否是好友的接口**
```go
// link_saga.go:344 createRelation
relation := &tcaplusv1.FriendRelation{
    PlayerLowId: saga.PlayerLowId,   // 较小的玩家 ID
    PlayerHighId: saga.PlayerHighId, // 较大的玩家 ID
    RelationId:  saga.RelationId,
    Status:      FRIEND_RELATION_STATUS_ACTIVE,
}
l.store.InsertRelation(ctx, relation)  // 插入一条
```
**好友列表**
```go
// link_saga.go:403
func (l *FriendLinker) projectBothSides(ctx, saga, now) error {
    l.consumeReservation(ctx, saga.PlayerLowId,  ...)  // 写给【Low 玩家】的好友列表
    l.consumeReservation(ctx, saga.PlayerHighId, ...)  // 写给【High 玩家】的好友列表
}
```

> 问题：是否需要一直维护这个表？还是判断好友直接从好友列表查找?
> 不需要，直接查好友双方的关系就能查到，redis 可以做到，要看 tcaplus 是否支持

## 三、访问他人农场
**registry**
农场主侧维护一个访客注册表——记录"哪个访客现在正以哪个 visit_id、在哪个 Gate、持有哪座农场的多久有效访问权"。
访客所有请求来到农场主作用前都应该校验当前租约是否过期。

```go
type Registry struct {
    mu     sync.Mutex
    owners map[uint64]*ownerVisits          // key = 农场主 player_id
}

type ownerVisits struct {
    byVisitor map[uint64]*VisitRecord        // key = 访客 player_id
    byVisitID map[string]*VisitRecord        // key = visit_id(16字节)
}

type VisitRecord struct {                   // 一条访客租约
    OwnerPlayerID   uint64
    VisitorPlayerID uint64
    VisitID         []byte                  // 随机生成的临时门票
    GateID          string                  // 访客当前连的 Gate
    LastHeartbeatAt time.Time
    ExpiresAt       time.Time               // 租约到期 = 上次心跳 + VisitTTL
    RequestID       string                  // 幂等用：相同 request_id 重入返回同 visit_id
}
```

**申请进入农场的时序图**
```mermaid
sequenceDiagram
    autonumber
    participant C as 客户端
    participant G as Gateway<br/>(gateway.go:536)
    participant VS as 访客侧 Service<br/>(service.go:52)
    participant F as FriendSvr
    participant OC as OwnerFarmClient<br/>(gRPC)
    participant OS as 农场主侧 OwnerService<br/>(owner_service.go:61)
    participant RG as Registry<br/>(registry.go:45)
    participant RT as player.Runtime

    C->>G: ENTER_FRIEND_FARM{TargetPlayerId=访客自己}
    Note right of G: 按访客ID路由<br/>NOT_OWNER则同request_id重试1次
    G->>VS: callVisitor → Enter(访客, req)
    VS->>VS: 拒绝自访 / 参数校验
    VS->>F: CheckMutualFriend(访客, 农场主)
    F-->>VS: mutual=true, relationID
    opt 访客已在另一位好友农场
        VS->>OC: ExitVisitor(旧农场主, 访客, 旧visitID)
        OC->>OS: gRPC Exit
        OS->>RG: Exit() 删旧租约
        OS->>RT: 推 FARM_VISITOR_LEFT
    end
    VS->>OC: EnterVisitor(农场主,访客,gateID,relationID,reqID)
    OC->>OS: gRPC EnterVisitor
    OS->>RT: BuildPublicFarmSnapshot(农场主)
    RT-->>OS: 公开快照
    OS->>RG: Enter(农场主,访客,gateID,reqID,now)
    RG->>RG: 生成随机 visit_id<br/>写 owners[农场主].byVisitor/byVisitID<br/>ExpiresAt=now+VisitTTL
    RG-->>OS: (visitID, expiresAt, newlyCreated)
    OS->>RT: 推 FARM_VISITOR_ENTERED
    OS-->>VS: (visitID, expiresAtMs, snapshot)
    VS->>VS: current[访客]={农场主, visitID}
    VS-->>C: EnterFriendFarmResponse
```

**访客的心跳续租**
客户端在**进入农场后**，每 30 s 发一条 `Action_FARM_HEARTBEAT` 业务消息，经gRPC `RefreshVisitorHeartbeat` → 农场主侧 `OwnerService.RefreshVisitorHeartbeat` → [`Registry.Refresh`](command:gongfeng.gongfeng-copilot.chat.open-symbol-in-file?%5B%7B%22%24mid%22%3A1%2C%22fsPath%22%3A%22%2Fdata%2Fworkspace%2Fsupernova-classic-farm%2Fserver%2Finternal%2Fvisit%2Fregistry.go%22%2C%22external%22%3A%22file%3A%2F%2F%2Fdata%2Fworkspace%2Fsupernova-classic-farm%2Fserver%2Finternal%2Fvisit%2Fregistry.go%22%2C%22path%22%3A%22%2Fdata%2Fworkspace%2Fsupernova-classic-farm%2Fserver%2Finternal%2Fvisit%2Fregistry.go%22%2C%22scheme%22%3A%22file%22%7D%2C%22Registry.Refresh%22%2C%5B%7B%22line%22%3A44%2C%22character%22%3A5%7D%2C%7B%22line%22%3A47%2C%22character%22%3A1%7D%5D%5D) 把 [`ExpiresAt`](command:gongfeng.gongfeng-copilot.chat.open-symbol-in-file?%5B%7B%22%24mid%22%3A1%2C%22fsPath%22%3A%22%2Fdata%2Fworkspace%2Fsupernova-classic-farm%2Fserver%2Finternal%2Fvisit%2Fregistry.go%22%2C%22external%22%3A%22file%3A%2F%2F%2Fdata%2Fworkspace%2Fsupernova-classic-farm%2Fserver%2Finternal%2Fvisit%2Fregistry.go%22%2C%22path%22%3A%22%2Fdata%2Fworkspace%2Fsupernova-classic-farm%2Fserver%2Finternal%2Fvisit%2Fregistry.go%22%2C%22scheme%22%3A%22file%22%7D%2C%22ExpiresAt%22%2C%5B%7B%22line%22%3A26%2C%22character%22%3A1%7D%2C%7B%22line%22%3A26%2C%22character%22%3A26%7D%5D%5D) 延长 90 s。
```mermaid
sequenceDiagram
    autonumber
    participant C as 客户端
    participant G as Gateway
    participant VS as 访客侧 Service
    participant OC as OwnerFarmClient (gRPC)
    participant OS as 农场主侧 OwnerService
    participant RG as Registry

    C->>G: FARM_HEARTBEAT{visitID}
    G->>VS: Heartbeat(ctx, 访客, req)
    VS->>OC: RefreshVisitorHeartbeat(农场主,访客,visitID,gateID)
    OC->>OS: gRPC Refresh
    OS->>RG: Refresh(...) 校验visitID匹配且未过期
    RG->>RG: ExpiresAt = now + VisitTTL（延长）
    RG-->>OS: expiresAtMs
    OS-->>VS: expiresAtMs
    VS-->>C: FarmHeartbeatResponse{ExpiresAtMs}
```

**后台 tick 清理过期的租约**
```mermaid
sequenceDiagram
    autonumber
    participant OS as 农场主侧 OwnerService
    participant RG as Registry
    participant RT as player.Runtime

    loop evictionInterval = 5s
        OS->>RG: EvictExpired(now)
        RG->>RG: 移除 ExpiresAt 已过期的租约
        RG-->>OS: 被移除记录列表
        OS->>RT: 逐条推 FARM_VISITOR_LEFT
    end
```

## 四、访客视图更新同步
农场主的 zone 维护两个版本号：
- `player_seq` 随任何玩家状态变化（包括金币、购买种子、任务推进）而自增，且这些**私有变化访客无权看到**——把 `farm_view_seq` 合并进 `player_seq` 会让 seq 空跳，访客按 seq 增量合并时拿到一堆"我看不到的空洞"，且访客根本没有农场主的 `player_seq` 权限。
- `farm_view_seq` 只在**公开地块**变化时自增（金币变化、买种子**不**生成公开 Patch）。业务命令只报告 `DomainChanges`（例如 `PlotChanged(plotID)`），Runtime 不再用中央 Action switch（旧 `publicPlotIDsChanged`）猜测哪些操作需要广播。

如果访客链路丢包，`farm_view_seq` 的序号就对不上，需要访客再次拉取完整的农场主完整快照。除了这个 seq 外，视图还需要校验 epoch。
在 Actor 重建/迁移/重启时刷新。epoch 变了，旧 `seq` 直接作废，客户端靠 epoch 不同判定"必须重进拿全量快照"，避免用错乱的 `seq` 续接一个已经不存在的旧世界。

> 问题：没有访客的时候需要消耗资源更新这个访客的版本号吗？
> 不用，广播任务需要单独隔离出来（sprint 03：有界 Dispatcher）

```mermaid
sequenceDiagram
    participant Cmd as 命令
    participant Mailbox as 农场主Actor
    participant Pub as publishFarmViewChanges
    participant BV as buildFarmViewPatch
    participant Disp as farmview.Dispatcher
    participant BC as Broadcaster
    participant Reg as visit.Registry
    participant Gate as Gate
    participant Client as 客户端

    Cmd->>Mailbox: 进入 mailbox 改地块并报告 DomainChanges
    Mailbox-->>Cmd: 地块变更完成
    Cmd->>Pub: publishFarmViewChanges(owner, DomainChanges)
    Pub->>Mailbox: mailbox.Do 内 farmViewSeq++ 并构建 Patch
    Note over Pub,Mailbox: 在 mailbox 内同步，保证 seq 与状态同序
    Mailbox->>BV: 用 farmViewEpoch + 新 seq + 当前地块
    BV-->>Pub: FarmViewPatch(仅公开字段)
    Pub-->>Cmd: 返回 patch(不阻塞)
    Pub->>Disp: Enqueue(owner, patch)
    Note over Pub,Disp: 有界队列 + 固定 worker；队满丢最新；失败不影响命令
    Disp->>BC: Broadcast(ctx, owner, patch)
    BC->>Reg: ListVisitors(owner) 取有效访客
    Reg-->>BC: 访客列表 {visitorID, gateID}
    BC->>Gate: PublishFarmViewPatch(gateA, [ownerID, v1, v2], patch)
    BC->>Gate: PublishFarmViewPatch(gateB, [v3], patch)
    Gate->>Client: WS push FARM_VIEW_CHANGED(epoch, seq, plotUpserts)
    Note over Client: decideFarmViewPatch 合并/恢复 (farm-view.ts)
```
地块变更后，Actor 在 mailbox 内生成 Patch，再交给有界 Dispatcher 异步 fan-out；网络发送不阻塞业务，广播失败也不回滚已成功的命令。
有两类路径会进入 `publishFarmViewChanges`：后台成熟结算，以及业务成功提交后汇总的 `DomainChanges`（含 FriendInteraction 在 SaveCAS 成功之后）。

