#!/usr/bin/env bash
# Copyright (c) 2026 Pius Alfred
# License: MIT
#
# Integration test suite for gotools.sh.
# Run from the test/ directory or via `make test-integration`.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GOTOOLS="$SCRIPT_DIR/../gotools.sh"
TMP_BASE=$(mktemp -d)
PASSED=0
FAILED=0
SUITE=""

cleanup() { cd "$SCRIPT_DIR" 2>/dev/null || true; rm -rf "$TMP_BASE"; }
trap cleanup EXIT

setup_project() { local d="$1"; mkdir -p "$d"; cp "$SCRIPT_DIR/main.go" "$SCRIPT_DIR/go.mod" "$d/"; }

# --- test helpers ---
pass() { echo "    PASS $1"; PASSED=$((PASSED + 1)); }
fail() { echo "    FAIL $1"; FAILED=$((FAILED + 1)); }
fail_detail() { fail "$1"; shift; for line in "$@"; do echo "          $line"; done; }

suite() { SUITE="$1"; echo ""; echo "  [$SUITE]"; }

# run_cmd — checks exit code only (stdout/stderr suppressed)
run_cmd() { local d="$1"; shift; if "$@" >/dev/null 2>&1; then pass "$d"; else fail "$d"; fi; }

# list_has — checks whether a tool name appears in the first column of `list` output.
# Uses awk instead of grep -w for reliable cross-platform word matching.
list_has() {
    local name="$1" expected="$2"
    if "$GOTOOLS" list 2>/dev/null | awk -v n="$name" 'NR>2 && $1 == n { found=1 } END { exit found ? 0 : 1 }'; then
        if [[ "$expected" == "yes" ]]; then pass "list contains $name"
        else fail_detail "list still contains $name after removal" "expected: not present" "actual:   $name found in list"; fi
    else
        if [[ "$expected" == "no" ]]; then pass "list does not contain $name"
        else fail_detail "list missing $name after install" "expected: $name present in list" "actual:   not found"; fi
    fi
}

# exec_test — runs a tool and verifies no Go infrastructure errors in output.
exec_test() {
    local name="$1"; shift
    local output
    output=$("$GOTOOLS" exec "$name" "$@" 2>&1) || true
    if echo "$output" | grep -qiE 'go:.*(work|vendor|module|cannot find)'; then
        fail_detail "exec $name" "expected: tool runs without Go errors" "stderr:   $output"
    else
        pass "exec $name"
    fi
}

# info_has — checks that `gotools info` output contains expected needle.
info_has() {
    local name="$1" needle="$2"
    local output
    output=$("$GOTOOLS" info "$name" 2>/dev/null) || true
    if [[ "$output" == *"$needle"* ]]; then
        pass "info $name contains '$needle'"
    else
        fail_detail "info $name missing '$needle'" "expected: $needle in output" "actual:   $output"
    fi
}

# ---------------------------------------------------------------------------
# Tool definitions
# ---------------------------------------------------------------------------
TOOL_GOIMPORTS_NAME="goimports"
TOOL_GOIMPORTS_PKG="golang.org/x/tools/cmd/goimports@latest"

TOOL_GOSEC_NAME="gosec"
TOOL_GOSEC_PKG="github.com/securego/gosec/v2/cmd/gosec@latest"

TOOL_STATICCHECK_NAME="staticcheck"
TOOL_STATICCHECK_PKG="honnef.co/go/tools/cmd/staticcheck@latest"

TOOL_GCI_NAME="gci"
TOOL_GCI_PKG="github.com/daixiang0/gci@latest"

