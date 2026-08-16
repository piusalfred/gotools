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

# Lock reliability tests (issue #26): pid-aware stale detection, configurable
# timeout, GOTOOLS_NO_LOCK, and the lock-free offline fast path. All syncs
# below hit the fingerprint fast path, so any go call beyond `env` proves a
# lock/liveness regression.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_helpers.sh"

GOTOOLS_SH="$SCRIPT_DIR/../../gotools.sh"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT
STUB_BIN="$TMPDIR/stub-bin"
mkdir -p "$STUB_BIN"

# Stub go: only `env` is allowed (version resolution). Everything else would
# mean the fast path leaked into a real go operation.
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

fp_for() {
    local dir="$1"
    (
        _manifest_parse "$dir/.gotools.json"
        _sync_fingerprint "1.24"
    )
}

setup_project() {
    local dir="$1"
    mkdir -p "$dir"
    write_manifest "$dir"
    write_split_modfile "$dir"
    fp_for "$dir" > "$dir/tools/.gotools.fingerprint"
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

# A process that stays alive for the duration of the test — the "holder".
# Must look like a gotools holder: the pid-reuse guard treats a live pid
# whose command is NOT bash/sh/gotools as stale, so a bare sleep here
# would be reclaimed as an unrelated process.
bash -c 'while sleep 1; do :; done' & LIVE_PID=$!
trap 'kill $LIVE_PID 2>/dev/null; wait $LIVE_PID 2>/dev/null || true; rm -rf "$TMPDIR"' EXIT

category "lock: live holder is never stale"
S="$TMPDIR/live"
setup_project "$S"
mkdir -p "$S/tools/.gotools.lock"
echo "$LIVE_PID" > "$S/tools/.gotools.lock/pid"
rc=$(run_rc "$S" "GOTOOLS_LOCK_TIMEOUT=1" -- sync)
assert_eq "live-pid lock exits 4 after short timeout" "4" "$rc"
out=$(run_out "$S" "GOTOOLS_LOCK_TIMEOUT=1" -- sync)
assert_contains "live-pid lock message" "Another gotools process is running" "$out"
# The live lock must survive (a live holder is never stale).
assert_file_exists "live lock not removed" "$S/tools/.gotools.lock/pid"

category "lock: dead pid is stale and removed"
S="$TMPDIR/dead"
setup_project "$S"
mkdir -p "$S/tools/.gotools.lock"
(exit 0) & DP=$!
wait "$DP" 2>/dev/null || true
echo "$DP" > "$S/tools/.gotools.lock/pid"
out=$(run_out "$S" -- sync)
assert_contains "stale message names the dead process" "Stale lock detected (process $DP is gone or not gotools)" "$out"
assert_contains "sync proceeds after stale removal" "Tools up to date" "$out"
# After sync the lock is released again — the pid record must not leak.
assert_file_absent "lock dir fully released after sync" "$S/tools/.gotools.lock"
mkdir -p "$S/tools/.gotools.lock"
echo "$DP" > "$S/tools/.gotools.lock/pid"
rc=$(run_rc "$S" -- sync)
assert_eq "dead-pid lock removed, sync exits 0" "0" "$rc"
rm -rf "$S/tools/.gotools.lock"

category "lock: unrelated live pid is reclaimed (pid reuse guard)"
S="$TMPDIR/reuse"
setup_project "$S"
mkdir -p "$S/tools/.gotools.lock"
sleep 300 & REUSE_PID=$!
echo "$REUSE_PID" > "$S/tools/.gotools.lock/pid"
# A live process whose command is clearly not gotools (sleep) must not
# block commands: the recorded pid was recycled by an unrelated process.
out=$(run_out "$S" -- sync)
assert_contains "reuse message" "not gotools" "$out"
assert_contains "sync proceeds after reuse reclaim" "Tools up to date" "$out"
assert_file_absent "reused lock removed" "$S/tools/.gotools.lock"
kill $REUSE_PID 2>/dev/null; wait $REUSE_PID 2>/dev/null || true

category "lock: symlinked lock entry removed without following"
S="$TMPDIR/symlink"
setup_project "$S"
VICTIM="$TMPDIR/victim"
mkdir -p "$VICTIM"
echo "precious" > "$VICTIM/pid"
ln -s "$VICTIM" "$S/tools/.gotools.lock"
out=$(run_out "$S" -- sync)
assert_contains "symlink lock message" "Removing symlinked lock entry" "$out"
assert_contains "sync proceeds after symlink removal" "Tools up to date" "$out"
# The link itself is gone, the victim's contents are untouched.
assert_file_absent "symlink removed" "$S/tools/.gotools.lock"
assert_file_exists "victim pid file untouched" "$VICTIM/pid"
assert_eq "victim pid content intact" "precious" "$(cat "$VICTIM/pid")"

category "lock: legacy lock without pid file — age based"
S="$TMPDIR/legacy-old"
setup_project "$S"
mkdir -p "$S/tools/.gotools.lock"
touch -t 202001010000 "$S/tools/.gotools.lock"
out=$(run_out "$S" -- sync)
assert_contains "legacy stale message" "Stale lock detected (age" "$out"
assert_contains "sync proceeds after legacy removal" "Tools up to date" "$out"
assert_file_absent "legacy lock dir removed" "$S/tools/.gotools.lock"
mkdir -p "$S/tools/.gotools.lock"
touch -t 202001010000 "$S/tools/.gotools.lock"
rc=$(run_rc "$S" -- sync)
assert_eq "old legacy lock removed, sync exits 0" "0" "$rc"
rm -rf "$S/tools/.gotools.lock"

S="$TMPDIR/legacy-fresh"
setup_project "$S"
mkdir -p "$S/tools/.gotools.lock"
rc=$(run_rc "$S" "GOTOOLS_LOCK_TIMEOUT=1" -- sync)
assert_eq "fresh legacy lock exits 4 (existing behavior)" "4" "$rc"
rmdir "$S/tools/.gotools.lock"

category "lock: GOTOOLS_NO_LOCK"
S="$TMPDIR/no-lock"
setup_project "$S"
mkdir -p "$S/tools/.gotools.lock"
rc=$(run_rc "$S" "GOTOOLS_NO_LOCK=1" -- sync)
assert_eq "NO_LOCK proceeds past a held lock" "0" "$rc"
rmdir "$S/tools/.gotools.lock"

category "lock: offline sync never acquires the lock"
S="$TMPDIR/offline"
setup_project "$S"
mkdir -p "$S/tools/.gotools.lock"
echo "$LIVE_PID" > "$S/tools/.gotools.lock/pid"
# A 1s lock timeout would kill any lock acquisition — passing proves the
# offline fast path skipped the lock entirely.
rc=$(run_rc "$S" "GOTOOLS_LOCK_TIMEOUT=1" -- sync --offline)
assert_eq "offline fast path ignores a held lock" "0" "$rc"
out=$(run_out "$S" "GOTOOLS_LOCK_TIMEOUT=1" -- sync --offline)
assert_contains "offline fast path message" "fingerprint match" "$out"

category "lock: helper functions"
LD="$TMPDIR/helper-live"
mkdir -p "$LD"
echo "$LIVE_PID" > "$LD/pid"
rc=0; _lock_detect_stale "$LD" 2>/dev/null || rc=$?
assert_eq "_lock_detect_stale live pid returns 1" "1" "$rc"

LD="$TMPDIR/helper-dead"
mkdir -p "$LD"
(exit 0) & DP2=$!
wait "$DP2" 2>/dev/null || true
echo "$DP2" > "$LD/pid"
rc=0; _lock_detect_stale "$LD" 2>/dev/null || rc=$?
assert_eq "_lock_detect_stale dead pid returns 0" "0" "$rc"
assert_file_absent "_lock_detect_stale removed the dir" "$LD"

LD="$TMPDIR/helper-fresh"
mkdir -p "$LD"
rc=0; _lock_detect_stale "$LD" 2>/dev/null || rc=$?
assert_eq "_lock_detect_stale fresh legacy lock returns 1" "1" "$rc"

LD="$TMPDIR/helper-old"
mkdir -p "$LD"
touch -t 202001010000 "$LD"
rc=0; _lock_detect_stale "$LD" 2>/dev/null || rc=$?
assert_eq "_lock_detect_stale old legacy lock returns 0" "0" "$rc"

if _lock_pid_live "$LIVE_PID"; then lv=0; else lv=1; fi
assert_eq "_lock_pid_live on live pid returns 0" "0" "$lv"
(exit 0) & DP3=$!
wait "$DP3" 2>/dev/null || true
if _lock_pid_live "$DP3"; then lv=0; else lv=1; fi
assert_eq "_lock_pid_live on dead pid returns 1" "1" "$lv"

finish
