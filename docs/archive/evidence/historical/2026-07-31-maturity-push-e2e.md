---
status: observed
date: 2026-07-31
scope: natural maturity Push through Zone and Gate
---

# Natural maturity Push E2E

## Claim boundary

This owner-machine run proves that the local four-process stack can:

1. authenticate and register the Gate subscription before the first snapshot;
2. execute `BUY_SEEDS -> PLANT -> APPLY_FERTILIZER`;
3. materialize natural maturity in the online Player Actor scheduler;
4. advance the state from `player_seq=3` to `player_seq=4`;
5. forward one Protobuf event from Zone to Gate over the loopback internal
   endpoint;
6. deliver an unsolicited `PLAYER_STATE_CHANGED` Push with reason `MATURED`,
   no `request_id`, and a `MATURE` Plot patch to the existing WebSocket.

The run used development in-memory state. It does not prove durable Push
delivery, retry after Gate unavailability, cross-Gate routing, production
backpressure, browser rendering, MySQL recovery across the maturity boundary,
or capacity.

A later run of the same wrapper continued after this Push with `HARVEST` and
reached `player_seq=5`; that command is recorded separately in
`2026-07-31-harvest-e2e.md`.

## Reproduction

```powershell
powershell -NoProfile -ExecutionPolicy Bypass `
  -File .\tests\e2e\run-maturity-push.ps1
```

The wrapper starts Login, Zone, Coordinator and Gate, runs the protocol client,
waits up to 90 seconds for natural maturity, and stops all child processes.

## Observed result

```text
BUY_SEEDS player_seq=1 replayed=true
PLANT player_seq=2 plot_state=GROWING replayed=true
APPLY_FERTILIZER player_seq=3 replayed=true
PLAYER_STATE_CHANGED reason=MATURED player_seq=4
  plot_id=1 plot_state=MATURE request_id_absent=true
PASS TestAuthenticatedSnapshot (71.89s)
RESULT maturity_push_e2e=PASS
```

## Supporting automated checks

```text
go test ./...
PASS

npm run build
vue-tsc --noEmit
vite build
PASS
```

`TestSnapshotBuffersPushAndFlushesOnlyNewerVersions` holds a snapshot response
in flight, publishes buffered Pushes at sequence 1 and 2, then returns a
sequence-1 snapshot. The client receives the snapshot first, the duplicate
sequence-1 Push is dropped, and sequence 2 is flushed next.

Player tests also verify one `MATURED` envelope from the online scheduler and
the Zone-side HTTP Protobuf forwarder. The Vue client accepts Push envelopes,
applies contiguous patches, suppresses stale versions, and requests a fresh
snapshot on a version gap; this client path has type-check/build evidence but
not automated browser evidence.
