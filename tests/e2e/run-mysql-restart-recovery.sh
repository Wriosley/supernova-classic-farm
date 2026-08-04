#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "${script_dir}/../.." && pwd)"
server_root="${repo_root}/server"
env_file="${repo_root}/.env"
login_port="${LOGIN_PORT:-}"
gate_port="${GATE_PORT:-}"
stack_pid=""
run_root="$(mktemp -d "${TMPDIR:-/tmp}/classic-farm-linux-e2e.XXXXXX")"
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
login_port="${login_port:-8080}"
gate_port="${gate_port:-8081}"
for port in "${login_port}" "${gate_port}"; do
    if [[ ! "${port}" =~ ^[0-9]+$ ]] ||
       (( port < 1 || port > 65535 )); then
        echo "Invalid E2E backend port: ${port}" >&2
        exit 1
    fi
done
login_url="http://127.0.0.1:${login_port}"
gate_ready_url="http://127.0.0.1:${gate_port}/readyz"

show_stack_log() {
    if [[ -f "${stack_log}" ]]; then
        echo "----- Linux backend stack log -----" >&2
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
    "${repo_root}/start-servers.sh" --dual-zone --mysql \
        >"${stack_log}" 2>&1 &
    stack_pid=$!

    local deadline=$((SECONDS + 60))
    while (( SECONDS < deadline )); do
        if ! kill -0 "${stack_pid}" >/dev/null 2>&1; then
            echo "Backend stack exited before readiness" >&2
            show_stack_log
            return 1
        fi
        if curl --fail --silent --show-error --max-time 2 \
            "${login_url}/readyz" >/dev/null 2>&1 &&
           curl --fail --silent --show-error --max-time 2 \
            "${gate_ready_url}" >/dev/null 2>&1; then
            echo "READY linux_dual_zone_mysql login=${login_url}"
            return 0
        fi
        sleep 0.2
    done
    echo "Backend stack did not become ready within 60 seconds" >&2
    show_stack_log
    return 1
}

run_owner_loop() {
    E2E_RUN=1 \
    E2E_LOGIN_URL="${login_url}" \
    E2E_ACCOUNT_NAME="${account_name}" \
    E2E_AUTH_MODE=register \
    E2E_BUY_SEEDS=1 \
    E2E_PLANT=1 \
    E2E_APPLY_FERTILIZER=1 \
    E2E_WAIT_MATURITY_PUSH=1 \
    E2E_HARVEST=1 \
    E2E_SELL_CROP=1 \
    E2E_CLAIM_CHAPTER_REWARD=1 \
    E2E_CLEAN_PLOT=1 \
    E2E_EXPECT_PLAYER_SEQ=0 \
        go -C "${server_root}" test ./test/e2e \
            -run '^TestAuthenticatedSnapshot$' -count=1 -v
}

run_recovery_check() {
    E2E_RUN=1 \
    E2E_LOGIN_URL="${login_url}" \
    E2E_ACCOUNT_NAME="${account_name}" \
    E2E_AUTH_MODE=login \
    E2E_EXPECT_PLAYER_SEQ=8 \
        go -C "${server_root}" test ./test/e2e \
            -run '^TestAuthenticatedSnapshot$' -count=1 -v
}

run_dual_zone_fence_check() {
    local dsn="${MYSQL_DSN:-}"
    if [[ -z "${dsn}" ]]; then
        local mysql_host mysql_port mysql_database mysql_user mysql_password
        mysql_host="$(dotenv_value MYSQL_HOST)"
        mysql_port="$(dotenv_value MYSQL_PORT)"
        mysql_database="$(dotenv_value MYSQL_DATABASE)"
        mysql_user="$(dotenv_value MYSQL_USER)"
        mysql_password="$(dotenv_value MYSQL_PASSWORD)"
        mysql_host="${mysql_host:-127.0.0.1}"
        mysql_port="${mysql_port:-3306}"
        mysql_database="${mysql_database:-classicfarm}"
        mysql_user="${mysql_user:-classicfarm}"
        mysql_password="${mysql_password:-classicfarm}"
        dsn="${mysql_user}:${mysql_password}@tcp(${mysql_host}:${mysql_port})/${mysql_database}?parseTime=true"
    fi
    E2E_RUN=1 \
    E2E_DUAL_ZONE=1 \
    E2E_SUITE=dual-zone-mysql \
    E2E_LOGIN_URL="${login_url}" \
    MYSQL_DSN="${dsn}" \
        go -C "${server_root}" test ./test/e2e \
            -run '^TestDualZoneMySQLRoutingAndPersistence$' -count=1 -v
}

command -v go >/dev/null 2>&1 || {
    echo "go was not found on PATH" >&2
    exit 1
}
command -v curl >/dev/null 2>&1 || {
    echo "curl was not found on PATH" >&2
    exit 1
}

"${repo_root}/deploy/migrate.sh"

account_name="linux_$(date -u +%Y%m%d%H%M%S)_$RANDOM"
echo "PHASE register account=${account_name}"
start_stack
run_owner_loop
stop_stack

echo "RESTART boundary=fresh-five-process-dual-zone-stack"
start_stack
run_recovery_check
run_dual_zone_fence_check
stop_stack

e2e_passed=true
echo "RESULT linux_dual_zone_mysql_restart_recovery=PASS account=${account_name}"
