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

# Sync fingerprint fast-path tests (issue #23). A stub go binary stands in
# for the toolchain: the "forbid" mode fails on ANY invocation, proving the
# fast path makes zero go calls, and the "netfail" mode lets the sync flow
# reach the reinstall phase and fail there.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_helpers.sh"

GOTOOLS_SH="$SCRIPT_DIR/../../gotools.sh"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT
STUB_BIN="$TMPDIR/stub-bin"
mkdir -p "$STUB_BIN"

make_go_stub() {
    local mode="$1"
    case "$mode" in
        forbid)
            cat > "$STUB_BIN/go" <<'STUB'
#!/usr/bin/env bash
if [[ "${1:-}" == "env" ]]; then echo "go1.24"; exit 0; fi
echo "go: unexpected invocation: $*" >&2
exit 1
STUB
            ;;
        netfail)
            cat > "$STUB_BIN/go" <<'STUB'
#!/usr/bin/env bash
if [[ "${1:-}" == "env" ]]; then echo "go1.24"; exit 0; fi
if [[ "${1:-}" == "get" ]]; then
    echo "go: dial tcp: lookup proxy.golang.org: no such host" >&2
    exit 1
fi
if [[ "${2:-}" == "tidy" ]]; then touch go.sum; fi
exit 0
STUB
            ;;
    esac
    chmod +x "$STUB_BIN/go"
}

write_manifest() {
    local dir="$1" strategy="${2:-split}" version="${3:-v0.8.0}"
    local tools_block=""
    if [[ -n "${4:-}" ]]; then
        tools_block="    \"gofumpt\": {
      \"source\": \"go\",
      \"package\": \"mvdan.cc/gofumpt\",
      \"version\": \"${version}\"
    }"
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

write_split_modfile() {
    local dir="$1" version="$2"
    mkdir -p "$dir/tools"
    cat > "$dir/tools/gofumpt.mod" <<EOF
module example.com/test/tools/gofumpt

go 1.24

tool mvdan.cc/gofumpt

require mvdan.cc/gofumpt ${version}
EOF
}

write_unified_modfile() {
    local dir="$1" version="$2"
    mkdir -p "$dir/tools"
    cat > "$dir/tools/go.mod" <<EOF
module example.com/test/tools

go 1.24

tool mvdan.cc/gofumpt

require mvdan.cc/gofumpt ${version}
EOF
}

write_module_modfile() {
    local dir="$1" version="$2"
    mkdir -p "$dir/tools/gofumpt"
    cat > "$dir/tools/gofumpt/go.mod" <<EOF
module example.com/test/tools/gofumpt

go 1.24

tool mvdan.cc/gofumpt

require mvdan.cc/gofumpt ${version}
EOF
}

# fp_for <dir> — the fingerprint the script would compute for this project.
fp_for() {
    local dir="$1"
    (
        _manifest_parse "$dir/.gotools.json"
        _sync_fingerprint "1.24"
    )
}

# run_rc <dir> <args...> — run gotools.sh in a sandbox, print exit code.
run_rc() {
    local dir="$1"
    shift
    local rc=0
    (cd "$dir" && env -u GOTOOLS_STRATEGY -u GOTOOLS_DIR -u GOTOOLS_GO_VERSION \
         -u GOTOOLS_MODULE_PREFIX PATH="$STUB_BIN:/usr/bin:/bin" \
         "$BASH" "$GOTOOLS_SH" "$@" </dev/null >/dev/null 2>&1) || rc=$?
    echo "$rc"
}

# run_out <dir> <args...> — run gotools.sh in a sandbox, print combined output.
run_out() {
    local dir="$1"
    shift
    local out=""
    out=$(cd "$dir" && env -u GOTOOLS_STRATEGY -u GOTOOLS_DIR -u GOTOOLS_GO_VERSION \
         -u GOTOOLS_MODULE_PREFIX PATH="$STUB_BIN:/usr/bin:/bin" \
         "$BASH" "$GOTOOLS_SH" "$@" </dev/null 2>&1) || true
    echo "$out"
}

category "sync fingerprint"
make_go_stub forbid

