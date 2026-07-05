#!/usr/bin/env bash
# Shared helpers for unit tests.
# Source this file first — it sets up globals and sources gotools.sh.

TEST_PASSED=0
TEST_FAILED=0

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GOTOOLS_SH="$SCRIPT_DIR/../../gotools.sh"

# Set required globals before sourcing (gotools.sh has `set -u`).
export GOTOOLS_STRATEGY="split"
export GOTOOLS_DIR="tools"
export GOTOOLS_GO_VERSION="inherit"
export GOTOOLS_MODULE_PREFIX=""
export MANIFEST_FILE=".gotools.json"
export DEFAULT_STRATEGY="split"
export DEFAULT_DIR="tools"
export DEFAULT_GO_VERSION="inherit"
export DEFAULT_MODULE_PREFIX=""
export _ORIG_ENV_STRATEGY=""
export _ORIG_ENV_DIR=""
export _ORIG_ENV_GO_VERSION=""
export _ORIG_ENV_MODULE_PREFIX=""

# shellcheck source=/dev/null
source "$GOTOOLS_SH"

# assert_eq <description> <expected> <actual>
assert_eq() {
    local desc="$1" expected="$2" actual="$3"
    if [[ "$expected" == "$actual" ]]; then
        echo "    PASS $desc"
        TEST_PASSED=$((TEST_PASSED + 1))
    else
        echo "    FAIL $desc"
        echo "          input:    $desc"
        echo "          expected: $expected"
        echo "          actual:   $actual"
        TEST_FAILED=$((TEST_FAILED + 1))
    fi
}

# assert_contains <description> <needle> <haystack>
assert_contains() {
    local desc="$1" needle="$2" haystack="$3"
    if [[ "$haystack" == *"$needle"* ]]; then
        echo "    PASS $desc"
        TEST_PASSED=$((TEST_PASSED + 1))
    else
        echo "    FAIL $desc"
        echo "          expected to contain: $needle"
        echo "          actual: $haystack"
        TEST_FAILED=$((TEST_FAILED + 1))
    fi
}

# assert_file_exists <description> <path>
assert_file_exists() {
    local desc="$1" path="$2"
    if [[ -f "$path" ]]; then
        echo "    PASS $desc"
        TEST_PASSED=$((TEST_PASSED + 1))
    else
        echo "    FAIL $desc"
        echo "          expected file: $path"
        echo "          actual:        not found"
        TEST_FAILED=$((TEST_FAILED + 1))
    fi
}

# assert_file_absent <description> <path>
assert_file_absent() {
    local desc="$1" path="$2"
    if [[ ! -f "$path" ]]; then
        echo "    PASS $desc"
        TEST_PASSED=$((TEST_PASSED + 1))
    else
        echo "    FAIL $desc"
        echo "          expected absent: $path"
        echo "          actual:          file exists"
        TEST_FAILED=$((TEST_FAILED + 1))
    fi
}

# category <name> — print a category header.
category() { echo ""; echo "  [$1]"; }

# finish — print summary. Always returns 0 due to `set -e` in gotools.sh.
finish() {
    echo ""
    echo "  $TEST_PASSED passed, $TEST_FAILED failed"
    if [[ $TEST_FAILED -gt 0 ]]; then echo "  ** FAILURES **"; fi
    return 0
}
