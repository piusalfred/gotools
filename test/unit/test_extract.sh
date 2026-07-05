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

# Unit tests for extract_tools_from_mod, extract_pkg_from_mod, extract_version_for_pkg.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_helpers.sh"

# Create fixture go.mod files
FIXTURE_DIR="$SCRIPT_DIR/../fixtures"
mkdir -p "$FIXTURE_DIR"

# Fixture 1: go.mod with single-line tool directive
cat > "$FIXTURE_DIR/single_tool.mod" <<'GOMOD'
module example.com/tools/gofumpt

go 1.26

tool mvdan.cc/gofumpt

require mvdan.cc/gofumpt v0.9.2
GOMOD

# Fixture 2: go.mod with block-form tool directives
cat > "$FIXTURE_DIR/block_tools.mod" <<'GOMOD'
module example.com/tools

go 1.26

tool (
	golang.org/x/tools/cmd/goimports
	honnef.co/go/tools/cmd/staticcheck
	github.com/daixiang0/gci
)

require (
	golang.org/x/tools v0.47.0
	honnef.co/go/tools v0.8.0
	github.com/daixiang0/gci v0.14.0
)
GOMOD

# Fixture 3: go.mod with empty tool block
cat > "$FIXTURE_DIR/empty_tool_block.mod" <<'GOMOD'
module example.com/tools/empty

go 1.26

tool (
)
GOMOD

# Fixture 4: go.mod with subpackage (tool is cmd/staticcheck, require is honnef.co/go/tools)
cat > "$FIXTURE_DIR/subpackage_tool.mod" <<'GOMOD'
module example.com/tools/sub

go 1.26

tool honnef.co/go/tools/cmd/staticcheck

require honnef.co/go/tools v0.8.0
GOMOD

# Fixture 5: go.mod with versioned package
cat > "$FIXTURE_DIR/versioned_tool.mod" <<'GOMOD'
module example.com/tools/ver

go 1.26

tool github.com/securego/gosec/v2/cmd/gosec

require github.com/securego/gosec/v2 v2.23.0
GOMOD

# Fixture 6: go.mod with no tools
cat > "$FIXTURE_DIR/no_tools.mod" <<'GOMOD'
module example.com/no-tools

go 1.26
GOMOD

# Fixture 7: go.mod with both direct and indirect deps
cat > "$FIXTURE_DIR/direct_indirect.mod" <<'GOMOD'
module example.com/tools/di

go 1.26

tool github.com/golangci/golangci-lint/v2/cmd/golangci-lint

require (
	github.com/golangci/golangci-lint/v2 v2.11.4
	github.com/foo/bar v1.0.0 // indirect
)
GOMOD

# Fixture 8: go.mod with require block (multiline)
cat > "$FIXTURE_DIR/require_block.mod" <<'GOMOD'
module example.com/tools/rb

go 1.26

tool github.com/google/addlicense

require github.com/google/addlicense v1.2.0

require (
	golang.org/x/sync v0.19.0
	golang.org/x/mod v0.33.0 // indirect
)
GOMOD

echo "==> extract_tools_from_mod"

category "single-line tool directive"
result=$(extract_tools_from_mod "$FIXTURE_DIR/single_tool.mod")
assert_eq "single tool extracted" "mvdan.cc/gofumpt" "$result"

category "block-form tool directives"
result=$(extract_tools_from_mod "$FIXTURE_DIR/block_tools.mod")
assert_contains "block tool: goimports" "golang.org/x/tools/cmd/goimports" "$result"
assert_contains "block tool: staticcheck" "honnef.co/go/tools/cmd/staticcheck" "$result"
assert_contains "block tool: gci" "github.com/daixiang0/gci" "$result"
# Should have exactly 3 tools
count=$(echo "$result" | grep -c .)
assert_eq "block tool: exactly 3 tools" "3" "$count"

category "empty tool block"
result=$(extract_tools_from_mod "$FIXTURE_DIR/empty_tool_block.mod")
assert_eq "empty tool block returns nothing" "" "$result"

category "no tools"
result=$(extract_tools_from_mod "$FIXTURE_DIR/no_tools.mod")
assert_eq "no tools returns nothing" "" "$result"

echo ""
echo "==> extract_pkg_from_mod"

category "returns first tool"
result=$(extract_pkg_from_mod "$FIXTURE_DIR/block_tools.mod")
assert_eq "first tool from block" "golang.org/x/tools/cmd/goimports" "$result"

result=$(extract_pkg_from_mod "$FIXTURE_DIR/single_tool.mod")
assert_eq "only tool" "mvdan.cc/gofumpt" "$result"

result=$(extract_pkg_from_mod "$FIXTURE_DIR/empty_tool_block.mod")
assert_eq "empty block returns empty" "" "$result"

echo ""
echo "==> extract_version_for_pkg"

category "exact package match"
result=$(extract_version_for_pkg "$FIXTURE_DIR/single_tool.mod" "mvdan.cc/gofumpt")
assert_eq "exact match" "v0.9.2" "$result"

category "subpackage match (strips path components)"
result=$(extract_version_for_pkg "$FIXTURE_DIR/subpackage_tool.mod" "honnef.co/go/tools/cmd/staticcheck")
assert_eq "subpackage to module root" "v0.8.0" "$result"

category "versioned module match"
result=$(extract_version_for_pkg "$FIXTURE_DIR/versioned_tool.mod" "github.com/securego/gosec/v2/cmd/gosec")
assert_eq "versioned module" "v2.23.0" "$result"

category "package not found"
result=$(extract_version_for_pkg "$FIXTURE_DIR/single_tool.mod" "example.com/nonexistent")
assert_eq "nonexistent returns empty" "" "$result"

category "direct dependency in require block"
result=$(extract_version_for_pkg "$FIXTURE_DIR/direct_indirect.mod" "github.com/golangci/golangci-lint/v2/cmd/golangci-lint")
assert_eq "direct dep in block" "v2.11.4" "$result"

category "require block with inline and multiline"
result=$(extract_version_for_pkg "$FIXTURE_DIR/require_block.mod" "github.com/google/addlicense")
assert_eq "inline require" "v1.2.0" "$result"

result=$(extract_version_for_pkg "$FIXTURE_DIR/require_block.mod" "golang.org/x/sync")
assert_eq "in require block" "v0.19.0" "$result"

result=$(extract_version_for_pkg "$FIXTURE_DIR/require_block.mod" "golang.org/x/mod")
assert_eq "indirect in require block" "v0.33.0" "$result"

cleanup() { rm -rf "$FIXTURE_DIR"; }
trap cleanup EXIT

finish
