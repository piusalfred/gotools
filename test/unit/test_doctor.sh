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

# doctor command tests (issue #28). Every check exercised in healthy and
# failure states via a stub `go`. Doctor is pure diagnostics: check results
# (pass/warn/fail/skip) NEVER fail the shell — only usage errors exit 2.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_helpers.sh"

GOTOOLS_SH="$SCRIPT_DIR/../../gotools.sh"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT
STUB_BIN="$TMPDIR/stub-bin"
mkdir -p "$STUB_BIN"

# Stub go for doctor. MODE selects the failure scenario. On `tool`/`mod`
# invocations the probe environment's GOPROXY is appended to $PROBE_LOG —
# offline defense-in-depth asserts that probes run with GOPROXY=off.
cat > "$STUB_BIN/go" <<'STUB'
#!/usr/bin/env bash
case "${1:-}" in
    env)
        case "${2:-}" in
            GOVERSION)
                if [[ "${MODE:-ok}" == "old" ]]; then echo "go1.23.9"; exit 0; fi
                if [[ "${MODE:-ok}" == "empty" ]]; then exit 0; fi
                echo "go1.24.3"; exit 0
                ;;
            GOPROXY) echo "https://proxy.golang.org,direct"; exit 0 ;;
            *) exit 0 ;;
        esac
        ;;
    list)
        if [[ "${MODE:-ok}" == "netfail" ]]; then
            echo "go: dial tcp: lookup proxy.golang.org: no such host" >&2
            exit 1
        fi
        exit 0
        ;;
    tool)
        if [[ -n "${PROBE_LOG:-}" ]]; then echo "${GOPROXY:-<unset>}" >> "$PROBE_LOG"; fi
        if [[ "${MODE:-ok}" == "forbid" ]]; then
            echo "go: unexpected invocation" >&2
            exit 1
        fi
        if [[ "${MODE:-ok}" == "toolfail" && "${@: -1}" == "staticcheck" ]]; then
            echo 'go: no such tool "staticcheck"' >&2
            exit 1
        fi
        if [[ "${MODE:-ok}" == "usageexit" && "${@: -1}" == "staticcheck" ]]; then
            echo "usage: staticcheck [flags] [packages]" >&2
            exit 2
        fi
        exit 0
        ;;
    mod)
        if [[ -n "${PROBE_LOG:-}" ]]; then echo "${GOPROXY:-<unset>}" >> "$PROBE_LOG"; fi
        if [[ "${MODE:-ok}" == "verifyfail" ]]; then
            echo "go: dir has been modified" >&2
            exit 1
        fi
        if [[ "${MODE:-ok}" == "forbid" ]]; then
            echo "go: unexpected invocation" >&2
            exit 1
        fi
        echo "all modules verified"
        exit 0
        ;;
    *)
        if [[ "${MODE:-ok}" == "forbid" ]]; then
            echo "go: unexpected invocation: $*" >&2
            exit 1
        fi
        exit 0
        ;;
esac
STUB
chmod +x "$STUB_BIN/go"

write_manifest() {
    local dir="$1" strategy="${2:-split}" version="${3:-1}"
    cat > "$dir/.gotools.json" <<EOF
{
  "version": $version,
  "strategy": "$strategy",
  "dir": "tools",
  "go_version": "inherit",
  "module_prefix": "example.com/test",
  "tools": {
    "gofumpt": {
      "source": "go",
      "package": "mvdan.cc/gofumpt",
      "version": "v0.8.0"
    },
    "staticcheck": {
      "source": "go",
      "package": "honnef.co/go/tools/cmd/staticcheck",
      "version": "v0.6.0"
    }
  }
}
EOF
}

write_split_modfiles() {
    local dir="$1"
    mkdir -p "$dir/tools"
    # Note: the staticcheck package is a SUBPACKAGE of its module — the
    # proxy check must resolve honnef.co/go/tools/cmd/staticcheck to the
    # module root honnef.co/go/tools via the require block.
    cat > "$dir/tools/gofumpt.mod" <<'EOF'
module example.com/test/tools/gofumpt

go 1.24

tool mvdan.cc/gofumpt

require mvdan.cc/gofumpt v0.8.0
EOF
    touch "$dir/tools/gofumpt.sum"
    cat > "$dir/tools/staticcheck.mod" <<'EOF'
module example.com/test/tools/staticcheck

go 1.24

tool honnef.co/go/tools/cmd/staticcheck

require honnef.co/go/tools v0.6.0
EOF
    touch "$dir/tools/staticcheck.sum"
}

