---
status: implemented-offline
date: 2026-08-16
owners:
  - project-owner
scope:
  - MailSvr
  - FriendSvr
---

# Mail/Friend three-replica gRPC balancing plan

## Goal

Run three MailSvr and three FriendSvr API replicas. Internal callers discover
all Ready Pod addresses through Kubernetes headless Services and let gRPC
`round_robin` distribute calls. Removing one Pod must not stop later calls; an
in-flight `UNAVAILABLE` may be retried once only for explicitly safe methods.

Coordinator remains single-replica and outside this plan.

## Accepted boundaries

- Keep the existing ClusterIP Services for compatibility and health/debug use.
- Add `mail-headless` and `friend-headless`; clients use `dns:///` targets.
- Retain the internal HMAC contract and existing message-size limits.
- Do not transparently retry `ClaimMail`, friend-code creation or friend-code
  redemption.
- Read-only Mail/Friend RPCs and idempotent read acknowledgements may retry one
  `UNAVAILABLE` within the caller's original deadline.
- Mail creation calls are balanced but not transparently retried in this phase;
  existing source/dedup protection remains the application-level retry path.
- FriendLinkSaga's five-second production scan stays disabled. No replacement
  background worker is introduced in this phase.
- No protobuf or Tcaplus schema change is planned.

## Tasks

1. Add a shared gRPC client builder that accepts validated HTTP-origin and
   `dns:///` targets, installs `round_robin`, HMAC interceptors and bounded
   retry method configs.
2. Migrate Gate/Zone/Info callers of FriendSvr and Gate/Zone/Friend callers of
   MailSvr to the builder.
3. Add multi-backend resolver tests, safe-method failover tests, cancellation
   tests and write-dedup concurrency tests.
4. Add the two headless Services, set Mail/Friend Deployments to three replicas,
   use zero-unavailable rolling updates, expose Pod instance IDs, and add
   `minAvailable: 2` PDBs plus soft spreading.
5. Point `FRIEND_RPC_URL` and `MAIL_RPC_URL` at their headless DNS targets.
6. Run focused/race Go tests, Kustomize render/dry-run and a kind live gate when
   the images are deployed.

## Completion gate

- Three Ready endpoints exist for each headless Service.
- Requests are observed across all replicas.
- Removing one replica preserves safe queries through another Ready SubConn.
- Unsafe writes are never transparently retried.
- Friend and Mail authoritative rows remain duplicate-free under concurrent
  calls.
- FriendLinkSaga and MailClaimSaga periodic full-table scans are absent.
