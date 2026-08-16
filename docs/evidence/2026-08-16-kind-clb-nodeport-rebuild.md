# kind CLB NodePort rebuild evidence

Date: 2026-08-16

## Goal

Expose Login and the three-replica Gate Service on stable CVM ports so a
manually managed Tencent Cloud CLB can reach kind without a long-running
`kubectl port-forward`.

## Configuration

- `deploy/kind-config.yaml` maps CVM `0.0.0.0:31238/32591` to the kind
  control-plane container's same ports.
- `deploy/k8s/services.yaml` fixes Login `nodePort: 31238` and Gate
  `nodePort: 32591`.
- Intended CLB bindings are `:8080 -> CVM:31238` and
  `:8081 -> CVM:32591`.

## Observed result

- All seven backend images built from the current workspace and loaded.
- Existing Tcaplus and internal-RPC Secrets were restored without writing
  their values to the repository.
- Eight Deployments were Available and `zone-pool` completed at four Ready
  replicas.
- `curl http://127.0.0.1:31238/readyz` returned `{"status":"ready"}`.
- `curl http://127.0.0.1:32591/readyz` returned `{"status":"ready"}`.
- Gate Service exposed three endpoints:
  `10.244.0.10:8081`, `10.244.0.7:8081`, `10.244.0.8:8081`.
- Docker published both NodePorts on host `0.0.0.0`.

## Remaining external validation

The CLB backend was still configured for the temporary port-forward ports at
handoff. After changing it to `31238/32591`, validate both CLB `/readyz`
endpoints and one browser login/WebSocket connection from another VPC-reachable
machine. This evidence does not yet claim that external check passed.
