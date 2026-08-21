---
status: active
date: 2026-08-03
owner: project-owner
related:
  - 2026-08-03-remaining-roadmap-and-iterations.md
  - ../../../archive/evidence/historical/2026-07-31-h5-farm-loop-browser.md
---

# MySQL 浏览器主人环计划（R2）

## Goal

用浏览器在 `MYSQL_DSN` 模式下跑通完整主人环，并验证服务重启后检查点仍在。

## Steps

1. 启动单 Zone MySQL 四进程 + Vite H5。
2. 注册临时账号，完成买种→种→肥→成熟→收→卖→领奖→清理。
3. 停栈再起，同一账号登录，确认 `player_seq=8` 与最终资源。
4. 写 evidence，更新 CURRENT / 路线图。

## Non-goals

- 双 Zone 迁移演示
- 性能压测
- Ticket 持久化
