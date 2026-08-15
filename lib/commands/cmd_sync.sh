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
    # Sort the tool list before hashing: the in-memory order depends on how
    # the manifest was built (parsed vs appended), but the fingerprint must
    # not. Same ordering as _manifest_flush.
    fp=$(printf '%s' "$_MANIFEST_TOOLS" | LC_ALL=C sort -t'|' -k2,2 -k3,3 -k4,4 -k1,1 | _sha256 /dev/stdin)
    fp="${fp}|${GOTOOLS_STRATEGY}|${go_ver}|$(resolve_module_prefix)"
    printf '%s' "$fp"
}

# _sync_write_fingerprint <go-version> — record the reconciled state so the
# next sync can take the fast path. Written only after a successful sync.
_sync_write_fingerprint() {
    printf '%s\n' "$(_sync_fingerprint "$1")" > "$GOTOOLS_DIR/.gotools.fingerprint"
}

# _sync_install_one <name> <pkg@version> — one backgroundable reinstall unit.
# _INSTALL_NO_FLUSH=1 makes cmd_install skip the manifest write: parallel
# jobs each run in their own subshell, so letting them flush would race and
# drop tools (last writer wins). The parent merges and flushes once.
_sync_install_one() {
    local name="$1" pkg_at_version="$2"
    _INSTALL_NO_FLUSH=1 cmd_install --force "$name" "$pkg_at_version"
}

# _sync_run_reinstalls_serial <list> — sequential reinstalls (unified
# strategy and --jobs 1).
_sync_run_reinstalls_serial() {
    local _n pkg_at_version
    while IFS='|' read -r _n pkg_at_version; do
        [[ -z "$_n" ]] && continue
        # --force: the tool is already in the manifest — restoring a missing
        # or drifted modfile must not trip cmd_install's duplicate check.
        cmd_install --force "$_n" "$pkg_at_version"
    done <<< "$1"
}

