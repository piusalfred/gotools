#!/usr/bin/env bash
# Run all unit test suites.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUITES=0; SUITES_FAILED=0; TOTAL_PASS=0; TOTAL_FAIL=0

echo "unit tests"

for test_file in "$SCRIPT_DIR"/test_*.sh; do
    name=$(basename "$test_file" .sh)
    echo ""
    echo "  suite: ${name#test_}"
    SUITES=$((SUITES + 1))
    out=$(bash "$test_file" 2>&1) || true
    echo "$out"
    # Count failures from summary line
    failed=$(echo "$out" | grep -oE '[0-9]+ failed' | grep -oE '[0-9]+' || echo 0)
    if [[ "$failed" -gt 0 ]]; then
        SUITES_FAILED=$((SUITES_FAILED + 1))
        TOTAL_FAIL=$((TOTAL_FAIL + failed))
    fi
    passed=$(echo "$out" | grep -oE '[0-9]+ passed' | grep -oE '[0-9]+' || echo 0)
    TOTAL_PASS=$((TOTAL_PASS + passed))
done

echo ""
echo "  suites: $SUITES, failed: $SUITES_FAILED"
echo "  tests:  $TOTAL_PASS passed, $TOTAL_FAIL failed"
[[ $SUITES_FAILED -eq 0 ]] || exit 1
