#!/usr/bin/env bash
# scripts/run_eval.sh - Argus Full-Chain Agentic Validation & Evaluation Harness
# Compatible with macOS default Bash 3.2+, Zsh, and Linux Bash.

set -eo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

cd "${ROOT_DIR}"

echo "=================================================="
echo " Starting Argus Full-Chain Golden Scenarios Eval  "
echo "=================================================="

# Check Go toolchain
if ! command -v go >/dev/null 2>&1; then
  echo "Error: 'go' binary not found in PATH." >&2
  exit 1
fi

EVAL_LOG="$(mktemp "${TMPDIR:-/tmp}/argus_eval_XXXXXX.log")"
trap 'rm -f "${EVAL_LOG}"' EXIT

if command -v python3 >/dev/null 2>&1; then
  START_TIME=$(python3 -c "import time; print(time.time())")
else
  START_TIME=$(date +%s)
fi

# Run Golden Scenarios Integration Test Suite
set +e
go test -tags=integration -v -run 'TestGoldenScenario' . > "${EVAL_LOG}" 2>&1
TEST_EXIT_CODE=$?
set -e

if command -v python3 >/dev/null 2>&1; then
  END_TIME=$(python3 -c "import time; print(time.time())")
  TOTAL_WALL_TIME=$(python3 -c "import sys; print(f'{float(sys.argv[1]) - float(sys.argv[2]):.1f}s')" "${END_TIME}" "${START_TIME}")
else
  END_TIME=$(date +%s)
  ELAPSED=$((END_TIME - START_TIME))
  if [ "$ELAPSED" -le 0 ]; then
    TOTAL_WALL_TIME="<1s"
  else
    TOTAL_WALL_TIME="${ELAPSED}s"
  fi
fi

TOTAL_COUNT=5
PASSED_COUNT=0
TOTAL_TURNS=0
TOTAL_RETRIES=0

echo ""
echo "=== Argus Agentic Eval Report ==="

get_scenario_name() {
  case "$1" in
    G1) echo "G1 (DevServer Error)" ;;
    G2) echo "G2 (Controlled Form)" ;;
    G3) echo "G3 (Async Portal)   " ;;
    G4) echo "G4 (Ambiguity Hint) " ;;
    G5) echo "G5 (Virtual List)   " ;;
    *)  echo "$1" ;;
  esac
}

for ID in G1 G2 G3 G4 G5; do
  NAME="$(get_scenario_name "$ID")"
  LINE=$(grep -E "${ID} \(" "${EVAL_LOG}" | head -n 1 || true)
  if [ -n "${LINE}" ] && echo "${LINE}" | grep -q "PASS"; then
    PASSED_COUNT=$((PASSED_COUNT + 1))

    # Extract turns using sed
    TURNS=$(echo "${LINE}" | sed -E -n 's/.*Turns: ([0-9]+).*/\1/p')
    if [ -n "${TURNS}" ]; then
      TOTAL_TURNS=$((TOTAL_TURNS + TURNS))
    else
      TURNS="N/A"
    fi

    # Extract retries using sed
    RETRIES=$(echo "${LINE}" | sed -E -n 's/.*Retries: ([0-9]+).*/\1/p')
    if [ -n "${RETRIES}" ]; then
      TOTAL_RETRIES=$((TOTAL_RETRIES + RETRIES))
    fi

    # Extract tokens using sed
    RAW_TOKENS=$(echo "${LINE}" | sed -E -n 's/.*Tokens: ~?([0-9]+).*/\1/p')
    TOKENS="N/A"
    if [ -n "${RAW_TOKENS}" ]; then
      if [ "${RAW_TOKENS}" -ge 1000 ]; then
        TOKENS="$(awk "BEGIN {printf \"%.1fk\", ${RAW_TOKENS}/1000}")"
      else
        TOKENS="${RAW_TOKENS}"
      fi
    fi

    # Extract duration using sed
    DURATION=$(echo "${LINE}" | sed -E -n 's/.*Duration: ([0-9a-zA-Z\.]+)\).*/\1/p')
    if [ -z "${DURATION}" ]; then
      DURATION="N/A"
    fi

    printf "%s: PASS (Turns: %s, Tokens: %s, Duration: %s)\n" "${NAME}" "${TURNS}" "${TOKENS}" "${DURATION}"
  else
    printf "%s: FAIL\n" "${NAME}"
  fi
done

echo "---------------------------------"
SUCCESS_RATE=$(( (PASSED_COUNT * 100) / TOTAL_COUNT ))
if [ "${PASSED_COUNT}" -gt 0 ]; then
  MEAN_TURNS=$(awk "BEGIN {printf \"%.1f\", ${TOTAL_TURNS}/${PASSED_COUNT}}")
else
  MEAN_TURNS="0.0"
fi

if [ "${TOTAL_TURNS}" -gt 0 ]; then
  FRICTION_RETRY_RATE=$(awk "BEGIN {printf \"%.1f%%\", (${TOTAL_RETRIES} * 100)/${TOTAL_TURNS}}")
else
  FRICTION_RETRY_RATE="0.0%"
fi

printf "Overall Success Rate: %d%% | MTTR (Mean Turns): %s | Friction Retry Rate: %s | Total Wall Time: %s\n" "${SUCCESS_RATE}" "${MEAN_TURNS}" "${FRICTION_RETRY_RATE}" "${TOTAL_WALL_TIME}"
echo "================================="

if [ "${TEST_EXIT_CODE}" -ne 0 ] || [ "${PASSED_COUNT}" -lt "${TOTAL_COUNT}" ]; then
  echo ""
  echo "Eval failed. Test output log snippet:" >&2
  tail -n 30 "${EVAL_LOG}" >&2
  exit 1
fi

exit 0