# ---------------------------------------------------------------------------
# Strategy lifecycle — init → install → list → info → exec → sync →
#                       upgrade → config → remove → purge
# ---------------------------------------------------------------------------
test_strategy_lifecycle() {
    local strategy="$1"
    local tmpdir="$TMP_BASE/lifecycle-$strategy"
    setup_project "$tmpdir"
    cd "$tmpdir" || return

    suite "lifecycle: $strategy — init + install"
    run_cmd "init --strategy=$strategy"     "$GOTOOLS" init --strategy="$strategy"
    run_cmd "install $TOOL_GOIMPORTS_NAME"  "$GOTOOLS" install "$TOOL_GOIMPORTS_NAME" "$TOOL_GOIMPORTS_PKG"
    run_cmd "install $TOOL_GOSEC_NAME"      "$GOTOOLS" install "$TOOL_GOSEC_NAME"     "$TOOL_GOSEC_PKG"
    run_cmd "install $TOOL_STATICCHECK_NAME" "$GOTOOLS" install "$TOOL_STATICCHECK_NAME" "$TOOL_STATICCHECK_PKG"
    run_cmd "install $TOOL_GCI_NAME"        "$GOTOOLS" install "$TOOL_GCI_NAME"       "$TOOL_GCI_PKG"

    suite "lifecycle: $strategy — list"
    run_cmd "list" "$GOTOOLS" list
    list_has "$TOOL_GOIMPORTS_NAME" yes
    list_has "$TOOL_GOSEC_NAME" yes
    list_has "$TOOL_STATICCHECK_NAME" yes
    list_has "$TOOL_GCI_NAME" yes

    suite "lifecycle: $strategy — info"
    info_has "$TOOL_GOIMPORTS_NAME" "$TOOL_GOIMPORTS_NAME"
    info_has "$TOOL_GOSEC_NAME" "$TOOL_GOSEC_NAME"
    info_has "$TOOL_STATICCHECK_NAME" "$TOOL_STATICCHECK_NAME"
    info_has "$TOOL_GCI_NAME" "$TOOL_GCI_NAME"

    suite "lifecycle: $strategy — exec"
    exec_test "$TOOL_GOIMPORTS_NAME" --help
    exec_test "$TOOL_GOSEC_NAME" --help
    exec_test "$TOOL_STATICCHECK_NAME" --help
    exec_test "$TOOL_GCI_NAME" --help

    suite "lifecycle: $strategy — sync + upgrade + config"
    run_cmd "sync" "$GOTOOLS" sync
    run_cmd "upgrade $TOOL_GCI_NAME" "$GOTOOLS" upgrade "$TOOL_GCI_NAME"

    # Fast path runs BEFORE the config write: the write step is known to
    # reset the manifest strategy to the default (pre-existing bug), which
    # would auto-migrate the project and make the fingerprint meaningless.
    suite "lifecycle: $strategy — sync fast path"
    run_cmd "sync reconciles after upgrade" "$GOTOOLS" sync
    run_cmd "sync hits the fast path" bash -c "\"$GOTOOLS\" sync | grep -q 'Tools up to date.'"
    run_cmd "sync --offline hits the fast path" bash -c "\"$GOTOOLS\" sync --offline | grep -q 'fingerprint match'"

    run_cmd "config read GOTOOLS_STRATEGY" "$GOTOOLS" config GOTOOLS_STRATEGY
    run_cmd "config write GOTOOLS_DIR" "$GOTOOLS" config GOTOOLS_DIR tools

    suite "lifecycle: $strategy — remove"
    run_cmd "remove $TOOL_GOIMPORTS_NAME" "$GOTOOLS" remove "$TOOL_GOIMPORTS_NAME"
    list_has "$TOOL_GOIMPORTS_NAME" no
    run_cmd "remove $TOOL_GOSEC_NAME $TOOL_STATICCHECK_NAME" "$GOTOOLS" remove "$TOOL_GOSEC_NAME" "$TOOL_STATICCHECK_NAME"
    list_has "$TOOL_GOSEC_NAME" no
    list_has "$TOOL_STATICCHECK_NAME" no

    suite "lifecycle: $strategy — purge"
    run_cmd "purge" bash -c "echo YES | \"$GOTOOLS\" purge"

    if [[ ! -f .gotools.json ]]; then pass "purge removes .gotools.json"
    else fail_detail "purge removes .gotools.json" "expected: .gotools.json deleted" "actual:   file still exists"; fi
}

# ---------------------------------------------------------------------------
# Migration — unified → split → module → unified round-trip
# ---------------------------------------------------------------------------
test_migration() {
    local tmpdir="$TMP_BASE/migration"
    setup_project "$tmpdir"
    cd "$tmpdir" || return

    suite "migration: unified init + install"
    run_cmd "init unified" "$GOTOOLS" init --strategy=unified
    run_cmd "install gci" "$GOTOOLS" install "$TOOL_GCI_NAME" "$TOOL_GCI_PKG"
    run_cmd "install staticcheck" "$GOTOOLS" install "$TOOL_STATICCHECK_NAME" "$TOOL_STATICCHECK_PKG"
    exec_test "$TOOL_GCI_NAME" --help
    exec_test "$TOOL_STATICCHECK_NAME" --help

    suite "migration: unified → split"
    run_cmd "migrate split" "$GOTOOLS" migrate split
    list_has "$TOOL_GCI_NAME" yes
    list_has "$TOOL_STATICCHECK_NAME" yes
    exec_test "$TOOL_GCI_NAME" --help
    exec_test "$TOOL_STATICCHECK_NAME" --help

    suite "migration: split → module"
    run_cmd "migrate module" "$GOTOOLS" migrate module
    list_has "$TOOL_GCI_NAME" yes
    list_has "$TOOL_STATICCHECK_NAME" yes
    exec_test "$TOOL_GCI_NAME" --help
    exec_test "$TOOL_STATICCHECK_NAME" --help

    suite "migration: module → unified"
    run_cmd "migrate unified" "$GOTOOLS" migrate unified
    list_has "$TOOL_GCI_NAME" yes
    list_has "$TOOL_STATICCHECK_NAME" yes
    exec_test "$TOOL_GCI_NAME" --help
    exec_test "$TOOL_STATICCHECK_NAME" --help

    suite "migration: purge"
    run_cmd "purge" bash -c "echo YES | \"$GOTOOLS\" purge"
}

