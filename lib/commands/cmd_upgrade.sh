# Copyright (c) 2026 Pius Alfred
# License: MIT


# ---- upgrade -------------------------------------------------------------



cmd_upgrade() {
    _require_go
    _DRY_RUN=false
    # Filter flags out so they never leak into the target list.
    local filtered=()
    for a in "$@"; do
        case "$a" in
            --dry-run) _DRY_RUN=true ;;
            --offline) _OFFLINE=true ;;
            *) filtered+=("$a") ;;
        esac
    done
    _parse_offline "$@"
    set -- ${filtered[@]+"${filtered[@]}"}

    load_config
    _acquire_lock
    if [[ $# -eq 0 ]]; then
        echo "❌ Usage: $(basename "$0") upgrade <name|all> [--dry-run]" >&2
        exit $E_USAGE
    fi

    # Upgrading resolves @latest from the proxy — refuse in offline mode.
    if ${_OFFLINE:-false}; then
        echo "❌ Offline mode: cannot upgrade tools without network." >&2
        exit $E_OFFLINE
    fi

    local targets=()

    if [[ "$1" == "all" ]]; then
        # Use manifest for target collection — single code path for all strategies.
        # Use tool names (not package paths) for unified strategy collection.
        while IFS='|' read -r name _ _ _; do
            [[ -n "$name" ]] && targets+=("$name")
        done <<< "$_MANIFEST_TOOLS"
    else
        targets=("$@")
    fi

    if [[ ${#targets[@]} -eq 0 ]]; then
        echo "⚠️  No tools found to upgrade."
        return 0
    fi

    for t in "${targets[@]}"; do
        local entry pkg ver
        entry=$(_manifest_tool_entry "$t" 2>/dev/null || true)
        if [[ -z "$entry" ]]; then
            echo "  ⚠️  Tool $t not found in manifest, skipping."
            continue
        fi
        pkg=$(echo "$entry" | cut -d'|' -f2)
        local old_ver
        old_ver=$(echo "$entry" | cut -d'|' -f3)

        if $_DRY_RUN; then
            echo "  🔍 [dry-run] Would upgrade $t ($pkg) to @latest"
            continue
        fi
        echo "🚀 Upgrading $t ($pkg) from ${old_ver:-?}..."
        case "$GOTOOLS_STRATEGY" in
            unified)
                (cd "$GOTOOLS_DIR" && _go_timeout get -tool "${pkg}@latest") || _timeout_fatal $? "Upgrading $t"
                ;;
            split)
                (cd "$GOTOOLS_DIR" && _go_timeout get -tool -modfile="$t.mod" "${pkg}@latest") || _timeout_fatal $? "Upgrading $t"
                ;;
            module)
                (cd "$GOTOOLS_DIR/$t" && _go_timeout get -tool "${pkg}@latest") || _timeout_fatal $? "Upgrading $t"
                ;;
        esac

        # Update version in manifest after upgrade.
        local new_ver=""
        case "$GOTOOLS_STRATEGY" in
            unified) new_ver=$(_resolve_installed_version "$GOTOOLS_DIR/go.mod" "$pkg") ;;
            split)   new_ver=$(_resolve_installed_version "$GOTOOLS_DIR/$t.mod" "$pkg") ;;
            module)  new_ver=$(_resolve_installed_version "$GOTOOLS_DIR/$t/go.mod" "$pkg") ;;
        esac
        if [[ -n "$new_ver" ]]; then
            _manifest_tool_set "$t" "go" "$pkg" "$new_ver"
            if [[ "$new_ver" != "$old_ver" ]]; then
                echo "  ✅ $t: ${old_ver:-?} → $new_ver"
            else
                echo "  ✅ $t: already at latest ($new_ver)"
            fi
        else
            echo "  ⚠️  $t: could not resolve new version"
        fi
    done

    if $_DRY_RUN; then
        echo "🔍 [dry-run] Would upgrade ${#targets[@]} tool(s)."
    else
        _manifest_flush
        echo "✅ Upgrade complete."
    fi
}
