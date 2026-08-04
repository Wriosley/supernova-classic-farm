---
status: completed
date: 2026-08-03
---

# WS Ticket 重启边界（R1）

## Human decisions

- 选择 A：短命 Ticket/CSRF，不持久化到 MySQL。

## AI-assisted work

- 写 ADR-0010。
- 更新 `http-api` §13.1（中英）。
- 更新 `CURRENT.md`、路线图 R1→R2。

## Verification

文档与代码结构一致：MySQL Login 存账号/Session，Ticket/CSRF 仍进程内。
无新功能代码变更。
