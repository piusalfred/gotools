# Copyright (c) 2026 Pius Alfred
# License: MIT


# ---- purge ---------------------------------------------------------------



cmd_purge() {
    _DRY_RUN=false
    for a in "$@"; do [[ "$a" == "--dry-run" ]] && _DRY_RUN=true; done
    load_config
    _acquire_lock

    if $_DRY_RUN; then
        echo "🔍 [dry-run] Would delete:"
        echo "  - $GOTOOLS_DIR/ (tools directory)"
        echo "  - $MANIFEST_FILE (manifest)"
        return
    fi

    echo "⚠️  WARNING: This will delete the tools directory ('$GOTOOLS_DIR') and '$MANIFEST_FILE'."
    echo "This action cannot be undone."
    printf "Are you sure you want to proceed? (type YES to confirm): "
    read -r confirmation

    if [[ "$confirmation" != "YES" ]]; then
        echo "❌ Purge cancelled."
        return 0
    fi

    rm -rf "${GOTOOLS_DIR:?}" "${MANIFEST_FILE:?}"
    # Reset in-memory state.
    _MANIFEST_TOOLS=""
    echo "✅ Purge complete. All tools and configurations have been removed."
}
