#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "${script_dir}/.." && pwd)"
compose_file="${script_dir}/docker-compose.yml"
migration_dir="${script_dir}/migrations"
env_file="${repo_root}/.env"

if ! command -v docker >/dev/null 2>&1; then
    echo "docker was not found on PATH" >&2
    exit 1
fi

compose_args=(compose)
if [[ -f "${env_file}" ]]; then
    compose_args+=(--env-file "${env_file}")
fi
compose_args+=(-f "${compose_file}")

if ! docker "${compose_args[@]}" exec -T mysql \
    sh -c 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysqladmin ping -h 127.0.0.1 -u root --silent' \
    >/dev/null 2>&1; then
    echo "MySQL is not ready; start it with docker compose first" >&2
    exit 1
fi

shopt -s nullglob
migrations=("${migration_dir}"/*.up.sql)
if (( ${#migrations[@]} == 0 )); then
    echo "No migration files found under ${migration_dir}" >&2
    exit 1
fi

for migration in "${migrations[@]}"; do
    echo "Applying $(basename -- "${migration}")"
    docker "${compose_args[@]}" exec -T mysql \
        sh -c 'MYSQL_PWD="$MYSQL_PASSWORD" exec mysql -u"$MYSQL_USER" "$MYSQL_DATABASE"' \
        < "${migration}"
done

echo "Migrations completed."