# ---------------------------------------------------------------------------
# go.mod adoption (init) + reversal (purge --restore)
# ---------------------------------------------------------------------------
# ---------------------------------------------------------------------------
# Parallel sync — cold restore with --jobs (issue #25)
# ---------------------------------------------------------------------------
test_parallel_sync() {
    local tmpdir="$TMP_BASE/parallel"
    setup_project "$tmpdir"
    cd "$tmpdir" || return

    suite "parallel sync: cold restore"
    run_cmd "init split" "$GOTOOLS" init --strategy=split --no-migrate
    run_cmd "install gci" "$GOTOOLS" install "$TOOL_GCI_NAME" "$TOOL_GCI_PKG"
    run_cmd "install staticcheck" "$GOTOOLS" install "$TOOL_STATICCHECK_NAME" "$TOOL_STATICCHECK_PKG"
    run_cmd "wipe modfiles + fingerprint" bash -c 'rm -f tools/*.mod tools/*.sum tools/.gotools.fingerprint'
    run_cmd "sync --jobs 2 restores both tools" "$GOTOOLS" sync --jobs 2
    list_has "$TOOL_GCI_NAME" yes
    list_has "$TOOL_STATICCHECK_NAME" yes
    exec_test "$TOOL_GCI_NAME" --help
    exec_test "$TOOL_STATICCHECK_NAME" --help
}

test_gomod_adoption() {
    local tmpdir="$TMP_BASE/adopt"
    setup_project "$tmpdir"
    cd "$tmpdir" || return

    suite "go.mod adoption: seed root go.mod with tools"
    run_cmd "go get -tool goimports" go get -tool "$TOOL_GOIMPORTS_PKG"
    run_cmd "go get -tool gci" go get -tool "$TOOL_GCI_PKG"
    grep -q '^tool ' go.mod
    if [[ $? -eq 0 ]]; then
        pass "root go.mod contains tool directives"
    else
        fail "root go.mod contains tool directives"
    fi

    suite "go.mod adoption: init adopts + strips"
    run_cmd "init adopts go.mod tools" "$GOTOOLS" init --strategy=split
    if grep -q '^tool ' go.mod; then
        fail_detail "init strips tool directives" "expected: no tool directives" "actual:   $(grep '^tool ' go.mod | head -1)"
    else
        pass "init strips tool directives"
    fi
    list_has "$TOOL_GOIMPORTS_NAME" yes
    list_has "$TOOL_GCI_NAME" yes
    run_cmd "go mod verify" go mod verify
    exec_test "$TOOL_GOIMPORTS_NAME" --help

    suite "go.mod adoption: purge --restore puts tools back"
    run_cmd "purge --restore" bash -c "echo YES | \"$GOTOOLS\" purge --restore"
    if grep -q '^tool ' go.mod; then
        pass "restore re-adds tool directives to go.mod"
    else
        fail "restore re-adds tool directives to go.mod"
    fi
    if [[ ! -f .gotools.json && ! -d tools ]]; then
        pass "purge --restore wipes gotools state"
    else
        fail "purge --restore wipes gotools state"
    fi
    run_cmd "go mod verify after restore" go mod verify
}

# ---------------------------------------------------------------------------
# Smoke tests — remaining commands
# ---------------------------------------------------------------------------
test_smoke() {
    local tmpdir="$TMP_BASE/smoke"
    setup_project "$tmpdir"
    cd "$tmpdir" || return

    suite "smoke: init + install"
    run_cmd "init split" "$GOTOOLS" init --strategy=split
    run_cmd "install goimports" "$GOTOOLS" install "$TOOL_GOIMPORTS_NAME" "$TOOL_GOIMPORTS_PKG"

    suite "smoke: version + exec + upgrade"
    run_cmd "version" "$GOTOOLS" version
    exec_test "$TOOL_GOIMPORTS_NAME" -l "$tmpdir/main.go"
    run_cmd "upgrade all" "$GOTOOLS" upgrade all

    suite "smoke: info + remove + purge"
    info_has "$TOOL_GOIMPORTS_NAME" "$TOOL_GOIMPORTS_NAME"
    run_cmd "remove goimports" "$GOTOOLS" remove "$TOOL_GOIMPORTS_NAME"
    run_cmd "purge" bash -c "echo YES | \"$GOTOOLS\" purge"
}

