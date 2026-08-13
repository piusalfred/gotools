# Copyright (c) 2026 Pius Alfred
# License: MIT


# ---- self-update ---------------------------------------------------------



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

    local tmp_file
    tmp_file=$(mktemp)
    local tag_url="https://raw.githubusercontent.com/$REPO/$latest_tag/gotools.sh"

    if curl -sL "$tag_url" -o "$tmp_file"; then
        mv "$tmp_file" "$0"
        chmod +x "$0"
        echo "✨ Successfully updated to $latest_tag!"
    else
        echo "❌ Error: Update download failed." >&2
        rm -f "$tmp_file"
        return $E_NETWORK
    fi
}
