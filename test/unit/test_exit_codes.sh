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

# Exit code tests — assert the structured exit codes (issue #19) for each
# failure mode. No network required: a stub `go` binary on PATH stands in for
# the real toolchain.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_helpers.sh"

GOTOOLS_SH="$SCRIPT_DIR/../../gotools.sh"

# run_rc <dir> [env VAR=value ...] [--] <args...>
#   Run gotools.sh in <dir> with a clean GOTOOLS_* environment and print the
#   exit code. Extra env assignments (e.g. PATH=...) go before the `--`.
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
         "$BASH" "$GOTOOLS_SH" "$@" </dev/null >/dev/null 2>&1) || rc=$?
    echo "$rc"
}

# make_go_stub <dir> <mode> — write a fake `go` executable that answers
# `go env GOVERSION` with go1.24 (so _require_go passes) and, for the
# failure modes, fails `go get` with the given error output.
make_go_stub() {
    local dir="$1" mode="$2"
    mkdir -p "$dir"
    case "$mode" in
        ok)
            cat > "$dir/go" <<'STUB'
#!/usr/bin/env bash
if [[ "${1:-}" == "env" ]]; then
    echo "go1.24"
    exit 0
fi
echo "go: unexpected invocation: $*" >&2
exit 1
STUB
            ;;
        old)
            cat > "$dir/go" <<'STUB'
#!/usr/bin/env bash
echo "go1.23"
STUB
            ;;
        empty)
            cat > "$dir/go" <<'STUB'
#!/usr/bin/env bash
exit 0
STUB
            ;;
        netfail)
            cat > "$dir/go" <<'STUB'
#!/usr/bin/env bash
if [[ "${1:-}" == "env" ]]; then
    echo "go1.24"
    exit 0
fi
if [[ "${1:-}" == "get" ]]; then
    echo "go: dial tcp: lookup proxy.golang.org: no such host" >&2
    exit 1
fi
echo "go: unexpected invocation: $*" >&2
exit 1
STUB
            ;;
        pkgnotfound)
            cat > "$dir/go" <<'STUB'
#!/usr/bin/env bash
if [[ "${1:-}" == "env" ]]; then
    echo "go1.24"
    exit 0
fi
if [[ "${1:-}" == "get" ]]; then
    echo "go: github.com/typo/misspeled@v1.0.0: module github.com/typo/misspeled: not found" >&2
    exit 1
fi
echo "go: unexpected invocation: $*" >&2
exit 1
STUB
            ;;
        badpath)
            cat > "$dir/go" <<'STUB'
#!/usr/bin/env bash
if [[ "${1:-}" == "env" ]]; then
    echo "go1.24"
    exit 0
fi
if [[ "${1:-}" == "get" ]]; then
    echo 'go: invalid module version syntax "tool@"' >&2
    exit 1
fi
echo "go: unexpected invocation: $*" >&2
exit 1
STUB
            ;;
    esac
    chmod +x "$dir/go"
}