# _sync_run_reinstalls_parallel <jobs> <list> — concurrent reinstalls for
# split/module strategies (independent modfiles). After all jobs complete,
# the parent merges every reinstalled tool into the manifest and flushes
# once, so concurrent jobs never race on the manifest.
_sync_run_reinstalls_parallel() {
    local jobs="$1" list="$2"
    # Separate declaration/assignment for bash 3.2 compatibility.
    local -a spec
    spec=()
    local _n pkg_at_version
    while IFS='|' read -r _n pkg_at_version; do
        [[ -z "$_n" ]] && continue
        spec+=("$_n" "$pkg_at_version" "::")
    done <<< "$list"
    _parallel_for "$jobs" _sync_install_one ${spec[@]+"${spec[@]}"}

    local _name _pkg_ver pkg resolved
    while IFS='|' read -r _name _pkg_ver; do
        [[ -z "$_name" ]] && continue
        pkg="${_pkg_ver%%@*}"
        resolved=$(_tool_disk_version "$_name" "$pkg")
        _manifest_tool_set "$_name" "go" "$pkg" "${resolved:-latest}"
    done <<< "$list"
    _manifest_flush
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

# _sync_fast_path <go-version> [message] — skip the full reconciliation when
# nothing changed. The fingerprint must match AND (defense in depth) every
# modfile must exist with the manifest version — catches manual edits that
# bypassed the manifest. Returns 0 and prints the success message on a hit.
_sync_fast_path() {
    local go_ver="$1"
    local success_message="${2:-✅ Tools up to date.}"
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
            echo "$success_message"
            return 0
        fi
    fi
    return 1
}

cmd_sync() {
    _require_go
    _DRY_RUN=false
    _parse_offline "$@"
    # Parallelism is opt-in: sync stays serial unless --jobs N (or
    # GOTOOLS_JOBS / the manifest "jobs" setting) requests more than one
    # concurrent install. Precedence: --jobs flag > GOTOOLS_JOBS env >
    # manifest "jobs" setting > default 1.
    local jobs=""
    local jobs_explicit=false
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --dry-run) _DRY_RUN=true ;;
            --jobs=*) jobs="${1#*=}"; jobs_explicit=true ;;
            --jobs) jobs="${2:-4}"; jobs_explicit=true; shift ;;
        esac
        shift
    done

    load_config

    if ! $jobs_explicit; then
        jobs="${_JOBS:-1}"
    fi
    if ! [[ "$jobs" =~ ^[0-9]+$ ]]; then
        echo "❌ Invalid --jobs value: must be a positive number" >&2
        exit $E_USAGE
    fi
    local target_v
    target_v=$(resolve_go_version)

    # Offline mode: hermetic CI runs take ONLY the fingerprint fast path.
    # Anything that would need the network refuses with exit 6 instead of
    # making the build depend on proxy conditions. Also covers a strategy
    # mismatch (migrating needs the network).
    # The lock is NOT acquired here: offline sync can never write (fast
    # path returns, anything else exits 6), so it never contends with a
    # writer — skipping the lock lets concurrent CI jobs share a workspace.
    if ${_OFFLINE:-false}; then
        if _sync_fast_path "$target_v" "✅ Tools up to date (fingerprint match)."; then
            return 0
        fi
        echo "❌ Offline mode: manifest has changed. Network required." >&2
        echo "   Run 'gotools sync' locally and commit the updated files." >&2
        exit $E_OFFLINE
    fi

    _acquire_lock

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
                _reject_symlink "$GOTOOLS_DIR/go.mod" "sync (unified)"
                if [[ ! -f "$GOTOOLS_DIR/go.mod" ]]; then
                    (cd "$GOTOOLS_DIR" && go mod init "$(tool_module_path)")
                fi
                (cd "$GOTOOLS_DIR" && go mod edit -go="$target_v" && _go_timeout mod tidy || _timeout_fatal $? "go mod tidy in $GOTOOLS_DIR")
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
                    _reject_symlink "$f" "sync (split: $base)"
                    _reject_symlink "$sumfile" "sync (split: $base)"
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
                        (cd "$tmpdir" && go mod edit -replace="${parent_mod}=${PWD}" && go mod edit -go="$target_v" && _go_timeout mod tidy || _timeout_fatal $? "go mod tidy for $base" && go mod edit -dropreplace="$parent_mod")
                    else
                        (cd "$tmpdir" && go mod edit -go="$target_v" && _go_timeout mod tidy || _timeout_fatal $? "go mod tidy for $base")
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
                    _reject_symlink "${d%/}" "sync (module)"
                    _reject_symlink "$modfile" "sync (module)"
                    local name
                    name=$(basename "$d")
                    echo "  ↻ $name"
                    (cd "$d" && go mod edit -go="$target_v" && _go_timeout mod tidy || _timeout_fatal $? "go mod tidy for $name")
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
    # First collect the work (one "name|pkg@version" line per reinstall),
    # then run it serial or parallel depending on strategy and --jobs.
    local reinstall_list=""
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
            reinstall_list+="${_n}|${_p}@${_v}"$'\n'
        fi
    done <<< "$_MANIFEST_TOOLS"

    local missing_count
    missing_count=$(printf '%s' "$reinstall_list" | grep -c . || true)

    if ! $_DRY_RUN && [[ -n "$reinstall_list" ]]; then
        if [[ "$GOTOOLS_STRATEGY" == "unified" ]]; then
            if $jobs_explicit && [[ "$jobs" -gt 1 ]]; then
                echo "  ⚠ unified strategy: installs are serial (shared go.mod)."
            fi
            _sync_run_reinstalls_serial "$reinstall_list"
        elif [[ "$jobs" -le 1 ]]; then
            _sync_run_reinstalls_serial "$reinstall_list"
        else
            _sync_run_reinstalls_parallel "$jobs" "$reinstall_list"
        fi
    fi

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
