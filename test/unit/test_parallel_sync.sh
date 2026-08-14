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

# Parallel sync tests (issue #25): --jobs runs reinstalls concurrently for
# split/module, the parent merges the manifest once (no last-writer-wins
# races), unified stays serial with a warning, and a failing job aborts the
# sync with its structured code while leaving the manifest untouched.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_helpers.sh"

GOTOOLS_SH="$SCRIPT_DIR/../../gotools.sh"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT
STUB_BIN="$TMPDIR/stub-bin"
mkdir -p "$STUB_BIN"

# make_go_stub <mode>
#   success     go get writes tool+require into the target modfile, exit 0
#   netfail-one go get fails with a network error for gofumpt only
#   netfail     go get always fails with a network error
# The mode travels via the MODE env var (portable, no sed needed).
CURRENT_MODE="success"
make_go_stub() {
    local mode="$1"
    CURRENT_MODE="$mode"
    cat > "$STUB_BIN/go" <<'STUB'
#!/usr/bin/env bash
if [[ "${1:-}" == "env" ]]; then echo "go1.24"; exit 0; fi
if [[ "${2:-}" == "tidy" ]]; then touch go.sum; exit 0; fi
if [[ "${1:-}" == "get" ]]; then
    modfile="go.mod"
    pkg=""
    for a in "$@"; do
        if [[ "$a" == "-modfile="* ]]; then modfile="${a#-modfile=}"; fi
        pkg="$a"
    done
    if [[ "$MODE" == "netfail" ]] || { [[ "$MODE" == "netfail-one" && "$pkg" == *gofumpt* ]]; }; then
        echo "go: dial tcp: lookup proxy.golang.org: no such host" >&2
        exit 1
    fi
    base="${pkg%@*}"
    ver="${pkg#*@}"
    printf 'tool %s\n' "$base" >> "$modfile"
    printf 'require %s %s\n' "$base" "$ver" >> "$modfile"
    exit 0
fi
exit 0
STUB
    chmod +x "$STUB_BIN/go"
}

write_manifest() {
    local dir="$1" strategy="${2:-split}" tools="${3:-2}"
    local tools_block=""
    if [[ "$tools" == "2" ]]; then
        tools_block='    "gofumpt": {
      "source": "go",
      "package": "mvdan.cc/gofumpt",
      "version": "v0.8.0"
    },
    "staticcheck": {
      "source": "go",
      "package": "honnef.co/go/tools/cmd/staticcheck",
      "version": "v0.7.0"
    }'
    else
        tools_block='    "gofumpt": {
      "source": "go",
      "package": "mvdan.cc/gofumpt",
      "version": "v0.8.0"
    }'
    fi
    cat > "$dir/.gotools.json" <<EOF
{
  "version": 1,
  "strategy": "${strategy}",
  "dir": "tools",
  "go_version": "1.24",
  "module_prefix": "example.com/test",
  "tools": {
${tools_block}
  }
}
EOF
}

run_rc() {
    local dir="$1"
    shift
    local rc=0
    (cd "$dir" && env -u GOTOOLS_STRATEGY -u GOTOOLS_DIR -u GOTOOLS_GO_VERSION \
         -u GOTOOLS_MODULE_PREFIX MODE="$CURRENT_MODE" PATH="$STUB_BIN:/usr/bin:/bin" \
         "$BASH" "$GOTOOLS_SH" "$@" </dev/null >/dev/null 2>&1) || rc=$?
    echo "$rc"
}

run_out() {
    local dir="$1"
    shift
    local out=""
    out=$(cd "$dir" && env -u GOTOOLS_STRATEGY -u GOTOOLS_DIR -u GOTOOLS_GO_VERSION \
         -u GOTOOLS_MODULE_PREFIX MODE="$CURRENT_MODE" PATH="$STUB_BIN:/usr/bin:/bin" \
         "$BASH" "$GOTOOLS_SH" "$@" </dev/null 2>&1) || true
    echo "$out"
}

category "parallel sync"