setup_project() {
    local dir="$1"
    mkdir -p "$dir"
    write_manifest "$dir"
    write_split_modfiles "$dir"
}

# run_rc <dir> [env VAR=val ...] -- <args...> — run gotools.sh, print exit code.
run_rc() {
    local dir="$1"
    shift
    local -a env_args=()
    while [[ $# -gt 0 && "$1" != "--" ]]; do
        env_args+=("$1")
        shift
    done
    [[ "${1:-}" == "--" ]] && shift
    local rc=0
    (cd "$dir" && env -u GOTOOLS_STRATEGY -u GOTOOLS_DIR -u GOTOOLS_GO_VERSION \
         -u GOTOOLS_MODULE_PREFIX PATH="$STUB_BIN:/usr/bin:/bin" \
         ${env_args[@]+"${env_args[@]}"} \
         "$BASH" "$GOTOOLS_SH" "$@" </dev/null >/dev/null 2>&1) || rc=$?
    echo "$rc"
}

# run_out <dir> [env VAR=val ...] -- <args...> — run gotools.sh, print output.
run_out() {
    local dir="$1"
    shift
    local -a env_args=()
    while [[ $# -gt 0 && "$1" != "--" ]]; do
        env_args+=("$1")
        shift
    done
    [[ "${1:-}" == "--" ]] && shift
    local out=""
    out=$(cd "$dir" && env -u GOTOOLS_STRATEGY -u GOTOOLS_DIR -u GOTOOLS_GO_VERSION \
         -u GOTOOLS_MODULE_PREFIX PATH="$STUB_BIN:/usr/bin:/bin" \
         ${env_args[@]+"${env_args[@]}"} \
         "$BASH" "$GOTOOLS_SH" "$@" </dev/null 2>&1) || true
    echo "$out"
}

category "doctor healthy"
S="$TMPDIR/healthy"
setup_project "$S"
rc=$(run_rc "$S" -- doctor)
assert_eq "healthy doctor exits 0" "0" "$rc"
out=$(run_out "$S" -- doctor)
assert_contains "doctor banner" "🔍 gotools doctor" "$out"
assert_contains "go heading" "Go installation" "$out"
assert_contains "proxy heading" "Module proxy" "$out"
assert_contains "config heading" "Configuration" "$out"
assert_contains "tools heading with count" "Managed tools (2 declared)" "$out"
assert_contains "lock heading" "Lock file" "$out"
assert_contains "integrity heading" "Module integrity" "$out"
assert_contains "disk heading" "Disk usage" "$out"
assert_contains "go version detail" "Go 1.24.3" "$out"
assert_contains "proxy reachable" "reachable" "$out"
assert_contains "strategy matches" "Strategy: split — matches disk" "$out"
assert_contains "tool runnable" "gofumpt@v0.8.0 — runnable" "$out"
assert_contains "staticcheck runnable" "staticcheck@v0.6.0 — runnable" "$out"
assert_contains "integrity passed" "go mod verify passed" "$out"
assert_contains "disk usage" "tools/ uses" "$out"
assert_contains "healthy summary" "All checks passed. Your environment is healthy." "$out"

category "doctor failures report but exit 0"
S="$TMPDIR/oldgo"
setup_project "$S"
rc=$(run_rc "$S" "MODE=old" -- doctor)
assert_eq "old go exits 0 (diagnostics)" "0" "$rc"
out=$(run_out "$S" "MODE=old" -- doctor)
assert_contains "old go reported" "Go 1.23.9 is too old" "$out"
assert_contains "old go summary counts issue" "1 issue found." "$out"

S="$TMPDIR/nogo"
setup_project "$S"
mkdir -p "$TMPDIR/empty-bin"
rc=$(run_rc "$S" "PATH=$TMPDIR/empty-bin" -- doctor)
assert_eq "go missing exits 0 (diagnostics)" "0" "$rc"
out=$(run_out "$S" "PATH=$TMPDIR/empty-bin" -- doctor)
assert_contains "go missing reported" "Go not installed" "$out"

S="$TMPDIR/netfail"
setup_project "$S"
rc=$(run_rc "$S" "MODE=netfail" -- doctor)
assert_eq "proxy unreachable exits 0 (warn only)" "0" "$rc"
out=$(run_out "$S" "MODE=netfail" -- doctor)
assert_contains "proxy unreachable detail" "unreachable: go: dial tcp" "$out"
assert_contains "warn summary" "1 issue found." "$out"

S="$TMPDIR/toolfail"
setup_project "$S"
rc=$(run_rc "$S" "MODE=toolfail" -- doctor)
assert_eq "tool go-level failure exits 0 (warn only)" "0" "$rc"
out=$(run_out "$S" "MODE=toolfail" -- doctor)
assert_contains "not runnable warn" 'not runnable: go: no such tool "staticcheck"' "$out"
assert_contains "hints to sync" "(run 'gotools sync')" "$out"

S="$TMPDIR/usageexit"
setup_project "$S"
rc=$(run_rc "$S" "MODE=usageexit" -- doctor)
assert_eq "tool usage-error exit is still runnable" "0" "$rc"
out=$(run_out "$S" "MODE=usageexit" -- doctor)
assert_contains "staticcheck usage exit is runnable" "staticcheck@v0.6.0 — runnable" "$out"

S="$TMPDIR/verifyfail"
setup_project "$S"
rc=$(run_rc "$S" "MODE=verifyfail" -- doctor)
assert_eq "verify failure exits 0 (diagnostics)" "0" "$rc"
out=$(run_out "$S" "MODE=verifyfail" -- doctor)
assert_contains "verify failure detail" "go mod verify failed" "$out"

category "doctor lock checks"
S="$TMPDIR/stalelock"
setup_project "$S"
mkdir -p "$S/tools/.gotools.lock"
touch -t 202001010000 "$S/tools/.gotools.lock"
rc=$(run_rc "$S" -- doctor)
assert_eq "stale lock exits 0" "0" "$rc"
out=$(run_out "$S" -- doctor)
assert_contains "stale lock warn" "stale lock directory" "$out"

S="$TMPDIR/freshlock"
setup_project "$S"
mkdir -p "$S/tools/.gotools.lock"
out=$(run_out "$S" -- doctor)
assert_contains "fresh lock detail" "another gotools process may be running" "$out"

category "doctor config failures"
S="$TMPDIR/nomanifest"
mkdir -p "$S"
rc=$(run_rc "$S" -- doctor)
assert_eq "missing manifest exits 0" "0" "$rc"
out=$(run_out "$S" -- doctor)
assert_contains "missing manifest fail" "no .gotools.json — run 'gotools init'" "$out"

S="$TMPDIR/v2manifest"
mkdir -p "$S"
write_manifest "$S" split 2
rc=$(run_rc "$S" -- doctor)
assert_eq "v2 manifest exits 0" "0" "$rc"
out=$(run_out "$S" -- doctor)
assert_contains "v2 manifest reason" "requires schema version 2" "$out"
assert_contains "report renders despite bad manifest" "Go installation" "$out"

category "doctor strategy mismatch"
S="$TMPDIR/mismatch"
mkdir -p "$S/tools"
write_manifest "$S" split
cat > "$S/tools/go.mod" <<'EOF'
module example.com/test/tools

go 1.24

tool example.com/faketool
EOF
rc=$(run_rc "$S" -- doctor)
assert_eq "strategy mismatch exits 0 (warn)" "0" "$rc"
out=$(run_out "$S" -- doctor)
assert_contains "strategy mismatch warn" "disk looks like unified" "$out"

S="$TMPDIR/notsynced"
mkdir -p "$S"
write_manifest "$S" split
out=$(run_out "$S" -- doctor)
assert_contains "empty tools dir warn" "tools not synced — run 'gotools sync'" "$out"

category "doctor offline"
S="$TMPDIR/offline"
setup_project "$S"
rc=$(run_rc "$S" "PROBE_LOG=$S/probe.log" -- doctor --offline)
assert_eq "offline doctor exits 0" "0" "$rc"
out=$(run_out "$S" "PROBE_LOG=$S/probe.log" -- doctor --offline)
assert_contains "proxy skipped offline" "offline mode — reachability not checked" "$out"
if [[ -f "$S/probe.log" ]]; then
    probe_vals=$(sort -u "$S/probe.log")
    assert_eq "offline probes run with GOPROXY=off" "off" "$probe_vals"
else
    echo "    FAIL offline probes run with GOPROXY=off"
    echo "          expected: probe.log exists"
    TEST_FAILED=$((TEST_FAILED + 1))
fi

S="$TMPDIR/offline-env"
setup_project "$S"
rc=$(run_rc "$S" "GOTOOLS_OFFLINE=1" -- doctor)
assert_eq "GOTOOLS_OFFLINE=1 doctor exits 0" "0" "$rc"
out=$(run_out "$S" "GOTOOLS_OFFLINE=1" -- doctor)
assert_contains "GOTOOLS_OFFLINE=1 skips proxy" "offline mode — reachability not checked" "$out"

category "doctor --format json"
S="$TMPDIR/json"
setup_project "$S"
out=$(run_out "$S" -- doctor --format=json)
assert_contains "json schema_version" '"schema_version":1' "$out"
assert_contains "json healthy" '"healthy":true' "$out"
assert_contains "json issues" '"issues":0' "$out"
assert_contains "json checks array" '"checks":[{' "$out"
assert_contains "json go check" '"name":"go","status":"pass"' "$out"
assert_contains "json tools entry" '"name":"tools.gofumpt","status":"pass"' "$out"
alias_out=$(run_out "$S" -- doctor --json)
assert_eq "doctor --json is an alias for --format=json" "$out" "$alias_out"

out=$(run_out "$S" "MODE=netfail" -- doctor --format=json)
assert_contains "json unhealthy" '"healthy":false' "$out"
assert_contains "json issues count" '"issues":1' "$out"
assert_contains "json warn status" '"status":"warn"' "$out"

rc=$(run_rc "$S" -- doctor --format=yaml)
assert_eq "doctor --format=yaml exits 2" "2" "$rc"
rc=$(run_rc "$S" -- doctor --bogus)
assert_eq "doctor with stray arg exits 2" "2" "$rc"

category "doctor helpers"
# _version_meets_min (rc 0/1/2) — guarded under set -e.
if _version_meets_min "1.24.3" "1.24"; then pass_out=0; else pass_out=1; fi
assert_eq "_version_meets_min 1.24.3 >= 1.24" "0" "$pass_out"
if _version_meets_min "1.23.9" "1.24"; then pass_out=0; else pass_out=1; fi
assert_eq "_version_meets_min 1.23.9 < 1.24" "1" "$pass_out"
_version_meets_min "" "1.24" || pass_out=$?
assert_eq "_version_meets_min empty is undetectable" "2" "$pass_out"
if _version_meets_min "devel go1.26-abc123" "1.24"; then pass_out=0; else pass_out=1; fi
assert_eq "_version_meets_min devel string >= 1.24" "0" "$pass_out"

escaped=$(_json_escape 'a"b\nc	d')
assert_eq "_json_escape round-trip" 'a\"b\\nc\td' "$escaped"

# _doctor_run_check maps rc → status.
_t_pass() { echo "p detail"; return 0; }
_t_warn() { echo "w detail"; return 1; }
_t_fail() { echo "f detail"; return 2; }
_t_skip() { echo "s detail"; return 3; }
_doctor_run_check "t.pass" _t_pass
_doctor_run_check "t.warn" _t_warn
_doctor_run_check "t.fail" _t_fail
_doctor_run_check "t.skip" _t_skip
assert_contains "runner maps pass" "t.pass|pass|p detail" "$_DOCTOR_RESULTS"
assert_contains "runner maps warn" "t.warn|warn|w detail" "$_DOCTOR_RESULTS"
assert_contains "runner maps fail" "t.fail|fail|f detail" "$_DOCTOR_RESULTS"
assert_contains "runner maps skip" "t.skip|skip|s detail" "$_DOCTOR_RESULTS"

f="$TMPDIR/mtime-probe"
touch -t 202001010000 "$f"
mtime=$(_file_mtime "$f")
if [[ "$mtime" =~ ^[0-9]{10}$ ]]; then
    echo "    PASS _file_mtime returns epoch seconds"
    TEST_PASSED=$((TEST_PASSED + 1))
else
    echo "    FAIL _file_mtime returns epoch seconds"
    echo "          actual: $mtime"
    TEST_FAILED=$((TEST_FAILED + 1))
fi

finish
