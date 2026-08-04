---
status: measured
date: 2026-07-31
scope: Vue H5 complete owner-loop browser flow
---

# H5 farm loop browser evidence

## Claim boundary

This run proves that the Vue H5 can:

1. register and establish the HTTP/Ticket/WebSocket chain;
2. load `GET_SHOP` buy and sell quotes;
3. apply command response patches for buy, plant, fertilizer, harvest, sale,
   chapter claim and cleanup;
4. display server-authoritative inventory, coins, plot and chapter tasks;
5. receive and apply one unsolicited `MATURED` Push without snapshot-gap
   recovery;
6. finish with `state_version=1/8`, 29 coins, two old seeds, one fertilizer,
   three next-chapter seeds, no crop, chapter two and an empty plot.

The UI uses project-owned placeholder pixel art. It is a demonstrable H5
interaction baseline, not accepted final visual design.

## Reproduction

Start the four local services without `MYSQL_DSN`, then:

```powershell
cd web
npm run dev -- --host 127.0.0.1 --port 5173
```

Open `http://localhost:5173`, register a temporary account, then click:

```text
购买 -> 种植 -> 施肥 -> wait for MATURED -> 收获
-> 出售全部作物 -> 领取奖励 -> 清理地块
```

Static verification:

```powershell
cd web
npm run typecheck
npm run build
```

Both commands passed. The production build emitted approximately 180 KiB of
JavaScript and 9 KiB of CSS before gzip.

## Observed result

The browser inspection at completion returned:

```text
player_id=1
state_version=1/8
coins=29
inventory: seed_1001=2 fertilizer_1=1 crop_1002=0 next_seed_1003=3
plot=EMPTY
chapter=2
push_count=1
last_push_reason=MATURED
gap_recovery_count=0
message="地块清理完成，服务端单玩家闭环已完成。"
```

A device-metrics check at 320 CSS pixels reported:

```text
innerWidth=320
scrollWidth=320
horizontalOverflow=false
farmDashboardWidth=288
```

## Limitations

The run used process-local development adapters. It does not combine browser
interaction with MySQL restart recovery, force a version gap, exercise business
error banners, prove accessibility beyond semantic controls, or validate final
art quality.
