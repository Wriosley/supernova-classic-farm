#!/usr/bin/env bash
set -euo pipefail

namespace="${NAMESPACE:-classic-farm}"
deployment="${COORDINATOR_DEPLOYMENT:-coordinator}"
first_shard="${FIRST_SHARD:-20}"
proxy_base="/api/v1/namespaces/${namespace}/services/http:coordinator:8083/proxy"
boundaries=(
  SOURCE_DRAINING SOURCE_FLUSHED ROUTE_PREPARING FENCE_ADVANCED
  TARGET_LOADING TARGET_READY ROUTE_ACTIVE PROGRESS_COMPLETED TASK_COMPLETED
)
if [[ -n "${BOUNDARY:-}" ]]; then
  boundaries=("${BOUNDARY}")
fi

cleanup() {
  kubectl -n "${namespace}" set env "deployment/${deployment}" \
    COORDINATOR_MIGRATION_WORKER_ENABLED=0 >/dev/null || true
  kubectl -n "${namespace}" set env "deployment/${deployment}" \
    COORDINATOR_MIGRATION_CRASH_AFTER- \
    COORDINATOR_MIGRATION_CRASH_SHARD_ID- >/dev/null || true
}
trap cleanup EXIT

field() {
  sed -n 's/.*"'"$2"'":"\([^"]*\)".*/\1/p' <<<"$1"
}

for index in "${!boundaries[@]}"; do
  boundary="${boundaries[$index]}"
  shard_id=$((first_shard + index))
  route="$(kubectl get --raw "${proxy_base}/internal/v1/routes/${shard_id}")"
  source="$(field "${route}" owner_zone_id)"
  case "${source}" in
    zone-a) target=zone-b ;;
    zone-b) target=zone-a ;;
    *) echo "unsupported source ${source} for Shard ${shard_id}" >&2; exit 1 ;;
  esac

  kubectl -n "${namespace}" set env "deployment/${deployment}" \
    COORDINATOR_MIGRATION_WORKER_ENABLED=1 \
    "COORDINATOR_MIGRATION_CRASH_AFTER=${boundary}" \
    "COORDINATOR_MIGRATION_CRASH_SHARD_ID=${shard_id}" >/dev/null
  kubectl -n "${namespace}" rollout status "deployment/${deployment}" --timeout=180s >/dev/null
  pod="$(kubectl -n "${namespace}" get pod -l app.kubernetes.io/name=coordinator -o jsonpath='{.items[0].metadata.name}')"
  before="$(kubectl -n "${namespace}" get pod "${pod}" -o jsonpath='{.status.containerStatuses[0].restartCount}')"

  # The POST may lose its response because the selected persisted boundary can
  # be reached immediately. Task recovery, not the HTTP response, is the oracle.
  kubectl -n "${namespace}" port-forward "service/${deployment}" 64884:8083 >/tmp/classic-farm-migration-pf.log 2>&1 &
  forward_pid=$!
  forward_ready=false
  for _ in $(seq 1 100); do
    if curl -fsS --max-time 1 "http://127.0.0.1:64884/internal/v1/routes/${shard_id}" >/dev/null 2>&1; then
      forward_ready=true
      break
    fi
    sleep 0.1
  done
  if [[ "${forward_ready}" != true ]]; then
    echo "port-forward did not become ready for ${boundary}" >&2
    exit 1
  fi
  curl -sS --max-time 15 -H 'Content-Type: application/json' \
    -d "{\"target_zone_id\":\"${target}\"}" \
    "http://127.0.0.1:64884/internal/v1/shards/${shard_id}/move" >/tmp/classic-farm-migration-response.json || true
  kill "${forward_pid}" >/dev/null 2>&1 || true

  recovered=false
  for _ in $(seq 1 180); do
    current_pod="$(kubectl -n "${namespace}" get pod -l app.kubernetes.io/name=coordinator -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
    if [[ "${current_pod}" == "${pod}" ]]; then
      restarts="$(kubectl -n "${namespace}" get pod "${pod}" -o jsonpath='{.status.containerStatuses[0].restartCount}' 2>/dev/null || true)"
    else
      restarts=""
    fi
    route="$(kubectl get --raw "${proxy_base}/internal/v1/routes/${shard_id}" 2>/dev/null || true)"
    owner="$(field "${route}" owner_zone_id)"
    state="$(field "${route}" state)"
    if [[ "${restarts}" =~ ^[0-9]+$ ]] && ((restarts > before)) && [[ "${owner}" == "${target}" && "${state}" == "ACTIVE" ]]; then
      recovered=true
      echo "PASS boundary=${boundary} shard=${shard_id} source=${source} target=${target} restarts=${restarts}"
      break
    fi
    sleep 1
  done
  if [[ "${recovered}" != true ]]; then
    echo "FAIL boundary=${boundary} shard=${shard_id} route=${route}" >&2
    kubectl -n "${namespace}" logs "${pod}" --previous --tail=80 >&2 || true
    exit 1
  fi
done
