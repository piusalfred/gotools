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

set -euo pipefail

REPO="piusalfred/gotools"
BINARY_NAME="gotools.sh"

resolve_version() {
    local requested="${VERSION:-latest}"

    if [[ "$requested" == "latest" ]]; then
        # Fetch the latest release tag from the GitHub API
        local tag
        tag=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
            | grep '"tag_name"' \
            | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')

        if [[ -z "$tag" ]]; then
            echo "⚠️  Could not determine latest release, falling back to main branch." >&2
            echo "main"
            return
        fi

        echo "$tag"
        return
    fi

    # Normalise: accept both "v0.0.10" and "0.0.10" as valid input.
    if [[ "$requested" != v* ]]; then
        requested="v${requested}"
    fi

    echo "$requested"
}

detect_gobin() {
    # 1. GOBIN env var (highest priority)
    if [[ -n "${GOBIN:-}" ]]; then
        echo "$GOBIN"
        return
    fi

    # 2. Ask the go toolchain
    if command -v go &>/dev/null; then
        local gobin
        gobin=$(go env GOBIN 2>/dev/null || true)
        if [[ -n "$gobin" ]]; then
            echo "$gobin"
            return
        fi

        # 3. Fall back to GOPATH/bin
        local gopath
        gopath=$(go env GOPATH 2>/dev/null || true)
        if [[ -n "$gopath" ]]; then
            echo "${gopath%%:*}/bin"
            return
        fi
    fi

    # 4. Last resort: ~/go/bin (Go default)
    echo "${HOME}/go/bin"
}

main() {
    local version
    version=$(resolve_version)

    local download_url="https://raw.githubusercontent.com/${REPO}/${version}/${BINARY_NAME}"

    local install_dir
    install_dir=$(detect_gobin)

    echo "📍 Detected Go bin directory: ${install_dir}"
    echo "📦 Version: ${version}"

    mkdir -p "$install_dir"

    echo "⬇️  Downloading ${BINARY_NAME} (${version})..."
    if ! curl -fsSL "$download_url" -o "${install_dir}/${BINARY_NAME}"; then
        echo "❌ Download failed. Check that version '${version}' exists at:" >&2
        echo "   https://github.com/${REPO}/releases" >&2
        exit 1
    fi

    chmod +x "${install_dir}/${BINARY_NAME}"

    echo "✅ Installed ${BINARY_NAME} to ${install_dir}/${BINARY_NAME}"

    # Verify it's on PATH
    if command -v "$BINARY_NAME" &>/dev/null; then
        echo "🎉 ${BINARY_NAME} is ready to use!"
    else
        echo ""
        echo "⚠️  ${install_dir} is not in your PATH."
        echo "   Add it by running:"
        echo ""
        echo "     export PATH=\"${install_dir}:\$PATH\""
        echo ""
    fi
}

main
