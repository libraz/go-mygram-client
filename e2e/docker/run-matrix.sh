#!/usr/bin/env bash
#
# Run the closed-loop e2e suite once per database target, tearing the stack
# down between each (delegated to run-e2e.sh, which already does up -> test
# -> down -v per invocation).
#
# Usage:
#   e2e/docker/run-matrix.sh                          # every target below
#   e2e/docker/run-matrix.sh --only mysql:8.4,mariadb:11.8
#
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

TARGETS=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --only)
      IFS=',' read -ra TARGETS <<< "$2"
      shift 2
      ;;
    *)
      echo "Unknown argument: $1" >&2
      exit 1
      ;;
  esac
done

if [[ ${#TARGETS[@]} -eq 0 ]]; then
  # Every supported LTS series of both flavors, plus the current MySQL
  # Innovation release through the floating "innovation" tag, which always
  # names the current one and so never goes out of support between runs.
  TARGETS=(
    "mysql:8.4" "mysql:9.7" "mysql:innovation"
    "mariadb:10.11" "mariadb:11.4" "mariadb:11.8" "mariadb:12.3"
  )
fi

overall_pass=0
overall_fail=0
declare -a summary_lines

for target in "${TARGETS[@]}"; do
  flavor="${target%%:*}"
  version="${target##*:}"

  echo "============================================="
  echo " ${target}"
  echo "============================================="

  if [[ "${flavor}" == "mariadb" ]]; then
    DB_FLAVOR=mariadb MARIADB_VERSION="${version}" bash "${SCRIPT_DIR}/run-e2e.sh"
  else
    DB_FLAVOR=mysql MYSQL_VERSION="${version}" bash "${SCRIPT_DIR}/run-e2e.sh"
  fi
  rc=$?

  if [[ ${rc} -eq 0 ]]; then
    summary_lines+=("PASS  ${target}")
    ((overall_pass++))
  else
    summary_lines+=("FAIL  ${target}  (exit code ${rc})")
    ((overall_fail++))
  fi
  echo ""
done

echo "============================================="
echo " Matrix Summary"
echo "============================================="
for line in "${summary_lines[@]}"; do
  echo "  ${line}"
done
echo ""
echo "  Pass: ${overall_pass} / $((overall_pass + overall_fail))"

if [[ ${overall_fail} -gt 0 ]]; then
  echo ""
  echo "FAILED: ${overall_fail} target(s) had failures."
  exit 1
fi

echo ""
echo "All targets passed!"
exit 0
