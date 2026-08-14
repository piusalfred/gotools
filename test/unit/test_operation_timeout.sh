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

# Operation timeout tests (issue #27): GOTOOLS_OPERATION_TIMEOUT bounds go
# network operations (go get -tool / go mod tidy). The watchdog path is
# exercised with a hanging stub go; the GNU timeout(1) dispatcher path is
# exercised with a recording fake `timeout` in STUB_BIN. Timeouts exit
# E_NETWORK (3) with a clear message and leave no partial state behind.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_helpers.sh"

GOTOOLS_SH="$SCRIPT_DIR/../../gotools.sh"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT
STUB_BIN="$TMPDIR/stub-bin"
mkdir -p "$STUB_BIN"

# Stub go: MODE=hang sleeps on network operations (simulating a stalled
# proxy); MODE=ok exits immediately.
cat > "$STUB_BIN/go" <<'STUB'
#!/usr/bin/env bash
case "${1:-}" in
    env) echo "go1.24"; exit 0 ;;
    *)
        if [[ "${MODE:-ok}" == "hang" ]]; then sleep 30; fi
        exit 0
        ;;
esac
STUB
chmod +x "$STUB_BIN/go"

write_manifest() {
    local dir="$1"
    cat > "$dir/.gotools.json" <<'EOF'
{
  "version": 1,
  "strategy": "split",
  "dir": "tools",
  "go_version": "1.24",
  "module_prefix": "example.com/test",
  "tools": {
    "gofumpt": {
      "source": "go",
      "package": "mvdan.cc/gofumpt",
      "version": "v0.8.0"
    }
  }
}
EOF
}

# Manifest with no tools — install tests must not hit the duplicate check.
write_empty_manifest() {
    local dir="$1"
    cat > "$dir/.gotools.json" <<'EOF'
{
  "version": 1,
  "strategy": "split",
  "dir": "tools",
  "go_version": "1.24",
  "module_prefix": "example.com/test",
  "tools": {}
}
EOF
}

