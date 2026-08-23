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

    # Real doctor run: real go tool probes (incl. go mod verify -modfile for
    # split) and the proxy @latest reachability check.
    suite "lifecycle: $strategy — doctor"
    run_cmd "doctor healthy" "$GOTOOLS" doctor
    run_cmd "doctor --format=json healthy" bash -c "\"$GOTOOLS\" doctor --format=json | grep -q '\"healthy\":true'"
    run_cmd "list --format=json emits tools array" bash -c "\"$GOTOOLS\" list --format=json | grep -Fq '\"tools\":[{'"

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
# Doctor — warn/fail paths and JSON parity (issue #28, #29)
# ---------------------------------------------------------------------------
test_doctor() {
    local tmpdir="$TMP_BASE/doctor"
    setup_project "$tmpdir"
    cd "$tmpdir" || return

    # --- warn path: init only, nothing synced ---
    suite "doctor: warn path"
    run_cmd "init split" "$GOTOOLS" init --strategy=split --no-migrate
    local output
    output=$("$GOTOOLS" doctor 2>&1) || true
    if [[ "$output" == *"tools not synced"* ]]; then
        pass "doctor reports unsynced tools"
    else
        fail_detail "doctor reports unsynced tools" "expected: 'tools not synced' in output" "actual:   $output"
    fi
    run_cmd "doctor exits 0 on warnings" "$GOTOOLS" doctor

    # --- stale lock warn ---
    mkdir -p tools/.gotools.lock
    touch -t 202001010000 tools/.gotools.lock
    output=$("$GOTOOLS" doctor 2>&1) || true
    if [[ "$output" == *"stale lock directory"* ]]; then
        pass "doctor reports stale lock"
    else
        fail_detail "doctor reports stale lock" "expected: 'stale lock directory' in output" "actual:   $output"
    fi
    rmdir tools/.gotools.lock

    # --- fail path: fresh dir without init ---
    suite "doctor: fail path"
    local fresh="$TMP_BASE/doctor-fresh"
    mkdir -p "$fresh"
    cd "$fresh" || return
    run_cmd "doctor exits 0 without manifest" "$GOTOOLS" doctor
    output=$("$GOTOOLS" doctor 2>&1) || true
    if [[ "$output" == *"no .gotools.json"* ]]; then
        pass "doctor reports missing manifest"
    else
        fail_detail "doctor reports missing manifest" "expected: 'no .gotools.json' in output" "actual:   $output"
    fi

    # --- JSON parity ---
    suite "doctor: json parity"
    cd "$tmpdir" || return
    run_cmd "doctor --format=json emits schema" bash -c "\"$GOTOOLS\" doctor --format=json | grep -q '\"schema_version\":1'"
    run_cmd "version --format=json emits schema" bash -c "\"$GOTOOLS\" version --format=json | grep -q '\"schema_version\":1'"
}

