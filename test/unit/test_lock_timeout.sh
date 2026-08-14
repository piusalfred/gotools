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

# Lock improvements (#26) + per-operation timeouts (#27):
#   GOTOOLS_LOCK_TIMEOUT, GOTOOLS_LOCK_STALE_TIMEOUT, GOTOOLS_NO_LOCK,
#   offline fast-path lock skip, GOTOOLS_OPERATION_TIMEOUT.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_helpers.sh"

GOTOOLS_SH="$SCRIPT_DIR/../../gotools.sh"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT
STUB_BIN="$TMPDIR/stub-bin"
mkdir -p "$STUB_BIN"

# make_go_stub <mode> — ok (harmless no-ops), forbid (fail non-env calls),
# hang (go get sleeps 30s — for the watchdog).
make_go_stub() {
    local mode="$1"
    case "$mode" in
        ok)
            cat > "$STUB_BIN/go" <<'STUB'
#!/usr/bin/env bash
if [[ "${1:-}" == "env" ]]; then echo "go1.24"; exit 0; fi
if [[ "${2:-}" == "tidy" ]]; then touch go.sum; fi
exit 0
STUB
            ;;
        forbid)
            cat > "$STUB_BIN/go" <<'STUB'
#!/usr/bin/env bash
if [[ "${1:-}" == "env" ]]; then echo "go1.24"; exit 0; fi
echo "go: unexpected invocation: $*" >&2
exit 1
STUB
            ;;
        hang)
            cat > "$STUB_BIN/go" <<'STUB'
#!/usr/bin/env bash
if [[ "${1:-}" == "env" ]]; then echo "go1.24"; exit 0; fi
if [[ "${1:-}" == "get" ]]; then sleep 30; exit 0; fi
exit 0
STUB
            ;;
    esac
    chmod +x "$STUB_BIN/go"
}

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

category "lock improvements"

# 1. Configurable lock timeout: fail fast with a 1s budget.
S="$TMPDIR/lock-timeout"
mkdir -p "$S" "$S/tools"
write_manifest "$S"
mkdir -p "$S/tools/.gotools.lock"
make_go_stub ok
rc=$(run_rc "$S" "GOTOOLS_LOCK_TIMEOUT=1" -- sync)
assert_eq "held lock with 1s budget exits 4" "4" "$rc"

# 2. Stale lock (old mtime) is removed and sync proceeds.
S="$TMPDIR/lock-stale"
mkdir -p "$S" "$S/tools"
write_manifest "$S"
write_split_modfile "$S" v0.8.0
mkdir -p "$S/tools/.gotools.lock"
touch -t 202001010000 "$S/tools/.gotools.lock"
make_go_stub ok
out=$(run_out "$S" -- sync)
assert_contains "stale lock message" "Stale lock detected" "$out"
assert_file_exists "stale lock sync completes (fingerprint written)" "$S/tools/.gotools.fingerprint"

# 3. GOTOOLS_LOCK_STALE_TIMEOUT=0 treats any lock as stale.
S="$TMPDIR/lock-stale0"
mkdir -p "$S" "$S/tools"
write_manifest "$S"
write_split_modfile "$S" v0.8.0
mkdir -p "$S/tools/.gotools.lock"
make_go_stub ok
rc=$(run_rc "$S" "GOTOOLS_LOCK_STALE_TIMEOUT=0" -- sync)
assert_eq "fresh lock with stale timeout 0 is removed" "0" "$rc"

# 4. GOTOOLS_NO_LOCK=1 skips locking entirely.
S="$TMPDIR/no-lock"
mkdir -p "$S" "$S/tools"
write_manifest "$S"
write_split_modfile "$S" v0.8.0
mkdir -p "$S/tools/.gotools.lock"
make_go_stub ok
rc=$(run_rc "$S" "GOTOOLS_NO_LOCK=1" -- sync)
assert_eq "NO_LOCK bypasses held lock" "0" "$rc"

# 5. Offline fast path never touches the lock.
S="$TMPDIR/offline-lock"
mkdir -p "$S" "$S/tools"
write_manifest "$S"
write_split_modfile "$S" v0.8.0
fp_for "$S" > "$S/tools/.gotools.fingerprint"
mkdir -p "$S/tools/.gotools.lock"
make_go_stub forbid
rc=$(run_rc "$S" "GOTOOLS_LOCK_TIMEOUT=1" -- sync --offline)
assert_eq "offline fast path skips the lock" "0" "$rc"

category "per-operation timeout"

# 6. A hung go get is killed by the watchdog and surfaces as exit 3.
S="$TMPDIR/timeout"
mkdir -p "$S"
write_manifest "$S"
make_go_stub hang
rc=$(run_rc "$S" "GOTOOLS_OPERATION_TIMEOUT=1" -- sync)
assert_eq "hung go get times out with exit 3" "3" "$rc"
out=$(run_out "$S" "GOTOOLS_OPERATION_TIMEOUT=1" -- sync)
assert_contains "timeout message" "Operation timed out" "$out"

# 7. GOTOOLS_OPERATION_TIMEOUT=0 disables the watchdog.
S="$TMPDIR/no-timeout"
mkdir -p "$S"
write_manifest "$S"
write_split_modfile "$S" v0.8.0
make_go_stub ok
rc=$(run_rc "$S" "GOTOOLS_OPERATION_TIMEOUT=0" -- sync)
assert_eq "timeout disabled, normal flow exits 0" "0" "$rc"

finish
