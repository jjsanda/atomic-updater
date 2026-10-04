#!/usr/bin/env bash
# Runs the server storage integration tests against a throwaway Postgres 15
# container. No host psql needed.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
name="atomic-storage-test-$$"
port="${STORAGE_TEST_PORT:-55432}"

cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; }
trap cleanup EXIT

docker run -d --name "$name" \
    -e POSTGRES_USER=atomic -e POSTGRES_PASSWORD=atomic -e POSTGRES_DB=atomic_test \
    -p "127.0.0.1:${port}:5432" postgres:15-alpine >/dev/null

echo "waiting for postgres..."
for _ in $(seq 1 60); do
    if docker exec "$name" pg_isready -U atomic >/dev/null 2>&1; then
        break
    fi
    sleep 1
done
docker exec "$name" pg_isready -U atomic >/dev/null

export TEST_DATABASE_URL="postgres://atomic:atomic@127.0.0.1:${port}/atomic_test?sslmode=disable"
cd "$root/server"
go test -v -count=1 ./internal/storage/
