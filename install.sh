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
BINARY_NAME="gotools"

# DEFAULT_VERSION is what gets installed when VERSION is not set. Here, in the
# repository, it is "latest". The release workflow stamps the release's own tag
# into the copy it attaches to the release (and lists that copy's SHA-256 in
# checksums-sha256.txt), so the installer published with v1.2.3 installs v1.2.3
# without asking the API what is newest.
DEFAULT_VERSION="latest"

# Exit codes mirror gotools' public contract (see docs/USAGE.md).
# This script is standalone, so the subset it needs is duplicated here
# deliberately.
E_GENERIC=1
E_USAGE=2
E_NETWORK=3

# _abort <code> <msg...> — print an error to stderr and exit.
_abort() {
    local code="$1"
    shift
    echo "❌ $*" >&2
    exit "$code"
}

# _sha256 <file> — print a file's SHA-256: sha256sum on Linux,
# shasum -a 256 on macOS; rc 1 if neither exists.
_sha256() {
    if command -v sha256sum &>/dev/null; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum &>/dev/null; then
        shasum -a 256 "$1" | awk '{print $1}'
    else
        return 1
    fi
}

# _asset_name — the release asset for this machine, e.g. gotools-linux-amd64.
# Only the platforms the release workflow builds are accepted.
_asset_name() {
    local os arch
    case "$(uname -s)" in
        Linux) os="linux" ;;
        Darwin) os="darwin" ;;
        *) return 1 ;;
    esac
    case "$(uname -m)" in
        x86_64 | amd64) arch="amd64" ;;
        arm64 | aarch64) arch="arm64" ;;
        *) return 1 ;;
    esac
    echo "${BINARY_NAME}-${os}-${arch}"
}

# _validate_version_tag <tag> — reject anything that is not tag-shaped before
# it reaches a URL. The authoritative check is the checksum-manifest fetch
# (which 404s for any non-release ref); this just fails fast and keeps
# branch names, paths and option-looking strings out of the request.
_validate_version_tag() {
    local tag="$1"
    case "$tag" in
        "" | -*) return 1 ;;
        */* | *..* | *[[:space:]]*) return 1 ;;
    esac
    [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-+][A-Za-z0-9._-]+)?$ ]]
}

resolve_version() {
    local requested="${VERSION:-$DEFAULT_VERSION}"

    if [[ "$requested" == "latest" ]]; then
        # Resolve "latest" through the release API. There is deliberately NO
        # fallback to the main branch: silently installing unreleased,
        # unchecksummed development code is exactly the failure mode this
        # installer is supposed to prevent.
        local tag=""
        tag=$(curl -fsSL --connect-timeout 10 "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
            | grep '"tag_name"' \
            | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/') || true

        if [[ -z "$tag" ]]; then
            echo "❌ Could not determine the latest release from the GitHub API." >&2
            echo "   Retry, or pin an explicit version:" >&2
            echo "     VERSION=v0.6.7 curl -fsSL .../install.sh | bash" >&2
            exit $E_NETWORK
        fi

        echo "$tag"
        return
    fi

    # Normalise: accept both "v0.0.10" and "0.0.10" as valid input.
    if [[ "$requested" != v* ]]; then
        requested="v${requested}"
    fi

    if ! _validate_version_tag "$requested"; then
        echo "❌ Invalid VERSION: ${VERSION:-$DEFAULT_VERSION}" >&2
        echo "   Expected a release tag such as v0.6.7." >&2
        echo "   Branches and arbitrary refs are not installable — every" >&2
        echo "   supported version is published as a release with checksums." >&2
        exit $E_USAGE
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

# _expected_checksum <manifest> <asset> — print the published SHA-256 for
# <asset>. The manifest is `sha256sum` output ("<hash>  <file>"). Anchor on
# the whole filename so one platform's binary can never be mistaken for
# another's, and require a well-formed hash so a truncated manifest is caught.
_expected_checksum() {
    local manifest="$1" asset="$2" hash=""
    hash=$(awk -v n="$asset" '{ f = $2; sub(/^\*/, "", f); if (f == n) { print $1; exit } }' "$manifest" 2>/dev/null) || true
    [[ "$hash" =~ ^[0-9a-f]{64}$ ]] || return 1
    echo "$hash"
}

# Temp files live next to the install target so the final mv is an atomic
# same-filesystem rename, and are cleaned up on every exit path.
_TMP_BIN=""
_TMP_MANIFEST=""
cleanup() {
    rm -f "${_TMP_BIN:-}" "${_TMP_MANIFEST:-}"
}
trap cleanup EXIT

