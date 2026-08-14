# Copyright (c) 2026 Pius Alfred
# License: MIT

# ---- init ----------------------------------------------------------------

# _gomod_migrate <root-modfile>
#   Adopt the tools already declared in the project's root go.mod:
#   scan -> install (additive) -> strip -> tidy. The root go.mod is not
#   modified until every tool has been installed successfully, so a failed
#   migration leaves the project exactly as it was and a re-run resumes.
_gomod_migrate() {
    local root_mod="$1"
    local scan
    scan=$(_gomod_tool_scan "$root_mod")

    if [[ -z "$scan" ]]; then
        echo "ℹ No tool directives in go.mod — nothing to migrate."
        return 0
    fi

    local count
    count=$(wc -l <<< "$scan" | tr -d ' ')
    echo "🔍 Found $count tool(s) in go.mod:"
    while IFS=' ' read -r name pkg_at_version; do
        echo "     $name  $pkg_at_version"
    done <<< "$scan"

    # Phase 2 — install (additive; root go.mod untouched until this succeeds)
    while IFS=' ' read -r name pkg_at_version; do
        [[ -z "$name" ]] && continue
        if _manifest_tool_exists "$name"; then
            echo "  ⏭ $name — already managed, skipping"
            continue
        fi
        if $_DRY_RUN; then
            echo "  [dry-run] Would install $name ($pkg_at_version)"
            continue
        fi
        cmd_install "$name" "$pkg_at_version"
    done <<< "$scan"

    # Phases 3–4 — destructive to root go.mod, only after installs succeeded
    if $_DRY_RUN; then
        echo "[dry-run] Would strip tool directives from go.mod and run 'go mod tidy'."
        return 0
    fi

    echo "✂️  Removing tool directives from go.mod..."
    local tmp="$root_mod.tmp.$$"
    strip_tools_from_mod "$root_mod" > "$tmp"
    mv "$tmp" "$root_mod"

    echo "🧹 Running 'go mod tidy' on the root module..."
    _go mod tidy

    echo "✅ Migrated $count tool(s) from go.mod into gotools."
}

cmd_init() {
    local strategy=$DEFAULT_STRATEGY dir=$DEFAULT_DIR go_v=$DEFAULT_GO_VERSION prefix=""
    local no_migrate=false
    for arg in "$@"; do
        case $arg in
            --strategy=*) strategy="${arg#*=}" ;;
            --dir=*)      dir="${arg#*=}" ;;
            --go=*)       go_v="${arg#*=}" ;;
            --prefix=*)   prefix="${arg#*=}" ;;
            --no-migrate) no_migrate=true ;;
            --dry-run)    _DRY_RUN=true ;;
            *)            echo "❓ Unknown flag: $arg"; usage ;;
        esac
    done

    # Validate strategy.
    case "$strategy" in
        unified|split|module) ;;
        *) echo "❌ Invalid strategy: $strategy (must be unified, split, or module)" >&2; exit $E_USAGE ;;
    esac

    # Load the existing manifest FIRST so re-running init merges the tool
    # list instead of wiping it.
    load_config

    # Set config vars (flags/defaults win), then flush.
    GOTOOLS_STRATEGY="$strategy"
    GOTOOLS_DIR="$dir"
    GOTOOLS_GO_VERSION="$go_v"
    GOTOOLS_MODULE_PREFIX="$prefix"
    if $_DRY_RUN; then
        echo "🔍 [dry-run] Would write $MANIFEST_FILE (strategy=$strategy, dir=$dir)"
    else
        _manifest_flush
        mkdir -p "$dir"
        echo "✅ Initialized $MANIFEST_FILE (strategy=$strategy, dir=$dir)"
    fi

    # Adopt tools already declared in the root go.mod (unless --no-migrate).
    if ! $no_migrate; then
        local root_mod="$_PROJECT_ROOT/go.mod"
        if [[ -f "$root_mod" ]]; then
            _gomod_migrate "$root_mod"
        else
            echo "ℹ No go.mod in project root — skipping tool migration."
        fi
    fi

    # Reconcile; pass --dry-run through so a dry-run init stays read-only.
    if $_DRY_RUN; then
        cmd_sync --dry-run
    else
        cmd_sync
    fi
}