# ---------------------------------------------------------------------------
# Path validation — hostile dirs/names/symlinks must never escape the project
# ---------------------------------------------------------------------------
test_path_validation() {
    suite "security: hostile manifest dir cannot delete outside the project"
    local d="$TMP_BASE/hostile-dir"
    local victim="$TMP_BASE/victim-dir"
    mkdir -p "$d" "$victim"
    cp "$SCRIPT_DIR/main.go" "$SCRIPT_DIR/go.mod" "$d/"
    echo "canary" > "$victim/canary.txt"
    cat > "$d/.gotools.json" <<'JSON'
{
  "version": 1,
  "strategy": "split",
  "dir": "../victim-dir",
  "go_version": "inherit",
  "module_prefix": "example.com/test",
  "tools": {
  }
}
JSON
    local rc=0
    (cd "$d" && "$GOTOOLS" sync >/dev/null 2>&1) || rc=$?
    if [[ "$rc" -eq 8 ]]; then pass "sync refuses hostile dir with exit 8"
    else fail_detail "sync refuses hostile dir with exit 8" "expected: 8" "actual:   $rc"; fi
    if [[ -f "$victim/canary.txt" ]]; then pass "victim directory untouched"
    else fail "victim directory untouched (canary deleted!)"; fi

    suite "security: traversal tool names refused (no outside writes)"
    local d2="$TMP_BASE/name-traversal"
    mkdir -p "$d2"
    cp "$SCRIPT_DIR/main.go" "$SCRIPT_DIR/go.mod" "$d2/"
    cat > "$d2/.gotools.json" <<'JSON'
{
  "version": 1,
  "strategy": "split",
  "dir": "tools",
  "go_version": "inherit",
  "module_prefix": "example.com/test",
  "tools": {
  }
}
JSON
    mkdir -p "$d2/tools"
    rc=0
    (cd "$d2" && "$GOTOOLS" remove ../../victim-dir >/dev/null 2>&1) || rc=$?
    if [[ "$rc" -eq 2 ]]; then pass "remove traversal name exits 2"
    else fail_detail "remove traversal name exits 2" "expected: 2" "actual:   $rc"; fi
    if [[ -f "$victim/canary.txt" ]]; then pass "victim directory still untouched"
    else fail "victim directory still untouched (deleted!)"; fi

    suite "security: symlinked tools dir refused"
    local d3="$TMP_BASE/symlink-dir"
    local outside="$TMP_BASE/outside-mod"
    mkdir -p "$d3" "$outside"
    cp "$SCRIPT_DIR/main.go" "$SCRIPT_DIR/go.mod" "$d3/"
    echo "outside" > "$outside/go.mod"
    cat > "$d3/.gotools.json" <<'JSON'
{
  "version": 1,
  "strategy": "split",
  "dir": "tools",
  "go_version": "inherit",
  "module_prefix": "example.com/test",
  "tools": {
  }
}
JSON
    ln -s "$outside" "$d3/tools"
    rc=0
    (cd "$d3" && "$GOTOOLS" sync >/dev/null 2>&1) || rc=$?
    if [[ "$rc" -eq 8 ]]; then pass "sync refuses symlinked tools dir with exit 8"
    else fail_detail "sync refuses symlinked tools dir with exit 8" "expected: 8" "actual:   $rc"; fi
    if [[ "$(cat "$outside/go.mod")" == "outside" ]]; then pass "outside go.mod untouched"
    else fail "outside go.mod untouched (rewritten!)"; fi
}

