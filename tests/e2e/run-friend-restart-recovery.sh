#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "${script_dir}/../.." && pwd)"
server_root="${repo_root}/server"
env_file="${repo_root}/.env"
login_port="${LOGIN_PORT:-}"
gate_port="${GATE_PORT:-}"
friend_port="${FRIEND_PORT:-}"
stack_pid=""
run_root="$(mktemp -d "${TMPDIR:-/tmp}/classic-farm-friend-recovery.XXXXXX")"
stack_log="${run_root}/stack.log"

dotenv_value() {
    local key="$1"
    [[ -f "${env_file}" ]] || return 0
    awk -F= -v wanted="${key}" '
        $1 == wanted {
            sub(/^[^=]*=/, "")
            sub(/\r$/, "")
            if (($0 ~ /^".*"$/) || ($0 ~ /^\047.*\047$/)) {
                print substr($0, 2, length($0) - 2)
            } else {
                print
            }
            exit
        }
    ' "${env_file}"
}

if [[ -z "${login_port}" ]]; then
    login_port="$(dotenv_value LOGIN_PORT)"
fi
if [[ -z "${gate_port}" ]]; then
    gate_port="$(dotenv_value GATE_PORT)"
fi
if [[ -z "${friend_port}" ]]; then
    friend_port="$(dotenv_value FRIEND_PORT)"
fi
login_port="${login_port:-8080}"
gate_port="${gate_port:-8081}"
friend_port="${friend_port:-8085}"
for port in "${login_port}" "${gate_port}" "${friend_port}"; do
    if [[ ! "${port}" =~ ^[0-9]+$ ]] ||
       (( port < 1 || port > 65535 )); then
        echo "Invalid E2E backend port: ${port}" >&2
        exit 1
    fi
done
login_url="http://127.0.0.1:${login_port}"
gate_ready_url="http://127.0.0.1:${gate_port}/readyz"
friend_ready_url="http://127.0.0.1:${friend_port}/readyz"
gate_ws_url="ws://127.0.0.1:${gate_port}/ws"

show_stack_log() {
    if [[ -f "${stack_log}" ]]; then
        echo "----- Friend recovery stack log -----" >&2
        sed -n '1,240p' "${stack_log}" >&2
    fi
}

stop_stack() {
    if [[ -z "${stack_pid}" ]]; then
        return
    fi
    if kill -0 "${stack_pid}" >/dev/null 2>&1; then
        kill -TERM "${stack_pid}" >/dev/null 2>&1 || true
    fi
    wait "${stack_pid}" 2>/dev/null || true
    stack_pid=""
}

cleanup() {
    stop_stack
    if [[ "${e2e_passed:-false}" == true ]]; then
        rm -rf -- "${run_root}"
    else
        show_stack_log
        echo "E2E logs retained at ${run_root}" >&2
    fi
}
trap cleanup EXIT INT TERM

start_stack() {
    : > "${stack_log}"
    "${repo_root}/start-servers.sh" --dual-zone --tcaplus \
        >"${stack_log}" 2>&1 &
    stack_pid=$!

    local deadline=$((SECONDS + 90))
    while (( SECONDS < deadline )); do
        if ! kill -0 "${stack_pid}" >/dev/null 2>&1; then
            echo "Backend stack exited before readiness" >&2
            show_stack_log
            return 1
        fi
        if curl --fail --silent --show-error --max-time 2 \
            "${login_url}/readyz" >/dev/null 2>&1 &&
           curl --fail --silent --show-error --max-time 2 \
            "${gate_ready_url}" >/dev/null 2>&1 &&
           curl --fail --silent --show-error --max-time 2 \
            "${friend_ready_url}" >/dev/null 2>&1; then
            echo "READY friend_dual_zone_tcaplus login=${login_url} friend=${friend_port}"
            return 0
        fi
        sleep 0.3
    done
    echo "Backend stack did not become ready within 90 seconds" >&2
    show_stack_log
    return 1
}

run_friend_interaction() {
    E2E_RUN=1 \
    E2E_SUITE=friend-interaction \
    E2E_LOGIN_URL="${login_url}" \
    E2E_GATE_WS_URL="${gate_ws_url}" \
    E2E_OWNER_ACCOUNT="${owner_account}" \
    E2E_VISITOR_A_ACCOUNT="${visitor_a_account}" \
    E2E_VISITOR_B_ACCOUNT="${visitor_b_account}" \
        go -C "${server_root}" test ./test/e2e \
            -run '^TestFriendInteraction$' -count=1 -timeout 5m -v
}

run_friend_recovery() {
    E2E_RUN=1 \
    E2E_SUITE=friend-saga-recovery \
    E2E_LOGIN_URL="${login_url}" \
    E2E_GATE_WS_URL="${gate_ws_url}" \
    E2E_OWNER_ACCOUNT="${owner_account}" \
    E2E_VISITOR_A_ACCOUNT="${visitor_a_account}" \
    E2E_VISITOR_B_ACCOUNT="${visitor_b_account}" \
        go -C "${server_root}" test ./test/e2e \
            -run '^TestFriendRestartRecovery$' -count=1 -timeout 2m -v
}

command -v go >/dev/null 2>&1 || {
    echo "go was not found on PATH" >&2
    exit 1
}
command -v curl >/dev/null 2>&1 || {
    echo "curl was not found on PATH" >&2
    exit 1
}

# Account names must be 3..32 lowercase/digit/_ chars (auth.ValidateCredentials).
suffix="$(date -u +%y%m%d%H%M%S)_$RANDOM"
owner_account="fo_${suffix}"
visitor_a_account="fa_${suffix}"
visitor_b_account="fb_${suffix}"
echo "PHASE register-and-interact owner=${owner_account}"
start_stack
run_friend_interaction
stop_stack

echo "RESTART boundary=fresh-six-process-dual-zone-friend-stack"
start_stack
run_friend_recovery
stop_stack

e2e_passed=true
echo "RESULT friend_restart_recovery=PASS owner=${owner_account}"
