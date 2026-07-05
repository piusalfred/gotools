#!/usr/bin/env bash
# Copyright (c) 2026 Pius Alfred
#
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:
#
# The above copyright notice and this permission notice shall be included in
# all copies or substantial portions of the Software.
#
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
# SOFTWARE.

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
