# Copyright (c) 2026 Pius Alfred
# License: MIT


# ---- sync ----------------------------------------------------------------



cmd_sync() {
    _require_go
    _DRY_RUN=false
    for a in "$@"; do [[ "$a" == "--dry-run" ]] && _DRY_RUN=true; done

    load_config
    _acquire_lock
    local target_v
    target_v=$(resolve_go_version)

    local disk_strategy
    disk_strategy=$(detect_strategy "$GOTOOLS_DIR")

    if [[ -n "$disk_strategy" && "$disk_strategy" != "$GOTOOLS_STRATEGY" ]]; then
        if $_DRY_RUN; then
            echo "🔍 [dry-run] Would auto-migrate from '$disk_strategy' to '$GOTOOLS_STRATEGY'"
            return
        fi
        echo "⚠️  Strategy mismatch: $MANIFEST_FILE says '$GOTOOLS_STRATEGY' but $GOTOOLS_DIR/ looks like '$disk_strategy'."
        echo "🔀 Auto-migrating to '$GOTOOLS_STRATEGY'..."
        cmd_migrate "$GOTOOLS_STRATEGY"
        return
    fi

    if $_DRY_RUN; then
        echo "🔍 [dry-run] Would sync (strategy=$GOTOOLS_STRATEGY, go=$target_v, dir=$GOTOOLS_DIR)"
    else
        echo "🔄 Syncing (strategy=$GOTOOLS_STRATEGY, go=$target_v, dir=$GOTOOLS_DIR)..."
    fi

    # ---- Existing Go sync logic (tidy existing modfiles) ----
    case "$GOTOOLS_STRATEGY" in
        unified)
            if $_DRY_RUN; then
                echo "  [dry-run] Would go mod tidy in $GOTOOLS_DIR/"
            else
                mkdir -p "$GOTOOLS_DIR"
                if [[ ! -f "$GOTOOLS_DIR/go.mod" ]]; then
                    (cd "$GOTOOLS_DIR" && go mod init "$(tool_module_path)")
                fi
                (cd "$GOTOOLS_DIR" && go mod edit -go="$target_v" && go mod tidy)
            fi
            ;;

        split)
            if $_DRY_RUN; then
                for f in "$GOTOOLS_DIR"/*.mod; do
                    [[ -f "$f" ]] || continue
                    echo "  [dry-run] Would tidy $(basename "$f")"
                done
            else
                mkdir -p "$GOTOOLS_DIR"
                local parent_mod
                parent_mod=$(resolve_module_prefix)
                for f in "$GOTOOLS_DIR"/*.mod; do
                    [[ -f "$f" ]] || continue
                    local base sumfile
                    base=$(basename "$f")
                    sumfile="${f%.mod}.sum"
                    echo "  ↻ $base"
                    # Isolate: copy to a temp dir so Go doesn't discover the
                    # parent module (go mod tidy -modfile cannot reconcile two
                    # module identities when run from inside another module's tree).
                    # Also add a replace directive so Go uses the local parent
                    # module (which has all packages) instead of the stale
                    # published version which may be missing packages.
                    local tmpdir
                    tmpdir=$(mktemp -d)
                    cp "$f" "$tmpdir/go.mod"
                    [[ -f "$sumfile" ]] && cp "$sumfile" "$tmpdir/go.sum"
                    if [[ -n "$parent_mod" ]]; then
                        (cd "$tmpdir" && go mod edit -replace="${parent_mod}=${PWD}" && go mod edit -go="$target_v" && go mod tidy && go mod edit -dropreplace="$parent_mod")
                    else
                        (cd "$tmpdir" && go mod edit -go="$target_v" && go mod tidy)
                    fi
                    cp "$tmpdir/go.mod" "$f"
                    cp "$tmpdir/go.sum" "$sumfile"
                    rm -rf "$tmpdir"
                done
            fi
            ;;

        module)
            if $_DRY_RUN; then
                for d in "$GOTOOLS_DIR"/*/; do
                    [[ -d "$d" ]] || continue
                    [[ -f "$d/go.mod" ]] || continue
                    echo "  [dry-run] Would tidy $(basename "$d")"
                done
            else
                mkdir -p "$GOTOOLS_DIR"
                for d in "$GOTOOLS_DIR"/*/; do
                    [[ -d "$d" ]] || continue
                    local modfile="$d/go.mod"
                    [[ -f "$modfile" ]] || continue
                    local name
                    name=$(basename "$d")
                    echo "  ↻ $name"
                    (cd "$d" && go mod edit -go="$target_v" && go mod tidy)
                done
            fi
            ;;

        *)
            echo "❌ Unknown strategy: $GOTOOLS_STRATEGY" >&2
            exit $E_ENVIRONMENT
            ;;
    esac

    # ---- Discover tools on disk not yet in the manifest ----
    local discovered=0
    local tool_line
    while IFS= read -r tool_line; do
        [[ -z "$tool_line" ]] && continue
        local disk_name disk_pkg
        disk_name=$(echo "$tool_line" | awk '{print $1}')
        disk_pkg=$(echo "$tool_line" | awk '{print $2}')
        if ! _manifest_tool_exists "$disk_name"; then
            local disk_ver
            disk_ver=$(echo "$disk_pkg" | sed 's/.*@//')
            local disk_pkg_base="${disk_pkg%%@*}"
            _manifest_tool_set "$disk_name" "go" "$disk_pkg_base" "$disk_ver"
            echo "  ✚ $disk_name ($disk_pkg) — discovered from disk"
            discovered=$((discovered + 1))
        fi
    done <<< "$(extract_tools_with_versions)"
    if [[ $discovered -gt 0 ]]; then
        _manifest_flush
        echo "✅ Added $discovered tool(s) to manifest."
    fi

    # ---- Reinstall tools from manifest that are missing on disk ----
    local missing_count=0
    local _n _s _p _v
    while IFS='|' read -r _n _s _p _v; do
        [[ -z "$_n" ]] && continue
        local on_disk=false
        case "$GOTOOLS_STRATEGY" in
            unified) [[ -f "$GOTOOLS_DIR/go.mod" ]] && on_disk=true ;;
            split)   [[ -f "$GOTOOLS_DIR/${_n}.mod" ]] && on_disk=true ;;
            module)  [[ -f "$GOTOOLS_DIR/${_n}/go.mod" ]] && on_disk=true ;;
        esac
        if ! $on_disk; then
            if $_DRY_RUN; then
                echo "  🔍 [dry-run] Would reinstall $_n ($_p@$_v)"
            else
                echo "  ⬇ $_n ($_p@$_v) — missing, reinstalling..."
                # --force: the tool is already in the manifest — restoring a
                # missing modfile must not trip cmd_install's duplicate check.
                cmd_install --force "$_n" "${_p}@${_v}"
            fi
            missing_count=$((missing_count + 1))
        fi
    done <<< "$_MANIFEST_TOOLS"

    if $_DRY_RUN; then
        echo "🔍 [dry-run] Would restore $missing_count tool(s) from manifest."
    elif [[ $missing_count -gt 0 ]]; then
        echo "✅ Sync restored $missing_count tool(s) from manifest."
    else
        echo "✅ Sync complete."
    fi
}
