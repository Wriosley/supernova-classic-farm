# AI Work Record: maturity Push and gap recovery

## Goal

Continue after fertilizer by delivering an online natural-maturity transition
from the Zone Player Actor through Gate to the subscribed WebSocket, while
preserving the frozen snapshot-to-Push race rules.

## Boundary

The implementation is a local V3 mechanism slice, not a production Push
service. Zone-to-Gate transport is loopback HTTP without durable retry,
cross-Gate routing or production backpressure.

## Changes

- changed maturity materialization to retain one event per plot transition,
  with the exact `player_seq` assigned by the Actor;
- created `PLAYER_STATE_CHANGED/MATURED` Push envelopes without a
  `request_id`;
- added a loopback-only Protobuf callback from Zone to Gate;
- added authenticated per-player Gate subscriptions;
- registered and buffered the subscription before requesting a snapshot;
- sent the snapshot first, discarded buffered versions not newer than it and
  flushed newer Pushes in state-version order;
- taught the H5 WebSocket client to accept Push frames, apply contiguous
  inventory/plot/coin/chapter patches, ignore stale versions and request a
  fresh snapshot on a gap;
- added a dedicated live maturity-Push wrapper and focused unit tests.

Offline maturities materialized during Actor activation remain folded into the
first snapshot and do not emit intermediate Pushes.

## Verification

- `go test ./...`: pass.
- `npm run build`: Vue type-check and production build pass.
- focused Gate test proves the snapshot-buffer order and duplicate filter.
- focused Player tests prove scheduler event construction and HTTP forwarding.
- `tests/e2e/run-maturity-push.ps1`: pass in 72 seconds; the existing
  connection received `MATURED` at `player_seq=4` after buy, plant and
  fertilizer.

Detailed observed output and limitations are in
`../../../archive/evidence/historical/2026-07-31-maturity-push-e2e.md`.

## Remaining uncertainty

- The H5 gap path is compiled but has no automated browser test.
- Zone does not retain or retry a Push when Gate is unavailable.
- Multi-Gate subscription routing, bounded production queues and disconnect
  backpressure remain unimplemented.
- MySQL recovery across fertilizer expiry and maturity remains unverified.
