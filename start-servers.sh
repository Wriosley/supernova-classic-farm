#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
server_root="${repo_root}/server"
env_file="${repo_root}/.env"
dual_zone=false
mysql_mode=false
tcaplus_mode=false
run_seconds=0

usage() {
    cat <<'EOF'
Usage: ./start-servers.sh [--dual-zone] [--mysql|--tcaplus] [--run-seconds N]

  --dual-zone      Start Zone A and Zone B.
  --mysql          Use MySQL persistence. MYSQL_DSN is preferred; otherwise
                   local MYSQL_* fields are used to construct it.
  --tcaplus        Use Tcaplus for auth, checkpoints, fences, migration
                   progress, Outbox and friend tables. Also starts FriendSvr
                   (FriendSvr has no in-memory mode). Requires --dual-zone
                   and TCAPLUS_*.
  --run-seconds N  Stop automatically after N seconds. Zero waits for Ctrl+C.

This script is for the Linux loopback baseline. It does not deploy Kubernetes.
EOF
}

while (( $# > 0 )); do
    case "$1" in
        --dual-zone)
            dual_zone=true
            shift
            ;;
        --mysql)
            mysql_mode=true
            shift
            ;;
        --tcaplus)
            tcaplus_mode=true
            shift
            ;;
        --run-seconds)
            if (( $# < 2 )) || [[ ! "$2" =~ ^[0-9]+$ ]]; then
                echo "--run-seconds requires a non-negative integer" >&2
                exit 2
            fi
            run_seconds="$2"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "Unknown argument: $1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

require_command() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "$1 was not found on PATH" >&2
        exit 1
    fi
}

load_dotenv() {
    [[ -f "${env_file}" ]] || return 0
    while IFS= read -r line || [[ -n "${line}" ]]; do
        line="${line%$'\r'}"
        [[ -n "${line}" && "${line}" != \#* && "${line}" == *=* ]] || continue
        key="${line%%=*}"
        value="${line#*=}"
        key="${key#"${key%%[![:space:]]*}"}"
        key="${key%"${key##*[![:space:]]}"}"
        [[ "${key}" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || continue
        if [[ "${value}" == \"*\" && "${value}" == *\" ]] ||
           [[ "${value}" == \'*\' && "${value}" == *\' ]]; then
            value="${value:1:${#value}-2}"
        fi
        if [[ -z "${!key+x}" ]]; then
            export "${key}=${value}"
        fi
    done < "${env_file}"
}

port_is_open() {
    local port="$1"
    (echo > "/dev/tcp/127.0.0.1/${port}") >/dev/null 2>&1
}

wait_ready() {
    local name="$1"
    local url="$2"
    local pid="$3"
    local deadline=$((SECONDS + 30))
    while (( SECONDS < deadline )); do
        if ! kill -0 "${pid}" >/dev/null 2>&1; then
            echo "${name} exited during startup; log follows:" >&2
            if [[ -f "${run_root}/${name}.log" ]]; then
                sed -n '1,200p' "${run_root}/${name}.log" >&2
            fi
            return 1
        fi
        if curl --fail --silent --show-error --max-time 2 "${url}" >/dev/null 2>&1; then
            echo "[ready] ${name} -> ${url}"
            return 0
        fi
        sleep 0.2
    done
    echo "${name} did not become ready within 30 seconds" >&2
    return 1
}

names=()
pids=()
stopping=false

stop_services() {
    local index
    if [[ "${stopping}" == true ]]; then
        return
    fi
    stopping=true
    trap - INT TERM EXIT
    for ((index=${#pids[@]}-1; index>=0; index--)); do
        if kill -0 "${pids[index]}" >/dev/null 2>&1; then
            kill -TERM "${pids[index]}" >/dev/null 2>&1 || true
        fi
    done
    local deadline=$((SECONDS + 10))
    while (( SECONDS < deadline )); do
        local alive=false
        for pid in "${pids[@]}"; do
            if kill -0 "${pid}" >/dev/null 2>&1; then
                alive=true
                break
            fi
        done
        [[ "${alive}" == false ]] && break
        sleep 0.2
    done
    for pid in "${pids[@]}"; do
        if kill -0 "${pid}" >/dev/null 2>&1; then
            kill -KILL "${pid}" >/dev/null 2>&1 || true
        fi
        wait "${pid}" 2>/dev/null || true
    done
    echo "All backend services stopped."
}

start_service() {
    local name="$1"
    local binary="$2"
    shift 2
    echo "[start] ${name}"
    env "$@" "${binary}" >"${run_root}/${name}.log" 2>&1 &
    service_pid=$!
    names+=("${name}")
    pids+=("${service_pid}")
}

require_command go
require_command curl
load_dotenv

if [[ "${mysql_mode}" == true && "${tcaplus_mode}" == true ]]; then
    echo "--mysql and --tcaplus are mutually exclusive" >&2
    exit 2
fi
if [[ "${tcaplus_mode}" == true && "${dual_zone}" != true ]]; then
    echo "--tcaplus currently requires --dual-zone" >&2
    exit 2
fi

login_port="${LOGIN_PORT:-8080}"
gate_port="${GATE_PORT:-8081}"
zone_port="${ZONE_PORT:-8082}"
coordinator_port="${COORDINATOR_PORT:-8083}"
zone_b_port="${ZONE_B_PORT:-8084}"
friend_port="${FRIEND_PORT:-8085}"

for port in "${login_port}" "${gate_port}" "${zone_port}" \
    "${coordinator_port}" "${zone_b_port}" "${friend_port}"; do
    if [[ ! "${port}" =~ ^[0-9]+$ ]] || (( port < 1 || port > 65535 )); then
        echo "Invalid backend port: ${port}" >&2
        exit 1
    fi
done

ports=("${login_port}" "${gate_port}" "${zone_port}" "${coordinator_port}")
if [[ "${dual_zone}" == true ]]; then
    ports+=("${zone_b_port}")
fi
if [[ "${tcaplus_mode}" == true ]]; then
    ports+=("${friend_port}")
fi
for port in "${ports[@]}"; do
    if port_is_open "${port}"; then
        echo "Port ${port} is already in use; stop the existing process first" >&2
        exit 1
    fi
done

if [[ "${mysql_mode}" == true ]]; then
    if [[ -z "${MYSQL_DSN:-}" ]]; then
        mysql_host="${MYSQL_HOST:-127.0.0.1}"
        mysql_port="${MYSQL_PORT:-3306}"
        mysql_database="${MYSQL_DATABASE:-classicfarm}"
        mysql_user="${MYSQL_USER:-classicfarm}"
        mysql_password="${MYSQL_PASSWORD:-classicfarm}"
        if [[ "${mysql_password}" == "请在本地填写" ]]; then
            echo "Set MYSQL_PASSWORD or MYSQL_DSN before using --mysql" >&2
            exit 1
        fi
        MYSQL_DSN="${mysql_user}:${mysql_password}@tcp(${mysql_host}:${mysql_port})/${mysql_database}?parseTime=true"
        export MYSQL_DSN
    fi
fi
if [[ "${tcaplus_mode}" == true ]]; then
    if [[ -n "${MYSQL_DSN:-}" ]]; then
        echo "MYSQL_DSN must be unset in pure Tcaplus mode" >&2
        exit 1
    fi
    required_tcaplus=(
        TCAPLUS_APP_ID TCAPLUS_ZONE_ID TCAPLUS_DIR_URL TCAPLUS_SIGNATURE
    )
    for key in "${required_tcaplus[@]}"; do
        if [[ -z "${!key:-}" ]]; then
            echo "${key} is required for --tcaplus" >&2
            exit 1
        fi
    done
    export STORAGE_MODE=tcaplus
    export TCAPLUS_CHECKPOINT_TABLE="${TCAPLUS_CHECKPOINT_TABLE:-PlayerCheckpoint}"
    export TCAPLUS_PLAYER_ID_COUNTER_TABLE="${TCAPLUS_PLAYER_ID_COUNTER_TABLE:-PlayerIdCounter}"
    export TCAPLUS_ACCOUNT_BY_NAME_TABLE="${TCAPLUS_ACCOUNT_BY_NAME_TABLE:-AccountByName}"
    export TCAPLUS_ACCOUNT_BY_PLAYER_TABLE="${TCAPLUS_ACCOUNT_BY_PLAYER_TABLE:-AccountByPlayer}"
    export TCAPLUS_SESSION_TABLE="${TCAPLUS_SESSION_TABLE:-Session}"
    export TCAPLUS_FENCE_TABLE="${TCAPLUS_FENCE_TABLE:-ShardFence}"
    export TCAPLUS_MIGRATION_TABLE="${TCAPLUS_MIGRATION_TABLE:-MigrationProgress}"
    export TCAPLUS_OUTBOX_TABLE="${TCAPLUS_OUTBOX_TABLE:-PlayerOutbox}"
    export TCAPLUS_FRIEND_CODE_CURRENT_TABLE="${TCAPLUS_FRIEND_CODE_CURRENT_TABLE:-FriendCodeCurrent}"
    export TCAPLUS_FRIEND_CODE_LOOKUP_TABLE="${TCAPLUS_FRIEND_CODE_LOOKUP_TABLE:-FriendCodeLookup}"
    export TCAPLUS_FRIEND_RELATION_TABLE="${TCAPLUS_FRIEND_RELATION_TABLE:-FriendRelation}"
    export TCAPLUS_FRIEND_LIST_TABLE="${TCAPLUS_FRIEND_LIST_TABLE:-FriendList}"
    export TCAPLUS_FRIEND_LINK_SAGA_TABLE="${TCAPLUS_FRIEND_LINK_SAGA_TABLE:-FriendLinkSaga}"
    export TCAPLUS_FRIEND_INTERACTION_TABLE="${TCAPLUS_FRIEND_INTERACTION_TABLE:-FriendInteraction}"
fi

run_root="$(mktemp -d "${TMPDIR:-/tmp}/classic-farm-servers.XXXXXX")"
trap stop_services INT TERM EXIT

build_targets=(login zone coordinator gate)
if [[ "${tcaplus_mode}" == true ]]; then
    build_targets+=(friend)
fi
for name in "${build_targets[@]}"; do
    echo "[build] ${name}"
    (
        cd "${server_root}"
        go build -o "${run_root}/${name}" "./cmd/${name}"
    )
done

export APP_ENV="${APP_ENV:-development}"
export H5_ORIGIN="${H5_ORIGIN:-http://localhost:5173}"
export GATEWAY_ID="${GATEWAY_ID:-local-gateway}"
export GATEWAY_URL="${GATEWAY_URL:-ws://127.0.0.1:${gate_port}/ws}"
export CLIENT_CONFIG_URL="${CLIENT_CONFIG_URL:-http://127.0.0.1:${login_port}/v1/client-config/1}"
export LOGIN_TICKET_CONSUME_URL="${LOGIN_TICKET_CONSUME_URL:-http://127.0.0.1:${login_port}/internal/v1/ws-tickets/consume}"
export COORDINATOR_URL="${COORDINATOR_URL:-http://127.0.0.1:${coordinator_port}}"
export GATE_RPC_URL="${GATE_RPC_URL:-http://127.0.0.1:${gate_port}}"
if [[ "${tcaplus_mode}" == true ]]; then
    export FRIEND_RPC_URL="${FRIEND_RPC_URL:-http://127.0.0.1:${friend_port}}"
fi
export INTERNAL_GRPC_HMAC_KEY="${INTERNAL_GRPC_HMAC_KEY:-classic-farm-local-development-hmac-key-2026}"
export ROUTING_MODE="$([[ "${dual_zone}" == true ]] && echo static-dual-zone || echo local)"

coordinator_env=(
    "COORDINATOR_PORT="
    "HTTP_ADDRESS=127.0.0.1:${coordinator_port}"
)
if [[ "${dual_zone}" == true ]]; then
    coordinator_env+=(
        "ZONE_A_ID=zone-a"
        "ZONE_A_ENDPOINT=http://127.0.0.1:${zone_port}"
        "ZONE_B_ID=zone-b"
        "ZONE_B_ENDPOINT=http://127.0.0.1:${zone_b_port}"
    )
    if [[ "${mysql_mode}" == true || "${tcaplus_mode}" == true ]]; then
        coordinator_env+=("DUAL_ZONE_FENCE_BOOTSTRAP=1")
    fi
fi

start_service coordinator "${run_root}/coordinator" "${coordinator_env[@]}"
coordinator_pid="${service_pid}"
wait_ready coordinator "http://127.0.0.1:${coordinator_port}/readyz" "${coordinator_pid}"

start_service login "${run_root}/login" \
    "LOGIN_PORT=" \
    "HTTP_ADDRESS=127.0.0.1:${login_port}"
login_pid="${service_pid}"
wait_ready login "http://127.0.0.1:${login_port}/readyz" "${login_pid}"
wait_ready login "http://127.0.0.1:${login_port}/v1/client-config/1" "${login_pid}"

if [[ "${dual_zone}" == true ]]; then
    start_service zone-a "${run_root}/zone" \
        "OWNER_ZONE_ID=zone-a" \
        "ZONE_HTTP_ADDRESS=127.0.0.1:${zone_port}"
    zone_a_pid="${service_pid}"
    wait_ready zone-a "http://127.0.0.1:${zone_port}/readyz" "${zone_a_pid}"

    start_service zone-b "${run_root}/zone" \
        "OWNER_ZONE_ID=zone-b" \
        "ZONE_HTTP_ADDRESS=127.0.0.1:${zone_b_port}"
    zone_b_pid="${service_pid}"
    wait_ready zone-b "http://127.0.0.1:${zone_b_port}/readyz" "${zone_b_pid}"
else
    start_service zone "${run_root}/zone" \
        "ZONE_HTTP_ADDRESS=127.0.0.1:${zone_port}"
    zone_pid="${service_pid}"
    wait_ready zone "http://127.0.0.1:${zone_port}/readyz" "${zone_pid}"
fi

if [[ "${tcaplus_mode}" == true ]]; then
    start_service friend "${run_root}/friend" \
        "FRIEND_PORT=" \
        "HTTP_ADDRESS=127.0.0.1:${friend_port}"
    friend_pid="${service_pid}"
    wait_ready friend "http://127.0.0.1:${friend_port}/readyz" "${friend_pid}"
fi

start_service gate "${run_root}/gate" \
    "GATE_PORT=" \
    "HTTP_ADDRESS=127.0.0.1:${gate_port}"
gate_pid="${service_pid}"
wait_ready gate "http://127.0.0.1:${gate_port}/readyz" "${gate_pid}"

echo "All backend services are running."
echo "Runtime logs: ${run_root}"
if [[ "${mysql_mode}" == true ]]; then
    echo "Data mode: MySQL checkpoint baseline."
elif [[ "${tcaplus_mode}" == true ]]; then
    echo "Data mode: pure TcaplusDB."
    echo "FriendSvr: http://127.0.0.1:${friend_port}"
else
    echo "Data mode: development-only in-memory."
fi

deadline=0
if (( run_seconds > 0 )); then
    deadline=$((SECONDS + run_seconds))
fi
while :; do
    for index in "${!pids[@]}"; do
        if ! kill -0 "${pids[index]}" >/dev/null 2>&1; then
            echo "${names[index]} exited unexpectedly; log follows:" >&2
            sed -n '1,200p' "${run_root}/${names[index]}.log" >&2 || true
            exit 1
        fi
    done
    if (( deadline > 0 && SECONDS >= deadline )); then
        exit 0
    fi
    sleep 1
done
