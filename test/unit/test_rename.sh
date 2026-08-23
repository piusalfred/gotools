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

# Rename command tests: per-strategy disk + manifest moves, refusals,
# --dry-run, and the install duplicity flow. The interactive install
# prompt (choice 1/2/3 on a TTY) is exercised manually via a pty — the
# non-interactive path (inform and proceed) is covered here.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_helpers.sh"

GOTOOLS_SH="$SCRIPT_DIR/../../gotools.sh"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT
STUB_BIN="$TMPDIR/stub-bin"
mkdir -p "$STUB_BIN"

# Stub go: answers the version probe; everything else exits 0 without
# touching the filesystem (go mod edit is NOT applied by the stub — the
# module-line rewrite is verified by the integration suite with real go).
cat > "$STUB_BIN/go" <<'STUB'
#!/usr/bin/env bash
case "${1:-}" in
    env) echo "go1.24"; exit 0 ;;
    *)   exit 0 ;;
esac
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

# make_project <dir> — go.mod + main.go so module resolution works.
make_project() {
    local dir="$1"
    mkdir -p "$dir"
    cp "$SCRIPT_DIR/../main.go" "$SCRIPT_DIR/../go.mod" "$dir/"
}

# write_manifest <dir> <strategy> <tool-name> <pkg> <ver>
write_manifest() {
    local dir="$1" strategy="$2" name="$3" pkg="$4" ver="$5"
    cat > "$dir/.gotools.json" <<EOF
{
  "version": 1,
  "strategy": "$strategy",
  "dir": "tools",
  "go_version": "inherit",
  "module_prefix": "example.com/test",
  "tools": {
    "$name": {
      "source": "go",
      "package": "$pkg",
      "version": "$ver"
    }
  }
}
EOF
}

# seed_split <dir> — split layout for tool "t1". The require line carries
# the same version as the manifest so sync's fast-path version check passes.
seed_split() {
    local dir="$1"
    mkdir -p "$dir/tools"
    printf 'module example.com/test/tools/t1\n\ngo 1.24\n\nrequire example.com/pkg v1.0.0\n\ntool example.com/pkg\n' > "$dir/tools/t1.mod"
    touch "$dir/tools/t1.sum"
}

# seed_module <dir> — module layout for tool "t1".
seed_module() {
    local dir="$1"
    mkdir -p "$dir/tools/t1"
    printf 'module example.com/test/tools/t1\n\ngo 1.24\n' > "$dir/tools/t1/go.mod"
}

# seed_unified <dir> — unified layout (single tools/go.mod).
seed_unified() {
    local dir="$1"
    mkdir -p "$dir/tools"
    printf 'module example.com/test/tools\n\ngo 1.24\n\ntool example.com/pkg\n' > "$dir/tools/go.mod"
}

category "_manifest_tool_find_by_package helper"

_MANIFEST_TOOLS=$'addlicense|go|github.com/google/addlicense|v1.2.0\ngofumpt|go|mvdan.cc/gofumpt|v0.10.0\n'
assert_eq "finds the name for a managed package" "addlicense" "$(_manifest_tool_find_by_package github.com/google/addlicense)"
assert_eq "finds a later entry too" "gofumpt" "$(_manifest_tool_find_by_package mvdan.cc/gofumpt)"
rc=0
_manifest_tool_find_by_package example.com/absent >/dev/null 2>&1 || rc=$?
assert_eq "unknown package returns 1" "1" "$rc"
_MANIFEST_TOOLS=""

category "rename: split strategy"

S="$TMPDIR/split-ok"
make_project "$S" && write_manifest "$S" split t1 example.com/pkg v1.0.0 && seed_split "$S"
rc=$(run_rc "$S" -- rename t1 t2)
assert_eq "rename exits 0" "0" "$rc"
assert_contains "manifest keeps the new name" '"t2"' "$(cat "$S/.gotools.json")"
assert_eq "manifest drops the old name" "0" "$(grep -c '"t1"' "$S/.gotools.json" || true)"
assert_file_exists "modfile moved" "$S/tools/t2.mod"
assert_file_exists "sumfile moved" "$S/tools/t2.sum"
assert_file_absent "old modfile gone" "$S/tools/t1.mod"
assert_file_exists "rename writes the sync fingerprint" "$S/tools/.gotools.fingerprint"
rc=$(run_rc "$S" -- sync --offline)
assert_eq "sync --offline fast-paths right after rename" "0" "$rc"

S="$TMPDIR/split-dry"
make_project "$S" && write_manifest "$S" split t1 example.com/pkg v1.0.0 && seed_split "$S"
rc=$(run_rc "$S" -- rename t1 t2 --dry-run)
assert_eq "dry-run exits 0" "0" "$rc"
assert_contains "dry-run does not change the manifest" '"t1"' "$(cat "$S/.gotools.json")"
assert_file_exists "dry-run keeps the old modfile" "$S/tools/t1.mod"
assert_file_absent "dry-run creates nothing" "$S/tools/t2.mod"
assert_file_absent "dry-run writes no fingerprint" "$S/tools/.gotools.fingerprint"

