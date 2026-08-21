---
status: verified
date: 2026-08-16
scope: two-chapter task rewards and H5 task presentation
---

# Two-chapter task rewards

## Implemented behavior

- Development config version is now 2. New players receive chapter 1 reward:
  10 coins, 1 basic fertilizer and 3 pumpkin seeds.
- Chapter 2 has named friend tasks: add one friend, steal one friend crop and
  apply one pest. Its reward is 10 coins, 5 basic fertilizers and 10 watermelon
  seeds.
- Chapter 2 is terminal. Claiming it grants the reward once and retains chapter
  2 as `CLAIMED`, so the H5 can display the completed terminal state.
- The H5 pages between chapter 1 and 2, shows exact reward text, and displays
  “暂时没有更多任务了” after the terminal claim.
- A claimable chapter raises a browser-local task navigation red dot. Opening
  the task drawer acknowledges that player/chapter reminder without changing
  authoritative task or claim state.

This change intentionally has no migration or backfill for existing players;
the verified boundary is newly created players using config version 2.

## Verification

From `server/`:

```bash
GOCACHE=/tmp/classic-farm-go-cache go test -count=1 ./internal/player
```

Result: pass (`0.190s`). This required a normal local loopback socket because
the package includes a gRPC push-forwarder listener test.

From `web/`:

```bash
npm test
npm run build
```

Result: 6 test files and 34 tests passed; Vue type checking and the Vite
production build passed.

## Not verified here

- No live kind/Tcaplus player was created and advanced through both chapters.
- Existing checkpoints with config version 1 were not migrated and are outside
  this change's supported boundary.
