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

# Self-update checksum verification tests (issue #22). A stub curl serves
# canned release metadata, a checksum manifest, and a fixture script, so the
# whole flow runs offline.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_helpers.sh"

GOTOOLS_SH="$SCRIPT_DIR/../../gotools.sh"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

STUB_BIN="$TMPDIR/stub-bin"
CTRL="$TMPDIR/ctrl"
mkdir -p "$STUB_BIN" "$CTRL"

# sha256 <file> — hash the same way _sha256 does.
sha256() {
    if command -v sha256sum &>/dev/null; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

# Fixture "release" files.
cat > "$CTRL/api.json" <<'EOF'
{"tag_name": "v9.9.9"}
EOF
cat > "$CTRL/script.sh" <<'EOF'
#!/usr/bin/env bash
echo "fixture v9.9.9"
EOF
chmod +x "$CTRL/script.sh"

make_stub_curl() {
    cat > "$STUB_BIN/curl" <<'STUB'
#!/usr/bin/env bash
url=""
out=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        -o) out="$2"; shift 2 ;;
        -*) shift ;;
        *) url="$1"; shift ;;
    esac
done
emit() { # curl writes to -o file when given, stdout otherwise
    if [[ -n "$out" ]]; then cat "$1" > "$out"; else cat "$1"; fi
}
case "$url" in
    *api.github.com*)
        emit "$CTRL/api.json"
        ;;
    *checksums-sha256.txt*)
        if [[ -f "$CTRL/fail_checksums" ]]; then
            echo "curl: (22) The requested URL returned error: 404" >&2
            exit 22
        fi
        emit "$CTRL/checksums.txt"
        ;;
    *raw.githubusercontent.com*)
        if [[ -f "$CTRL/fail_script" ]]; then
            echo "curl: (28) Timeout was reached" >&2
            exit 28
        fi
        emit "$CTRL/script.sh"
        ;;
    *)
        echo "curl: unexpected URL: $url" >&2
        exit 1
        ;;
esac
STUB
    chmod +x "$STUB_BIN/curl"
}

# new_sandbox — fresh project dir with a copy of gotools.sh as the target.
new_sandbox() {
    local dir="$1"
    mkdir -p "$dir"
    cp "$GOTOOLS_SH" "$dir/gotools.sh"
    cp "$GOTOOLS_SH" "$dir/.original"
}

# run_rc <dir> <args...> — run the sandbox copy, print exit code.
run_rc() {
    local dir="$1"
    shift
    local rc=0
    (cd "$dir" && env -u GOTOOLS_STRATEGY -u GOTOOLS_DIR -u GOTOOLS_GO_VERSION \
         -u GOTOOLS_MODULE_PREFIX CTRL="$CTRL" PATH="$STUB_BIN:/usr/bin:/bin" \
         "$BASH" "$dir/gotools.sh" "$@" </dev/null >/dev/null 2>&1) || rc=$?
    echo "$rc"
}

# run_out <dir> <args...> — run the sandbox copy, print combined output.
run_out() {
    local dir="$1"
    shift
    local out=""
    out=$(cd "$dir" && env -u GOTOOLS_STRATEGY -u GOTOOLS_DIR -u GOTOOLS_GO_VERSION \
         -u GOTOOLS_MODULE_PREFIX CTRL="$CTRL" PATH="$STUB_BIN:/usr/bin:/bin" \
         "$BASH" "$dir/gotools.sh" "$@" </dev/null 2>&1) || true
    echo "$out"
}

category "self-update checksum verification"
make_stub_curl

# 1. Happy path: checksum matches, script is replaced.
GOOD_HASH=$(sha256 "$CTRL/script.sh")
printf '%s  gotools.sh\n' "$GOOD_HASH" > "$CTRL/checksums.txt"
S="$TMPDIR/happy"
new_sandbox "$S"
rc=$(run_rc "$S" self-update)
assert_eq "matching checksum updates successfully" "0" "$rc"
cmp -s "$S/gotools.sh" "$CTRL/script.sh"
assert_eq "installed script matches the verified download" "0" "$?"
if [[ -x "$S/gotools.sh" ]]; then rc=0; else rc=1; fi
assert_eq "installed script stays executable" "0" "$rc"

# 2. Checksum mismatch: refuse, delete download, leave $0 untouched.
printf '%064d  gotools.sh\n' 0 > "$CTRL/checksums.txt"
S="$TMPDIR/mismatch"
new_sandbox "$S"
rc=$(run_rc "$S" self-update)
assert_eq "checksum mismatch exits 1" "1" "$rc"
out=$(run_out "$S" self-update)
assert_contains "mismatch message shown" "Checksum verification FAILED" "$out"
assert_contains "mismatch explains tampering" "tampered with" "$out"
cmp -s "$S/gotools.sh" "$S/.original"
assert_eq "mismatch leaves the installed script untouched" "0" "$?"

# 3. Checksums manifest download failure → network exit code.
GOOD_HASH=$(sha256 "$CTRL/script.sh")
printf '%s  gotools.sh\n' "$GOOD_HASH" > "$CTRL/checksums.txt"
touch "$CTRL/fail_checksums"
S="$TMPDIR/nomanifest"
new_sandbox "$S"
rc=$(run_rc "$S" self-update)
assert_eq "checksums download failure exits 3" "3" "$rc"
rm -f "$CTRL/fail_checksums"

# 4. Already on the latest version: no download at all.
CUR_VER=$(grep '^VERSION=' "$GOTOOLS_SH" | cut -d'"' -f2)
cat > "$CTRL/api.json" <<EOF
{"tag_name": "$CUR_VER"}
EOF
S="$TMPDIR/latest"
new_sandbox "$S"
rc=$(run_rc "$S" self-update)
assert_eq "already latest exits 0" "0" "$rc"
out=$(run_out "$S" self-update)
assert_contains "already latest message" "already on the latest" "$out"
cat > "$CTRL/api.json" <<'EOF'
{"tag_name": "v9.9.9"}
EOF

# 5. No hash tool available → refuse with a clear error.
cat > "$STUB_BIN/sha256sum" <<'EOF'
#!/usr/bin/env bash
exit 1
EOF
cat > "$STUB_BIN/shasum" <<'EOF'
#!/usr/bin/env bash
exit 1
EOF
chmod +x "$STUB_BIN/sha256sum" "$STUB_BIN/shasum"
S="$TMPDIR/nohash"
new_sandbox "$S"
rc=$(run_rc "$S" self-update)
assert_eq "missing hash tools exits 1" "1" "$rc"
out=$(run_out "$S" self-update)
assert_contains "missing hash tools message" "Neither sha256sum nor shasum" "$out"

# 6. Go wrapper path ($0 without .sh): updates via go install and reports
#    that integrity is covered by the Go module checksum database.
cat > "$STUB_BIN/go" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "install" ]]; then exit 0; fi
exit 1
EOF
chmod +x "$STUB_BIN/go"
S="$TMPDIR/wrapper"
mkdir -p "$S"
cp "$GOTOOLS_SH" "$S/gotools"
out=""
rc=0
out=$(cd "$S" && env -u GOTOOLS_STRATEGY -u GOTOOLS_DIR -u GOTOOLS_GO_VERSION \
    -u GOTOOLS_MODULE_PREFIX CTRL="$CTRL" PATH="$STUB_BIN:/usr/bin:/bin" \
    "$BASH" "$S/gotools" self-update 2>&1) || rc=$?
assert_eq "wrapper self-update exits 0" "0" "$rc"
assert_contains "wrapper update reports integrity" "🔐 Integrity verified by the Go module checksum database." "$out"

finish