S="$TMPDIR/split-refuse"
make_project "$S" && write_manifest "$S" split t1 example.com/pkg v1.0.0 && seed_split "$S"
rc=$(run_rc "$S" -- rename missing t2)
assert_eq "unknown old name exits 5" "5" "$rc"
rc=$(run_rc "$S" -- rename t1 t1)
assert_eq "identical names exit 2" "2" "$rc"
rc=$(run_rc "$S" -- rename t1 bad/name)
assert_eq "invalid new name exits 2" "2" "$rc"
rc=$(run_rc "$S" -- rename t1 t2 extra)
assert_eq "extra argument exits 2" "2" "$rc"
# a second tool named t2 makes the target name taken
cat > "$S/.gotools.json" <<'EOF'
{
  "version": 1,
  "strategy": "split",
  "dir": "tools",
  "go_version": "inherit",
  "module_prefix": "example.com/test",
  "tools": {
    "t1": {
      "source": "go",
      "package": "example.com/pkg",
      "version": "v1.0.0"
    },
    "t2": {
      "source": "go",
      "package": "example.com/other",
      "version": "v2.0.0"
    }
  }
}
EOF
rc=$(run_rc "$S" -- rename t1 t2)
assert_eq "target name already managed exits 2" "2" "$rc"
# orphan file at the target path
cat > "$S/.gotools.json" <<'EOF'
{
  "version": 1,
  "strategy": "split",
  "dir": "tools",
  "go_version": "inherit",
  "module_prefix": "example.com/test",
  "tools": {
    "t1": {
      "source": "go",
      "package": "example.com/pkg",
      "version": "v1.0.0"
    }
  }
}
EOF
touch "$S/tools/t2.mod"
rc=$(run_rc "$S" -- rename t1 t2)
assert_eq "existing target artifact exits 8" "8" "$rc"

S="$TMPDIR/split-nomod"
make_project "$S" && write_manifest "$S" split t1 example.com/pkg v1.0.0
rc=$(run_rc "$S" -- rename t1 t2)
assert_eq "missing modfile: manifest-only rename exits 0" "0" "$rc"
assert_contains "manifest renamed without the modfile" '"t2"' "$(cat "$S/.gotools.json")"
assert_file_absent "missing modfile: no fingerprint written" "$S/tools/.gotools.fingerprint"

category "rename: module strategy"

S="$TMPDIR/module-ok"
make_project "$S" && write_manifest "$S" module t1 example.com/pkg v1.0.0 && seed_module "$S"
rc=$(run_rc "$S" -- rename t1 t2)
assert_eq "module rename exits 0" "0" "$rc"
assert_contains "manifest keeps the new name" '"t2"' "$(cat "$S/.gotools.json")"
assert_file_exists "module dir moved" "$S/tools/t2/go.mod"
assert_file_absent "old module dir gone" "$S/tools/t1/go.mod"
assert_file_exists "module rename writes the sync fingerprint" "$S/tools/.gotools.fingerprint"

S="$TMPDIR/module-dry"
make_project "$S" && write_manifest "$S" module t1 example.com/pkg v1.0.0 && seed_module "$S"
rc=$(run_rc "$S" -- rename t1 t2 --dry-run)
assert_eq "module dry-run exits 0" "0" "$rc"
assert_file_exists "module dry-run keeps the dir" "$S/tools/t1/go.mod"

category "rename: unified strategy refuses"

S="$TMPDIR/unified-ok"
make_project "$S" && write_manifest "$S" unified t1 example.com/pkg v1.0.0 && seed_unified "$S"
before_mod=$(shasum -a 256 "$S/tools/go.mod" | awk '{print $1}')
before_mf=$(shasum -a 256 "$S/.gotools.json" | awk '{print $1}')
rc=$(run_rc "$S" -- rename t1 t2)
assert_eq "unified rename refuses with exit 8" "8" "$rc"
out=$(run_out "$S" -- rename t1 t2)
assert_contains "unified refusal points at migrate" "migrate split" "$out"
after_mod=$(shasum -a 256 "$S/tools/go.mod" | awk '{print $1}')
after_mf=$(shasum -a 256 "$S/.gotools.json" | awk '{print $1}')
assert_eq "unified refusal leaves tools/go.mod byte-identical" "$before_mod" "$after_mod"
assert_eq "unified refusal leaves the manifest byte-identical" "$before_mf" "$after_mf"

category "install: same package under a different name"

S="$TMPDIR/dup"
make_project "$S" && write_manifest "$S" split addlicense github.com/google/addlicense v1.2.0 && seed_split "$S"
out=$(run_out "$S" -- install alias github.com/google/addlicense@v1.2.0)
assert_contains "informs the package is already installed" "already installed as 'addlicense'" "$out"
assert_contains "states the existing version" "version=v1.2.0" "$out"
assert_contains "points at the rename command" "rename addlicense alias" "$out"
assert_contains "installs the second entry" '"alias"' "$(cat "$S/.gotools.json")"
assert_contains "keeps the original entry" '"addlicense"' "$(cat "$S/.gotools.json")"

# same name AND package still refuses (existing contract).
rc=$(run_rc "$S" -- install addlicense github.com/google/addlicense@v1.2.0)
assert_eq "same name+package exits 2" "2" "$rc"
out=$(run_out "$S" -- install addlicense github.com/google/addlicense@v1.2.0)
assert_contains "same-name refusal mentions --force" "--force" "$out"

# --force skips the prompt and overwrites the same name.
rc=$(run_rc "$S" -- install --force addlicense github.com/google/addlicense@v1.2.0)
assert_eq "forced reinstall exits 0" "0" "$rc"

finish
