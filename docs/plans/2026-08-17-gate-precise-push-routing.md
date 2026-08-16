---
status: implemented-offline
date: 2026-08-17
owners:
  - project-owner
scope:
  - GateSvr
  - ZoneSvr
---

# Gate precise Push routing plan

## Problem

Three Gate Pods currently share `GATEWAY_ID=local-gateway`. Each Pod owns only
its local WebSocket `PushHub`, while every Zone keeps one gRPC connection to the
ClusterIP `http://gate:8081`. Kubernetes selects one arbitrary Gate for that
long-lived connection. A Push sent to a Gate without the recipient WebSocket
is accepted and silently dropped.

This breaks natural-maturity state Push, friend-farm patches and mail/friend
red dots. A later snapshot/query still reads authoritative state, explaining
why browser refresh appears to repair the UI.

Only ZoneSvr calls `GatePushService`. MailSvr and FriendSvr resolve the owner
Zone first and ask that Zone to deliver red dots; they do not call Gate
directly.

## Target invariant

Every live WebSocket lease stored by the owner Zone binds all of:

```text
player_id
gate_id                 unique Gate process incarnation
gate_endpoint           exact Pod gRPC origin
connection_id
expires_at_ms
owner shard/epoch/route version from the registration request
```

Zone may send Push only to the exact `gate_endpoint` belonging to a currently
live lease whose `gate_id` matches the target Gate server. Gate Push must never
use the load-balanced ClusterIP and must never fall back to another Gate.

## Task 1: extend the internal connection contract

Modify `proto/classicfarm/v1/rpc/runtime.proto`:

- add `gate_endpoint` to `RegisterPlayerConnectionRequest`;
- add `gate_endpoint` to `RefreshPlayerConnectionRequest`;
- add `gate_endpoint` to `EnterFriendFarmRequest` and
  `HeartbeatFriendFarmRequest` so the visitor's own Zone can forward it;
- add `gate_endpoint` to `EnterVisitorRequest` and
  `RefreshVisitorHeartbeatRequest` so the farm Owner Zone can retain the
  cross-Zone visitor's exact Gate binding;
- keep Unregister keyed by `(player_id, gate_id, connection_id)`;
- regenerate Go code with Buf.

No browser protocol or Tcaplus schema changes.

## Task 2: give every Gate an incarnation identity and direct endpoint

Replace the Gate Deployment with a three-replica StatefulSet using
`gate-headless`:

```text
Pod name:          gate-0 / gate-1 / gate-2
gate_id:           metadata.uid
advertised target: http://gate-N.gate-headless.classic-farm.svc.cluster.local:8081
```

The existing `gate` LoadBalancer Service continues selecting all Gate Pods for
browser WebSockets. WebSockets remain pinned naturally by their TCP connection;
no CLB session cookie is required.

Use Pod UID rather than only Pod name so a restarted `gate-0` cannot accept a
Push addressed to the previous `gate-0` process. Add a Gate PDB with
`minAvailable: 2` and keep zero-unavailable rolling updates.

## Task 3: bind endpoint into the Zone lease

Extend `connection.PlayerConnection` with `GateEndpoint` and validate on every
register/refresh:

- endpoint is a pathless internal HTTP origin;
- host is an allowed `gate-*.gate-headless.<namespace>.svc.cluster.local` name;
- port is the configured Gate gRPC port;
- refresh cannot change the endpoint for the same `(gate_id, connection_id)`;
- conflicting live endpoints for one `gate_id` fail closed;
- expired leases are never used for Push.

Extend `visit.VisitRecord` with the same `GateEndpoint`. The Owner Zone cannot
look up a cross-Zone visitor in its local `ConnectionRegistry`, so
`ENTER_FRIEND_FARM` and each visitor heartbeat must carry and refresh the exact
Gate binding all the way through:

```text
visitor WebSocket Gate
→ visitor owner Zone
→ farm owner Zone VisitorRegistry
```

