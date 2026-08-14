# Copyright (c) 2026 Pius Alfred
# License: MIT

# ---- sync ----------------------------------------------------------------

# _sync_fingerprint <go-version> — a deterministic fingerprint of the state
# sync reconciles: the full tool list (names, packages, AND versions), the
# strategy, the resolved Go version, and the module prefix. Any manifest or
# environment change produces a different fingerprint.
_sync_fingerprint() {
    local go_ver="$1"
    local fp
    fp=$(printf '%s' "$_MANIFEST_TOOLS" | _sha256 /dev/stdin)
    fp="${fp}|${GOTOOLS_STRATEGY}|${go_ver}|$(resolve_module_prefix)"
    printf '%s' "$fp"
}

# _sync_write_fingerprint <go-version> — record the reconciled state so the
# next sync can take the fast path. Written only after a successful sync.
_sync_write_fingerprint() {
    printf '%s\n' "$(_sync_fingerprint "$1")" > "$GOTOOLS_DIR/.gotools.fingerprint"
}

# _tool_disk_version <name> <pkg> — the version currently on disk for a tool,
# per strategy. Callers must ensure the modfile exists first.
_tool_disk_version() {
    local name="$1" pkg="$2"
    case "$GOTOOLS_STRATEGY" in
        unified) extract_version_for_pkg "$GOTOOLS_DIR/go.mod" "$pkg" ;;
        split)   extract_version_for_pkg "$GOTOOLS_DIR/${name}.mod" "$pkg" ;;
        module)  extract_version_for_pkg "$GOTOOLS_DIR/${name}/go.mod" "$pkg" ;;
    esac
}

_tool_version_matches() {
    local name="$1" pkg="$2" expected="$3"
    local disk
    disk=$(_tool_disk_version "$name" "$pkg")
    [[ -n "$disk" && "$disk" == "$expected" ]]
}

# _sync_fast_path <go-version> — skip the full reconciliation when nothing
# changed. The fingerprint must match AND (defense in depth) every modfile
# must exist with the manifest version — catches manual edits that bypassed
# the manifest. Returns 0 and prints "Tools up to date." on a hit.
_sync_fast_path() {
    local go_ver="$1"
    local fingerprint_file="$GOTOOLS_DIR/.gotools.fingerprint"
    local current_fp
    current_fp=$(_sync_fingerprint "$go_ver")
    if [[ -f "$fingerprint_file" ]] && [[ "$(cat "$fingerprint_file")" == "$current_fp" ]]; then
        local all_ok=true
        local _n _s _p _v
        while IFS='|' read -r _n _s _p _v; do
            [[ -z "$_n" ]] && continue
            case "$GOTOOLS_STRATEGY" in
                unified) [[ -f "$GOTOOLS_DIR/go.mod" ]] || { all_ok=false; break; } ;;
                split)   [[ -f "$GOTOOLS_DIR/${_n}.mod" ]] || { all_ok=false; break; } ;;
                module)  [[ -f "$GOTOOLS_DIR/${_n}/go.mod" ]] || { all_ok=false; break; } ;;
            esac
            _tool_version_matches "$_n" "$_p" "$_v" || { all_ok=false; break; }
        done <<< "$_MANIFEST_TOOLS"
        if $all_ok; then
            echo "✅ Tools up to date."
            return 0
        fi
    fi
    return 1
}

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

    # Fast path: when the fingerprint matches and every modfile still carries
    # the manifest version, there is nothing to reconcile.
    if ! $_DRY_RUN && _sync_fast_path "$target_v"; then
        return 0
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

    # ---- Reinstall tools from the manifest that are missing or drifted ----
    local missing_count=0
    local _n _s _p _v
    while IFS='|' read -r _n _s _p _v; do
        [[ -z "$_n" ]] && continue
        local on_disk=false disk_ver=""
        case "$GOTOOLS_STRATEGY" in
            unified) [[ -f "$GOTOOLS_DIR/go.mod" ]] && on_disk=true ;;
            split)   [[ -f "$GOTOOLS_DIR/${_n}.mod" ]] && on_disk=true ;;
            module)  [[ -f "$GOTOOLS_DIR/${_n}/go.mod" ]] && on_disk=true ;;
        esac
        if $on_disk; then
            disk_ver=$(_tool_disk_version "$_n" "$_p")
        fi
        if ! $on_disk || [[ "$disk_ver" != "$_v" ]]; then
            if $_DRY_RUN; then
                echo "  🔍 [dry-run] Would reinstall $_n ($_p@$_v)"
            elif ! $on_disk; then
                echo "  ⬇ $_n ($_p@$_v) — missing, reinstalling..."
            else
                echo "  ⚠ $_n: manifest $_v, disk ${disk_ver:-unknown} — reinstalling..."
            fi
            if ! $_DRY_RUN; then
                # --force: the tool is already in the manifest — restoring a
                # missing or drifted modfile must not trip cmd_install's
                # duplicate check.
                cmd_install --force "$_n" "${_p}@${_v}"
            fi
            missing_count=$((missing_count + 1))
        fi
    done <<< "$_MANIFEST_TOOLS"

    if $_DRY_RUN; then
        echo "🔍 [dry-run] Would restore $missing_count tool(s) from manifest."
    else
        # Record the state we just reconciled so the next sync can skip
        # straight to the fast path.
        _sync_write_fingerprint "$target_v"
        if [[ $missing_count -gt 0 ]]; then
            echo "✅ Sync restored $missing_count tool(s) from manifest."
        else
            echo "✅ Sync complete."
        fi
    fi
}
