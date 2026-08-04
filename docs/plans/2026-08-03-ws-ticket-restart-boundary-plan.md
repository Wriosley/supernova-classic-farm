---
status: completed
date: 2026-08-03
owner: project-owner
related:
  - ../decisions/ADR-0010-local-ws-ticket-restart-boundary.md
  - ../contracts/http-api.md
  - 2026-08-03-remaining-roadmap-and-iterations.md
---

# WS Ticket 重启边界计划（R1）

## Goal

冻结本地原型：未使用 `ws_ticket` / CSRF 记录不随 MySQL Session 持久化。

## Decision

主人选择 A：短命边界，不实现 Ticket 持久化。

## Work done

1. ADR-0010 accepted.
2. `http-api.md` / `.zh-CN.md` 增加 §13.1。
3. `CURRENT.md` 与路线图 R1→R2 更新。

## Non-goals

- Ticket/CSRF MySQL 表
- 生产多实例 Ticket 一致性
