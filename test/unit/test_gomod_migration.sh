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

# go.mod adoption (init) + reversal (purge --restore) + config-write fix
# tests. Stub go binaries keep everything offline.

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
        restore-ok)
            cat > "$STUB_BIN/go" <<'STUB'
#!/usr/bin/env bash
if [[ "${1:-}" == "env" ]]; then echo "go1.24"; exit 0; fi
if [[ "${1:-}" == "get" ]]; then
    echo "$*" >> "$RESTORE_LOG"
    exit 0
fi
exit 0
STUB
            ;;
    esac
    chmod +x "$STUB_BIN/go"
}

write_root_gomod() {
    cat > "$1/go.mod" <<'EOF'
module example.com/test

go 1.24

tool (
    golang.org/x/tools/cmd/stringer
)

require golang.org/x/tools v0.30.0
EOF
}

write_manifest() {
    local dir="$1" strategy="${2:-split}"
    local tools_block=""
    if [[ -n "${3:-}" ]]; then
        tools_block="    \"gofumpt\": {
      \"source\": \"go\",
      \"package\": \"mvdan.cc/gofumpt\",
      \"version\": \"v0.8.0\"
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

category "strip_tools_from_mod + _gomod_tool_scan"

FIX="$TMPDIR/fix"
mkdir -p "$FIX"
cat > "$FIX/mixed.mod" <<'EOF'
module example.com/mixed

go 1.24

// keep this comment
tool github.com/google/addlicense

tool (
    golang.org/x/tools/cmd/stringer
)

require (
    github.com/google/addlicense v1.1.1
    golang.org/x/tools v0.30.0
)
EOF
out=$(strip_tools_from_mod "$FIX/mixed.mod")
assert_contains "strip keeps comments" "// keep this comment" "$out"
if [[ "$out" == *"tool ("* || "$out" == *$'\ntool '* ]]; then
    echo "    FAIL strip removes tool directives"
    TEST_FAILED=$((TEST_FAILED + 1))
else
    echo "    PASS strip removes tool directives"
    TEST_PASSED=$((TEST_PASSED + 1))
fi
assert_contains "strip keeps require block" "github.com/google/addlicense v1.1.1" "$out"

scan=$(_gomod_tool_scan "$FIX/mixed.mod")
assert_contains "scan resolves addlicense pin" "addlicense github.com/google/addlicense@v1.1.1" "$scan"
assert_contains "scan resolves stringer pin" "stringer golang.org/x/tools/cmd/stringer@v0.30.0" "$scan"

cat > "$FIX/nopins.mod" <<'EOF'
module example.com/nopins

go 1.24

tool github.com/foo/bar
EOF
scan=$(_gomod_tool_scan "$FIX/nopins.mod")
assert_contains "scan falls back to @latest" "bar github.com/foo/bar@latest" "$scan"

category "init adopts root go.mod tools"

# Install failure must leave the root go.mod untouched.
S="$TMPDIR/init-netfail"
mkdir -p "$S"
write_root_gomod "$S"
cp "$S/go.mod" "$S/.before"
make_go_stub netfail
rc=$(run_rc "$S" init)
assert_eq "init with network failure exits 3" "3" "$rc"
cmp -s "$S/go.mod" "$S/.before"
assert_eq "failed adoption leaves go.mod untouched" "0" "$?"
assert_file_exists "manifest still bootstrapped" "$S/.gotools.json"

# --no-migrate skips adoption entirely.
S="$TMPDIR/init-nomigrate"
mkdir -p "$S"
write_root_gomod "$S"
cp "$S/go.mod" "$S/.before"
make_go_stub forbid
rc=$(run_rc "$S" init --no-migrate)
assert_eq "init --no-migrate exits 0" "0" "$rc"
cmp -s "$S/go.mod" "$S/.before"
assert_eq "--no-migrate leaves go.mod untouched" "0" "$?"

# --dry-run prints the plan and writes nothing.
S="$TMPDIR/init-dryrun"
mkdir -p "$S"
write_root_gomod "$S"
cp "$S/go.mod" "$S/.before"
make_go_stub forbid
out=$(run_out "$S" init --dry-run)
assert_contains "dry-run shows adoption plan" "[dry-run] Would install stringer" "$out"
assert_contains "dry-run shows strip plan" "[dry-run] Would strip tool directives" "$out"
assert_file_absent "dry-run init writes no manifest" "$S/.gotools.json"

# Re-running init merges the existing manifest instead of wiping it.
S="$TMPDIR/init-merge"
mkdir -p "$S"
write_manifest "$S" split tool
write_root_gomod "$S"
cp "$S/go.mod" "$S/.before"
make_go_stub netfail
rc=$(run_rc "$S" init)
assert_eq "re-init with existing manifest runs adoption" "3" "$rc"
grep -q gofumpt "$S/.gotools.json"
assert_eq "re-init preserves existing manifest tools" "0" "$?"

category "purge --restore puts tools back into go.mod"

# Dry-run: plan only, nothing deleted.
S="$TMPDIR/purge-dry"
mkdir -p "$S"
write_manifest "$S" split tool
write_root_gomod "$S"
make_go_stub forbid
out=$(run_out "$S" purge --restore --dry-run)
assert_contains "dry-run restore plan" "[dry-run] Would restore gofumpt" "$out"
assert_contains "dry-run shows re-init hint" "[dry-run] To come back: gotools.sh init --strategy=split --go=1.24 --prefix=example.com/test" "$out"
assert_file_exists "dry-run purge keeps the manifest" "$S/.gotools.json"

# Restore failure must abort before the wipe.
S="$TMPDIR/purge-fail"
mkdir -p "$S"
write_manifest "$S" split tool
write_root_gomod "$S"
make_go_stub netfail
rc=$(cd "$S" && echo YES | env -u GOTOOLS_STRATEGY -u GOTOOLS_DIR \
    -u GOTOOLS_GO_VERSION -u GOTOOLS_MODULE_PREFIX PATH="$STUB_BIN:/usr/bin:/bin" \
    "$BASH" "$GOTOOLS_SH" purge --restore >/dev/null 2>&1; echo $?)
assert_eq "failed restore exits 3" "3" "$rc"
assert_file_exists "failed restore keeps the manifest" "$S/.gotools.json"

# Successful restore: go get -tool runs per tool, then gotools wipes itself.
S="$TMPDIR/purge-ok"
mkdir -p "$S"
write_manifest "$S" split tool
write_root_gomod "$S"
make_go_stub restore-ok
out=""
rc=0
out=$(cd "$S" && echo YES | env -u GOTOOLS_STRATEGY -u GOTOOLS_DIR \
    -u GOTOOLS_GO_VERSION -u GOTOOLS_MODULE_PREFIX PATH="$STUB_BIN:/usr/bin:/bin" \
    RESTORE_LOG="$S/restore.log" \
    "$BASH" "$GOTOOLS_SH" purge --restore 2>&1) || rc=$?
assert_eq "successful restore exits 0" "0" "$rc"
assert_contains "restore prints the re-init hint" "💡 To come back: gotools.sh init --strategy=split --go=1.24 --prefix=example.com/test" "$out"
assert_file_absent "purge removes the manifest after restore" "$S/.gotools.json"
if [[ -d "$S/tools" ]]; then
    echo "    FAIL purge removes the tools dir after restore"
    TEST_FAILED=$((TEST_FAILED + 1))
else
    echo "    PASS purge removes the tools dir after restore"
    TEST_PASSED=$((TEST_PASSED + 1))
fi
grep -q "get -tool mvdan.cc/gofumpt@v0.8.0" "$S/restore.log"
assert_eq "restore ran go get -tool at the pinned version" "0" "$?"

category "config writes no longer wipe the manifest"

S="$TMPDIR/config-fix"
mkdir -p "$S"
write_manifest "$S" unified tool
rc=$(run_rc "$S" config GOTOOLS_DIR custom-tools)
assert_eq "config write exits 0" "0" "$rc"
grep -q '"strategy": "unified"' "$S/.gotools.json"
assert_eq "config write preserves the strategy" "0" "$?"
grep -q gofumpt "$S/.gotools.json"
assert_eq "config write preserves the tools list" "0" "$?"
grep -q '"dir": "custom-tools"' "$S/.gotools.json"
assert_eq "config write applies the new value" "0" "$?"

finish