FarmView fan-out reads owner Gate bindings from `ConnectionRegistry` and
visitor Gate bindings from `VisitorRegistry`; both produce the same strict
`(gate_id, gate_endpoint)` target type.

Remove the single `expectedGatewayID=local-gateway` assumption from Zone. Gate
identity is dynamic and authenticated by the existing internal HMAC caller,
then constrained by the Kubernetes DNS validation above.

## Task 4: replace the static forwarder with GatePushRouter

Replace Zone's one `player.GRPCPushForwarder` connection with a bounded client
pool keyed by `(gate_id, gate_endpoint)`.

Routing rules:

- `PLAYER_STATE_CHANGED`: read all live leases for the target player, group by
  Gate identity and send once to each exact Gate (supporting multiple tabs on
  different Gate Pods).
- `FARM_PRESENCE_CHANGED`: same owner-player lease lookup.
- `FARM_VIEW_CHANGED`: use the gate groups already produced from visitor
  leases, then resolve each group to its exact endpoint.
- `RED_DOT_CHANGED`: group recipient leases by exact Gate and send per group.
- no live lease means the player is offline; do not emit a Push.
- endpoint missing, conflicting or invalid means fail closed and log; never
  guess, broadcast or route through the ClusterIP.

Connections are reused per exact endpoint and removed after the last lease
expires or after bounded idle time. A direct Gate failure is not rerouted to a
different Gate, because that Pod cannot own the original WebSocket. Client
reconnect through the CLB creates a new lease and restores routing.

## Task 5: make silent drops observable

Gate `PushHub.Publish` should report whether at least one local subscription
accepted the Push. Gate Push RPC behavior:

- matching Gate incarnation with a local recipient: success;
- matching Gate incarnation but no local recipient: return a typed `NOT_FOUND`
  or an explicit delivered count of zero;
- mismatched `gate_id`: reject;
- Zone logs gate ID, endpoint, player IDs and Push category on zero delivery.

Zero delivery does not trigger cross-Gate retry. Authoritative state remains
recoverable by reconnect snapshot/query.

## Task 6: tests

Add focused tests for:

1. three Gate servers receive only Pushes for leases registered to them;
2. one player connected through two Gate Pods receives one Push on each;
3. another Gate never receives the Push;
4. expired/missing lease sends nothing;
5. endpoint/gate-ID mismatch and endpoint mutation fail closed;
6. Gate restart with a new Pod UID rejects stale-incarnation Push;
7. cross-Zone visitor Enter/Heartbeat preserves the exact Gate endpoint;
8. maturity, farm patch and mail/friend red-dot paths all use the same router;
9. direct Gate failure never falls back to another Gate;
10. WebSocket reconnect registers the new Gate lease and Push resumes;
11. HMAC, race and connection-pool cleanup tests pass.

## Task 7: kind acceptance

1. deploy three Gate Pods and confirm each has a distinct Pod UID/gate ID;
2. connect test players through the CLB and inspect the Zone lease binding;
3. mature a crop and observe immediate state Push without refresh;
4. steal a friend crop and observe immediate farm patch/float text;
5. deliver mail and observe the unread red dot immediately;
6. delete the connected Gate Pod, reconnect through CLB, and repeat all three;
7. confirm no Push is delivered to unrelated Gate Pods.

## Completion gate

- No Zone production Push path dials `http://gate:8081`.
- Every Push is derived from a live `(gate_id, gate_endpoint)` lease.
- Gate incarnation mismatch and missing subscription are observable failures.
- All three reported regressions pass without browser refresh.
- Coordinator, Mail and Friend do not gain direct Gate dependencies.

## Implementation status (2026-08-17)

Tasks 1–5 and the Kubernetes part of Task 2 are implemented. Focused
connection/visitor/fan-out/Zone tests and the full Go compile gate pass, and
the kustomization renders offline. Bounded idle client cleanup and the
three-Pod live acceptance in Tasks 6–7 remain.
They must not be reported as verified until the owner rebuilds Gate/Zone and
runs the cluster checks.
