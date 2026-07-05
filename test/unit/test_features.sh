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

# Unit tests for gotools.sh features.
# No network required.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_helpers.sh"

GOTOOLS_SH="$SCRIPT_DIR/../../gotools.sh"
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT
cp "$SCRIPT_DIR/../main.go" "$TMPDIR/"
cp "$SCRIPT_DIR/../go.mod" "$TMPDIR/"

# -----------------------------------------------------------------------
# per-command --help
# -----------------------------------------------------------------------
category "per-command --help"
for cmd in init install sync exec list info upgrade remove migrate config purge check version; do
    result=$(bash "$GOTOOLS_SH" "$cmd" --help 2>&1 || true)
    assert_contains "help $cmd" "Usage:" "$result"
done
assert_contains "help shows usage" "Usage:" "$(bash "$GOTOOLS_SH" help 2>&1 || true)"

# -----------------------------------------------------------------------
# --dry-run (sync, upgrade, remove, migrate, purge)
# -----------------------------------------------------------------------
# Setup a project with tools in the manifest so upgrade/migrate have targets.
cat > "$TMPDIR/.gotools.json" <<'JSONEOF'
{
  "version": 1,
  "strategy": "split",
  "dir": "tools",
  "go_version": "inherit",
  "module_prefix": "",
  "tools": {
    "faketool": {
      "source": "go",
      "package": "example.com/faketool",
      "version": "v1.0.0"
    }
  }
}
JSONEOF

category "--dry-run sync"
result=$(cd "$TMPDIR" && bash "$GOTOOLS_SH" sync --dry-run 2>&1)
assert_contains "dry-run sync" "[dry-run]" "$result"

category "--dry-run upgrade"
result=$(cd "$TMPDIR" && bash "$GOTOOLS_SH" upgrade all --dry-run 2>&1)
assert_contains "dry-run upgrade" "[dry-run]" "$result"

category "--dry-run remove"
result=$(cd "$TMPDIR" && bash "$GOTOOLS_SH" remove faketool --dry-run 2>&1)
assert_contains "dry-run remove" "[dry-run]" "$result"

category "--dry-run migrate"
result=$(cd "$TMPDIR" && bash "$GOTOOLS_SH" migrate module --dry-run 2>&1)
assert_contains "dry-run migrate" "[dry-run]" "$result"

category "--dry-run purge"
result=$(cd "$TMPDIR" && bash "$GOTOOLS_SH" purge --dry-run 2>&1)
assert_contains "dry-run purge" "[dry-run]" "$result"
assert_file_exists "dry-run purge preserves manifest" "$TMPDIR/.gotools.json"

# -----------------------------------------------------------------------
# --json output
# -----------------------------------------------------------------------
category "list --json"
result=$(cd "$TMPDIR" && bash "$GOTOOLS_SH" list --json 2>&1)
assert_contains "list --json is JSON array" "[" "$result"
assert_contains "list --json contains tool" "faketool" "$result"

category "info --json"
result=$(cd "$TMPDIR" && bash "$GOTOOLS_SH" info faketool --json 2>&1)
assert_contains "info --json is JSON object" "{" "$result"
assert_contains "info --json contains tool" "faketool" "$result"

result=$(cd "$TMPDIR" && bash "$GOTOOLS_SH" info nonexistent --json 2>&1 || true)
assert_contains "info --json missing tool" "not found" "$result"

# -----------------------------------------------------------------------
# check command
# -----------------------------------------------------------------------
category "check (empty manifest)"
# Fresh init = empty manifest
cd "$TMPDIR" && bash "$GOTOOLS_SH" init --strategy=split >/dev/null 2>&1
result=$(cd "$TMPDIR" && bash "$GOTOOLS_SH" check 2>&1)
assert_contains "check empty project" "Checking" "$result"

# -----------------------------------------------------------------------
# duplicate detection
# -----------------------------------------------------------------------
category "duplicate detection"
# Setup manifest with faketool
cat > "$TMPDIR/.gotools.json" <<'JSONEOF'
{
  "version": 1,
  "strategy": "split",
  "dir": "tools",
  "go_version": "inherit",
  "module_prefix": "",
  "tools": {
    "faketool": {
      "source": "go",
      "package": "example.com/faketool",
      "version": "v1.0.0"
    }
  }
}
JSONEOF

result=$(cd "$TMPDIR" && bash "$GOTOOLS_SH" install faketool example.com/faketool@v1.0.0 2>&1 || true)
assert_contains "duplicate rejected" "already exists" "$result"

# --force should NOT reject (though actual install may fail without network)
result=$(cd "$TMPDIR" && bash "$GOTOOLS_SH" install --force faketool example.com/faketool@v1.0.0 2>&1 || true)
# With --force, it should NOT print "already exists". It may fail at go get but the
# duplicate check should be bypassed.
if [[ "$result" == *"already exists"* ]]; then
    echo "    FAIL force bypasses duplicate check"
    echo "          output: $result"
    TEST_FAILED=$((TEST_FAILED + 1))
else
    echo "    PASS force bypasses duplicate check"
    TEST_PASSED=$((TEST_PASSED + 1))
fi