main() {
    local asset
    asset=$(_asset_name) || {
        echo "❌ Unsupported platform: $(uname -s)/$(uname -m)." >&2
        echo "   Prebuilt binaries exist for linux and darwin on amd64 and arm64." >&2
        echo "   Install with Go instead:" >&2
        echo "     go install github.com/${REPO}/cmd/gotools@latest" >&2
        exit $E_GENERIC
    }

    local version
    version=$(resolve_version)

    local download_url="https://github.com/${REPO}/releases/download/${version}/${asset}"
    local manifest_url="https://github.com/${REPO}/releases/download/${version}/checksums-sha256.txt"

    local install_dir
    install_dir=$(detect_gobin)

    echo "📍 Detected Go bin directory: ${install_dir}"
    echo "📦 Version: ${version} (${asset})"

    mkdir -p "$install_dir"

    _TMP_MANIFEST=$(mktemp "${install_dir}/.gotools-checksums.XXXXXX") \
        || _abort $E_GENERIC "Could not create a temporary file in ${install_dir}."
    _TMP_BIN=$(mktemp "${install_dir}/.${BINARY_NAME}.XXXXXX") \
        || _abort $E_GENERIC "Could not create a temporary file in ${install_dir}."

    # 1. Fetch the release checksum manifest. This doubles as the "is this a
    #    real release?" check — the URL 404s for branches and unknown tags.
    echo "🔎 Fetching checksums for ${version}..."
    local rc=0
    curl -fsSL --connect-timeout 15 "$manifest_url" -o "$_TMP_MANIFEST" || rc=$?
    if [[ $rc -eq 22 ]]; then
        echo "❌ No checksum manifest published for '${version}'." >&2
        echo "   Either the version does not exist, or it predates checksum" >&2
        echo "   publishing (added in v0.6.0). Pick a release from:" >&2
        echo "   https://github.com/${REPO}/releases" >&2
        exit $E_USAGE
    elif [[ $rc -ne 0 ]]; then
        _abort $E_NETWORK "Could not download the checksum manifest (curl exit ${rc})."
    fi

    # 2. Extract the expected hash BEFORE downloading the payload, so a
    #    release without a published hash never reaches the install path.
    local expected=""
    if ! expected=$(_expected_checksum "$_TMP_MANIFEST" "$asset"); then
        echo "❌ The release manifest for '${version}' has no usable checksum for ${asset}." >&2
        echo "   Refusing to install an unverifiable binary." >&2
        echo "   Use a release that publishes ${asset}, or install the" >&2
        echo "   checksum-database-verified binary instead:" >&2
        echo "     go install github.com/${REPO}/cmd/gotools@${version}" >&2
        exit $E_GENERIC
    fi

    # 3. Download to a temp file — NEVER straight to the install path. A
    #    truncated body (curl exits 18 mid-transfer) would otherwise overwrite
    #    a working install with a partial file.
    echo "⬇️  Downloading ${asset} (${version})..."
    rc=0
    curl -fsSL --connect-timeout 30 "$download_url" -o "$_TMP_BIN" || rc=$?
    if [[ $rc -ne 0 ]]; then
        echo "❌ Download failed (curl exit ${rc}). Check that version '${version}' exists at:" >&2
        echo "   https://github.com/${REPO}/releases" >&2
        exit $E_NETWORK
    fi

    # 4. Verify before anything touches the installed binary.
    local actual=""
    if ! actual=$(_sha256 "$_TMP_BIN"); then
        _abort $E_GENERIC "Neither sha256sum nor shasum is available — cannot verify the download."
    fi
    if [[ "$actual" != "$expected" ]]; then
        echo "❌ Checksum verification FAILED." >&2
        echo "   Expected: $expected" >&2
        echo "   Got:      $actual" >&2
        echo "   The downloaded file has been tampered with or is incomplete." >&2
        echo "   Nothing was installed. Report this at:" >&2
        echo "   https://github.com/${REPO}/issues" >&2
        exit $E_GENERIC
    fi
    echo "  🔐 Checksum verified: ${expected:0:16}..."

    # 5. The verified binary must run before it replaces anything.
    chmod +x "$_TMP_BIN"
    if ! "$_TMP_BIN" version >/dev/null 2>&1; then
        _abort $E_GENERIC "The downloaded binary failed its self-check. Nothing was installed."
    fi

    # 6. Activate atomically. Only now can the existing install be replaced.
    mv "$_TMP_BIN" "${install_dir}/${BINARY_NAME}" \
        || _abort $E_GENERIC "Could not install to ${install_dir}/${BINARY_NAME}."
    _TMP_BIN=""

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