write_split_modfile() {
    local dir="$1"
    mkdir -p "$dir/tools"
    cat > "$dir/tools/gofumpt.mod" <<'EOF'
module example.com/test/tools/gofumpt

go 1.24

tool mvdan.cc/gofumpt

require mvdan.cc/gofumpt v0.8.0
EOF
    touch "$dir/tools/gofumpt.sum"
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

category "install: watchdog kills a stalled go get"
S="$TMPDIR/hang-install"
mkdir -p "$S"
write_empty_manifest "$S"
rc=$(run_rc "$S" "MODE=hang" "GOTOOLS_OPERATION_TIMEOUT=2" -- install mvdan.cc/gofumpt@v0.8.0)
assert_eq "stalled install exits 3 (E_NETWORK)" "3" "$rc"
out=$(run_out "$S" "MODE=hang" "GOTOOLS_OPERATION_TIMEOUT=2" -- install mvdan.cc/gofumpt@v0.8.0)
assert_contains "timeout message" "timed out after 2s" "$out"
assert_file_absent "created modfile cleaned up on timeout" "$S/tools/gofumpt.mod"
assert_file_absent "created sum cleaned up on timeout" "$S/tools/gofumpt.sum"

category "install: no false timeout on fast operations"
S="$TMPDIR/ok-install"
mkdir -p "$S"
write_empty_manifest "$S"
rc=$(run_rc "$S" "MODE=ok" "GOTOOLS_OPERATION_TIMEOUT=120" -- install mvdan.cc/gofumpt@v0.8.0)
assert_eq "fast install exits 0" "0" "$rc"
# Fresh sandbox: the run_rc install above wrote the manifest, so a second
# install against the same dir would hit the duplicate check.
S2="$TMPDIR/ok-install-out"
mkdir -p "$S2"
write_empty_manifest "$S2"
out=$(run_out "$S2" "MODE=ok" "GOTOOLS_OPERATION_TIMEOUT=120" -- install mvdan.cc/gofumpt@v0.8.0)
assert_contains "fast install succeeds" "Installed gofumpt" "$out"

category "upgrade: watchdog kills a stalled go get"
S="$TMPDIR/hang-upgrade"
mkdir -p "$S"
write_manifest "$S"
write_split_modfile "$S"
rc=$(run_rc "$S" "MODE=hang" "GOTOOLS_OPERATION_TIMEOUT=2" -- upgrade all)
assert_eq "stalled upgrade exits 3 (E_NETWORK)" "3" "$rc"
out=$(run_out "$S" "MODE=hang" "GOTOOLS_OPERATION_TIMEOUT=2" -- upgrade all)
assert_contains "upgrade timeout message" "timed out after 2s" "$out"

category "purge --restore: watchdog kills a stalled go get"
S="$TMPDIR/hang-restore"
mkdir -p "$S"
cp "$SCRIPT_DIR/../main.go" "$SCRIPT_DIR/../go.mod" "$S/"
write_manifest "$S"
rc=0
(cd "$S" && echo YES | env -u GOTOOLS_STRATEGY -u GOTOOLS_DIR -u GOTOOLS_GO_VERSION      -u GOTOOLS_MODULE_PREFIX MODE=hang GOTOOLS_OPERATION_TIMEOUT=2      PATH="$STUB_BIN:/usr/bin:/bin"      "$BASH" "$GOTOOLS_SH" purge --restore >/dev/null 2>&1) || rc=$?
assert_eq "stalled restore exits 3 (E_NETWORK)" "3" "$rc"
# A failed restore must leave gotools state intact (nothing purged).
assert_file_exists "manifest intact after failed restore" "$S/.gotools.json"

# Recording fake timeout(1): logs its args, then runs the command. Placing
# it in STUB_BIN exercises the GNU-timeout dispatcher branch of _go_timeout
# on any machine. Created only AFTER the watchdog-path tests above — it must
# not bypass the watchdog there.
cat > "$STUB_BIN/timeout" <<'STUB'
#!/usr/bin/env bash
echo "$*" >> "${TIMEOUT_LOG:-/dev/null}"
shift
exec "$@"
STUB
chmod +x "$STUB_BIN/timeout"

category "dispatcher: GNU timeout(1) branch"
S="$TMPDIR/gnu-timeout"
mkdir -p "$S"
write_empty_manifest "$S"
rc=$(run_rc "$S" "MODE=ok" "GOTOOLS_OPERATION_TIMEOUT=5" "TIMEOUT_LOG=$S/timeout.log" -- install mvdan.cc/gofumpt@v0.8.0)
assert_eq "install via fake timeout exits 0" "0" "$rc"
if [[ -f "$S/timeout.log" ]]; then
    logged=$(cat "$S/timeout.log")
    assert_contains "timeout(1) received the configured seconds" "5" "$logged"
    assert_contains "timeout(1) wrapped the go get" "get" "$logged"
else
    echo "    FAIL timeout(1) received the configured seconds"
    echo "          expected: timeout.log exists"
    TEST_FAILED=$((TEST_FAILED + 1))
fi

category "dispatcher: GOTOOLS_OPERATION_TIMEOUT=0 disables"
S="$TMPDIR/disabled"
mkdir -p "$S"
write_empty_manifest "$S"
rc=$(run_rc "$S" "MODE=ok" "GOTOOLS_OPERATION_TIMEOUT=0" "TIMEOUT_LOG=$S/timeout.log" -- install mvdan.cc/gofumpt@v0.8.0)
assert_eq "disabled timeout install exits 0" "0" "$rc"
assert_file_absent "timeout(1) not used when disabled" "$S/timeout.log"

category "dispatcher: garbage timeout value falls back to default"
S="$TMPDIR/garbage"
mkdir -p "$S"
write_empty_manifest "$S"
rc=$(run_rc "$S" "MODE=ok" "GOTOOLS_OPERATION_TIMEOUT=abc" -- install mvdan.cc/gofumpt@v0.8.0)
assert_eq "garbage timeout treated as default" "0" "$rc"

category "helper: _run_with_timeout"
rc=0; _run_with_timeout 5 true || rc=$?
assert_eq "fast command returns 0" "0" "$rc"

rc=0; out=$(_run_with_timeout 5 bash -c 'echo hello-out; echo hello-err >&2' 2>&1) || rc=$?
assert_eq "output passthrough rc" "0" "$rc"
assert_contains "stdout passthrough" "hello-out" "$out"
assert_contains "stderr passthrough" "hello-err" "$out"

rc=0; _run_with_timeout 5 bash -c 'exit 7' || rc=$?
assert_eq "exit code passthrough" "7" "$rc"

rc=0; S1=$(date +%s); _run_with_timeout 1 sleep 30 || rc=$?; E1=$(date +%s)
assert_eq "hanging command returns 124" "124" "$rc"
if [[ $((E1 - S1)) -le 5 ]]; then
    echo "    PASS watchdog kills within budget"
    TEST_PASSED=$((TEST_PASSED + 1))
else
    echo "    FAIL watchdog kills within budget"
    echo "          actual elapsed: $((E1 - S1))s"
    TEST_FAILED=$((TEST_FAILED + 1))
fi

finish
