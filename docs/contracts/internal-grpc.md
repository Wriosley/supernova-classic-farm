---
status: accepted
version: 2
date: 2026-08-13
owners:
  - project-owner
related:
  - websocket-protocol.md
  - idempotency-and-errors.md
  - ../archive/development/plans/friend_design_plan/04-gRPC协议与消息详细设计.md
---

# Internal gRPC Contract V1

Game-internal business calls are unary gRPC. Coordinator `WatchRoutes` is a
bidirectional control-plane stream; Login public HTTP and ticket consumption
remain outside this contract. The accepted service
and message definitions are generated from:

- `classicfarm/v1/friend/friend.proto`;
- `classicfarm/v1/rpc/runtime.proto`.

## Identity and authentication

Every call carries `x-cf-caller-service`, `x-cf-timestamp`, `x-cf-nonce`,
`x-cf-body-sha256` and `x-cf-signature`. The SHA-256 input is deterministic
Protobuf request bytes. The HMAC-SHA-256 input is:

```text
caller-service + "\n"
+ full-rpc-method + "\n"
+ timestamp + "\n"
+ nonce + "\n"
+ lowercase-body-sha256
```

The server checks an allowlist for caller service and full method, accepts at
most 30 seconds of clock skew, rejects a reused nonce during that window,
verifies the body hash, and compares signatures in constant time. Authentication
metadata and secrets are never logged. The shared key comes only from a local
environment variable or Kubernetes Secret.

Zone processes authenticate with the bounded caller-service role `zone`.
Logical routing identities (including stable discovered Zone UUIDs) remain in
the request and routing records where ownership is checked; they are never
used as HMAC allowlist entries. During the static-workload transition only,
servers may explicitly enable compatibility callers `zone-local`, `zone-a`
and `zone-b`. Strict mode accepts only `zone`; wildcard and UUID-prefix caller
authorization are forbidden.

For a streaming RPC, the client signs stream establishment once using the same
metadata tuple with SHA-256 of an empty body. The server applies the same
allowlist, clock-window, nonce-replay and signature checks before invoking the
stream handler. This does not provide per-message HMAC: `WatchRoutes` separately
validates its first Subscribe message, subscriber identity and subsequent
Ack/Pong state machine.

## Deadlines

| Call class | Client deadline |
|---|---:|
| Friend code, list and mutual check | 2 seconds |
| Enter, heartbeat, exit and public-farm read | 3 seconds |
| Game command, interaction Saga step and task credit | 5 seconds |
| Gate push | 2 seconds |

Callers set the deadline; servers stop work when context is cancelled where it
is still safe to stop. A deadline after a durable friend-interaction step is an
unknown outcome, not proof of failure.

## Errors and retries

Invalid HMAC is gRPC `UNAUTHENTICATED`; a disallowed caller/method is
`PERMISSION_DENIED`; malformed internal input is `INVALID_ARGUMENT`; stale
ownership is `FAILED_PRECONDITION`; unavailable dependencies use `UNAVAILABLE`.
Stable game-domain failures use `classicfarm.ws.v1.Error` in the response and
the codes in `idempotency-and-errors.md`.

`UNAVAILABLE`, `DEADLINE_EXCEEDED` and `INTERACTION_OUTCOME_UNKNOWN` retries
preserve the request ID and exact payload. `NOT_OWNER`/stale-route handling
refreshes Coordinator state before retry and also preserves the ID.

## Trust boundaries

Gate derives `caller_player_id` from the authenticated WebSocket. The client
cannot choose `gate_id`, route, Zone, epoch, relation ID or interaction ID.
Owner Zone validates the committed route and visit tuple
`(owner_player_id, visitor_player_id, visit_id)`. Friend access is authorized
only by an active `FriendRelation`. Visitor Zone forwards the original
WebSocket request ID to `EnterVisitor`; Owner Zone deduplicates that ID for the
visitor so retrying an unknown enter outcome returns the same visit lease.
