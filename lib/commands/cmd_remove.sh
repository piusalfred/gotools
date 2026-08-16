# Copyright (c) 2026 Pius Alfred
# License: MIT


# ---- remove --------------------------------------------------------------



cmd_remove() {
    _require_go
    _DRY_RUN=false
    for a in "$@"; do [[ "$a" == "--dry-run" ]] && _DRY_RUN=true; done
    load_config
    _acquire_lock
    if [[ $# -eq 0 ]]; then
        echo "❌ Usage: $(basename "$0") remove <name1> [name2...] [--dry-run]" >&2
        exit $E_USAGE
    fi

    for name in "$@"; do
        [[ "$name" == "--dry-run" ]] && continue

        # The name is path-joined into rm -f / rm -rf below — reject
        # anything that could escape the tools directory.
        if ! _validate_tool_name "$name"; then
            exit $E_USAGE
        fi

        if $_DRY_RUN; then
            echo "  🔍 [dry-run] Would remove $name"
            continue
        fi
        echo "🗑️  Removing $name..."
        case "$GOTOOLS_STRATEGY" in
            unified)
                local modfile="$GOTOOLS_DIR/go.mod"
                if [[ -f "$modfile" ]]; then
                    _reject_symlink "$modfile" "remove $name"
                    local pkg
                    if pkg=$(pkg_for_tool "$name"); then
                        (cd "$GOTOOLS_DIR" && go mod edit -droptool="$pkg" && _go_timeout mod tidy || _timeout_fatal $? "go mod tidy in $GOTOOLS_DIR")
                        echo "  ✅ Dropped $name from $modfile"
                    else
                        echo "  ⚠️  Tool $name not found in $modfile"
                    fi
                fi
                ;;

            split)
                if [[ -f "$GOTOOLS_DIR/$name.mod" ]]; then
                    _reject_symlink "$GOTOOLS_DIR/$name.mod" "remove $name"
                    _reject_symlink "$GOTOOLS_DIR/$name.sum" "remove $name"
                    rm -f "$GOTOOLS_DIR/$name.mod" "$GOTOOLS_DIR/$name.sum"
                    echo "  ✅ Removed $name.mod / $name.sum"
                else
                    echo "  ⚠️  $name.mod not found."
                fi
                ;;

            module)
                if [[ -d "$GOTOOLS_DIR/$name" ]]; then
                    _reject_symlink "$GOTOOLS_DIR/$name" "remove $name"
                    rm -rf "${GOTOOLS_DIR:?}/${name:?}"
                    echo "  ✅ Removed $GOTOOLS_DIR/$name/"
                else
                    echo "  ⚠️  $GOTOOLS_DIR/$name/ not found."
                fi
                ;;

            *)
                echo "❌ Unknown strategy: $GOTOOLS_STRATEGY" >&2
                exit $E_ENVIRONMENT
                ;;
        esac

        # Remove from manifest.
        _manifest_tool_remove "$name"
    done

    _manifest_flush
}
