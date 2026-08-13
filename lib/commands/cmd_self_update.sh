# Copyright (c) 2026 Pius Alfred
# License: MIT

# ---- self-update ---------------------------------------------------------

# _self_update_verify_checksum <file> <expected> — refuse to install a
# self-update whose SHA-256 does not match the release manifest. On
# mismatch the downloaded file is deleted before exiting.
_self_update_verify_checksum() {
    local file="$1" expected_checksum="$2"
    local actual
    if ! actual=$(_sha256 "$file"); then
        echo "❌ Error: Neither sha256sum nor shasum is available." >&2
        rm -f "$file"
        exit $E_GENERIC
    fi
    if [[ "$actual" != "$expected_checksum" ]]; then
        echo "❌ Checksum verification FAILED." >&2
        echo "   Expected: $expected_checksum" >&2
        echo "   Got:      $actual" >&2
        echo "   The downloaded file has been tampered with." >&2
        rm -f "$file"
        echo "   The file has been deleted. Report this at:" >&2
        echo "   https://github.com/$REPO/issues" >&2
        exit $E_GENERIC
    fi
    echo "  🔐 Checksum verified: ${expected_checksum:0:16}..."
}

cmd_self_update() {
    echo "🔍 Checking for updates..."
    local latest_tag
    latest_tag=$(curl -sL "$API_URL" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/') || true

    if [[ -z "$latest_tag" ]]; then
        echo "❌ Error: Could not fetch latest version from GitHub." >&2
        return $E_NETWORK
    fi

    if [[ "$latest_tag" == "$VERSION" ]]; then
        echo "✅ You are already on the latest version ($VERSION)."
        return 0
    fi

    echo "🚀 New version found: $latest_tag (Current: $VERSION)"

    # If $0 doesn't end in .sh, we're running inside the Go binary wrapper.
    # We can't overwrite a compiled binary with a shell script — use go install.
    if [[ "$0" != *.sh ]]; then
        echo "📥 Updating via go install..."
        go install "github.com/piusalfred/gotools/cmd/gotools@$latest_tag" || {
            echo "❌ Error: go install failed." >&2
            return $E_GENERIC
        }
        echo "✨ Successfully updated to $latest_tag!"
        return 0
    fi

    echo "📥 Downloading update..."

    # 1. Fetch the release checksum manifest.
    local checksum_file
    checksum_file=$(mktemp)
    local checksum_url="https://github.com/$REPO/releases/download/$latest_tag/checksums-sha256.txt"
    if ! curl -sSL --fail --connect-timeout 10 "$checksum_url" -o "$checksum_file"; then
        echo "❌ Error: Failed to download checksums file." >&2
        rm -f "$checksum_file"
        return $E_NETWORK
    fi

    # 2. Extract the expected SHA-256 for gotools.sh.
    local expected_checksum
    expected_checksum=$(grep 'gotools\.sh' "$checksum_file" | awk '{print $1}' | head -n1)
    if [[ -z "$expected_checksum" ]]; then
        echo "❌ Error: Could not find checksum for gotools.sh" >&2
        rm -f "$checksum_file"
        return $E_GENERIC
    fi

    # 3. Download the script and verify it before touching $0.
    local tmp_file
    tmp_file=$(mktemp)
    local tag_url="https://raw.githubusercontent.com/$REPO/$latest_tag/gotools.sh"

    if ! curl -sSL --fail --connect-timeout 30 "$tag_url" -o "$tmp_file"; then
        echo "❌ Error: Update download failed." >&2
        rm -f "$tmp_file" "$checksum_file"
        return $E_NETWORK
    fi

    _self_update_verify_checksum "$tmp_file" "$expected_checksum"

    mv "$tmp_file" "$0"
    chmod +x "$0"
    rm -f "$checksum_file"
    echo "✨ Successfully updated to $latest_tag!"
}
