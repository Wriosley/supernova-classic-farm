---
status: implemented-offline-verified
date: 2026-08-15
---

# 离线农场红点与访客提醒实施计划

## 目标

- 在线农场成熟依靠 Zone 主动推送，不依赖 InfoSvr 成熟时间查询。
- Actor 仅在回收前发布离线农场摘要。
- 离线好友列表通过 InfoSvr 的摘要版本和访问者已看版本决定红点。
- 农场主离线期间只记录去重的访问者，不记录访问次数；登录时提醒一次。

## 数据边界

InfoSvr 继续使用可丢失内存投影，不新增 Tcaplus 表。离线农场版本绑定
`owner_epoch + checkpoint_revision`。按 `(owner, visitor)` 保存已看 revision；
按 owner 保存最多 50 个去重访客及单调 visitor version。版本 ACK 只删除该
版本之前的访客，保留查询之后的新访问。

## 调用链

1. Actor 回收完成最终结算和 SaveCAS 后发布 FarmQuickInfo。
2. 成功 ENTER 后访客 Zone 异步 RecordOfflineFarmVisit；InfoSvr 根据 presence
   判断农场主是否离线，并同时更新已看 revision。
3. FriendSvr ListFriends 只把离线好友的 `show_offline_farm_red_dot` 返回为红点；
   在线好友完全依靠 Zone 主动成熟推送。
4. 登录完成后 H5 经 Gate/FriendSvr 查询离线访客，显示名单并按版本 ACK。

## 完成标准

- 在线农场不通过 InfoSvr 查询产生红点。
- 相同离线摘要访问后不重复显示，新 revision 可以再次显示。
- 重复访问者只出现一次，旧 ACK 不删除新访客。
- InfoSvr 故障不阻塞访问、登录或 Actor 回收。
- 后端 race 与前端 typecheck/test/build 通过。

