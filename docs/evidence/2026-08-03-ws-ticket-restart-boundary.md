---
status: verified
date: 2026-08-03
related:
  - ../decisions/ADR-0010-local-ws-ticket-restart-boundary.md
  - ../plans/2026-08-03-ws-ticket-restart-boundary-plan.md
  - ../contracts/http-api.md
---

# WS Ticket 重启边界证据

## Scope

记录主人接受的本地认证耐久边界：未使用 Ticket / CSRF 保持进程内。

## Decision evidence

- ADR-0010 status `accepted`。
- `http-api.md` §13.1 与中文镜像同步。
- 实现现状：`NewMySQLStore` 复用内存 `Store` 的 Ticket/CSRF 映射；账号与 Session 走 MySQL。

## Code check

```text
server/internal/auth/mysql_store.go  NewMySQLStore → NewStore() + mysql backend
server/internal/auth/store.go        IssueTicket / NewCSRF / tickets map
```

## Limitations

未做「杀 Login 进程后旧 Ticket 必失败」的独立活测；边界以实现结构 + 合约/ADR 为准。
现有同进程 Ticket 签发/消费/重放拒绝测试仍适用。
