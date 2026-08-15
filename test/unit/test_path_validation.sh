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

# Path-validation tests: tools-dir and tool-name validation, symlink
# rejection, and unpredictable manifest temp files. A hostile or mistaken
# manifest/CLI must never redirect rm/mkdir/cat/cd outside the project.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_helpers.sh"

GOTOOLS_SH="$SCRIPT_DIR/../../gotools.sh"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

STUB_BIN="$TMPDIR/stub-bin"
mkdir -p "$STUB_BIN"
cat > "$STUB_BIN/go" <<'STUB'
#!/usr/bin/env bash
if [[ "${1:-}" == "env" ]]; then echo "go1.24"; exit 0; fi
echo "go: unexpected invocation: $*" >&2
exit 1
STUB
chmod +x "$STUB_BIN/go"

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

write_manifest() {
    local dir="$1" tools_dir="${2:-tools}"
    mkdir -p "$dir"
    cat > "$dir/.gotools.json" <<EOF
{
  "version": 1,
  "strategy": "split",
  "dir": "$tools_dir",
  "go_version": "1.24",
  "module_prefix": "example.com/test",
  "tools": {
  }
}
EOF
}

category "validation helpers: _validate_tools_dir"
rc=0; _validate_tools_dir "tools" 2>/dev/null || rc=$?
assert_eq "relative dir accepted" "0" "$rc"
rc=0; _validate_tools_dir "build/tools" 2>/dev/null || rc=$?
assert_eq "nested dir accepted" "0" "$rc"
rc=0; _validate_tools_dir "./tools" 2>/dev/null || rc=$?
assert_eq "dot-prefixed dir accepted" "0" "$rc"
for bad in "" "/abs" "../x" "a/../b" ".." "."; do
    rc=0; _validate_tools_dir "$bad" 2>/dev/null || rc=$?
    assert_eq "dir '$bad' rejected" "1" "$rc"
done

category "validation helpers: _validate_tool_name"
for good in "goose" "golangci-lint" "addlicense" "my.tool_x"; do
    rc=0; _validate_tool_name "$good" 2>/dev/null || rc=$?
    assert_eq "name '$good' accepted" "0" "$rc"
done
for bad in "" "." ".." "-x" "a/b" "../../victims/x" "my tool" $'a\tb'; do
    rc=0; _validate_tool_name "$bad" 2>/dev/null || rc=$?
    assert_eq "name '$bad' rejected" "1" "$rc"
done

category "validation helpers: _reject_symlink"
S="$TMPDIR/sym"
mkdir -p "$S"
echo "regular" > "$S/plain"
rc=0; ( _reject_symlink "$S/plain" 2>/dev/null ) || rc=$?
assert_eq "regular file not rejected" "0" "$rc"
ln -s /tmp "$S/link"
rc=0; ( _reject_symlink "$S/link" 2>/dev/null ) || rc=$?
assert_eq "symlink rejected with E_ENVIRONMENT" "8" "$rc"

category "manifest dir: hostile values refused before any filesystem use"
V="$TMPDIR/victim-dir"
mkdir -p "$V"
echo "canary" > "$V/canary.txt"
S="$TMPDIR/hostile-dir"
write_manifest "$S" "../../victim-dir"
rc=$(run_rc "$S" -- sync)
assert_eq "hostile manifest dir exits E_ENVIRONMENT" "8" "$rc"
assert_file_exists "victim dir untouched by sync" "$V/canary.txt"
rc=$(run_rc "$S" -- list)
assert_eq "hostile manifest dir breaks list too" "8" "$rc"

S="$TMPDIR/hostile-env"
write_manifest "$S"
rc=$(run_rc "$S" "GOTOOLS_DIR=/tmp/gotools-abs-victim" -- sync)
assert_eq "absolute GOTOOLS_DIR env exits E_ENVIRONMENT" "8" "$rc"

category "init --dir: traversal refused"
S="$TMPDIR/init-dir"
mkdir -p "$S"
rc=$(run_rc "$S" -- init --strategy=split --dir=../../victim-dir)
assert_eq "init --dir=../../victim-dir exits E_USAGE" "2" "$rc"
assert_file_absent "init created no outside dir" "$V/../victim-dir/.gotools.json"
rc=$(run_rc "$S" -- init --strategy=split --dir=build/tools)
assert_eq "init with nested relative dir accepted" "0" "$rc"

