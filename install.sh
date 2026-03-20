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

REPO_URL="https://raw.githubusercontent.com/piusalfred/gotools.sh/main/gotools.sh"
BINARY_NAME="gotools.sh"

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
    local install_dir
    install_dir=$(detect_gobin)

    echo "📍 Detected Go bin directory: ${install_dir}"

    mkdir -p "$install_dir"

    echo "⬇️  Downloading ${BINARY_NAME}..."
    curl -fsSL "$REPO_URL" -o "${install_dir}/${BINARY_NAME}"
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
