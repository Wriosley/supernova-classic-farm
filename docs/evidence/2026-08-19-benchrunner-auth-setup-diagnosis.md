---
status: observed
date: 2026-08-19
---

# Benchrunner 50-connection setup diagnosis

## Scope

Diagnose why the `snapshot` benchmark stopped while creating 50 clients before
its measurement window. This is setup-path evidence, not a Gate or Zone
capacity result.

## Observations

- Existing `gogc400_snap10_50` artifacts recorded 10 closed-loop clients at
  about 3131.82 successful snapshot responses/s with zero measured errors.
  The following 50-client step stopped during `authenticateAll`, before a
  50-client measurement result was written.
- The live Login Service had four Ready Pods but `sessionAffinity: None`.
  CSRF records are process-local, so one client's CSRF issuance and following
  mutation can reach different Login Pods.
- A short 50-client reproduction with `connect-workers=10`, zero warmup and a
  two-second requested measurement stopped at setup with:
  `register: POST /v1/auth/register: HTTP 403, code 203 (CSRF_REJECTED)`.
- HTTP code 501 in this project means `INTERNAL_ERROR`; CSRF rejection is code
  203 with HTTP 403. The earlier 500/501 therefore was not itself proof of a
  CSRF mismatch.
- The deployed Gate StatefulSet requested three replicas, but `gate-2` was in
  `CrashLoopBackOff`. Its previous log reported
  `invalid GATE_PORT "tcp://10.96.221.116:8081"`. Kubernetes injected the
  Service link variable because the deployed Pod template did not override
  `GATE_PORT`; the current workspace manifest already declares
  `GATE_PORT=""`.
- Only `gate-0` and `gate-1` were Ready Gate Service endpoints during the
  inspection.

## Local diagnostic change

Benchrunner HTTP status errors now include the safe request stage, method,
path and server `X-Request-ID`. They do not include passwords, cookies, CSRF
tokens or WS tickets.

Benchrunner also accepts comma-separated `-login-url` endpoints. It selects an
endpoint by a stable account-name hash before creating that account's cookie
jar. Consequently every HTTP step for one virtual user stays on one Login Pod,
while different users distribute across the supplied Login Pods. The original
single-URL form remains compatible. This avoids concentrating a single-source
load generator on one Login through Service `ClientIP` affinity.

Validation:

```text
go test ./cmd/benchrunner    PASS
go build ./cmd/benchrunner   PASS (sandbox emitted a non-fatal cache trim warning)
git diff --check             PASS
```

## Interpretation and limits

The observed 50-client stop is an authentication setup failure, not evidence
that Gate or Zone saturates at 50 concurrent WebSocket clients. Login setup
must be stabilized or separated from the measured window. Gate and Zone
bottlenecks require all intended Gate replicas Ready, connections distributed
across them, and per-Gate/per-Zone traffic plus resource measurements during a
successful steady-state window.

## Multi-Login follow-up

Benchrunner was extended to accept comma-separated Login URLs and select one
by stable account-name hash. Four direct Login Pod port-forwards eliminated the
cross-Pod CSRF failure. A fresh-account attempt with 16 setup workers then
reproduced the original failure precisely at
`POST /v1/auth/register -> HTTP 500 / INTERNAL_ERROR(501)`.

Code inspection found that all Login replicas allocate new player IDs through
one Tcaplus `PlayerIdCounter` row with at most eight immediate CAS attempts.
Concurrent new-account registration can exhaust those attempts. Reusing the
partially created account cohort and lowering setup concurrency to four allowed
all 50 clients to connect in 9.581 seconds. The following five-second
closed-loop snapshot window observed 22,465 successes, zero errors, 4482.15
QPS, P50 10.979 ms, P95 19.723 ms and P99 25.421 ms.

This short run proves that the earlier stop was not a 50-client Gate/Zone
capacity boundary. It is not a formal multi-Gate capacity result: the Gate URL
used a Service port-forward, which commonly selects one backing Pod, the run
was only five seconds, and one of three Gate Pods was not Ready.

The organized performance plan and raw short-run artifacts are stored outside
the repository under `/data/workspace/yace/` at the owner's request.
