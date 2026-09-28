#!/usr/bin/env bash
#
# Run the closed-loop e2e suite against a throwaway MygramDB + MySQL stack.
#
# Usage:
#   e2e/docker/run-e2e.sh            # up -> go test -tags=e2e -> down -v
#   MYGRAMDB_VERSION=latest e2e/docker/run-e2e.sh
#   KEEP_UP=1 e2e/docker/run-e2e.sh  # leave the stack running for debugging
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
# -p pins the project even if the compose file is ever invoked from a context
# that overrides it, so teardown can never reach an unrelated stack.
COMPOSE=(docker compose -p go-mygram-e2e -f "${SCRIPT_DIR}/docker-compose.yml")

MYGRAM_PORT="${MYGRAM_PORT:-11016}"
MYGRAM_HTTP_PORT="${MYGRAM_HTTP_PORT:-18080}"
# Must match the compose stack; administrative commands need AUTH from v1.10.
MYGRAM_ADMIN_TOKEN="${MYGRAM_ADMIN_TOKEN:-e2e_admin_token}"

# Lowest server version whose TCP surface understands AUTH. An older server
# rejects the command outright, so the client must not send one.
AUTH_MIN_VERSION='1.10.0'

# Dump both services' logs. Called from every failure path, since a container
# that dies during startup leaves nothing on this script's stdout to explain it.
dump_logs() {
  echo "---- mygramdb logs ----" >&2
  "${COMPOSE[@]}" logs --no-log-prefix mygramdb >&2 2>&1 || true
  echo "---- mysql logs (tail) ----" >&2
  "${COMPOSE[@]}" logs --no-log-prefix --tail 30 mysql >&2 2>&1 || true
}

cleanup() {
  if [ "${KEEP_UP:-0}" = "1" ]; then
    echo "KEEP_UP=1 set; leaving the stack running. Tear down with:"
    echo "  ${COMPOSE[*]} down -v"
    return
  fi
  echo "==> Tearing down e2e stack"
  "${COMPOSE[@]}" down -v --remove-orphans || true
}
trap cleanup EXIT

echo "==> Starting e2e stack (mygramdb=${MYGRAMDB_VERSION:-1.10.2})"
if ! MYGRAM_PORT="${MYGRAM_PORT}" MYGRAM_HTTP_PORT="${MYGRAM_HTTP_PORT}" \
  MYGRAM_ADMIN_TOKEN="${MYGRAM_ADMIN_TOKEN}" "${COMPOSE[@]}" up -d --wait; then
  echo "ERROR: e2e stack did not come up" >&2
  dump_logs
  exit 1
fi

# `--wait` already blocks on the healthcheck (/health/ready), which only turns
# green after the initial snapshot finishes loading. Do a final explicit poll
# so a missing curl in the image still fails loudly rather than racing.
echo "==> Waiting for MygramDB readiness on :${MYGRAM_HTTP_PORT}"
for i in $(seq 1 30); do
  if curl -sf "http://127.0.0.1:${MYGRAM_HTTP_PORT}/health/ready" >/dev/null 2>&1; then
    echo "    ready"
    break
  fi
  if [ "$i" = "30" ]; then
    echo "ERROR: MygramDB did not become ready in time" >&2
    dump_logs
    exit 1
  fi
  sleep 2
done

# Ask the running server what it is rather than parsing MYGRAMDB_VERSION: the
# tag may be a moving alias such as `latest`, and only the reported version
# decides whether AUTH is available.
SERVER_VERSION="$(curl -sf "http://127.0.0.1:${MYGRAM_HTTP_PORT}/info" |
  sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"[^0-9]*\([0-9][^"]*\)".*/\1/p')"
if [ -z "${SERVER_VERSION}" ]; then
  echo "ERROR: could not read the server version from /info" >&2
  dump_logs
  exit 1
fi

if [ "$(printf '%s\n%s\n' "${AUTH_MIN_VERSION}" "${SERVER_VERSION}" | sort -V | head -n 1)" = "${AUTH_MIN_VERSION}" ]; then
  echo "==> Server ${SERVER_VERSION} gates administrative commands; running the suite with a token"
  E2E_ADMIN_TOKEN="${MYGRAM_ADMIN_TOKEN}"
else
  echo "==> Server ${SERVER_VERSION} predates AUTH; running the suite without a token"
  E2E_ADMIN_TOKEN=''
fi

echo "==> Running e2e suite"
cd "${REPO_ROOT}"
MYGRAM_E2E_SEEDED=1 MYGRAM_HOST=127.0.0.1 MYGRAM_PORT="${MYGRAM_PORT}" \
  MYGRAM_ADMIN_TOKEN="${E2E_ADMIN_TOKEN}" \
  go test -tags=e2e -count=1 -v ./...