# ---------------------------------------------------------------------------
# Robustness — lockfile placement, install failure cleanup, verification
# ---------------------------------------------------------------------------
test_robustness() {
    local tmpdir="$TMP_BASE/robustness"
    setup_project "$tmpdir"
    cd "$tmpdir" || return

    # --- Issue #14: lockfile goes to configured GOTOOLS_DIR, not default ---
    suite "robustness: lockfile in configured dir"
    # Configure a non-default tools directory.
    "$GOTOOLS" init --strategy=split --dir=my-tools >/dev/null 2>&1
    # Run a lock-acquiring command (sync on a fresh project).  With the fix,
    # _acquire_lock runs *after* load_config, so the lock — and its parent
    # directory — land in my-tools/, never in tools/.
    "$GOTOOLS" sync >/dev/null 2>&1 || true
    if [[ -d tools ]]; then
        fail_detail "lockfile in wrong dir — 'tools/' exists but dir is 'my-tools'"
    else
        pass "lockfile in correct dir (no stray 'tools/')"
    fi
    # Clean up for next sub-tests.
    rm -rf my-tools .gotools.json

    # --- Issue #15: install with non-existent package fails and cleans up ---
    suite "robustness: install failure — bad package"
    "$GOTOOLS" init --strategy=split --dir=tools >/dev/null 2>&1
    local bad_pkg="example.com/definitely-not-a-real-package@latest"
    # The install must fail.
    if "$GOTOOLS" install fakename "$bad_pkg" >/dev/null 2>&1; then
        fail_detail "install bad package succeeded" "expected: install to fail"
    else
        pass "install bad package fails"
    fi
    # No orphaned mod/sum files left behind.
    if [[ -f tools/fakename.mod || -f tools/fakename.sum ]]; then
        fail_detail "install failure leaves orphaned files" \
            "expected: no tools/fakename.mod" \
            "actual:   $(ls tools/fakename.* 2>/dev/null || echo 'none')"
    else
        pass "install failure cleans up mod/sum files"
    fi
    # The failed tool must not appear in the manifest list.
    if "$GOTOOLS" list 2>/dev/null | grep -q fakename; then
        fail_detail "install failure writes manifest entry" \
            "expected: fakename not in list" \
            "actual:   fakename found in list"
    else
        pass "install failure does not write manifest entry"
    fi
    # The manifest file should still exist (just without the failed tool).
    if [[ -f .gotools.json ]]; then
        pass "manifest file preserved after failed install"
    else
        fail_detail "manifest file deleted after failed install"
    fi

    # Clean up.
    rm -rf tools .gotools.json

    # --- typo in package path (regression check for the user-reported bug) ---
    suite "robustness: install failure — typo in package path"
    "$GOTOOLS" init --strategy=split --dir=tools >/dev/null 2>&1
    local typo_pkg="github.com/golangci/golangci-lint/v2/cmd/golangci-lidnt@latest"
    if "$GOTOOLS" install linter "$typo_pkg" >/dev/null 2>&1; then
        fail_detail "install with typo succeeded" "expected: install to fail"
    else
        pass "install with typo fails"
    fi
    # No linter.mod left behind (the exact scenario from the bug report).
    if [[ -f tools/linter.mod ]]; then
        fail_detail "install with typo leaves linter.mod" \
            "expected: linter.mod deleted" \
            "actual:   file exists"
    else
        pass "install with typo cleans up linter.mod"
    fi
    rm -rf tools .gotools.json
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
echo "gotools.sh integration tests"
echo ""
echo "  suite: integration"

if [[ ! -x "$GOTOOLS" ]]; then
    echo "ERROR: $GOTOOLS not found or not executable" >&2
    exit 1
fi

test_strategy_lifecycle unified
test_strategy_lifecycle split
test_strategy_lifecycle module
test_migration
test_gomod_adoption
test_parallel_sync
test_smoke
test_robustness

echo ""
echo "Results: $PASSED passed, $FAILED failed"
[[ $FAILED -eq 0 ]] || exit 1
