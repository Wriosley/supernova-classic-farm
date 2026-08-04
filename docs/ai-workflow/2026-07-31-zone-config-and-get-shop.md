---
date: 2026-07-31
ai: Cursor coding agent
task: minimum Zone configuration snapshot and GET_SHOP
commit:
---

# AI Work Record: Zone config and GET_SHOP

## Goal and boundary

Replace the hard-coded `BUY_SEEDS` quote with the smallest configuration
mechanism required by the accepted business architecture, and implement the
frozen `GET_SHOP` contract.

This task deliberately did not create a standalone ConfigSvr or a production
publication protocol. It implemented the local Zone side of that future
boundary: immutable versioned snapshots and atomic pointer replacement.

## Changes

- added validated immutable `ConfigSnapshot` and `ShopEntry` types;
- added one explicit local development snapshot;
- made `GET_SHOP` return active entries in stable ID order without activating a
  Player Actor;
- made each request pin one snapshot pointer;
- made `BUY_SEEDS` resolve item, price, enabled state and price version from the
  pinned snapshot;
- made Player snapshots expose the current Zone configuration version;
- extended the multi-process protocol E2E to call `GET_SHOP` and use its quote.

## Verification

- configuration validation and replacement tests pass;
- Player runtime and Gateway tests pass;
- the four-process E2E returned config version `1`, entry `5001`, item `1001`,
  price `2` and price version `8`;
- the complete Go suite and `go vet ./...` are the final verification gate.

## Remaining uncertainty

- no ConfigSvr process, remote publication, multi-Zone convergence or rollback
  mechanism exists;
- the local entry values are implementation conventions, not accepted product
  configuration;
- the H5 does not yet render a shop screen;
- the next business command is `PLANT`.

## Related evidence

- `../evidence/2026-07-31-zone-config-get-shop-e2e.md`
- `../contracts/websocket-protocol.md`
- `../architecture/single-player-vertical-loop-business-architecture.md`
