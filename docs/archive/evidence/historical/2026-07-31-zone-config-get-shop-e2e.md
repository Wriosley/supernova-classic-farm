---
status: measured
date: 2026-07-31
scope: local versioned Zone configuration snapshot and GET_SHOP
---

# Zone configuration and GET_SHOP evidence

## Claim boundary

This evidence proves that the local Zone runtime:

1. owns an immutable, versioned configuration snapshot that can be atomically
   replaced;
2. returns active seed-sale entries through the accepted Protobuf `GET_SHOP`
   response in stable `shop_entry_id` order;
3. does not activate a Player Actor merely to read the global shop;
4. pins one snapshot for `BUY_SEEDS` and derives item identity, authoritative
   unit price and `price_version` from that snapshot;
5. exposes the pinned Zone version on `GET_SHOP` and Player snapshots.

It does not implement a standalone ConfigSvr, remote publication, rollback,
multi-Zone convergence or production configuration distribution. The one local
entry remains a development bootstrap convention:

```text
server_config_version=1
shop_entry_id=5001
item_id=1001
unit_price=2
price_version=8
```

## Verification

Unit tests cover validation, atomic replacement, active-entry filtering,
stable ordering and config-backed purchase behavior:

```powershell
cd server
go test ./internal/player ./internal/gateway ./test/e2e
```

A four-process in-memory protocol run executed:

```text
register -> ws_ticket -> AUTH -> PING
-> GET_SHOP -> GET_PLAYER_SNAPSHOT -> Ticket replay rejection
```

Observed shop result:

```text
GET_SHOP ... config_version=1 shop_entry_id=5001 item_id=1001 unit_price=2 price_version=8
RESULT authenticated_snapshot_e2e=PASS adapter=in-memory-config-shop
```

The later MySQL restart run used the same development snapshot for
`GET_SHOP -> BUY_SEEDS -> PLANT` and recovered the planted checkpoint at
`player_seq=2`.

A later SELL_CROP slice extended the immutable snapshot with a separately
validated crop sell rule. `GET_SHOP` now returns seed entry 5001 at price 2
version 8 and crop entry 5002 at price 5 version 9 in stable order; the newer
evidence is `2026-07-31-sell-crop-e2e.md`.
