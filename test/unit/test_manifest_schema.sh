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

# Manifest schema versioning tests (issue #20). Older gotools must refuse
# manifests written by a newer one with exit 8, while pre-version manifests
# (no top-level "version" key) keep working.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_helpers.sh"

GOTOOLS_SH="$SCRIPT_DIR/../../gotools.sh"

# write_manifest <dir> [version-line] [tools-block]
#   version-line: e.g. '  "version": 2,' (omit for pre-version manifests)
write_manifest() {
    local dir="$1" ver="${2:-}" tools="${3:-}"
    {
        echo "{"
        [[ -n "$ver" ]] && echo "$ver"
        echo '  "strategy": "split",'
        echo '  "dir": "tools",'
        echo '  "go_version": "1.24",'
        echo '  "module_prefix": "example.com/test",'
        echo '  "tools": {'
        [[ -n "$tools" ]] && echo "$tools"
        echo '  }'
        echo '}'
    } > "$dir/.gotools.json"
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

# run_out <dir> <args...> — run gotools.sh in a sandbox, print combined output.
run_out() {
    local dir="$1"
    shift
    local out=""
    out=$(cd "$dir" && env -u GOTOOLS_STRATEGY -u GOTOOLS_DIR -u GOTOOLS_GO_VERSION \
         -u GOTOOLS_MODULE_PREFIX "$BASH" "$GOTOOLS_SH" "$@" </dev/null 2>&1) || true
    echo "$out"
}

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

mkdir -p "$TMPDIR/v2" "$TMPDIR/v1" "$TMPDIR/pre" "$TMPDIR/pre-tools" "$TMPDIR/strver"

category "manifest schema versioning"

# A manifest written by a newer gotools must be refused loudly.
write_manifest "$TMPDIR/v2" '  "version": 2,'
rc=$(run_rc "$TMPDIR/v2" list)
assert_eq "schema v2 refused with exit 8" "8" "$rc"
out=$(run_out "$TMPDIR/v2" list)
assert_contains "v2 error names the required schema version" "requires schema version 2" "$out"
assert_contains "v2 error names the supported version" "only understands version 1" "$out"
assert_contains "v2 error points at the upgrade URL" "github.com/piusalfred/gotools/releases" "$out"

# Current-schema and pre-version manifests keep working.
write_manifest "$TMPDIR/v1" '  "version": 1,'
rc=$(run_rc "$TMPDIR/v1" list)
assert_eq "schema v1 accepted" "0" "$rc"

write_manifest "$TMPDIR/pre"
rc=$(run_rc "$TMPDIR/pre" list)
assert_eq "pre-version manifest accepted" "0" "$rc"

# Pre-version manifest WITH tools: the first "version" match is a tool's
# version string — the non-numeric guard must not mistake it for a schema.
write_manifest "$TMPDIR/pre-tools" "" '    "gofumpt": {
      "source": "go",
      "package": "mvdan.cc/gofumpt",
      "version": "v0.8.0"
    }'
rc=$(run_rc "$TMPDIR/pre-tools" list)
assert_eq "pre-version manifest with tools accepted" "0" "$rc"
out=$(run_out "$TMPDIR/pre-tools" list)
assert_contains "pre-version manifest still lists its tools" "gofumpt" "$out"

# Non-numeric top-level version falls back to the pre-version default.
write_manifest "$TMPDIR/strver" '  "version": "notanumber",'
rc=$(run_rc "$TMPDIR/strver" list)
assert_eq "non-numeric version treated as pre-version" "0" "$rc"

finish
