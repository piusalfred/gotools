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

# Manifest "settings" block tests: parse/flush/defaults, precedence
# (flag > env > manifest > default), config-command access (KEY VALUE and
# KEY=VALUE forms), and trace streaming (stderr default, stdout on request).

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_helpers.sh"

GOTOOLS_SH="$SCRIPT_DIR/../../gotools.sh"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT
STUB_BIN="$TMPDIR/stub-bin"
mkdir -p "$STUB_BIN"

# Stub go: MODE=hang sleeps on network operations; MODE=ok exits fast.
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
    local dir="$1" settings="${2:-none}"
    local settings_block=""
    if [[ "$settings" == "custom" ]]; then
        settings_block='  "settings": {
    "operation_timeout": 60,
    "trace": true
  },'
    elif [[ "$settings" == "lock" ]]; then
        settings_block='  "settings": {
    "lock_timeout": 3
  },'
    elif [[ "$settings" == "nolock" ]]; then
        settings_block='  "settings": {
    "no_lock": true
  },'
    elif [[ "$settings" == "all" ]]; then
        settings_block='  "settings": {
    "jobs": 2,
    "lock_stale_timeout": 90,
    "lock_timeout": 3,
    "no_lock": true,
    "offline": true,
    "operation_timeout": 45,
    "trace": true,
    "verbose": true
  },'
    fi
    cat > "$dir/.gotools.json" <<EOF
{
  "version": 1,
  "strategy": "split",
  "dir": "tools",
  "go_version": "1.24",
  "module_prefix": "example.com/test",
${settings_block}
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
         -u GOTOOLS_MODULE_PREFIX -u GOTOOLS_OFFLINE -u GOTOOLS_JOBS \
         -u GOTOOLS_OPERATION_TIMEOUT -u GOTOOLS_LOCK_TIMEOUT \
         -u GOTOOLS_LOCK_STALE_TIMEOUT -u GOTOOLS_NO_LOCK -u GOTOOLS_TRACE \
         -u GOTOOLS_VERBOSE PATH="$STUB_BIN:/usr/bin:/bin" \
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
         -u GOTOOLS_MODULE_PREFIX -u GOTOOLS_OFFLINE -u GOTOOLS_JOBS \
         -u GOTOOLS_OPERATION_TIMEOUT -u GOTOOLS_LOCK_TIMEOUT \
         -u GOTOOLS_LOCK_STALE_TIMEOUT -u GOTOOLS_NO_LOCK -u GOTOOLS_TRACE \
         -u GOTOOLS_VERBOSE PATH="$STUB_BIN:/usr/bin:/bin" \
         ${env_args[@]+"${env_args[@]}"} \
         "$BASH" "$GOTOOLS_SH" "$@" </dev/null 2>&1) || true
    echo "$out"
}

category "settings: init writes the full defaults block"
S="$TMPDIR/init"
mkdir -p "$S"
cp "$SCRIPT_DIR/../main.go" "$SCRIPT_DIR/../go.mod" "$S/"
rc=$(run_rc "$S" -- init --strategy=split)
assert_eq "init exits 0" "0" "$rc"
out=$(cat "$S/.gotools.json")
for k in '"offline": false' '"jobs": 1' '"lock_stale_timeout": 300' '"lock_timeout": 10' \
         '"no_lock": false' '"operation_timeout": 120' '"trace": false' '"verbose": false'; do
    assert_contains "init manifest carries default $k" "$k" "$out"
done

category "settings: parse and flush"
S="$TMPDIR/parse"
mkdir -p "$S"
write_manifest "$S" custom
# Parse in-process and check the store.
_manifest_parse "$S/.gotools.json"
assert_eq "parsed operation_timeout" "60" "$(_settings_get operation_timeout)"
assert_eq "parsed trace" "true" "$(_settings_get trace)"
# Flush: custom values preserved, missing keys filled with defaults.
_manifest_flush "$S/.gotools.json"
out=$(cat "$S/.gotools.json")
assert_contains "flush preserves custom value" '"operation_timeout": 60' "$out"
assert_contains "flush preserves boolean" '"trace": true' "$out"
assert_contains "flush fills missing defaults" '"jobs": 1' "$out"
assert_contains "flush fills missing defaults (verbose)" '"verbose": false' "$out"

