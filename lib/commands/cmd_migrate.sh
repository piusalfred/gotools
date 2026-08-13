# Copyright (c) 2026 Pius Alfred
# License: MIT


# ---- migrate -------------------------------------------------------------



cmd_migrate() {
    _require_go
    _DRY_RUN=false
    for a in "$@"; do [[ "$a" == "--dry-run" ]] && _DRY_RUN=true; done
    if [[ $# -eq 0 ]]; then
        echo "❌ Usage: $(basename "$0") migrate <unified|split|module> [--dry-run]" >&2
        exit $E_USAGE
    fi

    local target_strategy="$1"

    case "$target_strategy" in
        unified|split|module) ;;
        *) echo "❌ Invalid strategy: $target_strategy (must be unified, split, or module)" >&2; exit $E_USAGE ;;
    esac

    load_config
    _acquire_lock

    # Use the on-disk structure as the source of truth for what we're
    # migrating *from*, not the config file (which may already be updated).
    local current_strategy
    current_strategy=$(detect_strategy "$GOTOOLS_DIR")
    current_strategy="${current_strategy:-$GOTOOLS_STRATEGY}"

    if [[ "$current_strategy" == "$target_strategy" ]]; then
        echo "ℹ️  Already using strategy '$target_strategy'. Nothing to do."
        return 0
    fi

    echo "🔀 Migrating from '$current_strategy' to '$target_strategy'..."

    # 1. Read the tools using the on-disk strategy, not the configured one.
    GOTOOLS_STRATEGY="$current_strategy"
    local tool_list
    tool_list=$(extract_tools_with_versions)

    if [[ -z "$tool_list" ]]; then
        echo "⚠️  No tools found to migrate."
    else
        echo "📋 Tools to migrate:"
        while IFS= read -r line; do
            echo "   $line"
        done <<< "$tool_list"
    fi

    if $_DRY_RUN; then
        echo "🔍 [dry-run] Would migrate $GOTOOLS_DIR/ from '$current_strategy' to '$target_strategy'."
        return
    fi

    # 2. Capture the Go version.
    local go_ver
    go_ver=$(resolve_go_version)

    # 3. Wipe old tools directory structure.
    echo "🧹 Cleaning old tools directory ($GOTOOLS_DIR)..."

    rm -rf "${GOTOOLS_DIR:?}"
    mkdir -p "$GOTOOLS_DIR"

    # 4. Update the manifest and force the live variable so that
    #    subsequent load_config / cmd_install calls in this process
    #    use the target strategy (not the old one).
    GOTOOLS_STRATEGY="$target_strategy"
    _manifest_flush
    _CONFIG_LOADED=true

    # 5. Re-initialize the new strategy structure.
    echo "🔧 Initializing new strategy ($target_strategy)..."

    case "$target_strategy" in
        unified)
            (cd "$GOTOOLS_DIR" && go mod init "$(tool_module_path)" && go mod edit -go="$go_ver")
            ;;
        split|module)
            ;;
    esac

    # 6. Re-install all tools at their exact pinned versions.
    # Clear the in-memory tool store so cmd_install doesn't refuse them as duplicates.
    _MANIFEST_TOOLS=""
    if [[ -n "$tool_list" ]]; then
        echo "📦 Re-installing tools under '$target_strategy' strategy..."
        while IFS=' ' read -r name pkg_at_version; do
            [[ -z "$name" ]] && continue
            echo "  → $name ($pkg_at_version)"
            cmd_install "$name" "$pkg_at_version"
        done <<< "$tool_list"
    fi

    echo "✅ Migration from '$current_strategy' to '$target_strategy' complete."
}
