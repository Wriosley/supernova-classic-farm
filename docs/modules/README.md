# 模块职责

| 模块/服务 | 权威数据与职责 | 关键不变量 |
|---|---|---|
| Login | Account、Session、CSRF、Ticket | Ticket一次消费；密码和Session不进入游戏协议 |
| Gate | WebSocket连接、路由缓存、Push连接 | 不保存玩家业务权威状态；按当前ShardMap转发 |
| Coordinator | ShardRoute、Fence、迁移进度 | 一个Shard同一epoch只有一个有效Owner |
| Player/Zone | Checkpoint、农场、资产、任务 | 同玩家命令在Actor内串行；旧epoch写入被拒绝 |
| Friend | FriendRelation、FriendList、邀请码 | 好友关系由FriendSvr权威管理 |
| Interaction | 偷菜、放虫、抓虫、帮忙清理 | 跨Actor步骤必须幂等并可识别终态 |
| Mail | 公共/私人邮件、阅读和领取状态 | 奖励去重；领取状态与业务回执可追溯 |
| Info | 玩家轻量信息、未读数、在线旁路 | 不是农场权威状态；旁路失败不能篡改Actor |
| FarmView/Push | 好友农场Snapshot和Patch | epoch变化或seq缺口必须回退完整Snapshot |

跨模块拓扑见`../architecture/architecture.md`；精确协议、表字段、错误和幂等语义见
`../contracts/`；实现包位于`server/internal/`。
