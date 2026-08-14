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

# Manifest ordering tests — the committed .gotools.json is written with a
# deterministic tool order (source, package, version, name) so diffs are easy
# to read, and the sync fingerprint is independent of the in-memory order.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/_helpers.sh"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

category "manifest tool ordering"

# 1. _manifest_flush writes tools sorted by source, package, version, name.
# shellcheck disable=SC2034  # consumed by _manifest_flush via the sourced script
_MANIFEST_TOOLS="gofumpt|go|mvdan.cc/gofumpt|v0.10.0"$'\n'"staticcheck|go|honnef.co/go/tools/cmd/staticcheck|v0.7.0"$'\n'"zzz-early|go|github.com/aaa/zzz|v1.0.0"$'\n'"aaa-late|go|github.com/aaa/zzz|v1.0.0"$'\n'"mylint|go|github.com/aaa/zzz|v1.0.0"$'\n'"addlicense|go|github.com/google/addlicense|v1.2.0"$'\n'
# shellcheck disable=SC2034
GOTOOLS_STRATEGY="split"
# shellcheck disable=SC2034
GOTOOLS_DIR="tools"
# shellcheck disable=SC2034
GOTOOLS_GO_VERSION="inherit"
# shellcheck disable=SC2034
GOTOOLS_MODULE_PREFIX="example.com/test"
_manifest_flush "$TMPDIR/.gotools.json"

expected=$'aaa-late|go|github.com/aaa/zzz|v1.0.0\nmylint|go|github.com/aaa/zzz|v1.0.0\nzzz-early|go|github.com/aaa/zzz|v1.0.0\naddlicense|go|github.com/google/addlicense|v1.2.0\nstaticcheck|go|honnef.co/go/tools/cmd/staticcheck|v0.7.0\ngofumpt|go|mvdan.cc/gofumpt|v0.10.0\n'
_MANIFEST_TOOLS=""
_manifest_parse "$TMPDIR/.gotools.json"
assert_eq "flush sorts by source, package, version, name" "$expected" "$_MANIFEST_TOOLS"

# 2. The sync fingerprint is independent of the in-memory tool order.
# shellcheck disable=SC2034
_MANIFEST_TOOLS="b|go|pkg|v1"$'\n'"a|go|pkg|v1"$'\n'
fp1=$(_sync_fingerprint "1.24")
# shellcheck disable=SC2034
_MANIFEST_TOOLS="a|go|pkg|v1"$'\n'"b|go|pkg|v1"$'\n'
fp2=$(_sync_fingerprint "1.24")
assert_eq "fingerprint is order-independent" "$fp1" "$fp2"

finish
