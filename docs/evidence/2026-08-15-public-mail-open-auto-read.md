---
status: verified-offline
date: 2026-08-15
---

# 公共邮件打开即已读

当前 MailSvr 的公共邮件语义已改为：

- `OpenMailbox` 先返回邮箱列表，并把当前可见的公共未读邮件在响应里标记为已读；
- 对应的 `PlayerMailState.read` 由后台异步回写到 Tcaplus；
- MailSvr 同步刷新 Info 的未读数快照，打开邮箱后公共邮件红点应立即消失。

验证命令：

```text
env GOENV=/tmp/classic-farm-go-env GOCACHE=/tmp/classic-farm-go-cache go test -count=1 ./internal/mail
```

结果：

```text
ok  	github.com/Wriosley/supernova-classic-farm/server/internal/mail	0.021s
```
