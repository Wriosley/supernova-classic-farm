---
status: verified-offline
date: 2026-08-15
---

# 好友按钮红点与农场红点分离

好友导航按钮现在只表示“有未查看的好友农场成熟通知”，进入好友面板后会
立即清除；好友列表里每个农场条目的成熟红点继续保留，进入具体农场不会
把该条目的红点抹掉。

验证命令：

```text
cd web && npm test -- --run src/__tests__/game-shell.spec.ts
```

结果：

```text
Test Files  1 passed (1)
Tests  9 passed (9)
```