S="$TMPDIR/parse-defaults"
mkdir -p "$S"
write_manifest "$S" none
_manifest_parse "$S/.gotools.json"
_manifest_flush "$S/.gotools.json"
out=$(cat "$S/.gotools.json")
assert_contains "flush adds settings block to legacy manifest" '"settings": {' "$out"
assert_contains "legacy manifest gets default" '"operation_timeout": 120' "$out"

category "settings: precedence (env > manifest > default)"
S="$TMPDIR/precedence"
mkdir -p "$S"
write_manifest "$S" custom
# Run in the main shell (assertion counters must not be lost to a subshell);
# restore _ORIG_ENV_* state afterwards.
_SAVE_OE_TIMEOUT="$_ORIG_ENV_OPERATION_TIMEOUT"
_ORIG_ENV_OPERATION_TIMEOUT=""
_manifest_parse "$S/.gotools.json"
assert_eq "manifest value wins over default" "60" "$(_settings_resolve operation_timeout)"
assert_eq "manifest boolean wins over default" "true" "$(_settings_resolve trace)"
assert_eq "missing key falls back to default" "10" "$(_settings_resolve lock_timeout)"
assert_eq "missing key default (jobs)" "1" "$(_settings_resolve jobs)"
_ORIG_ENV_OPERATION_TIMEOUT="30"
assert_eq "env wins over manifest" "30" "$(_settings_resolve operation_timeout)"
_ORIG_ENV_OPERATION_TIMEOUT="0"
assert_eq "explicit env 0 wins over manifest" "0" "$(_settings_resolve operation_timeout)"
_ORIG_ENV_OPERATION_TIMEOUT="$_SAVE_OE_TIMEOUT"
assert_eq "boolean normalization" "true" "$(_settings_normalize offline 1)"
assert_eq "boolean normalization false" "false" "$(_settings_normalize offline 0)"
assert_eq "garbage number falls back" "120" "$(_settings_normalize operation_timeout abc)"
assert_eq "garbage boolean falls back" "false" "$(_settings_normalize verbose maybe)"
assert_eq "trace stdout value" "stdout" "$(_settings_normalize trace stdout)"

category "settings: manifest values drive commands"
# operation_timeout: env override beats the manifest value (2s vs 60s) —
# if the manifest value won, this would wait 60s instead of failing fast.
S="$TMPDIR/drive-timeout-env"
mkdir -p "$S"
write_manifest "$S" custom
rc=$(run_rc "$S" "MODE=hang" "GOTOOLS_OPERATION_TIMEOUT=2" -- install mvdan.cc/gofumpt@v0.8.0)
assert_eq "env timeout overrides manifest" "3" "$rc"

# lock_timeout from the manifest bounds the wait against a live lock.
S="$TMPDIR/drive-lock"
mkdir -p "$S"
write_manifest "$S" lock
mkdir -p "$S/tools/.gotools.lock"
sleep 300 & LIVE_PID=$!
trap 'kill $LIVE_PID 2>/dev/null; wait $LIVE_PID 2>/dev/null || true; rm -rf "$TMPDIR"' EXIT
echo "$LIVE_PID" > "$S/tools/.gotools.lock/pid"
rc=$(run_rc "$S" -- sync)
assert_eq "manifest lock_timeout bounds the wait (exit 4)" "4" "$rc"
kill $LIVE_PID 2>/dev/null; wait $LIVE_PID 2>/dev/null || true
rm -rf "$S/tools/.gotools.lock"

S="$TMPDIR/drive-no-lock"
mkdir -p "$S"
write_manifest "$S" nolock
mkdir -p "$S/tools/.gotools.lock"
sleep 300 & LIVE_PID2=$!
echo "$LIVE_PID2" > "$S/tools/.gotools.lock/pid"
rc=$(run_rc "$S" -- sync)
assert_eq "manifest no_lock skips a held lock" "0" "$rc"
kill $LIVE_PID2 2>/dev/null; wait $LIVE_PID2 2>/dev/null || true
rm -rf "$S/tools/.gotools.lock"

category "settings: manifest offline drives sync"
S="$TMPDIR/drive-offline"
mkdir -p "$S"
write_manifest "$S" all
write_split_modfile "$S"
rc=$(run_rc "$S" -- sync)
assert_eq "manifest offline refuses sync without fingerprint" "6" "$rc"
out=$(run_out "$S" -- sync)
assert_contains "offline message" "Offline mode" "$out"