# write_manifest <dir> <strategy> [tool-name tool-pkg tool-ver] — write a
# minimal .gotools.json in the sandbox.
write_manifest() {
    local dir="$1" strategy="$2"
    local tools_json=""
    if [[ $# -ge 5 ]]; then
        tools_json="    \"$3\": {
      \"source\": \"go\",
      \"package\": \"$4\",
      \"version\": \"$5\"
    }"
    fi
    cat > "$dir/.gotools.json" <<MANIFEST_EOF
{
  "version": 1,
  "strategy": "${strategy}",
  "dir": "tools",
  "go_version": "1.24",
  "module_prefix": "example.com/test",
  "tools": {
${tools_json}
  }
}
MANIFEST_EOF
}

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

STUB_BIN="$TMPDIR/stub-bin"
STUB_PATH="$STUB_BIN:/usr/bin:/bin"
GO_OK="PATH=$STUB_PATH"
GO_NETFAIL="PATH=$STUB_PATH"
GO_PKGNOTFOUND="PATH=$STUB_PATH"
GO_BADPATH="PATH=$STUB_PATH"
GO_OLD="PATH=$STUB_PATH"
GO_EMPTY="PATH=$STUB_PATH"

category "exit code 2 — usage errors"
make_go_stub "$STUB_BIN" ok

rc=$(run_rc "$TMPDIR" -- )
assert_eq "no args (empty stdin) exits 2" "2" "$rc"
rc=$(run_rc "$TMPDIR" -- frobnicate)
assert_eq "unknown command exits 2" "2" "$rc"
rc=$(run_rc "$TMPDIR" -- info)
assert_eq "info without tool name exits 2" "2" "$rc"
rc=$(run_rc "$TMPDIR" -- test)
assert_eq "test without seconds exits 2" "2" "$rc"
rc=$(run_rc "$TMPDIR" -- test abc)
assert_eq "test with non-numeric seconds exits 2" "2" "$rc"
rc=$(run_rc "$TMPDIR" -- init --strategy=bogus)
assert_eq "init with invalid strategy exits 2" "2" "$rc"
rc=$(run_rc "$TMPDIR" "$GO_OK" -- install)
assert_eq "install without package exits 2" "2" "$rc"
rc=$(run_rc "$TMPDIR" "$GO_OK" -- upgrade)
assert_eq "upgrade without target exits 2" "2" "$rc"
rc=$(run_rc "$TMPDIR" "$GO_OK" -- remove)
assert_eq "remove without names exits 2" "2" "$rc"
rc=$(run_rc "$TMPDIR" "$GO_OK" -- migrate)
assert_eq "migrate without strategy exits 2" "2" "$rc"
rc=$(run_rc "$TMPDIR" "$GO_OK" -- migrate bogus)
assert_eq "migrate with invalid strategy exits 2" "2" "$rc"

# Blank stdin goes down pipe mode, which finds no tool specs.
rc=0
(cd "$TMPDIR" && printf '\n' | env -u GOTOOLS_STRATEGY -u GOTOOLS_DIR \
    -u GOTOOLS_GO_VERSION -u GOTOOLS_MODULE_PREFIX "$BASH" "$GOTOOLS_SH" \
    >/dev/null 2>&1) || rc=$?
assert_eq "blank stdin (pipe mode) exits 2" "2" "$rc"

write_manifest "$TMPDIR" split gofumpt mvdan.cc/gofumpt v0.8.0
rc=$(run_rc "$TMPDIR" "$GO_OK" -- install mvdan.cc/gofumpt@v0.9.0)
assert_eq "duplicate tool without --force exits 2" "2" "$rc"

make_go_stub "$STUB_BIN" badpath
rc=$(run_rc "$TMPDIR" "$GO_BADPATH" -- install 'tool@')
assert_eq "install with invalid package path exits 2" "2" "$rc"

category "exit code 8 — environment errors"
rc=$(run_rc "$TMPDIR" "PATH=/usr/bin:/bin" -- sync)
assert_eq "sync without go on PATH exits 8" "8" "$rc"

make_go_stub "$STUB_BIN" old
rc=$(run_rc "$TMPDIR" "$GO_OLD" -- sync)
assert_eq "sync with go < 1.24 exits 8" "8" "$rc"

make_go_stub "$STUB_BIN" empty
rc=$(run_rc "$TMPDIR" "$GO_EMPTY" -- sync)
assert_eq "sync with undetectable go version exits 8" "8" "$rc"

write_manifest "$TMPDIR" bogus
rc=$(run_rc "$TMPDIR" -- list)
assert_eq "list with unknown manifest strategy exits 8" "8" "$rc"

category "exit code 5 — tool not found"
write_manifest "$TMPDIR" split
make_go_stub "$STUB_BIN" ok
rc=$(run_rc "$TMPDIR" "$GO_OK" -- exec goimports)
assert_eq "exec missing tool (split) exits 5" "5" "$rc"
rc=$(run_rc "$TMPDIR" -- info goimports)
assert_eq "info missing tool (split) exits 5" "5" "$rc"
rc=$(run_rc "$TMPDIR" -- info goimports --json)
assert_eq "info --json missing tool exits 5" "5" "$rc"

write_manifest "$TMPDIR" unified
rc=$(run_rc "$TMPDIR" "$GO_OK" -- exec goimports)
assert_eq "exec missing tool (unified) exits 5" "5" "$rc"

write_manifest "$TMPDIR" split gofumpt mvdan.cc/gofumpt v0.8.0
rc=$(run_rc "$TMPDIR" "$GO_OK" -- check)
assert_eq "check with non-runnable tool exits 5" "5" "$rc"

category "exit code 4 — lock contention"
write_manifest "$TMPDIR" split
mkdir -p "$TMPDIR/tools/.gotools.lock"
rc=$(run_rc "$TMPDIR" "$GO_OK" -- sync)
assert_eq "sync with held lock exits 4" "4" "$rc"
# The timed-out process is not the lock owner, so the dir stays behind.
# Remove it so later tests can acquire the lock.
rmdir "$TMPDIR/tools/.gotools.lock"

category "exit code 3 — network errors"
write_manifest "$TMPDIR" split
make_go_stub "$STUB_BIN" netfail
rc=$(run_rc "$TMPDIR" "$GO_NETFAIL" -- install mvdan.cc/gofumpt@v0.8.0)
assert_eq "install network failure exits 3" "3" "$rc"
# Failed install must clean up the modfile it created.
assert_file_absent "network-failed install leaves no modfile" "$TMPDIR/tools/gofumpt.mod"

category "exit code 1 — generic failure"
make_go_stub "$STUB_BIN" pkgnotfound
rc=$(run_rc "$TMPDIR" "$GO_PKGNOTFOUND" -- install github.com/typo/misspeled@v1.0.0)
assert_eq "install package-not-found exits 1" "1" "$rc"
assert_file_absent "failed install leaves no modfile" "$TMPDIR/tools/misspeled.mod"

category "exit code 0 — success paths"
rc=$(run_rc "$TMPDIR" -- version)
assert_eq "version exits 0" "0" "$rc"
rc=$(run_rc "$TMPDIR" -- install --help)
assert_eq "per-command help exits 0" "0" "$rc"

finish
