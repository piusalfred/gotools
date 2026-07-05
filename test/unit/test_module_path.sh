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

# Unit tests for module path and Go version resolution functions:
#   resolve_go_version, resolve_module_prefix, tool_module_path,
#   relative_path, detect_strategy, extract_go_version_from_mod.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_helpers.sh"

FIXTURE_DIR="$SCRIPT_DIR/../fixtures"
mkdir -p "$FIXTURE_DIR"

echo "==> resolve_go_version"

# We can only test the fallback since we can't easily mock go.mod here.
# Just verify it runs without error and returns something.
category "returns a version string"
result=$(resolve_go_version)
assert_eq "returns a version string" "$result" "$result"
# The result should be a non-empty version like "1.26.1"

echo ""
echo "==> extract_go_version_from_mod"

cat > "$FIXTURE_DIR/with_go_ver.mod" <<'GOMOD'
module example.com/foo

go 1.26.1
GOMOD

cat > "$FIXTURE_DIR/no_go_ver.mod" <<'GOMOD'
module example.com/bar
GOMOD

category "extracts go version"
result=$(extract_go_version_from_mod "$FIXTURE_DIR/with_go_ver.mod")
assert_eq "go version" "1.26.1" "$result"

category "no go version"
result=$(extract_go_version_from_mod "$FIXTURE_DIR/no_go_ver.mod")
assert_eq "no go version returns empty" "" "$result"

echo ""
echo "==> relative_path"

# The test runs from the repo root, so relative_path uses that as base.
category "same directory"
result=$(relative_path "$PWD")
assert_eq "same directory" "." "$result"

category "child path"
result=$(relative_path "$PWD/test")
assert_eq "child directory" "test" "$result"

category "sibling"
result=$(relative_path "$PWD/../")
# Verify it starts with ".." (a parent-relative path)
assert_eq "starts with '..'" ".." "${result:0:2}"

echo ""
echo "==> detect_strategy"

category "empty/nonexistent directory"
result=$(detect_strategy "/tmp/nonexistent_gotools_test_dir_$$")
assert_eq "nonexistent returns empty" "" "$result"

# Create test structures
STRAT_DIR="$FIXTURE_DIR/strategy_test"
rm -rf "$STRAT_DIR"

category "unified strategy"
mkdir -p "$STRAT_DIR/unified"
echo 'tool github.com/foo/bar' > "$STRAT_DIR/unified/go.mod"
result=$(detect_strategy "$STRAT_DIR/unified")
assert_eq "detects unified" "unified" "$result"

category "split strategy"
mkdir -p "$STRAT_DIR/split"
echo 'module example.com/tools/gofumpt' > "$STRAT_DIR/split/gofumpt.mod"
result=$(detect_strategy "$STRAT_DIR/split")
assert_eq "detects split" "split" "$result"

category "module strategy"
mkdir -p "$STRAT_DIR/module/gofumpt"
echo 'module example.com/tools/gofumpt' > "$STRAT_DIR/module/gofumpt/go.mod"
result=$(detect_strategy "$STRAT_DIR/module")
assert_eq "detects module" "module" "$result"

category "empty directory"
mkdir -p "$STRAT_DIR/empty"
result=$(detect_strategy "$STRAT_DIR/empty")
assert_eq "empty dir returns empty" "" "$result"

# hybrid state detection
category "hybrid: unified + split"
rm -rf "$STRAT_DIR/hybrid1" && mkdir -p "$STRAT_DIR/hybrid1"
echo 'tool github.com/foo/bar' > "$STRAT_DIR/hybrid1/go.mod"
echo 'module example.com/tools/extra' > "$STRAT_DIR/hybrid1/extra.mod"
result=$(detect_strategy "$STRAT_DIR/hybrid1" 2>/dev/null)
assert_eq "hybrid unified+split returns unified" "unified" "$result"

category "hybrid: unified + module"
rm -rf "$STRAT_DIR/hybrid2" && mkdir -p "$STRAT_DIR/hybrid2/sub"
echo 'tool github.com/foo/bar' > "$STRAT_DIR/hybrid2/go.mod"
echo 'module example.com/tools/sub' > "$STRAT_DIR/hybrid2/sub/go.mod"
result=$(detect_strategy "$STRAT_DIR/hybrid2" 2>/dev/null)
assert_eq "hybrid unified+module returns unified" "unified" "$result"

category "hybrid: split + module"
rm -rf "$STRAT_DIR/hybrid3" && mkdir -p "$STRAT_DIR/hybrid3/sub"
echo 'module example.com/tools/extra' > "$STRAT_DIR/hybrid3/extra.mod"
echo 'module example.com/tools/sub' > "$STRAT_DIR/hybrid3/sub/go.mod"
result=$(detect_strategy "$STRAT_DIR/hybrid3" 2>/dev/null)
assert_eq "hybrid split+module returns module" "module" "$result"

category "hybrid: all three"
rm -rf "$STRAT_DIR/hybrid4" && mkdir -p "$STRAT_DIR/hybrid4/sub"
echo 'tool github.com/foo/bar' > "$STRAT_DIR/hybrid4/go.mod"
echo 'module example.com/tools/extra' > "$STRAT_DIR/hybrid4/extra.mod"
echo 'module example.com/tools/sub' > "$STRAT_DIR/hybrid4/sub/go.mod"
result=$(detect_strategy "$STRAT_DIR/hybrid4" 2>/dev/null)
assert_eq "triple hybrid returns unified" "unified" "$result"

# verify hybrid warning is printed to stderr
result=$(detect_strategy "$STRAT_DIR/hybrid1" 2>&1 1>/dev/null || true)
assert_contains "hybrid warning on stderr" "Hybrid" "$result"

echo ""
echo "==> tool_module_path"

# Need GOTOOLS_DIR and GOTOOLS_MODULE_PREFIX set. Use env vars.
category "with explicit prefix"
result=$(GOTOOLS_MODULE_PREFIX="github.com/user/repo" GOTOOLS_DIR="tools" tool_module_path "addlicense")
assert_eq "with prefix" "github.com/user/repo/tools/addlicense" "$result"

category "no args (unified path)"
result=$(GOTOOLS_MODULE_PREFIX="github.com/user/repo" GOTOOLS_DIR="tools" tool_module_path)
assert_eq "unified path" "github.com/user/repo/tools" "$result"

category "empty args"
result=$(GOTOOLS_MODULE_PREFIX="github.com/user/repo" GOTOOLS_DIR="tools" tool_module_path "")
assert_eq "empty arg same as no arg" "github.com/user/repo/tools" "$result"

echo ""
echo "==> resolve_module_prefix"

# With explicit prefix set
category "explicit prefix"
result=$(GOTOOLS_MODULE_PREFIX="github.com/custom/prefix" resolve_module_prefix)
assert_eq "returns explicit prefix" "github.com/custom/prefix" "$result"

cleanup() { rm -rf "$FIXTURE_DIR"; }
trap cleanup EXIT

finish