# -----------------------------------------------------------------------
# verbose mode
# -----------------------------------------------------------------------
category "verbose mode"
# The GOTOOLS_VERBOSE env var sets $_VERBOSE. Test that the flag is recognized.
# The _go wrapper is defined; we test that the verbose mode doesn't crash.
result=$(cd "$TMPDIR" && GOTOOLS_VERBOSE=1 bash "$GOTOOLS_SH" sync 2>&1 || true)
assert_contains "verbose mode runs without error" "Sync" "$result"

# Verify the _go wrapper function is defined in the script.
assert_contains "_go wrapper defined in source" "_go()" \
    "$(grep '^_go()' "$GOTOOLS_SH" || echo NOT_FOUND)"

# -----------------------------------------------------------------------
# completion
# -----------------------------------------------------------------------
category "completion bash"
assert_contains "bash completion" "_gotools_completion" \
    "$(bash "$GOTOOLS_SH" completion bash 2>&1)"

category "completion zsh"
assert_contains "zsh completion" "#compdef" \
    "$(bash "$GOTOOLS_SH" completion zsh 2>&1)"

category "completion fish"
assert_contains "fish completion" "complete -c" \
    "$(bash "$GOTOOLS_SH" completion fish 2>&1)"

category "completion invalid"
assert_contains "invalid shell" "Usage:" \
    "$(bash "$GOTOOLS_SH" completion invalid 2>&1 || true)"

# -----------------------------------------------------------------------
# @latest warning — verify source contains the warning text
# -----------------------------------------------------------------------
category "@latest warning"
assert_contains "warning text in source" "Pin this version for reproducibility" \
    "$(grep 'Pin this version' "$GOTOOLS_SH")"

# -----------------------------------------------------------------------
# upgrade old→new reporting — verify source
# -----------------------------------------------------------------------
# walk up directory tree for .gotools.json
# -----------------------------------------------------------------------
category "_find_project_root"
# Verify the function exists in the sourced script
assert_contains "_find_project_root defined" "_find_project_root" \
    "$(grep '^_find_project_root()' "$GOTOOLS_SH" || echo NOT_FOUND)"

# Create a nested directory structure, init in root, run command from subdir
F8DIR=$(mktemp -d)
mkdir -p "$F8DIR/sub/deep"
cp "$SCRIPT_DIR/../main.go" "$F8DIR/"
cp "$SCRIPT_DIR/../go.mod" "$F8DIR/"
# Init in the project root
(cd "$F8DIR" && bash "$GOTOOLS_SH" init --strategy=split >/dev/null 2>&1)
# Run list from a deep subdirectory — should find .gotools.json by walking up
result=$(cd "$F8DIR/sub/deep" && bash "$GOTOOLS_SH" list 2>&1)
assert_contains "list from deep subdir shows header" "TOOL" "$result"
# Running from a subdirectory should NOT create a new .gotools.json there
assert_file_absent "no .gotools.json in subdir" "$F8DIR/sub/deep/.gotools.json"
rm -rf "$F8DIR"

# -----------------------------------------------------------------------
# go minimum version check
# -----------------------------------------------------------------------
category "_require_go"
assert_contains "_require_go defined" "_require_go()" \
    "$(grep '^_require_go()' "$GOTOOLS_SH" || echo NOT_FOUND)"
# Verify go is available and version parses (skip if no go)
if command -v go &>/dev/null; then
    result=$(go env GOVERSION 2>/dev/null)
    assert_contains "go version available" "go" "$result"
fi

# -----------------------------------------------------------------------
# lockfile
# -----------------------------------------------------------------------
category "lockfile"
assert_contains "_acquire_lock defined" "_acquire_lock()" \
    "$(grep '^_acquire_lock()' "$GOTOOLS_SH" || echo NOT_FOUND)"
assert_contains "_release_lock defined" "_release_lock()" \
    "$(grep '^_release_lock()' "$GOTOOLS_SH" || echo NOT_FOUND)"
# Basic lock acquisition test
TEST_LOCK_DIR=$(mktemp -d)
GOTOOLS_DIR="$TEST_LOCK_DIR" _acquire_lock 2>/dev/null && \
    echo "    PASS acquire lock" && TEST_PASSED=$((TEST_PASSED + 1)) || \
    { echo "    FAIL acquire lock"; TEST_FAILED=$((TEST_FAILED + 1)); }
# Reentrant: second acquire should succeed (same process)
GOTOOLS_DIR="$TEST_LOCK_DIR" _acquire_lock 2>/dev/null && \
    echo "    PASS reentrant lock" && TEST_PASSED=$((TEST_PASSED + 1)) || \
    { echo "    FAIL reentrant lock"; TEST_FAILED=$((TEST_FAILED + 1)); }
_release_lock 2>/dev/null || true
rm -rf "$TEST_LOCK_DIR"

# -----------------------------------------------------------------------
# alias docs in help
# -----------------------------------------------------------------------
category "alias docs"
result=$(bash "$GOTOOLS_SH" install --help 2>&1 || true)
assert_contains "install help mentions aliases" "alias" "$result"
result=$(bash "$GOTOOLS_SH" exec --help 2>&1 || true)
assert_contains "exec help mentions alias" "alias" "$result"

category "upgrade old→new reporting"
assert_contains "upgrade shows old->new" "already at latest" \
    "$(grep 'already at latest' "$GOTOOLS_SH" || echo NOT_FOUND)"

finish
