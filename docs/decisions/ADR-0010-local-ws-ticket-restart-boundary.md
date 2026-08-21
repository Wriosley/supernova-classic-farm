---
status: accepted
date: 2026-08-03
decision-makers:
  - project-owner
supersedes:
superseded-by:
related:
  - ../contracts/http-api.md
  - ../context/CURRENT.md
  - ../archive/development/plans/2026-08-03-remaining-roadmap-and-iterations.md
---

# ADR-0010: 本地原型冻结短命 WS Ticket / CSRF 重启丢失边界

## Context

MySQL 模式已把账号、HTTP Session 和 Player 检查点做成可重启恢复。LoginSvr 的一次性
`ws_ticket` 与 CSRF 校验记录仍在进程内存里。需要明确：本地原型是否还要把 Ticket
消费也持久化，才能宣称「MySQL 认证路径完成」。

## Owner's initial reasoning

选择 A：承认 Ticket 是短命入场券，Login 重启后旧票作废；玩家用仍有效的 Session
重新领票，或重新登录。把时间留给 MySQL 浏览器演示和性能测量。

## Considered options

### Option A — 冻结短命边界（选用）

- Approach: Ticket / CSRF 记录保持进程内；文档写清重启丢失语义。
- Benefits: 改动小；与「30 秒一次性 Ticket」语义一致；不挡答辩主线。
- Costs: Login 重启后未消费 Ticket 失效；浏览器需重新 bootstrap CSRF / 领票。

### Option B — 持久化 Ticket 消费

- Approach: MySQL 表记录签发与原子消费。
- Benefits: Login 重启后未过期 Ticket 仍可兑换。
- Costs: 额外 DDL、CAS、测试与证据；挤占好友/压测时间。

## Decision

接受 Option A。本地原型不把未消费 `ws_ticket` 与 CSRF 记录写入 MySQL。

## Rationale

Ticket 合约本就是短命、一次性、不进 URL/本地长期存储。账号与农场状态的耐久性
已由 Session/检查点覆盖。为短命入场券再做持久化，收益小于答辩窗口成本。

## Consequences

- 无 `MYSQL_DSN`：Login 重启丢失账号、Session、Ticket、CSRF。
- 有 `MYSQL_DSN`：账号与 Session 可恢复；Ticket/CSRF 内存记录仍随 Login 重启清空。
- 客户端：Session 仍有效时重新 `/v1/csrf` 与 `/v1/ws-tickets`；否则重新登录。
- 「MySQL 认证路径完成」指账号/Session/检查点耐久，不包含 Ticket 跨 Login 重启。

## Validation

- 合约与 `CURRENT.md` 写明该边界。
- 现有 Ticket 单测与 E2E（同进程签发/消费/重放拒绝）保持通过。
- 不增加 Ticket 持久化实现。

## Follow-up

- 下一步：MySQL 浏览器主人环（路线图 R2）。
- 若日后生产要求 Ticket 跨实例一致，另开 ADR，不修改本 ADR 正文。
