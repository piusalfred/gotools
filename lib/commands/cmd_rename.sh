# Copyright (c) 2026 Pius Alfred
# License: MIT


# ---- rename --------------------------------------------------------------



# _rename_tool <old> <new> — rename a managed tool: manifest entry, modfiles
# (split), module directory (module), or just the manifest name (unified —
# tool directives carry no name). The caller must hold the lock, have loaded
# config, and have verified `go` is available (the module-line rewrite runs
# `go mod edit`). Exits with the structured codes on failure; on success the
# manifest is flushed exactly once.
_rename_tool() {
    local old="$1" new="$2"

    # Both names are path-joined into modfile/dir paths below — reject
    # anything that could escape the tools directory.
    if ! _validate_tool_name "$old" || ! _validate_tool_name "$new"; then
        exit $E_USAGE
    fi
    if [[ "$old" == "$new" ]]; then
        echo "❌ Rename: the names are identical." >&2
        exit $E_USAGE
    fi

    local entry src pkg ver
    entry=$(_manifest_tool_entry "$old") || {
        echo "❌ Tool '$old' not found in manifest." >&2
        exit $E_TOOL_NOT_FOUND
    }
    IFS='|' read -r src pkg ver <<< "$entry"

    if _manifest_tool_exists "$new"; then
        echo "❌ Tool '$new' already exists in manifest. Remove it first." >&2
        exit $E_USAGE
    fi

    local disk_moved=false
    case "$GOTOOLS_STRATEGY" in
        split)
            local mod_old="$GOTOOLS_DIR/$old.mod" mod_new="$GOTOOLS_DIR/$new.mod"
            local sum_old="$GOTOOLS_DIR/$old.sum" sum_new="$GOTOOLS_DIR/$new.sum"
            _reject_symlink "$mod_old" "rename $old"
            _reject_symlink "$sum_old" "rename $old"
            if [[ -e "$mod_new" || -e "$sum_new" ]]; then
                echo "❌ Refusing: $GOTOOLS_DIR/$new.mod (or .sum) already exists but is not in the manifest." >&2
                exit $E_ENVIRONMENT
            fi
            if [[ ! -f "$mod_old" ]]; then
                echo "⚠️  $mod_old not found — renaming in the manifest only (sync will recreate it)."
            else
                mv "$mod_old" "$mod_new"
                [[ -f "$sum_old" ]] && mv "$sum_old" "$sum_new"
                # The module line embeds the tool name — rewrite it. Roll the
                # move back if go mod edit fails, so a failed rename loses
                # nothing.
                if ! _go mod edit -modfile="$mod_new" -module="$(tool_module_path "$new")" >/dev/null; then
                    mv "$mod_new" "$mod_old"
                    [[ -f "$sum_new" ]] && mv "$sum_new" "$sum_old"
                    echo "❌ go mod edit failed while renaming $old → $new." >&2
                    exit $E_GENERIC
                fi
                disk_moved=true
            fi
            ;;

        module)
            local dir_old="$GOTOOLS_DIR/$old" dir_new="$GOTOOLS_DIR/$new"
            _reject_symlink "$dir_old" "rename $old"
            if [[ -e "$dir_new" ]]; then
                echo "❌ Refusing: $dir_new already exists but is not in the manifest." >&2
                exit $E_ENVIRONMENT
            fi
            if [[ ! -f "$dir_old/go.mod" ]]; then
                echo "⚠️  $dir_old/go.mod not found — renaming in the manifest only (sync will recreate it)."
            else
                mv "$dir_old" "$dir_new"
                if ! _go mod edit -modfile="$dir_new/go.mod" -module="$(tool_module_path "$new")" >/dev/null; then
                    mv "$dir_new" "$dir_old"
                    echo "❌ go mod edit failed while renaming $old → $new." >&2
                    exit $E_GENERIC
                fi
                disk_moved=true
            fi
            ;;

        unified)
            # Tool directives carry no name, and unified name handling
            # RESOLVES names from the directives — a manifest-only rename
            # would desync list/info/exec (they infer the old name from the
            # directive). Refuse with the honest migration path instead of
            # silently breaking the project.
            echo "❌ Rename is not supported with the unified strategy." >&2
            echo "   Tool names there come from the go.mod tool directives, not the manifest." >&2
            echo "   Migrate first:   gotools migrate split    (or module)" >&2
            echo "   Or reinstall:    gotools remove $old && gotools install $new $pkg@$ver" >&2
            exit $E_ENVIRONMENT
            ;;

        *)
            echo "❌ Unknown strategy: $GOTOOLS_STRATEGY" >&2
            exit $E_ENVIRONMENT
            ;;
    esac

    _manifest_tool_set "$new" "$src" "$pkg" "$ver"
    _manifest_tool_remove "$old"
    _manifest_flush

    # Keep the sync fingerprint current: rename has already moved the disk
    # state to match the manifest, so the next sync — including `sync
    # --offline` in CI — takes the fast path instead of re-reconciling.
    # Skipped when the modfile was missing: disk and manifest disagree
    # there, and a stale fingerprint makes sync reinstall it correctly.
    if $disk_moved; then
        _sync_write_fingerprint "$(resolve_go_version)"
    fi
}

# ---- rename -------------------------------------------------------------
cmd_rename() {
    _require_go
    load_config
    _acquire_lock

    _DRY_RUN=false
    local filtered=()
    for a in "$@"; do
        if [[ "$a" == "--dry-run" ]]; then _DRY_RUN=true
        else filtered+=("$a"); fi
    done
    set -- ${filtered[@]+"${filtered[@]}"}

    if [[ $# -ne 2 ]]; then
        echo "❌ Usage: $(basename "$0") rename <old-name> <new-name> [--dry-run]" >&2
        exit $E_USAGE
    fi

    local old="$1" new="$2"

    if ! _validate_tool_name "$old" || ! _validate_tool_name "$new"; then
        exit $E_USAGE
    fi

    if $_DRY_RUN; then
        echo "🔍 [dry-run] Would rename $old → $new"
        return 0
    fi

    echo "✏️  Renaming $old → $new..."
    _rename_tool "$old" "$new"
    echo "✅ Renamed $old → $new"
}