category "config GOTOOLS_DIR: traversal refused, safe values accepted"
S="$TMPDIR/config-dir"
write_manifest "$S"
rc=$(run_rc "$S" -- config GOTOOLS_DIR ../x)
assert_eq "config GOTOOLS_DIR ../x exits E_USAGE" "2" "$rc"
rc=$(run_rc "$S" -- config GOTOOLS_DIR build/tools)
assert_eq "config GOTOOLS_DIR build/tools accepted" "0" "$rc"
assert_contains "manifest stores the new dir" '"dir": "build/tools"' "$(cat "$S/.gotools.json")"

category "tool names: traversal refused at every entry point"
S="$TMPDIR/name-traversal"
write_manifest "$S"
mkdir -p "$S/tools"
rc=$(run_rc "$S" -- install ../../victim-dir/x github.com/example/tool@v1.0.0)
assert_eq "install traversal name exits E_USAGE" "2" "$rc"
rc=$(run_rc "$S" -- remove ../../victim-dir/x)
assert_eq "remove traversal name exits E_USAGE" "2" "$rc"
rc=$(run_rc "$S" -- exec ../../victim-dir/x -h)
assert_eq "exec traversal name exits E_USAGE" "2" "$rc"
rc=$(run_rc "$S" -- upgrade ../../victim-dir/x)
assert_eq "upgrade traversal name exits E_USAGE" "2" "$rc"
rc=$(run_rc "$S" -- install -leadingdash github.com/example/tool@v1.0.0)
assert_eq "install leading-dash name exits E_USAGE" "2" "$rc"
assert_file_exists "victim dir untouched by name attacks" "$V/canary.txt"

category "manifest tool names: unsafe entries skipped on parse"
S="$TMPDIR/manifest-name"
mkdir -p "$S"
cat > "$S/.gotools.json" <<'JSON'
{
  "version": 1,
  "strategy": "split",
  "dir": "tools",
  "go_version": "1.24",
  "module_prefix": "example.com/test",
  "tools": {
    "goose": {
      "source": "go",
      "package": "github.com/pressly/goose/v3/cmd/goose",
      "version": "v3.27.3"
    },
    "../../victims/x": {
      "source": "go",
      "package": "github.com/example/tool",
      "version": "v1.0.0"
    }
  }
}
JSON
out=$(run_out "$S" -- list)
assert_contains "good tool still listed" "goose" "$out"
assert_contains "unsafe entry skipped with a warning" "Ignoring tool entry with unsafe name" "$out"
rows=$(echo "$out" | awk 'NR>2 && $1 == "../../victims/x" { found=1 } END { print found ? 1 : 0 }')
assert_eq "hostile tool not listed as a row" "0" "$rows"
assert_file_exists "victim dir untouched by manifest parse" "$V/canary.txt"

category "symlinks in tools/: writes refused"
S="$TMPDIR/symlink-modfile"
write_manifest "$S"
mkdir -p "$S/tools"
W="$TMPDIR/outside-mod"
mkdir -p "$W"
cat > "$W/goose.mod" <<'EOF'
module example.com/outside

go 1.24
EOF
ln -s "$W/goose.mod" "$S/tools/goose.mod"
rc=$(run_rc "$S" -- sync)
assert_eq "sync refuses symlinked modfile" "8" "$rc"
assert_contains "outside modfile untouched" "example.com/outside" "$(cat "$W/goose.mod")"

S="$TMPDIR/symlink-toolsdir"
write_manifest "$S"
ln -s "$W" "$S/tools"
rc=$(run_rc "$S" -- sync)
assert_eq "sync refuses symlinked tools dir" "8" "$rc"
assert_file_exists "outside dir untouched" "$W/goose.mod"

category "manifest flush: unpredictable temp files, no symlink following"
S="$TMPDIR/flush-secure"
write_manifest "$S"
T="$TMPDIR/outside-flush"
mkdir -p "$T"
echo "precious" > "$T/canary.txt"
# An attacker plants symlinks at the OLD predictable temp paths
# (.gotools.json.tmp.<pid>) — the flush must never write through them.
ln -s "$T/canary.txt" "$S/.gotools.json.tmp.1"
ln -s "$T/canary.txt" "$S/.gotools.json.tmp.99999"
rc=$(run_rc "$S" -- config GOTOOLS_GO_VERSION 1.25)
assert_eq "config write succeeds despite planted symlinks" "0" "$rc"
assert_contains "manifest updated" '"go_version": "1.25"' "$(cat "$S/.gotools.json")"
assert_eq "outside file untouched" "precious" "$(cat "$T/canary.txt")"
leftovers=$(find "$S" -maxdepth 1 -name '.gotools.json.tmp.*' | wc -l | tr -d ' ')
assert_eq "no temp files left behind" "0" "$leftovers"

echo ""
echo "  ** Summary: $TEST_PASSED passed, $TEST_FAILED failed"
[[ $TEST_FAILED -eq 0 ]] || exit 1