# ---------------------------------------------------------------------------
# Rename + install duplicity — real go: modfile moves, module-line rewrite,
# exec under the new name, and the same-package install flow
# ---------------------------------------------------------------------------
test_rename() {
    suite "rename: split layout"
    local d="$TMP_BASE/rename-split"
    setup_project "$d"
    cd "$d" || return
    "$GOTOOLS" init --strategy=split >/dev/null 2>&1
    local rc=0
    "$GOTOOLS" install "$TOOL_GOIMPORTS_PKG" >/dev/null 2>&1 || rc=$?
    if [[ $rc -ne 0 ]]; then fail "rename setup: install goimports"; return; fi
    rc=0
    "$GOTOOLS" rename goimports myimports >/dev/null 2>&1 || rc=$?
    if [[ $rc -ne 0 ]]; then fail "rename goimports → myimports exits 0"; return; fi
    if [[ -f "tools/myimports.mod" && ! -f "tools/goimports.mod" ]]; then pass "modfile moved (myimports.mod, old gone)"
    else fail_detail "modfile moved" "expected: tools/myimports.mod present, tools/goimports.mod absent" "actual:   $(ls tools | tr '\n' ' ')"; fi
    if grep -q 'module .*/tools/myimports' "tools/myimports.mod"; then pass "module line rewritten to the new name"
    else fail_detail "module line rewritten" "expected: module .../tools/myimports" "actual:   $(head -1 "tools/myimports.mod")"; fi
    if grep -q '"myimports"' ".gotools.json" && ! grep -q '"goimports"' ".gotools.json"; then pass "manifest renamed"
    else fail "manifest renamed (myimports present, goimports gone)"; fi
    exec_test myimports -h
    rc=0
    "$GOTOOLS" info goimports >/dev/null 2>&1 || rc=$?
    if [[ $rc -eq 5 ]]; then pass "old name gone (info exits 5)"
    else fail_detail "old name gone (info exits 5)" "expected: 5" "actual:   $rc"; fi
    fp_before=$(cat "tools/.gotools.fingerprint" 2>/dev/null)
    if [[ -n "$fp_before" ]]; then pass "rename writes the sync fingerprint"
    else fail "rename writes the sync fingerprint (missing)"; fi
    out=$("$GOTOOLS" sync 2>&1) || true
    if echo "$out" | grep -q "Tools up to date"; then pass "sync after rename takes the fast path"
    else fail_detail "sync after rename takes the fast path" "expected: Tools up to date" "actual:   $out"; fi
    if [[ "$fp_before" == "$(cat "tools/.gotools.fingerprint" 2>/dev/null)" ]]; then pass "fast-path sync leaves the fingerprint unchanged"
    else fail "fast-path sync leaves the fingerprint unchanged (rewritten!)"; fi
    rc=0
    "$GOTOOLS" sync --offline >/dev/null 2>&1 || rc=$?
    if [[ $rc -eq 0 ]]; then pass "sync --offline fast-paths after rename"
    else fail_detail "sync --offline fast-paths after rename" "expected: 0" "actual:   $rc"; fi
    info_has myimports "goimports"
    # `check` is deliberately NOT asserted here: it fails for goimports
    # identically before and after the rename (goimports exits 2 on both
    # --version and --help — pre-existing `check` semantics, not rename).

    suite "rename: module layout"
    local m="$TMP_BASE/rename-module"
    setup_project "$m"
    cd "$m" || return
    "$GOTOOLS" init --strategy=module >/dev/null 2>&1
    rc=0
    "$GOTOOLS" install "$TOOL_GOIMPORTS_PKG" >/dev/null 2>&1 || rc=$?
    if [[ $rc -ne 0 ]]; then fail "rename setup: install goimports (module)"; return; fi
    rc=0
    "$GOTOOLS" rename goimports myimports >/dev/null 2>&1 || rc=$?
    if [[ $rc -ne 0 ]]; then fail "module rename exits 0"; return; fi
    if [[ -f "tools/myimports/go.mod" && ! -d "tools/goimports" ]]; then pass "module dir moved (myimports/, old gone)"
    else fail_detail "module dir moved" "expected: tools/myimports/go.mod present, tools/goimports absent" "actual:   $(ls tools | tr '\n' ' ')"; fi
    if grep -q 'module .*/tools/myimports' "tools/myimports/go.mod"; then pass "module go.mod line rewritten"
    else fail_detail "module go.mod line rewritten" "expected: module .../tools/myimports" "actual:   $(head -1 "tools/myimports/go.mod")"; fi
    exec_test myimports -h
    if [[ -n "$(cat "tools/.gotools.fingerprint" 2>/dev/null)" ]]; then pass "module rename writes the sync fingerprint"
    else fail "module rename writes the sync fingerprint (missing)"; fi

    suite "rename: unified layout refuses (directives carry the names)"
    local u="$TMP_BASE/rename-unified"
    setup_project "$u"
    cd "$u" || return
    "$GOTOOLS" init --strategy=unified >/dev/null 2>&1
    rc=0
    "$GOTOOLS" install "$TOOL_GOIMPORTS_PKG" >/dev/null 2>&1 || rc=$?
    if [[ $rc -ne 0 ]]; then fail "rename setup: install goimports (unified)"; return; fi
    before=$(shasum -a 256 "tools/go.mod" | awk '{print $1}')
    rc=0
    "$GOTOOLS" rename goimports myimports >/dev/null 2>&1 || rc=$?
    if [[ $rc -eq 8 ]]; then pass "unified rename refuses with exit 8"
    else fail_detail "unified rename refuses with exit 8" "expected: 8" "actual:   $rc"; fi
    after=$(shasum -a 256 "tools/go.mod" | awk '{print $1}')
    if [[ "$before" == "$after" ]]; then pass "unified refusal leaves tools/go.mod untouched"
    else fail "unified refusal leaves tools/go.mod untouched (rewritten!)"; fi
    list_has goimports yes

    suite "install: same package under a different name"
    local p="$TMP_BASE/rename-dup"
    setup_project "$p"
    cd "$p" || return
    "$GOTOOLS" init --strategy=split >/dev/null 2>&1
    rc=0
    "$GOTOOLS" install "$TOOL_GOIMPORTS_PKG" >/dev/null 2>&1 || rc=$?
    if [[ $rc -ne 0 ]]; then fail "dup setup: install goimports"; return; fi
    local out=""
    rc=0
    out=$("$GOTOOLS" install myimports2 "$TOOL_GOIMPORTS_PKG" </dev/null 2>&1) || rc=$?
    if [[ $rc -eq 0 ]]; then pass "same-package install exits 0 (informs, does not refuse)"
    else fail_detail "same-package install exits 0" "expected: 0" "actual:   $rc ($out)"; fi
    if echo "$out" | grep -q "already installed as 'goimports'"; then pass "prints the already-installed info"
    else fail_detail "prints the already-installed info" "expected: mentions 'goimports' and its version" "actual:   $out"; fi
    if echo "$out" | grep -q "rename goimports myimports2"; then pass "suggests the rename command"
    else fail_detail "suggests the rename command" "expected: rename goimports myimports2" "actual:   $out"; fi
    list_has goimports yes
    list_has myimports2 yes
    rc=0
    "$GOTOOLS" install goimports "$TOOL_GOIMPORTS_PKG" </dev/null >/dev/null 2>&1 || rc=$?
    if [[ $rc -eq 2 ]]; then pass "same name+package still refuses (exit 2)"
    else fail_detail "same name+package still refuses" "expected: 2" "actual:   $rc"; fi
}

