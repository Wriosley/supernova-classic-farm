#!/usr/bin/env bash
# Rebuild classic-farm images, load into kind, apply manifests, and rollout.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

CLUSTER_NAME="${CLUSTER_NAME:-classic-farm}"
NAMESPACE="${NAMESPACE:-classic-farm}"
SERVICES=(login gate coordinator zone friend info mail)

echo "==> docker/kind preflight"
docker info >/dev/null
kind get clusters | grep -qx "${CLUSTER_NAME}"
kubectl config use-context "kind-${CLUSTER_NAME}" >/dev/null
kubectl cluster-info >/dev/null

echo "==> build + kind load"
for service in "${SERVICES[@]}"; do
  echo "---- ${service}"
  docker build --build-arg "SERVICE=${service}" \
    -t "classic-farm/${service}:dev" .
  kind load docker-image "classic-farm/${service}:dev" \
    --name "${CLUSTER_NAME}"
done

echo "==> kubectl apply -k deploy/k8s"
kubectl apply -k deploy/k8s

echo "==> rollout restart"
kubectl -n "${NAMESPACE}" rollout restart \
  deploy/coordinator deploy/login \
  deploy/friend deploy/info deploy/mail
kubectl -n "${NAMESPACE}" rollout restart statefulset/gate
kubectl -n "${NAMESPACE}" rollout restart statefulset/zone-pool

for d in coordinator login friend info mail; do
  echo "---- wait ${d}"
  kubectl -n "${NAMESPACE}" rollout status "deploy/${d}" --timeout=300s
done
echo "---- wait gate"
kubectl -n "${NAMESPACE}" rollout status statefulset/gate --timeout=300s
echo "---- wait zone-pool"
kubectl -n "${NAMESPACE}" rollout status statefulset/zone-pool --timeout=300s

echo "==> pods"
kubectl -n "${NAMESPACE}" get pods -o wide
echo "DONE"
