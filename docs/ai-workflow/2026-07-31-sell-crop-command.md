# AI Work Record: SELL_CROP command

## Goal

Continue after harvest by selling warehouse crops under an authoritative
versioned quote and completing the first chapter's fifth task.

## Changes

- added immutable sell rules to the Zone configuration snapshot;
- exposed the development crop sell quote through `GET_SHOP`;
- implemented explicit quantity and `sell_all` request validation;
- validated sellability, `price_version`, inventory and integer overflow before
  mutation;
- updated crop inventory, coins and sell-task progress atomically;
- retained the resolved sell-all quantity and response for same-ID replay;
- moved the chapter from `IN_PROGRESS` to `CLAIMABLE` when all tasks complete;
- fixed checkpoint conversion so `CLAIMABLE` and `CLAIMED` are not forced back
  to `IN_PROGRESS`;
- extended the protocol and MySQL restart wrappers through sale.

## Verification

- focused configuration, quantity, sell-all, replay, failure-atomicity and
  checkpoint-round-trip tests pass;
- `go test ./...` and `go vet ./...` pass;
- a live in-memory four-process run sold all three crop items at unit price 5,
  reached 19 coins and `CLAIMABLE` at `player_seq=6`.
- the owner-run MySQL extension completed the same flow, stopped all four
  services, and recovered `player_seq=6`, 19 coins, two seeds, no crops,
  `NEED_CLEANUP` and `CLAIMABLE` from a fresh stack.

Observed output is in `../evidence/2026-07-31-sell-crop-e2e.md`.

## Remaining uncertainty

Reward claim and plot cleanup remain unimplemented.
