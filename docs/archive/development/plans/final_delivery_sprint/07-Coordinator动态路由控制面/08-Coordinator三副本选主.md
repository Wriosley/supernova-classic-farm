# Three-Replica Coordinator Leader Election Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:test-driven-development`.
> Execute after all ownership mutations are durable/CAS-safe in Phases 02–07.

**Goal:** Run three Coordinator replicas with Kubernetes Lease election so
only the active Leader plans, migrates, fails over and publishes route changes.

**Architecture:** A leadership adapter wraps client-go leader election.
Followers serve health and redirect/identify the Leader but run no mutators.
Leadership loss cancels one generation-scoped context; durable CAS protects
against already in-flight stale work.

## Global Constraints

- This is Kubernetes Lease election, not custom Raft or a 2/3 Route Log.
- Only Leader runs Planner, Scheduler, failover and authoritative Publisher.
- Read-only snapshot may be served by followers only from durable-loaded state;
  Watch clients must connect to Leader.
- Lease loss cancels mutation context immediately.
- Every write still validates expected map/route/epoch/transition versions.

## Task 1: Add leadership abstraction

**Files:**
- Create: `server/internal/coordinator/leadership/leadership.go`
- Create: `server/internal/coordinator/leadership/fake.go`
- Create: `server/internal/coordinator/leadership/leadership_test.go`

```go
type State struct { IsLeader bool; Identity, LeaderIdentity string; Generation uint64 }
type Elector interface {
  Run(context.Context, Callbacks) error
  State() State
}
type Callbacks struct {
  OnStartedLeading func(context.Context, uint64)
  OnStoppedLeading func(uint64)
  OnNewLeader func(string)
}
```

- Generation strictly increases on each local leadership acquisition.
- Stop callback is idempotent and closes the generation context once.
- No package outside wiring receives raw Kubernetes Lease objects.

- [ ] Test acquire/loss/reacquire, cancellation and callback races.

## Task 2: Implement Kubernetes Lease elector

**Files:**
- Create: `server/internal/coordinator/leadership/kubernetes.go`
- Create: `server/internal/coordinator/leadership/kubernetes_test.go`
- Modify: `server/go.mod`, `server/go.sum` using Phase 04 pinned client-go

Configuration:

```text
COORDINATOR_ELECTION_ENABLED=0|1
COORDINATOR_LEASE_NAME=classic-farm-coordinator
COORDINATOR_LEASE_DURATION=15s
COORDINATOR_RENEW_DEADLINE=10s
COORDINATOR_RETRY_PERIOD=2s
POD_NAME / POD_NAMESPACE
```

- Validate `leaseDuration > renewDeadline > retryPeriod`.
- Identity is Pod name plus process UUID.
- Use official leaderelection/resourcelock Lease implementation.
- API errors do not fabricate leadership.

- [ ] Use fake coordination client to test contention, loss and reacquire.

## Task 3: Put every mutator behind one Leader runtime

**Files:**
- Create: `server/cmd/coordinator/leader_runtime.go`
- Create: `server/cmd/coordinator/leader_runtime_test.go`
- Modify: `server/cmd/coordinator/main.go`
- Modify: publisher gRPC server

- On acquire: reload durable Current/Fence/Task/Progress, reconcile, then start
  membership publication, Planner and Scheduler.
- Do not become mutation-ready if reconciliation fails.
- On loss: cancel Planner/Scheduler/probes that can enqueue, reject mutation
  RPCs with `NOT_LEADER`, close Watch sessions with Leader hint.
- Followers expose `/internal/v1/leader` and readiness once election/watch is
  functioning, but never publish route batches.
- Static single-replica compatibility stays behind election disabled.

- [ ] Test follower zero writes, leadership loss during every commit boundary,
  reacquire reload and Watch reconnect.

## Task 4: Add Kubernetes deployment and RBAC

**Files:**
- Modify: `deploy/k8s/coordinator.yaml`
- Modify: `deploy/k8s/coordinator-rbac.yaml`
- Modify: `deploy/k8s/services.yaml`
- Modify: `deploy/k8s/kustomization.yaml`

- Set Coordinator replicas to 3 only in election-enabled rollout.
- Grant get/list/watch/create/update/patch on the one Lease resource name;
  retain Pod/EndpointSlice read permissions and no Secret access.
- Add PodDisruptionBudget `minAvailable: 2`.
- Add anti-affinity/topology spread when the cluster has capacity.
- Service may load-balance unary discovery; SDK follows Leader hint for Watch.

- [ ] Render/dry-run and run `kubectl auth can-i` positive/negative checks.

## Task 5: Split-brain and handoff E2E

- Create `server/test/e2e/coordinator_leader_election_test.go`.
- Start three replicas; verify exactly one Leader.
- Delete Leader during PREPARING, after Fence and after ACTIVE commit.
- Verify old Leader CAS cannot overwrite new state, new Leader resumes Progress,
  SDK reconnects and no duplicate ownership is published.
- Record `docs/evidence/2026-08-12-coordinator-three-replica-election.md`.

## Completion Gate

Exactly one active mutator exists under tested conditions; stale Leader writes
lose CAS; failover resumes durable work. Next: `09-Actor冷Load限流.md`.