category "settings: config command"
S="$TMPDIR/config"
mkdir -p "$S"
write_manifest "$S" none
rc=$(run_rc "$S" -- config GOTOOLS_TRACE=1)
assert_eq "config KEY=VALUE sets trace" "0" "$rc"
out=$(cat "$S/.gotools.json")
assert_contains "manifest carries trace true" '"trace": true' "$out"
rc=$(run_rc "$S" -- config GOTOOLS_OPERATION_TIMEOUT 45)
assert_eq "config KEY VALUE sets timeout" "0" "$rc"
out=$(cat "$S/.gotools.json")
assert_contains "manifest carries operation_timeout 45" '"operation_timeout": 45' "$out"
out=$(run_out "$S" -- config GOTOOLS_TRACE)
assert_eq "config GET shows value" "true" "$out"
out=$(run_out "$S" -- config GOTOOLS_LOCK_STALE_TIMEOUT)
assert_eq "config GET falls back to default" "300" "$out"
rc=$(run_rc "$S" -- config GOTOOLS_TRACE maybe)
assert_eq "config invalid trace value exits 2" "2" "$rc"
rc=$(run_rc "$S" -- config GOTOOLS_JOBS abc)
assert_eq "config invalid numeric value exits 2" "2" "$rc"
rc=$(run_rc "$S" -- config NOPE 1)
assert_eq "config unknown key exits 2" "2" "$rc"

category "trace: stream to stderr by default, stdout on request"
S="$TMPDIR/trace"
mkdir -p "$S"
write_manifest "$S" none
write_split_modfile "$S"
# stderr stream: stdout must stay clean.
out=$(cd "$S" && env -u GOTOOLS_STRATEGY -u GOTOOLS_DIR -u GOTOOLS_GO_VERSION \
     -u GOTOOLS_MODULE_PREFIX GOTOOLS_TRACE=1 PATH="$STUB_BIN:/usr/bin:/bin" \
     "$BASH" "$GOTOOLS_SH" exec gofumpt </dev/null 2>/dev/null) || true
if [[ "$out" == *'"level"'* ]]; then
    echo "    FAIL trace default stream keeps stdout clean"
    TEST_FAILED=$((TEST_FAILED + 1))
else
    echo "    PASS trace default stream keeps stdout clean"
    TEST_PASSED=$((TEST_PASSED + 1))
fi
out=$(cd "$S" && env -u GOTOOLS_STRATEGY -u GOTOOLS_DIR -u GOTOOLS_GO_VERSION \
     -u GOTOOLS_MODULE_PREFIX GOTOOLS_TRACE=1 PATH="$STUB_BIN:/usr/bin:/bin" \
     "$BASH" "$GOTOOLS_SH" exec gofumpt </dev/null 2>&1 >/dev/null) || true
assert_contains "trace record streamed to stderr" '"level":"info"' "$out"
assert_file_exists "trace file appended" "$S/.gotools_trace.log"
# stdout stream: record on stdout.
out=$(cd "$S" && env -u GOTOOLS_STRATEGY -u GOTOOLS_DIR -u GOTOOLS_GO_VERSION \
     -u GOTOOLS_MODULE_PREFIX GOTOOLS_TRACE=stdout PATH="$STUB_BIN:/usr/bin:/bin" \
     "$BASH" "$GOTOOLS_SH" exec gofumpt </dev/null 2>/dev/null) || true
assert_contains "trace stdout mode streams to stdout" '"level":"info"' "$out"
# Manifest trace:true behaves like GOTOOLS_TRACE=1.
S2="$TMPDIR/trace-manifest"
mkdir -p "$S2"
write_manifest "$S2" custom
write_split_modfile "$S2"
out=$(cd "$S2" && env -u GOTOOLS_STRATEGY -u GOTOOLS_DIR -u GOTOOLS_GO_VERSION \
     -u GOTOOLS_MODULE_PREFIX -u GOTOOLS_TRACE PATH="$STUB_BIN:/usr/bin:/bin" \
     "$BASH" "$GOTOOLS_SH" exec gofumpt </dev/null 2>&1 >/dev/null) || true
assert_contains "manifest trace true streams to stderr" '"level":"info"' "$out"

finish
