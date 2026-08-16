---
status: verified-offline
date: 2026-08-16
scope: MailSvr/FriendSvr client-side balancing and Kubernetes manifests
---

# Mail/Friend three-replica gRPC balancing

## Implemented

- Internal endpoints accept both historical HTTP origins and validated
  `dns:///host:port` resolver targets.
- Friend callers in Gate, Zone and Info, plus Mail callers in Gate, Zone and
  Friend, install gRPC `round_robin` through the shared `rpcnet` helper.
- Only Friend/Mail reads and idempotent acknowledgement methods retry one
  transport `UNAVAILABLE`. Friend-code writes, Mail creation and `ClaimMail`
  are balanced but never transparently retried.
- The real internal HMAC unary interceptor remains installed. The failover test
  exercises two independent server replay caches, proving the same signed call
  can retry on another replica without removing authentication.
- Kubernetes now declares three Mail and three Friend replicas, Ready-only
  headless Services, zero-unavailable rolling updates, soft host spreading and
  `minAvailable: 2` PDBs.
- FriendLinkSaga's five-second scan remains disabled; MailClaimSaga scanning was
  already disabled.

No protobuf or Tcaplus schema changed.

## Verification

Repeated resolver/failover race gate:

```bash
GOCACHE=/tmp/classic-farm-go-cache go test -race -count=5 ./internal/platform/rpcnet
```

Result: pass. Calls reached both SubConns; an injected `UNAVAILABLE` retried a
safe method through the other HMAC-authenticated server; after resolver removal
the removed backend received no new calls.

Focused race regression:

```bash
GOCACHE=/tmp/classic-farm-go-cache go test -race -count=1 \
  ./internal/platform/rpcnet ./internal/platform/rpcauth ./internal/gateway \
  ./internal/visit ./internal/info ./internal/outbox ./internal/friend \
  ./internal/mail ./cmd/gate ./cmd/zone ./cmd/info ./cmd/friend ./cmd/mail
```

Result: all tested packages passed; the three command packages without tests
compiled successfully.

Manifest gate:

```bash
kubectl kustomize deploy/k8s > /tmp/classic-farm-rendered.yaml
kubectl apply --dry-run=client -f /tmp/classic-farm-rendered.yaml
```

Result: render and API-schema dry-run passed, including both new headless
Services and PDBs.

## Remaining live gate

Images were not built or loaded and the current kind workloads were not
mutated in this change. After manual deployment, verify three Ready Pods and
three EndpointSlice addresses per service, then delete one replica during
repeated safe queries and record the observed distribution/failover.