# 1. Fingerprint determinism + sensitivity (sourced functions).
# The globals below are consumed by _sync_fingerprint via the sourced script.
# shellcheck disable=SC2034
GOTOOLS_STRATEGY="split"
# shellcheck disable=SC2034
GOTOOLS_DIR="tools"
# shellcheck disable=SC2034
GOTOOLS_MODULE_PREFIX="example.com/test"
_MANIFEST_TOOLS="gofumpt|go|mvdan.cc/gofumpt|v0.8.0"
fp1=$(_sync_fingerprint "1.24")
fp2=$(_sync_fingerprint "1.24")
assert_eq "fingerprint is deterministic" "$fp1" "$fp2"
_MANIFEST_TOOLS="gofumpt|go|mvdan.cc/gofumpt|v0.9.0"
fp3=$(_sync_fingerprint "1.24")
if [[ "$fp1" == "$fp3" ]]; then
    echo "    FAIL fingerprint changes with tool version"
    TEST_FAILED=$((TEST_FAILED + 1))
else
    echo "    PASS fingerprint changes with tool version"
    TEST_PASSED=$((TEST_PASSED + 1))
fi

# 2. Fast path hit — for EVERY strategy: fingerprint matches and modfiles are
#    correct, so sync must complete WITHOUT invoking go (the forbid stub
#    would fail any call).
for strategy in unified split module; do
    S="$TMPDIR/fasthit-$strategy"
    mkdir -p "$S"
    write_manifest "$S" "$strategy" v0.8.0 tool
    case "$strategy" in
        unified) write_unified_modfile "$S" v0.8.0 ;;
        split)   write_split_modfile "$S" v0.8.0 ;;
        module)  write_module_modfile "$S" v0.8.0 ;;
    esac
    mkdir -p "$S/tools"
    fp_for "$S" > "$S/tools/.gotools.fingerprint"
    rc=$(run_rc "$S" sync)
    assert_eq "fast path sync exits 0 ($strategy)" "0" "$rc"
    out=$(run_out "$S" sync)
    assert_contains "fast path message ($strategy)" "Tools up to date." "$out"
done

# 3. Fingerprint missing: full sync runs, writes the fingerprint, and the
#    NEXT sync takes the fast path.
S="$TMPDIR/selfheal"
mkdir -p "$S"
write_manifest "$S" split v0.8.0 tool
write_split_modfile "$S" v0.8.0
make_go_stub netfail
rc=$(run_rc "$S" sync)
assert_eq "sync without fingerprint exits 0" "0" "$rc"
expected=$(fp_for "$S")
actual=$(cat "$S/tools/.gotools.fingerprint")
assert_eq "fingerprint written after full sync" "$expected" "$actual"
make_go_stub forbid
out=$(run_out "$S" sync)
assert_contains "second sync hits the fast path" "Tools up to date." "$out"

# 4. Version drift on disk: fingerprint matches but the modfile was edited
#    by hand — full sync runs and reinstalls at the manifest version.
S="$TMPDIR/drift"
mkdir -p "$S"
write_manifest "$S" split v0.8.0 tool
write_split_modfile "$S" v0.5.0
mkdir -p "$S/tools"
fp_for "$S" > "$S/tools/.gotools.fingerprint"
make_go_stub netfail
out=$(run_out "$S" sync)
assert_contains "drift detected" "manifest v0.8.0, disk v0.5.0" "$out"
assert_contains "drift triggers reinstall" "reinstalling" "$out"
rc=$(run_rc "$S" sync)
assert_eq "drifted reinstall surfaces the network failure" "3" "$rc"

# 5. Manifest change invalidates the fingerprint: full sync runs and the
#    fingerprint is rewritten even when nothing needed reinstalling.
S="$TMPDIR/stale"
mkdir -p "$S"
write_manifest "$S" split v0.8.0
# fingerprint of the EMPTY manifest (as if written before the tool existed)
(
    # shellcheck disable=SC2034
    _MANIFEST_TOOLS=""
    # shellcheck disable=SC2034
    GOTOOLS_STRATEGY="split"
    # shellcheck disable=SC2034
    GOTOOLS_DIR="tools"
    # shellcheck disable=SC2034
    GOTOOLS_MODULE_PREFIX="example.com/test"
    _sync_fingerprint "1.24"
) > "$S/stale_fp"
mkdir -p "$S/tools"
cp "$S/stale_fp" "$S/tools/.gotools.fingerprint"
write_split_modfile "$S" v0.8.0
make_go_stub netfail
rc=$(run_rc "$S" sync)
assert_eq "stale fingerprint sync exits 0" "0" "$rc"
expected=$(fp_for "$S")
actual=$(cat "$S/tools/.gotools.fingerprint")
assert_eq "fingerprint rewritten after manifest change" "$expected" "$actual"

finish
