# Copyright (c) 2026 Pius Alfred
# License: MIT

# ---- purge ---------------------------------------------------------------

# _purge_restore_hint — the init command that recreates this project's
# configuration. The tools themselves come back via the restore; the
# strategy/dir/go-version/prefix live only in the manifest and would
# otherwise be lost, so print how to reconstruct them.
_purge_restore_hint() {
    local cmd
    cmd="$(basename "$0") init --strategy=${GOTOOLS_STRATEGY}"
    [[ "${GOTOOLS_DIR:-}" != "tools" ]] && cmd+=" --dir=${GOTOOLS_DIR}"
    [[ -n "${GOTOOLS_GO_VERSION:-}" && "$GOTOOLS_GO_VERSION" != "inherit" ]] && cmd+=" --go=${GOTOOLS_GO_VERSION}"
    [[ -n "${GOTOOLS_MODULE_PREFIX:-}" ]] && cmd+=" --prefix=${GOTOOLS_MODULE_PREFIX}"
    echo "$cmd"
}

# _purge_restore_tools — the reverse of init's go.mod adoption: add every
# managed tool back to the root go.mod at its pinned version via
# `go get -tool`. Runs BEFORE the wipe so a failure leaves gotools intact.
_purge_restore_tools() {
    local root_mod="$_PROJECT_ROOT/go.mod"
    if [[ ! -f "$root_mod" ]]; then
        echo "❌ Cannot restore tools: no go.mod in project root." >&2
        exit $E_ENVIRONMENT
    fi

    local count=0
    local _n _s _p _v
    while IFS='|' read -r _n _s _p _v; do
        [[ -z "$_n" ]] && continue
        if [[ "$_s" != "go" ]]; then
            echo "  ⚠ $_n: source '$_s' — skipping (only go tools can be restored)"
            continue
        fi
        if $_DRY_RUN; then
            echo "  [dry-run] Would restore $_n (${_p}@${_v}) to go.mod"
            count=$((count + 1))
            continue
        fi
        echo "  ⬆ $_n (${_p}@${_v}) → go.mod"
        local _get_out
        if ! _get_out=$(_go get -tool "${_p}@${_v}" 2>&1); then
            echo "❌ Failed to restore $_n" >&2
            echo "$_get_out" >&2
            if echo "$_get_out" | grep -qiE 'dial tcp|i/o timeout|connection timed out|no such host|connection refused|network is unreachable|could not resolve host|fetch failed|timeout exceeded'; then
                exit $E_NETWORK
            fi
            exit $E_GENERIC
        fi
        count=$((count + 1))
    done <<< "$_MANIFEST_TOOLS"

    if $_DRY_RUN; then
        echo "[dry-run] Would restore $count tool(s) to go.mod."
    elif [[ $count -gt 0 ]]; then
        echo "✅ Restored $count tool(s) to go.mod."
    else
        echo "ℹ No tools to restore."
    fi
}

cmd_purge() {
    _DRY_RUN=false
    local restore=false
    for a in "$@"; do
        case "$a" in
            --dry-run) _DRY_RUN=true ;;
            --restore) restore=true ;;
        esac
    done
    load_config
    _acquire_lock

    if $_DRY_RUN; then
        if $restore; then
            _purge_restore_tools
            echo "🔍 [dry-run] To come back: $(_purge_restore_hint)"
        fi
        echo "🔍 [dry-run] Would delete:"
        echo "  - $GOTOOLS_DIR/ (tools directory)"
        echo "  - $MANIFEST_FILE (manifest)"
        return
    fi

    echo "⚠️  WARNING: This will delete the tools directory ('$GOTOOLS_DIR') and '$MANIFEST_FILE'."
    if $restore; then
        echo "  It will also add the managed tools back to your go.mod."
    fi
    echo "This action cannot be undone."
    printf "Are you sure you want to proceed? (type YES to confirm): "
    read -r confirmation

    if [[ "$confirmation" != "YES" ]]; then
        echo "❌ Purge cancelled."
        return 0
    fi

    if $restore; then
        _purge_restore_tools
        echo "💡 To come back: $(_purge_restore_hint)"
    fi

    rm -rf "${GOTOOLS_DIR:?}" "${MANIFEST_FILE:?}"
    # Reset in-memory state.
    _MANIFEST_TOOLS=""
    echo "✅ Purge complete. All tools and configurations have been removed."
}
