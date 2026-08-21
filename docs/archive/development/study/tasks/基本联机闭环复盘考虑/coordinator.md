目前的 shard 分配：
脚本启动时向 coodinator 注册所有的 zone 信息，在 zone 证据 coodintor 的信息。
coodinator 进行哈希分配并构造 map，map 构造完成之后，zone 向 coodinator 申请完整的 map 并缓存。
gate 也在创建的时候向 coodinator 缓存。

> 这部分后续可以优化：目前注入的信息存在脚本里，后续具体信息从 db 中获取，然后由 coodinator 哈希分配，后续可以直接把分别配好的存在 db 里面并检查非法的分配进行少量迁移。
> 这个 k 8 s 可以自己识别到的，有多少 pod 的分配，所以需要让 ai 研究一下

```mermaid
sequenceDiagram
    autonumber
    participant Deploy as "启动脚本 / Kubernetes"
    participant C as "Coordinator"
    participant ZA as "zone-a"
    participant ZB as "zone-b"
    participant G as "Gate"

    Deploy->>C: 注入 ROUTING_MODE=static-dual-zone
    Deploy->>C: 注入 zone-a/zone-b 的 ID 和 Endpoint
    Deploy->>ZA: 注入 OWNER_ZONE_ID=zone-a
    Deploy->>ZA: 注入 COORDINATOR_URL
    Deploy->>ZB: 注入 OWNER_ZONE_ID=zone-b
    Deploy->>ZB: 注入 COORDINATOR_URL
    Deploy->>G: 注入 COORDINATOR_URL

    C->>C: 读取静态 ZoneCandidate 列表
    C->>C: 遍历 4096 个逻辑 Shard
    C->>C: Rendezvous Hash 计算候选 Owner
    C->>C: 生成 committed ACTIVE ShardMap
    C->>C: 初始化/加载 ShardFence
    C->>C: 从 Fence 恢复已迁移的路由
    C->>C: 启动 Lease 定时续期

    ZA->>C: GET /internal/v1/routes
    C-->>ZA: 返回完整 committed ShardMap
    ZA->>ZA: 只保留并授权 owner_zone_id=zone-a 的 Shard

    ZB->>C: GET /internal/v1/routes
    C-->>ZB: 返回完整 committed ShardMap
    ZB->>ZB: 只保留并授权 owner_zone_id=zone-b 的 Shard

    G->>C: GET /internal/v1/routes
    C-->>G: 返回完整 committed ShardMap
    G->>G: 构造本地 RouteCache

    loop Zone 每 5 秒
        ZA->>C: 拉取完整 ShardMap
        C-->>ZA: 最新快照
        ZB->>C: 拉取完整 ShardMap
        C-->>ZB: 最新快照
    end
```



