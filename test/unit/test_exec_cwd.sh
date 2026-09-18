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

# Working-directory tests for exec: _dir_in_module decides whether the tool
# can run in the invocation directory (go tool -modfile refuses outside a
# main module), and _INVOCATION_DIR must survive the cd to the project root.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_helpers.sh"

T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

category "_dir_in_module"

mkdir -p "$T/mod/pkg/deep"
touch "$T/mod/go.mod"
mkdir -p "$T/plain/pkg"

_dir_in_module "$T/mod" && rc=0 || rc=1
assert_eq "module root itself" "0" "$rc"

_dir_in_module "$T/mod/pkg" && rc=0 || rc=1
assert_eq "direct child of the module root" "0" "$rc"

_dir_in_module "$T/mod/pkg/deep" && rc=0 || rc=1
assert_eq "nested package directory" "0" "$rc"

_dir_in_module "$T/plain/pkg" && rc=0 || rc=1
assert_eq "directory outside any module" "1" "$rc"

_dir_in_module "" && rc=0 || rc=1
assert_eq "empty argument" "1" "$rc"

# A go.mod that is a directory, not a file, must not count.
mkdir -p "$T/fake/go.mod"
_dir_in_module "$T/fake" && rc=0 || rc=1
assert_eq "go.mod as a directory does not count" "1" "$rc"

category "_INVOCATION_DIR"

assert_eq "captured at load time" "$PWD" "$_INVOCATION_DIR"

# _find_project_root cds to the project root; _INVOCATION_DIR must not
# follow it, because cmd_exec runs the tool there. The assignment mirrors
# what config.sh does when the script loads.
mkdir -p "$T/proj/sub"
printf '{\n  "strategy": "split",\n  "dir": "tools",\n  "tools": {}\n}\n' > "$T/proj/.gotools.json"
captured=$(cd "$T/proj/sub" && _INVOCATION_DIR="$PWD" && _find_project_root \
    && echo "$_INVOCATION_DIR|$PWD|$_PROJECT_ROOT")
assert_eq "invocation dir survives the project-root cd" \
    "$T/proj/sub|$T/proj|$T/proj" "$captured"

echo ""
echo "  ** Summary: $TEST_PASSED passed, $TEST_FAILED failed"
[[ $TEST_FAILED -eq 0 ]] || exit 1
