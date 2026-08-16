---
status: verified-offline
date: 2026-08-16
scope: FriendSvr startup behavior
---

# Friend Saga scan disabled

FriendSvr no longer starts the five-second full-table `FriendLinkSaga`
reconciler. The online friend-code redemption path and its durable Saga rows
remain unchanged, but an interrupted multi-row friend link is not recovered
automatically after process failure. The accepted product behavior is that the
player retries adding the friend.

The Reconciler implementation and focused unit tests remain available as
historical/manual recovery code; no production startup path invokes
`ReconcileDue`.

Verification:

```bash
GOCACHE=/tmp/classic-farm-go-cache go test -count=1 ./cmd/friend ./internal/friend
```

No Tcaplus schema or table change is required.
