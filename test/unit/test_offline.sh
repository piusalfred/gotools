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

# Offline mode tests (issue #24). sync takes only the fingerprint fast path
# (exit 6 otherwise), install/upgrade refuse outright. The forbid stub fails
# on ANY go invocation other than `env`, proving offline paths make no
# network-bound go calls.

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

fp_for() {
    local dir="$1"
    (
        _manifest_parse "$dir/.gotools.json"
        _sync_fingerprint "1.24"
    )
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

category "offline mode"

# 1. Fingerprint match: sync --offline hits the fast path, zero go calls.
S="$TMPDIR/hit"
mkdir -p "$S" "$S/tools"
write_manifest "$S"
write_split_modfile "$S" v0.8.0
fp_for "$S" > "$S/tools/.gotools.fingerprint"
rc=$(run_rc "$S" -- sync --offline)
assert_eq "offline sync fast path exits 0" "0" "$rc"
out=$(run_out "$S" -- sync --offline)
assert_contains "offline fast path message" "Tools up to date (fingerprint match)." "$out"

# 2. Fingerprint mismatch: refuse with exit 6.
S="$TMPDIR/miss"
mkdir -p "$S" "$S/tools"
write_manifest "$S"
write_split_modfile "$S" v0.8.0
echo "stale" > "$S/tools/.gotools.fingerprint"
rc=$(run_rc "$S" -- sync --offline)
assert_eq "offline sync mismatch exits 6" "6" "$rc"
out=$(run_out "$S" -- sync --offline)
assert_contains "offline mismatch message" "Offline mode: manifest has changed. Network required." "$out"
assert_contains "offline mismatch hints to commit" "Run 'gotools sync' locally and commit" "$out"

# 3. Defense in depth: fingerprint matches but the modfile drifted — still 6.
S="$TMPDIR/drift"
mkdir -p "$S" "$S/tools"
write_manifest "$S"
write_split_modfile "$S" v0.5.0
fp_for "$S" > "$S/tools/.gotools.fingerprint"
rc=$(run_rc "$S" -- sync --offline)
assert_eq "offline sync with drift exits 6" "6" "$rc"

# 4. GOTOOLS_OFFLINE=1 env var works like the flag.
S="$TMPDIR/env"
mkdir -p "$S" "$S/tools"
write_manifest "$S"
write_split_modfile "$S" v0.8.0
fp_for "$S" > "$S/tools/.gotools.fingerprint"
rc=$(run_rc "$S" "GOTOOLS_OFFLINE=1" -- sync)
assert_eq "GOTOOLS_OFFLINE=1 fast path exits 0" "0" "$rc"

# 5. install refuses in offline mode.
S="$TMPDIR/install"
mkdir -p "$S"
write_manifest "$S"
rc=$(run_rc "$S" -- install --offline mvdan.cc/gofumpt@v0.9.0)
assert_eq "offline install exits 6" "6" "$rc"
out=$(run_out "$S" -- install --offline mvdan.cc/gofumpt@v0.9.0)
assert_contains "offline install message" "cannot install new tools without network" "$out"

# 6. upgrade refuses in offline mode.
S="$TMPDIR/upgrade"
mkdir -p "$S"
write_manifest "$S"
rc=$(run_rc "$S" -- upgrade --offline all)
assert_eq "offline upgrade exits 6" "6" "$rc"
out=$(run_out "$S" -- upgrade --offline all)
assert_contains "offline upgrade message" "cannot upgrade tools without network" "$out"

# 7. Usage errors still win: install --offline with no package is exit 2.
S="$TMPDIR/usage"
mkdir -p "$S"
rc=$(run_rc "$S" -- install --offline)
assert_eq "offline install without package exits 2" "2" "$rc"

finish
