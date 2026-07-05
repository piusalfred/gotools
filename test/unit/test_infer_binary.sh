#!/usr/bin/env bash
# Unit tests for infer_binary_name_from_pkg, resolve_binary_name, pkg_for_tool.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_helpers.sh"

echo "==> infer_binary_name_from_pkg"

# Standard /cmd/ path
category "standard /cmd/ paths"
assert_eq "golang.org/x/tools/cmd/goimports" \
    "goimports" "$(infer_binary_name_from_pkg "golang.org/x/tools/cmd/goimports")"

assert_eq "honnef.co/go/tools/cmd/staticcheck" \
    "staticcheck" "$(infer_binary_name_from_pkg "honnef.co/go/tools/cmd/staticcheck")"

# With /vN version suffix
category "paths with /vN version suffix"
assert_eq "github.com/goreleaser/goreleaser/v2" \
    "goreleaser" "$(infer_binary_name_from_pkg "github.com/goreleaser/goreleaser/v2")"

assert_eq "github.com/securego/gosec/v2/cmd/gosec" \
    "gosec" "$(infer_binary_name_from_pkg "github.com/securego/gosec/v2/cmd/gosec")"

assert_eq "github.com/foo/bar/v3/cmd/bar" \
    "bar" "$(infer_binary_name_from_pkg "github.com/foo/bar/v3/cmd/bar")"

# Simple path (no /cmd/, no /vN)
category "simple paths (basename fallback)"
assert_eq "github.com/google/addlicense" \
    "addlicense" "$(infer_binary_name_from_pkg "github.com/google/addlicense")"

assert_eq "github.com/daixiang0/gci" \
    "gci" "$(infer_binary_name_from_pkg "github.com/daixiang0/gci")"

assert_eq "mvdan.cc/gofumpt" \
    "gofumpt" "$(infer_binary_name_from_pkg "mvdan.cc/gofumpt")"

# Non-github domains
category "non-github domains"
assert_eq "honnef.co/go/tools/cmd/staticcheck" \
    "staticcheck" "$(infer_binary_name_from_pkg "honnef.co/go/tools/cmd/staticcheck")"

assert_eq "go.uber.org/mock/mockgen" \
    "mockgen" "$(infer_binary_name_from_pkg "go.uber.org/mock/mockgen")"

# Edge cases
category "edge cases"
assert_eq "single-token" \
    "single-token" "$(infer_binary_name_from_pkg "single-token")"

assert_eq "example.com" \
    "example.com" "$(infer_binary_name_from_pkg "example.com")"

# With version in unusual positions
category "version suffix in unusual positions"
assert_eq "github.com/foo/bar/cmd/bar/v2" \
    "bar" "$(infer_binary_name_from_pkg "github.com/foo/bar/cmd/bar/v2")"

assert_eq "github.com/foo/bar/v2" \
    "bar" "$(infer_binary_name_from_pkg "github.com/foo/bar/v2")"

assert_eq "github.com/foo/v10/cmd/foo" \
    "foo" "$(infer_binary_name_from_pkg "github.com/foo/v10/cmd/foo")"

finish
