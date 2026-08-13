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

# Atomic manifest write tests (issue #21). _manifest_flush must build in a
# temp file and rename over the target, stale temp files must be cleaned on
# the next invocation, and a failed write must leave the original manifest
# untouched.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_helpers.sh"

GOTOOLS_SH="$SCRIPT_DIR/../../gotools.sh"

write_manifest() {
    local dir="$1"
    cat > "$dir/.gotools.json" <<'MANIFEST_EOF'
{
  "version": 1,
  "strategy": "split",
  "dir": "tools",
  "go_version": "1.24",
  "module_prefix": "example.com/test",
  "tools": {
  }
}
MANIFEST_EOF
}

# run_rc <dir> <args...> — run gotools.sh in a sandbox, print exit code.
run_rc() {
    local dir="$1"
    shift
    local rc=0
    (cd "$dir" && env -u GOTOOLS_STRATEGY -u GOTOOLS_DIR -u GOTOOLS_GO_VERSION \
         -u GOTOOLS_MODULE_PREFIX "$BASH" "$GOTOOLS_SH" "$@" \
         </dev/null >/dev/null 2>&1) || rc=$?
    echo "$rc"
}

# count_tmp <dir> — number of .gotools.json.tmp.* files in the sandbox.
count_tmp() {
    find "$1" -maxdepth 1 -name '.gotools.json.tmp.*' | wc -l | tr -d ' '
}

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

mkdir -p "$TMPDIR/flush" "$TMPDIR/stale" "$TMPDIR/fail"

category "atomic manifest writes"

# 1. A successful flush updates the manifest and leaves no temp files.
write_manifest "$TMPDIR/flush"
rc=$(run_rc "$TMPDIR/flush" config GOTOOLS_DIR custom-tools)
assert_eq "config set succeeds" "0" "$rc"
grep -q '"dir": "custom-tools",' "$TMPDIR/flush/.gotools.json"
assert_eq "manifest content updated" "0" "$?"
assert_eq "no temp files left after successful flush" "0" "$(count_tmp "$TMPDIR/flush")"

# 2. A stale temp file from a crashed write is removed on next invocation.
write_manifest "$TMPDIR/stale"
echo '{"version": 1,' > "$TMPDIR/stale/.gotools.json.tmp.99999"
rc=$(run_rc "$TMPDIR/stale" list)
assert_eq "list with stale temp file succeeds" "0" "$rc"
assert_eq "stale temp file cleaned up" "0" "$(count_tmp "$TMPDIR/stale")"

# 3. A failed write (read-only directory) leaves the original intact.
write_manifest "$TMPDIR/fail"
cp "$TMPDIR/fail/.gotools.json" "$TMPDIR/fail/.before"
chmod 555 "$TMPDIR/fail"
rc=$(run_rc "$TMPDIR/fail" config GOTOOLS_DIR tools2)
chmod 755 "$TMPDIR/fail"
assert_eq "flush failure exits non-zero" "1" "$rc"
if cmp -s "$TMPDIR/fail/.before" "$TMPDIR/fail/.gotools.json"; then
    echo "    PASS failed flush leaves original manifest intact"
    TEST_PASSED=$((TEST_PASSED + 1))
else
    echo "    FAIL failed flush leaves original manifest intact"
    TEST_FAILED=$((TEST_FAILED + 1))
fi
# Whatever the failed flush left behind is cleaned on the next run.
rc=$(run_rc "$TMPDIR/fail" list)
assert_eq "next run after failed flush succeeds" "0" "$rc"
assert_eq "temp files from failed flush cleaned up" "0" "$(count_tmp "$TMPDIR/fail")"

finish
