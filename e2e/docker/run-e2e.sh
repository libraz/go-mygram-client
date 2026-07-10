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
COMPOSE=(docker compose -f "${SCRIPT_DIR}/docker-compose.yml")

MYGRAM_PORT="${MYGRAM_PORT:-11016}"
MYGRAM_HTTP_PORT="${MYGRAM_HTTP_PORT:-18080}"

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

echo "==> Starting e2e stack (mygramdb=${MYGRAMDB_VERSION:-1.8.0})"
MYGRAM_PORT="${MYGRAM_PORT}" MYGRAM_HTTP_PORT="${MYGRAM_HTTP_PORT}" "${COMPOSE[@]}" up -d --wait

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
    "${COMPOSE[@]}" logs mygramdb || true
    exit 1
  fi
  sleep 2
done

echo "==> Running e2e suite"
cd "${REPO_ROOT}"
MYGRAM_E2E_SEEDED=1 MYGRAM_HOST=127.0.0.1 MYGRAM_PORT="${MYGRAM_PORT}" \
  go test -tags=e2e -count=1 -v ./...