# 1. _parallel_for runs every group at jobs=1 and jobs=3.
for jobs in 1 3; do
    D="$TMPDIR/pf-$jobs"
    mkdir -p "$D"
    for i in 1 2 3 4; do
        rm -f "$D/marker-$i"
    done
    mark() { touch "$1"; }
    _parallel_for "$jobs" mark \
        "$D/marker-1" "::" "$D/marker-2" "::" "$D/marker-3" "::" "$D/marker-4" "::" >/dev/null 2>&1
    all=true
    for i in 1 2 3 4; do
        [[ -f "$D/marker-$i" ]] || all=false
    done
    if $all; then
        echo "    PASS _parallel_for runs all groups (jobs=$jobs)"
        TEST_PASSED=$((TEST_PASSED + 1))
    else
        echo "    FAIL _parallel_for runs all groups (jobs=$jobs)"
        TEST_FAILED=$((TEST_FAILED + 1))
    fi
done

# 2. Parallel reinstall: both tools land in the manifest (no merge race).
S="$TMPDIR/merge"
mkdir -p "$S"
write_manifest "$S" split 2
make_go_stub success
rc=$(run_rc "$S" sync --jobs 2)
assert_eq "parallel sync exits 0" "0" "$rc"
grep -q gofumpt "$S/.gotools.json"
assert_eq "manifest keeps gofumpt after parallel install" "0" "$?"
grep -q staticcheck "$S/.gotools.json"
assert_eq "manifest keeps staticcheck after parallel install" "0" "$?"
assert_file_exists "fingerprint written" "$S/tools/.gotools.fingerprint"

# 3. Serial parity: --jobs 1 produces the same result.
S="$TMPDIR/serial"
mkdir -p "$S"
write_manifest "$S" split 2
make_go_stub success
rc=$(run_rc "$S" sync --jobs 1)
assert_eq "serial sync exits 0" "0" "$rc"
grep -q gofumpt "$S/.gotools.json"
assert_eq "serial install keeps gofumpt" "0" "$?"
grep -q staticcheck "$S/.gotools.json"
assert_eq "serial install keeps staticcheck" "0" "$?"

# 4. unified stays serial and warns only when --jobs > 1 is explicit.
S="$TMPDIR/unified"
mkdir -p "$S"
write_manifest "$S" unified 2
make_go_stub success
out=$(run_out "$S" sync --jobs 4)
assert_contains "unified warns about serial installs" "unified strategy: installs are serial (shared go.mod)." "$out"
S2="$TMPDIR/unified-default"
mkdir -p "$S2"
write_manifest "$S2" unified 2
out=$(run_out "$S2" sync)
if [[ "$out" == *"installs are serial"* ]]; then
    echo "    FAIL default jobs prints no unified warning"
    TEST_FAILED=$((TEST_FAILED + 1))
else
    echo "    PASS default jobs prints no unified warning"
    TEST_PASSED=$((TEST_PASSED + 1))
fi

# 5. One failing job: sync exits with the structured code, the sibling still
#    ran, and the manifest is untouched (merge only happens on full success).
S="$TMPDIR/failure"
mkdir -p "$S"
write_manifest "$S" split 2
make_go_stub netfail-one
rc=$(run_rc "$S" sync --jobs 2)
assert_eq "parallel sync with one failure exits 3" "3" "$rc"
grep -q gofumpt "$S/.gotools.json"
assert_eq "failed parallel sync keeps gofumpt in manifest" "0" "$?"
grep -q staticcheck "$S/.gotools.json"
assert_eq "failed parallel sync keeps staticcheck in manifest" "0" "$?"
assert_file_absent "failed job cleans up its modfile" "$S/tools/gofumpt.mod"
assert_file_exists "successful sibling installed its modfile" "$S/tools/staticcheck.mod"

# 6. Invalid --jobs is a usage error.
S="$TMPDIR/usage"
mkdir -p "$S"
write_manifest "$S" split 1
make_go_stub success
rc=$(run_rc "$S" sync --jobs abc)
assert_eq "invalid --jobs exits 2" "2" "$rc"

finish