# ---------------------------------------------------------------------------
# Timeouts + lock reliability — real stalled proxy and real lock recovery
# ---------------------------------------------------------------------------
test_timeout_and_lock() {
    local tmpdir="$TMP_BASE/timeout-lock"
    setup_project "$tmpdir"
    cd "$tmpdir" || return

    "$GOTOOLS" init --strategy=split >/dev/null 2>&1

    # --- Real stall: a local proxy that accepts connections and never answers.
    suite "timeout: stalled proxy bounds install"
    if command -v python3 >/dev/null 2>&1; then
        local port=$((23000 + RANDOM % 10000))
        python3 - "$port" <<'PY' &
import socket, sys
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", int(sys.argv[1])))
s.listen(8)
conns = []
while True:
    # Keep every connection open forever: accept and never respond. (Dropping
    # the reference would let GC close the socket and the client would see
    # "connection reset" instead of a stall.)
    c, _ = s.accept()
    conns.append(c)
PY
        local srv=$!
        sleep 0.5
        local rc=0
        # Isolate the module cache: a warm cache would let go get succeed
        # without ever contacting the stalled proxy.
        GOPROXY="http://127.0.0.1:$port" GOSUMDB=off GOTOOLCHAIN=local \
            GOMODCACHE="$tmpdir/modcache" GOTOOLS_OPERATION_TIMEOUT=3 \
            "$GOTOOLS" install gofumpt mvdan.cc/gofumpt@v0.8.0 >timeout.out 2>&1 || rc=$?
        if [[ $rc -eq 3 ]]; then
            pass "stalled install exits 3 (E_NETWORK)"
        else
            fail_detail "stalled install exits 3 (E_NETWORK)" "expected: 3" "actual: $rc"
        fi
        if grep -q "timed out" timeout.out; then
            pass "stalled install prints timeout message"
        else
            fail_detail "stalled install prints timeout message" \
                "expected: 'timed out' in output" "actual: $(head -3 timeout.out)"
        fi
        if [[ -f tools/gofumpt.mod ]]; then
            fail_detail "stalled install cleans up created modfile" \
                "expected: tools/gofumpt.mod removed" "actual: file exists"
        else
            pass "stalled install cleans up created modfile"
        fi
        kill $srv 2>/dev/null
        wait $srv 2>/dev/null || true
    else
        echo "    SKIP stalled-proxy test (python3 not available)"
    fi

    # --- Lock env vars with the real toolchain ---
    suite "timeout: lock env vars"
    run_cmd "GOTOOLS_NO_LOCK=1 sync" env GOTOOLS_NO_LOCK=1 "$GOTOOLS" sync
    mkdir -p tools/.gotools.lock
    touch -t 202001010000 tools/.gotools.lock
    local output
    output=$("$GOTOOLS" sync 2>&1) || true
    if [[ "$output" == *"Stale lock detected"* ]]; then
        pass "sync removes a stale legacy lock"
    else
        fail_detail "sync removes a stale legacy lock" "expected: 'Stale lock detected' in output" "actual:   $output"
    fi
    if [[ ! -d tools/.gotools.lock ]]; then
        pass "stale lock dir removed after sync"
    else
        fail_detail "stale lock dir removed after sync" "expected: lock dir gone" "actual: still present"
    fi
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
test_doctor
test_timeout_and_lock
test_path_validation
test_rename

echo ""
echo "Results: $PASSED passed, $FAILED failed"
[[ $FAILED -eq 0 ]] || exit 1
