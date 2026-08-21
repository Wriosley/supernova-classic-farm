# Coordinator SDK active route delivery evidence (2026-08-13)

## Scope

Phase 03 was verified against the live `kind-classic-farm` cluster with the
Tcaplus durable RouteStore. Gate, Info, zone-a and zone-b used the shared
Coordinator SDK and authenticated bidirectional Watch stream.

## Offline verification

The required race suite passed:

```text
go test -race -count=1 ./internal/platform/rpcauth \
  ./internal/coordinator/publisher ./internal/coordinatorclient \
  ./internal/gateway ./internal/info ./internal/routing \
  ./cmd/coordinator ./cmd/gate ./cmd/info ./cmd/zone

all ten packages: PASS
```

The final rerun also included `./internal/coordinator/routestore`; all eleven
packages passed under `-race`.

`go test -count=1 ./...` passed every package except the pre-existing
`internal/player.TestApplyMailRewardAllOrNothingAndReplay`. On 2026-08-13 its
fixed prior-day fixture failed checkpoint validation with `checkpoint
timestamps are invalid`. The Coordinator, publisher, SDK, routing, Gate, Info,
Zone and E2E packages all passed in that same run; this unrelated clock-fixture
failure was not changed as part of Phase 03.

Manifest rendering and client dry-run passed for all resources:

```text
kubectl kustomize deploy/k8s >/tmp/classic-farm-rendered.yaml
kubectl apply --dry-run=client -f /tmp/classic-farm-rendered.yaml
```

## Live rollout and recovery

Rollout was performed Coordinator, Gate, Info, zone-a, zone-b. Subscriber
counts advanced 0 -> 1 -> 2 -> 3 -> 4. After a Coordinator restart, clients
reconnected without manual cache repair:

```json
{"active_subscribers":4,"queue_overflows":0,"resyncs":0,"last_published_map_version":4097}
```

The cluster ran in durable SDK mode for well beyond the original 30-second
persisted lease. Watch Ping/Pong freshness kept HTTP-compatible effective
routes usable without durable route/map/lease identity renewal.

An endpoint mismatch from a prior loopback bootstrap was reproduced: durable
routes contained `127.0.0.1:19082/19084`, which is unreachable inside kind.
The explicit non-production reinitialize operation CAS-overwrote all 4096
routes and committed current Kubernetes endpoints. Its first attempt exposed a
120-second startupProbe ceiling; widening only Coordinator's startup window to
one hour allowed the rate-limited initialization to finish. A sample committed
route then returned `http://zone-b:8082`. The reinitialize switch was restored
to false and a normal restart loaded the committed Current.

## Active migration E2E

Command (the HMAC key was injected from the Kubernetes Secret into the test
process and was neither printed nor written):

```text
E2E_RUN=1 E2E_DUAL_ZONE=1 E2E_SUITE=dual-zone \
  go test -count=1 -v ./test/e2e -run TestDualZoneRoutingAndCache
```

Result:

```text
active route delivered after 1 attempt(s)
DUAL_ZONE zone_a_player=137 shard=3114 zone_b_player=136 shard=3549
migrated_player=138 migrated_shard=2679 migrated_epoch=2
snapshot_lookups=12 shard_lookups=5
PASS (5.84s)
```

The test requires four active subscribers before proceeding. A stable ordinary
route hit did not increase the Coordinator single-Shard HTTP counter. During a
migration propagation race, the retained NOT_OWNER/single-Shard fallback may
run; the Watch-delivered ACTIVE route then succeeds within a bounded five-
second window. Publisher unit tests cover queue overflow isolation and forced
full resync for a deliberately slow subscriber.

## Explicit limitations

- zone-a/zone-b membership and endpoints remain static configuration.
- Phase 03 generates no availability transitions.
- There is no automatic rebalance, failover, Coordinator Leader Election or
  replicated control plane.
- Queue capacity, Watch timeout and retry defaults are configuration
  assumptions, not measured production limits.
- Reinitialize is a deliberate maintenance action, forbidden in production;
  normal restart never recomputes committed Current.
