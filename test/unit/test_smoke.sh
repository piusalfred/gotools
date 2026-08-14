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

# Smoke tests — verify the script loads and runs correctly in all execution
# modes. No network required. Catches regressions like the bash -c guard bug.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_helpers.sh"

GOTOOLS_SH="$SCRIPT_DIR/../../gotools.sh"
GOTOOLS_BIN="$SCRIPT_DIR/../../gotools"

category "execution modes"
result=$(bash "$GOTOOLS_SH" version 2>&1)
assert_contains "direct execution prints version" "gotools.sh" "$result"

if [[ -x "$GOTOOLS_BIN" ]]; then
    result=$("$GOTOOLS_BIN" version 2>&1)
    assert_contains "go wrapper prints version" "gotools.sh" "$result"
else
    echo "    SKIP go wrapper: not built (run: make build)"
fi

result=$(bash -c 'source "$1" 2>&1 && echo SOURCED_OK' _ "$GOTOOLS_SH")
assert_contains "source loads without error" "SOURCED_OK" "$result"
result=$(bash -c 'source "$1" 2>&1' _ "$GOTOOLS_SH")
assert_eq "source does not run dispatch" "" "$result"

category "help + usage"
result=$(bash "$GOTOOLS_SH" --help 2>&1 || true)
assert_contains "--help shows usage" "Usage:" "$result"
result=$(bash "$GOTOOLS_SH" 2>&1 || true)
assert_contains "no args shows usage" "Usage:" "$result"
result=$(bash "$GOTOOLS_SH" version 2>&1)
assert_contains "version command" "v0" "$result"

category "init + config + purge"
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT
cp "$SCRIPT_DIR/../main.go" "$TMPDIR/"
cp "$SCRIPT_DIR/../go.mod" "$TMPDIR/"

result=$(cd "$TMPDIR" && bash "$GOTOOLS_SH" init --strategy=split 2>&1)
assert_contains "init creates .gotools.json" "Initialized .gotools.json" "$result"
assert_file_exists "init: .gotools.json on disk" "$TMPDIR/.gotools.json"

result=$(cd "$TMPDIR" && bash "$GOTOOLS_SH" config GOTOOLS_STRATEGY 2>&1)
assert_eq "config reads strategy" "split" "$result"

result=$(cd "$TMPDIR" && bash "$GOTOOLS_SH" list 2>&1)
assert_contains "empty list shows header" "TOOL" "$result"

result=$(cd "$TMPDIR" && echo YES | bash "$GOTOOLS_SH" purge 2>&1)
assert_contains "purge succeeds" "Purge complete" "$result"
assert_file_absent "purge removes manifest" "$TMPDIR/.gotools.json"

category "function availability after source"
for fn in infer_binary_name_from_pkg extract_tools_from_mod extract_version_for_pkg \
          extract_module_for_pkg extract_go_version_from_mod detect_strategy resolve_go_version \
          resolve_module_prefix tool_module_path tool_runnable relative_path \
          _manifest_parse _manifest_flush _manifest_tool_set _manifest_tool_remove \
          _manifest_tool_entry _manifest_tools_list _manifest_config_get _manifest_config_set \
          _manifest_read_version _manifest_validate \
          _version_meets_min _go_version_raw _parse_output_format \
          _file_mtime _lock_pid_live _lock_detect_stale \
          cmd_init cmd_install cmd_sync cmd_exec cmd_list cmd_info cmd_upgrade \
          cmd_remove cmd_migrate cmd_config cmd_purge cmd_version; do
    if type "$fn" >/dev/null 2>&1; then
        echo "    PASS $fn"
        TEST_PASSED=$((TEST_PASSED + 1))
    else
        echo "    FAIL $fn — function not found"
        TEST_FAILED=$((TEST_FAILED + 1))
    fi
done

finish
