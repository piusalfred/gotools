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

# --format json tests (issue #29). list/info/version accept --format=json and
# --format=text (plus --json/--text aliases); unknown formats and stray args
# exit 2. JSON output is write-only — no JSON parsing in gotools itself.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_helpers.sh"

GOTOOLS_SH="$SCRIPT_DIR/../../gotools.sh"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT
STUB_BIN="$TMPDIR/stub-bin"
mkdir -p "$STUB_BIN"

# Stub go: MODE=ok → tool probe succeeds; MODE=toolfail → go-level failure.
cat > "$STUB_BIN/go" <<'STUB'
#!/usr/bin/env bash
case "${1:-}" in
    env) echo "go1.24.3"; exit 0 ;;
    tool)
        if [[ "${MODE:-ok}" == "toolfail" ]]; then
            echo 'go: no such tool "faketool"' >&2
            exit 1
        fi
        exit 0
        ;;
    *) echo "go: unexpected invocation: $*" >&2; exit 1 ;;
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
  "go_version": "inherit",
  "module_prefix": "example.com/test",
  "tools": {
    "faketool": {
      "source": "go",
      "package": "example.com/faketool",
      "version": "v1.2.3"
    }
  }
}
EOF
}

write_split_modfile() {
    local dir="$1"
    mkdir -p "$dir/tools"
    cat > "$dir/tools/faketool.mod" <<'EOF'
module example.com/test/tools/faketool

go 1.24

tool example.com/faketool

require example.com/faketool v1.2.3
EOF
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
         -u GOTOOLS_MODULE_PREFIX ${env_args[@]+"${env_args[@]}"} \
         PATH="$STUB_BIN:/usr/bin:/bin" \
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
         -u GOTOOLS_MODULE_PREFIX ${env_args[@]+"${env_args[@]}"} \
         PATH="$STUB_BIN:/usr/bin:/bin" \
         "$BASH" "$GOTOOLS_SH" "$@" </dev/null 2>&1) || true
    echo "$out"
}

S="$TMPDIR/proj"
mkdir -p "$S"
write_manifest "$S"

category "list --format json"
out=$(run_out "$S" -- list --format=json)
assert_contains "list json has schema_version" '"schema_version":1' "$out"
assert_contains "list json has strategy" '"strategy":"split"' "$out"
assert_contains "list json has dir" '"dir":"tools"' "$out"
assert_contains "list json has go_version" '"go_version":"inherit"' "$out"
assert_contains "list json has tools array" '"tools":[{' "$out"
assert_contains "list json has tool entry" '"name":"faketool"' "$out"

alias_out=$(run_out "$S" -- list --json)
assert_eq "list --json is an alias for --format=json" "$out" "$alias_out"

out=$(run_out "$S" -- list --format=text)
assert_contains "list --format=text shows table header" "TOOL" "$out"
out=$(run_out "$S" -- list --text)
assert_contains "list --text shows table header" "TOOL" "$out"
assert_contains "list --text is not JSON" "faketool" "$out"

category "list --format errors"
out=$(run_out "$S" -- list --format=yaml)
assert_contains "list --format=yaml error message" "Unknown output format: yaml" "$out"
rc=$(run_rc "$S" -- list --format=yaml)
assert_eq "list --format=yaml exits 2" "2" "$rc"
rc=$(run_rc "$S" -- list bogus)
assert_eq "list with stray arg exits 2" "2" "$rc"

category "version --format json"
out=$(run_out "$S" -- version --format=json)
assert_contains "version json has schema_version" '"schema_version":1' "$out"
assert_contains "version json has name" '"name":"gotools.sh"' "$out"
assert_contains "version json has version" '"version":"v0' "$out"
alias_out=$(run_out "$S" -- version --json)
assert_eq "version --json is an alias for --format=json" "$out" "$alias_out"

out=$(run_out "$S" -- version --text)
assert_contains "version --text prints plain line" "gotools.sh v0" "$out"
rc=$(run_rc "$S" -- version extra)
assert_eq "version with stray arg exits 2" "2" "$rc"
rc=$(run_rc "$S" -- version --format json)
assert_eq "version --format json (space form) exits 2" "2" "$rc"

category "info --format json"
write_split_modfile "$S"
out=$(run_out "$S" -- info faketool --format=json)
assert_contains "info json has schema_version" '"schema_version":1' "$out"
assert_contains "info json has runnable field" '"runnable":true' "$out"
assert_contains "info json has package" '"package":"example.com/faketool"' "$out"

out=$(run_out "$S" -- info --format=json faketool)
assert_contains "info --format=json flag-first works" '"runnable":true' "$out"

out=$(run_out "$S" "MODE=toolfail" -- info faketool --format=json)
assert_contains "info json toolfail is not runnable" '"runnable":false' "$out"

# No modfile on disk → runnable:false with zero go calls (stub fails on any go call).
S2="$TMPDIR/nomod"
mkdir -p "$S2"
write_manifest "$S2"
out=$(run_out "$S2" -- info faketool --format=json)
assert_contains "info json without modfile reports not runnable" '"runnable":false' "$out"

out=$(run_out "$S2" -- info missing --format=json)
assert_contains "info json missing tool error object" '"error":"tool not found: missing"' "$out"
rc=$(run_rc "$S2" -- info missing --format=json)
assert_eq "info json missing tool exits 5" "5" "$rc"

rc=$(run_rc "$S" -- info faketool extra)
assert_eq "info with stray arg exits 2" "2" "$rc"

finish
