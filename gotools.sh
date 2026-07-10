#!/usr/bin/env bash
# Copyright (c) 2026 Pius Alfred
# License: MIT

set -euo pipefail

VERSION="v0.5.4"
REPO="piusalfred/gotools"
API_URL="https://api.github.com/repos/$REPO/releases/latest"

MANIFEST_FILE=".gotools.json"
DEFAULT_STRATEGY="split"
DEFAULT_DIR="tools"
DEFAULT_GO_VERSION="inherit"
DEFAULT_MODULE_PREFIX=""

# In-memory tool store. Each line is: name|source|package|version
# Populated by _manifest_parse() and mutated by _manifest_tool_set/remove.
# Flushed to .gotools.json by _manifest_flush().
_MANIFEST_TOOLS=""

# Dry-run mode — set by _parse_dry_run(). When true, destructive commands
# print what they would do but make no changes.
_DRY_RUN=false

# Verbose mode — set via GOTOOLS_VERBOSE=1 env var or --verbose flag.
# When true, print every go command before executing it.
_VERBOSE="${GOTOOLS_VERBOSE:-0}"

# _PROJECT_ROOT — set by _find_project_root(). The directory containing
# .gotools.json, found by walking up from $PWD (like git finds .git).
# If not found, defaults to $PWD. Commands operate relative to this root.
_PROJECT_ROOT=""

# _find_project_root — walk up the directory tree looking for .gotools.json.
# If found, cd to that directory and set _PROJECT_ROOT. Otherwise, stay in $PWD.
_find_project_root() {
    local dir="$PWD"
    while [[ "$dir" != "/" ]]; do
        if [[ -f "$dir/$MANIFEST_FILE" ]]; then
            _PROJECT_ROOT="$dir"
            cd "$dir" 2>/dev/null || true
            return 0
        fi
        dir="$(dirname "$dir")"
    done
    # Check root directory too
    if [[ -f "/$MANIFEST_FILE" ]]; then
        _PROJECT_ROOT="/"
        cd "/" 2>/dev/null || true
        return 0
    fi
    _PROJECT_ROOT="$PWD"
    return 0
}

# _require_go — verify Go is installed and >= 1.24 (minimum for tool directives).
_require_go() {
    if ! command -v go &>/dev/null; then
        echo "❌ Go is not installed. Please install Go 1.24 or higher." >&2
        echo "   https://go.dev/dl/" >&2
        exit 1
    fi
    local go_ver
    go_ver=$(go env GOVERSION 2>/dev/null | sed 's/^go//')
    if [[ -z "$go_ver" ]]; then
        echo "❌ Could not determine Go version." >&2
        exit 1
    fi
    local major minor
    major=$(echo "$go_ver" | cut -d. -f1)
    minor=$(echo "$go_ver" | cut -d. -f2)
    if [[ "$major" -lt 1 ]] || { [[ "$major" -eq 1 ]] && [[ "${minor:-0}" -lt 24 ]]; }; then
        echo "❌ Go $go_ver is too old. Go 1.24 or higher is required." >&2
        exit 1
    fi
}

# ---- Lockfile (concurrent operation safety) -------------------------
_LOCK_FILE=""
_LOCK_HELD=false

# _acquire_lock — try to acquire the gotools lockfile. Reentrant: if this
# process already holds the lock, returns immediately.
_acquire_lock() {
    if $_LOCK_HELD; then return; fi
    local lock_dir="${GOTOOLS_DIR:-tools}"
    mkdir -p "$lock_dir"
    _LOCK_FILE="$lock_dir/.gotools.lock"
    local timeout=10 waited=0
    while ! mkdir "$_LOCK_FILE" 2>/dev/null; do
        if [[ $waited -ge $timeout ]]; then
            echo "❌ Another gotools process is running. Try again later." >&2
            exit 1
        fi
        sleep 0.5
        waited=$((waited + 1))
    done
    _LOCK_HELD=true
    trap '_release_lock' EXIT
}

# _release_lock — remove the lockfile.
_release_lock() {
    if $_LOCK_HELD; then
        [[ -n "${_LOCK_FILE:-}" && -d "${_LOCK_FILE:-}" ]] && rmdir "$_LOCK_FILE" 2>/dev/null || true
        _LOCK_HELD=false
    fi
}

# _go — run a go command, printing it first if verbose mode is on.
# Usage: _go [args...]  (calls `go` with the same args)
_go() {
    if [[ "$_VERBOSE" == "1" || "$_VERBOSE" == "true" ]]; then
        echo "  ↳ go $*" >&2
    fi
    go "$@"
}

# _parse_dry_run — check remaining args for --dry-run, remove it, set _DRY_RUN.
# Call this at the top of destructive commands after load_config.
_parse_dry_run() {
    local filtered=()
    for arg in "$@"; do
        if [[ "$arg" == "--dry-run" ]]; then
            _DRY_RUN=true
        else
            filtered+=("$arg")
        fi
    done
    # Return filtered args via a global (bash can't return arrays)
    _PARSED_ARGS=("${filtered[@]}")
}

# _cmd_help <command> — print per-command help.
_cmd_help() {
    local cmd="$1"
    case "$cmd" in
        init)
            echo "Usage: gotools.sh init [flags]"
            echo ""
            echo "  Bootstrap the project with a .gotools.json manifest."
            echo ""
            echo "  Flags:"
            echo "    --strategy=unified|split|module   Isolation strategy (default: split)"
            echo "    --dir=<path>                      Tools directory (default: tools)"
            echo "    --go=<version|inherit>            Go version for tools (default: inherit)"
            echo "    --prefix=<module-path>            Module prefix (default: auto from go.mod)"
            echo ""
            echo "  Examples:"
            echo "    gotools.sh init"
            echo "    gotools.sh init --strategy=module --dir=.tools"
            echo "    gotools.sh init --go=1.24 --prefix=github.com/me/repo"
            ;;
        install)
            echo "Usage: gotools.sh install [name] <package@version> [--force]"
            echo ""
            echo "  Install a Go tool via 'go get -tool'. If only a package is given,"
            echo "  the tool name is inferred from the package path."
            echo ""
            echo "  Tool aliases: You can give the tool a custom name (alias). The"
            echo "  alias is used in gotools.sh (list, info, remove, exec, upgrade)"
            echo "  but the underlying Go binary name stays the same."
            echo ""
            echo "  Examples:"
            echo "    gotools.sh install golang.org/x/tools/cmd/goimports@latest"
            echo "    gotools.sh install mylint golang.org/x/tools/cmd/goimports@v0.38.0"
            echo "    gotools.sh exec mylint -w .   # runs goimports binary"
            echo ""
            echo "  Options:"
            echo "    --force   Overwrite an existing tool with the same name"
            ;;
        sync)
            echo "Usage: gotools.sh sync [--dry-run]"
            echo ""
            echo "  Sync tool state to match .gotools.json. Tidies existing modfiles"
            echo "  and reinstalls any tools from the manifest that are missing on disk."
            echo ""
            echo "  If the on-disk strategy doesn't match the manifest, auto-migrates."
            echo ""
            echo "  Options:"
            echo "    --dry-run   Show what would be done without making changes"
            ;;
        exec)
            echo "Usage: gotools.sh exec <tool-name> [args...]"
            echo ""
            echo "  Run a managed tool with the given arguments. The tool name must"
            echo "  match a name in 'gotools.sh list'. If you installed a tool with an"
            echo "  alias, use that alias here."
            echo ""
            echo "  Examples:"
            echo "    gotools.sh exec gofumpt -w -extra ."
            echo "    gotools.sh exec golangci-lint run ./..."
            ;;
        list)
            echo "Usage: gotools.sh list [--json]"
            echo ""
            echo "  List all managed tools with their versions and strategy info."
            echo ""
            echo "  Options:"
            echo "    --json   Output as JSON array for scripting/CI"
            ;;
        info)
            echo "Usage: gotools.sh info <tool-name> [--json]"
            echo ""
            echo "  Show detailed information about a specific tool."
            echo ""
            echo "  Options:"
            echo "    --json   Output as JSON object for scripting/CI"
            ;;
        upgrade)
            echo "Usage: gotools.sh upgrade <name|all> [--dry-run]"
            echo ""
            echo "  Upgrade tools to their latest versions."
            echo ""
            echo "  Options:"
            echo "    --dry-run   Show what would be upgraded without making changes"
            ;;
        remove)
            echo "Usage: gotools.sh remove <name1> [name2...] [--dry-run]"
            echo ""
            echo "  Remove specific tools from the project."
            echo ""
            echo "  Options:"
            echo "    --dry-run   Show what would be removed without making changes"
            ;;
        migrate)
            echo "Usage: gotools.sh migrate <unified|split|module> [--dry-run]"
            echo ""
            echo "  Migrate all tools to a different isolation strategy."
            echo "  Preserves exact pinned versions across the migration."
            echo ""
            echo "  Options:"
            echo "    --dry-run   Show what would be migrated without making changes"
            ;;
        config)
            echo "Usage: gotools.sh config [key [value]]"
            echo ""
            echo "  View or edit .gotools.json configuration."
            echo ""
            echo "  No args:    Show the full manifest (cat .gotools.json)"
            echo "  One arg:    Show the value of <key>"
            echo "  Two args:   Set <key>=<value>"
            echo ""
            echo "  Valid keys: GOTOOLS_STRATEGY, GOTOOLS_DIR,"
            echo "              GOTOOLS_GO_VERSION, GOTOOLS_MODULE_PREFIX"
            ;;
        purge)
            echo "Usage: gotools.sh purge [--dry-run]"
            echo ""
            echo "  Remove all tools and the .gotools.json manifest."
            echo "  Interactive: requires typing YES to confirm."
            echo ""
            echo "  Options:"
            echo "    --dry-run   Show what would be removed without deleting"
            ;;
        check)
            echo "Usage: gotools.sh check"
            echo ""
            echo "  Verify all managed tools are installed and runnable."
            echo "  Runs each tool with --help or --version and reports pass/fail."
            ;;
        version)
            echo "Usage: gotools.sh version"
            echo ""
            echo "  Print the gotools.sh version."
            ;;
        self-update)
            echo "Usage: gotools.sh self-update"
            echo ""
            echo "  Update gotools.sh to the latest release from GitHub."
            ;;
        uninstall)
            echo "Usage: gotools.sh uninstall"
            echo ""
            echo "  Remove gotools.sh itself from your system."
            ;;
        *)
            usage
            ;;
    esac
}

# Capture the original environment once at startup, before any load_config
# call sets these variables in the current shell. This is the only reliable
# way to distinguish "user passed GOTOOLS_DIR=x ./gotools ..." from
# "load_config already ran and set GOTOOLS_DIR earlier in this process".
_ORIG_ENV_STRATEGY="${GOTOOLS_STRATEGY:-}"
_ORIG_ENV_DIR="${GOTOOLS_DIR:-}"
_ORIG_ENV_GO_VERSION="${GOTOOLS_GO_VERSION:-}"
_ORIG_ENV_MODULE_PREFIX="${GOTOOLS_MODULE_PREFIX:-}"

usage() {
    cat <<EOF
🧰 Go Tool Manager (Version: $VERSION)

Usage: $(basename "$0") <command> [arguments]

Commands:
  init [flags]            Bootstrap the project.
                            --strategy=unified|split|module  (default: $DEFAULT_STRATEGY)
                            --dir=<tools-dir>                     (default: $DEFAULT_DIR)
                            --go=<version|inherit>                (default: $DEFAULT_GO_VERSION)
                            --prefix=<module-prefix|auto>         (default: auto from root go.mod)
  install [name] <pkg>    Install a new tool.
                            If only <pkg> is given, name is inferred from its basename.
  sync [--dry-run]        Sync tool state to match $MANIFEST_FILE.
  exec <name> [args]      Run a managed tool.
  list [--json]           List tools, versions, and strategies.
  upgrade <name|all> [--dry-run]  Upgrade tools to @latest.
  remove <name...> [--dry-run]    Remove specific tools.
  migrate <strategy> [--dry-run]  Migrate to a different strategy.
  config [key [value]]    View or edit $MANIFEST_FILE configuration.
                            No args: show all config.
                            One arg: show value of <key>.
                            Two args: set <key>=<value>.
  purge [--dry-run]       Remove all tools and the $MANIFEST_FILE file.
  info <name> [--json]    Show detailed information about a specific tool.
  check                   Verify all managed tools are runnable.
  version                 Show script version.
  self-update             Update gotools.sh to the latest version.
  uninstall               Remove this script from your system.
  test <seconds>          Sleep for <seconds> (useful for testing Ctrl-C / signal handling).

Strategies:
  unified     One shared tools/go.mod with all tool directives.
  split       Flat files: tools/<name>.mod and tools/<name>.sum per tool.
  module      Dedicated subdirectories: tools/<name>/go.mod per tool.

Examples:
  gotools.sh init --strategy=module --dir=tools
  gotools.sh install staticcheck honnef.co/go/tools/cmd/staticcheck@latest
  gotools.sh install golang.org/x/tools/cmd/goimports@latest
  gotools.sh exec goimports -w .
  gotools.sh migrate unified
  gotools.sh upgrade all
  gotools.sh remove staticcheck goimports
  gotools.sh config
  gotools.sh config GOTOOLS_STRATEGY
  gotools.sh config GOTOOLS_STRATEGY module
  gotools.sh purge
  gotools.sh uninstall
EOF
    exit 1
}

# ---------------------------------------------------------------------------
# Manifest helpers (.gotools.json)
# ---------------------------------------------------------------------------

# _manifest_parse — read .gotools.json into config vars and _MANIFEST_TOOLS.
# Uses awk on the known, fixed JSON schema (no jq/python3 dependency).
_manifest_parse() {
    local mf="$1"
    [[ -f "$mf" ]] || return 1

    # Parse top-level string fields
    GOTOOLS_STRATEGY=$(awk -F'"' '/"strategy":/  {print $4; exit}' "$mf")
    GOTOOLS_DIR=$(awk -F'"'     '/"dir":/       {print $4; exit}' "$mf")
    GOTOOLS_GO_VERSION=$(awk -F'"' '/"go_version":/ {print $4; exit}' "$mf")
    GOTOOLS_MODULE_PREFIX=$(awk -F'"' '/"module_prefix":/ {print $4; exit}' "$mf")

    # Parse tools section. We match lines that look like tool entries:
    #     "name": {
    #       "source": "go",
    #       "package": "pkg",
    #       "version": "ver"
    #     }
    _MANIFEST_TOOLS=""
    local in_tools=false _tname="" _tsource="" _tpkg="" _tver=""
    while IFS= read -r line; do
        # Detect start/end of "tools" object
        if [[ "$line" =~ ^[[:space:]]*\"tools\"[[:space:]]*:[[:space:]]*\{ ]]; then
            in_tools=true
            continue
        fi
        if $in_tools && [[ "$line" =~ ^[[:space:]]*\} ]]; then
            # Could be closing a tool or closing the tools object
            if [[ -n "$_tname" ]]; then
                _MANIFEST_TOOLS+="${_tname}|${_tsource}|${_tpkg}|${_tver}"$'\n'
                _tname=""; _tsource=""; _tpkg=""; _tver=""
            fi
            # If next non-blank line would be the top-level closing, we're done
            continue
        fi
        if ! $in_tools; then continue; fi

        # Match tool name key:     "name": {
        if [[ "$line" =~ ^[[:space:]]*\"([^\"]+)\"[[:space:]]*:[[:space:]]*\{ ]]; then
            _tname="${BASH_REMATCH[1]}"
            _tsource=""; _tpkg=""; _tver=""
            continue
        fi
        # Match fields inside a tool entry
        if [[ -n "$_tname" ]]; then
            if [[ "$line" =~ \"source\":[[:space:]]*\"([^\"]+)\" ]]; then
                _tsource="${BASH_REMATCH[1]}"
            elif [[ "$line" =~ \"package\":[[:space:]]*\"([^\"]+)\" ]]; then
                _tpkg="${BASH_REMATCH[1]}"
            elif [[ "$line" =~ \"version\":[[:space:]]*\"([^\"]+)\" ]]; then
                _tver="${BASH_REMATCH[1]}"
            elif [[ "$line" =~ ^[[:space:]]*\} ]]; then
                # End of this tool entry
                _MANIFEST_TOOLS+="${_tname}|${_tsource}|${_tpkg}|${_tver}"$'\n'
                _tname=""; _tsource=""; _tpkg=""; _tver=""
            fi
        fi
    done < "$mf"
}

# _manifest_flush — write config vars and _MANIFEST_TOOLS back to .gotools.json.
_manifest_flush() {
    local mf="${1:-$MANIFEST_FILE}"
    local strategy="${GOTOOLS_STRATEGY:-$DEFAULT_STRATEGY}"
    local dir="${GOTOOLS_DIR:-$DEFAULT_DIR}"
    local go_ver="${GOTOOLS_GO_VERSION:-$DEFAULT_GO_VERSION}"

    # Resolve the module prefix at flush time so the manifest never stores
    # an empty string. Falls back to the tools directory itself if there
    # is no parent go.mod and no explicit prefix configured.
    local prefix
    prefix=$(resolve_module_prefix)
    if [[ -z "$prefix" ]]; then
        prefix="$dir"
    fi

    # Build tools JSON block
    local tools_json=""
    local first=true
    local _n _s _p _v
    while IFS='|' read -r _n _s _p _v; do
        [[ -z "$_n" ]] && continue
        if $first; then first=false; else tools_json+=","$'\n'; fi
        tools_json+="    \"${_n}\": {"$'\n'
        tools_json+="      \"source\": \"${_s}\","$'\n'
        tools_json+="      \"package\": \"${_p}\","$'\n'
        tools_json+="      \"version\": \"${_v}\""$'\n'
        tools_json+="    }"
    done <<< "$_MANIFEST_TOOLS"

    cat > "$mf" <<MANIFEST_EOF
{
  "version": 1,
  "strategy": "${strategy}",
  "dir": "${dir}",
  "go_version": "${go_ver}",
  "module_prefix": "${prefix}",
  "tools": {
${tools_json}
  }
}
MANIFEST_EOF
}

# _manifest_tools_list — echo tool names, one per line.
_manifest_tools_list() {
    local _n
    while IFS='|' read -r _n _ _ _; do
        [[ -n "$_n" ]] && echo "$_n"
    done <<< "$_MANIFEST_TOOLS"
}

# _manifest_tool_entry — echo "source|package|version" for a tool name.
_manifest_tool_entry() {
    local want="$1" _n _s _p _v
    while IFS='|' read -r _n _s _p _v; do
        if [[ "$_n" == "$want" ]]; then
            echo "${_s}|${_p}|${_v}"
            return 0
        fi
    done <<< "$_MANIFEST_TOOLS"
    return 1
}

# _manifest_tool_exists — check if a tool name is in the manifest.
_manifest_tool_exists() {
    _manifest_tool_entry "$1" >/dev/null 2>&1
}

# _manifest_tool_set — add or update a tool entry in memory (does NOT flush).
_manifest_tool_set() {
    local name="$1" source="$2" pkg="$3" ver="$4"
    local new_tools="" found=false
    while IFS='|' read -r n s p v; do
        [[ -z "$n" ]] && continue
        if [[ "$n" == "$name" ]]; then
            new_tools+="${name}|${source}|${pkg}|${ver}"$'\n'
            found=true
        else
            new_tools+="${n}|${s}|${p}|${v}"$'\n'
        fi
    done <<< "$_MANIFEST_TOOLS"
    if ! $found; then
        new_tools+="${name}|${source}|${pkg}|${ver}"$'\n'
    fi
    _MANIFEST_TOOLS="$new_tools"
}

# _manifest_tool_remove — remove a tool from memory (does NOT flush).
_manifest_tool_remove() {
    local name="$1"
    local new_tools=""
    while IFS='|' read -r n s p v; do
        [[ -z "$n" ]] && continue
        [[ "$n" != "$name" ]] && new_tools+="${n}|${s}|${p}|${v}"$'\n'
    done <<< "$_MANIFEST_TOOLS"
    _MANIFEST_TOOLS="$new_tools"
}

# _manifest_config_get — extract a single config value from the manifest file.
_manifest_config_get() {
    local key="$1" mf="${2:-$MANIFEST_FILE}"
    case "$key" in
        GOTOOLS_STRATEGY)     awk -F'"' '/"strategy":/  {print $4; exit}' "$mf" ;;
        GOTOOLS_DIR)          awk -F'"' '/"dir":/       {print $4; exit}' "$mf" ;;
        GOTOOLS_GO_VERSION)   awk -F'"' '/"go_version":/{print $4; exit}' "$mf" ;;
        GOTOOLS_MODULE_PREFIX) awk -F'"' '/"module_prefix":/{print $4; exit}' "$mf" ;;
        *) echo "❌ Unknown config key: $key" >&2; return 1 ;;
    esac
}

# _manifest_config_set — update a config field in the manifest file.
_manifest_config_set() {
    local key="$1" value="$2"
    case "$key" in
        GOTOOLS_STRATEGY)     GOTOOLS_STRATEGY="$value"     ;;
        GOTOOLS_DIR)          GOTOOLS_DIR="$value"          ;;
        GOTOOLS_GO_VERSION)   GOTOOLS_GO_VERSION="$value"    ;;
        GOTOOLS_MODULE_PREFIX) GOTOOLS_MODULE_PREFIX="$value" ;;
        *) echo "❌ Unknown config key: $key" >&2; return 1 ;;
    esac
    _manifest_flush
}

# _resolve_installed_version — after go get -tool, read back the version from the
# modfile's require block for the given package.
_resolve_installed_version() {
    local modfile="$1" pkg="$2"
    extract_version_for_pkg "$modfile" "$pkg"
}

# _list_json — output the tool list as a JSON array.
_list_json() {
    load_config
    local first=true
    echo "["
    while IFS='|' read -r _n _s _p _v; do
        [[ -z "$_n" ]] && continue
        local go_ver="?"
        case "$GOTOOLS_STRATEGY" in
            unified) go_ver=$(extract_go_version_from_mod "$GOTOOLS_DIR/go.mod" 2>/dev/null || echo "?") ;;
            split)   go_ver=$(extract_go_version_from_mod "$GOTOOLS_DIR/${_n}.mod" 2>/dev/null || echo "?") ;;
            module)  go_ver=$(extract_go_version_from_mod "$GOTOOLS_DIR/${_n}/go.mod" 2>/dev/null || echo "?") ;;
        esac
        $first && first=false || echo ","
        printf '  {"name":"%s","source":"%s","strategy":"%s","go":"%s","package":"%s","version":"%s"}' \
            "$_n" "$_s" "$GOTOOLS_STRATEGY" "$go_ver" "$_p" "$_v"
    done <<< "$_MANIFEST_TOOLS"
    echo ""
    echo "]"
}

# _info_json — output a single tool as a JSON object.
_info_json() {
    local name="$1"
    local entry
    entry=$(_manifest_tool_entry "$name" 2>/dev/null || true)
    if [[ -z "$entry" ]]; then
        echo "{\"error\":\"tool not found: $name\"}"
        return 1
    fi
    local _s _p _v
    _s=$(echo "$entry" | cut -d'|' -f1)
    _p=$(echo "$entry" | cut -d'|' -f2)
    _v=$(echo "$entry" | cut -d'|' -f3)
    local go_ver="?"
    case "$GOTOOLS_STRATEGY" in
        unified) go_ver=$(extract_go_version_from_mod "$GOTOOLS_DIR/go.mod" 2>/dev/null || echo "?") ;;
        split)   go_ver=$(extract_go_version_from_mod "$GOTOOLS_DIR/${name}.mod" 2>/dev/null || echo "?") ;;
        module)  go_ver=$(extract_go_version_from_mod "$GOTOOLS_DIR/${name}/go.mod" 2>/dev/null || echo "?") ;;
    esac
    printf '{"name":"%s","source":"%s","strategy":"%s","go":"%s","package":"%s","version":"%s"}\n' \
        "$name" "$_s" "$GOTOOLS_STRATEGY" "$go_ver" "$_p" "$_v"
}

# ---------------------------------------------------------------------------
# Config helpers
# ---------------------------------------------------------------------------
_CONFIG_LOADED=false

load_config() {
    if [[ "$_CONFIG_LOADED" == "true" ]]; then
        return
    fi

    # find project root by walking up from $PWD.
    _find_project_root

    if [[ -f "$MANIFEST_FILE" ]]; then
        _manifest_parse "$MANIFEST_FILE"
    fi

    # Startup env overrides > config file > defaults.
    # Need to re-resolve after parsing because the manifest/env may have set these
    # to empty strings, and we need defaults to kick in for empty values.
    GOTOOLS_STRATEGY="${_ORIG_ENV_STRATEGY:-${GOTOOLS_STRATEGY:-$DEFAULT_STRATEGY}}"
    GOTOOLS_DIR="${_ORIG_ENV_DIR:-${GOTOOLS_DIR:-$DEFAULT_DIR}}"
    GOTOOLS_GO_VERSION="${_ORIG_ENV_GO_VERSION:-${GOTOOLS_GO_VERSION:-$DEFAULT_GO_VERSION}}"
    GOTOOLS_MODULE_PREFIX="${_ORIG_ENV_MODULE_PREFIX:-${GOTOOLS_MODULE_PREFIX:-$DEFAULT_MODULE_PREFIX}}"

    # Normalize: treat empty module_prefix as unset so auto-detection kicks in
    # in resolve_module_prefix.
    [[ -n "${GOTOOLS_MODULE_PREFIX:-}" ]] || GOTOOLS_MODULE_PREFIX=""

    _CONFIG_LOADED=true
}

reload_config() {
    _CONFIG_LOADED=false
    load_config
}

# detect_strategy <dir>
#   Inspects the tools directory on disk and returns which strategy it
#   actually matches: unified, split, module, or empty (unknown).
#   If hybrid structures are detected (e.g., unified go.mod + split .mod files),
#   the dominant strategy is returned and a warning is printed to stderr.
detect_strategy() {
    local dir="${1:-$GOTOOLS_DIR}"
    [[ -d "$dir" ]] || return 0

    local has_unified=false has_module=false has_split=false

    # Check for unified: single go.mod at the tools root with tool directives
    if [[ -f "$dir/go.mod" ]] && grep -q '^tool ' "$dir/go.mod" 2>/dev/null; then
        has_unified=true
    fi

    # Check for module: subdirectories each containing go.mod
    for d in "$dir"/*/; do
        if [[ -d "$d" && -f "$d/go.mod" ]]; then
            has_module=true
            break
        fi
    done

    # Check for split: flat *.mod files (not go.mod)
    for f in "$dir"/*.mod; do
        if [[ -f "$f" && "$(basename "$f")" != "go.mod" ]]; then
            has_split=true
            break
        fi
    done

    # Count how many strategies are detected.
    local count=0
    $has_unified && count=$((count + 1))
    $has_module   && count=$((count + 1))
    $has_split    && count=$((count + 1))

    if [[ $count -gt 1 ]]; then
        echo "⚠️  Hybrid tools directory detected in $dir/:" >&2
        $has_unified && echo "   - unified  (go.mod with tool directives)" >&2
        $has_module   && echo "   - module   (subdirectories with go.mod)" >&2
        $has_split    && echo "   - split    (flat .mod files)" >&2
        echo "   Run 'gotools.sh migrate <strategy>' to clean up." >&2
    fi

    # Return the detected strategy using the original priority order.
    $has_unified && { echo "unified"; return; }
    $has_module   && { echo "module";   return; }
    $has_split    && { echo "split";    return; }
    # Empty / unknown
    return 0
}

resolve_go_version() {
    if [[ "$GOTOOLS_GO_VERSION" != "inherit" ]]; then
        echo "$GOTOOLS_GO_VERSION"
        return
    fi
    # Try the project root go.mod first.
    local root_mod="go.mod"
    if [[ -f "$root_mod" ]]; then
        local v
        v=$(awk '$1 == "go" { print $2; exit }' "$root_mod")
        if [[ -n "$v" ]]; then
            echo "$v"
            return
        fi
    fi
    # Fallback: ask the go tool itself.
    go env GOVERSION | sed 's/^go//' | awk -F. '{ print $1"."$2 }'
}

# resolve_module_prefix
#   Returns the module prefix to use for tool go.mod files.
#   Priority:
#     1. GOTOOLS_MODULE_PREFIX from .gotools.json (if non-empty)
#     2. Parent module path from root go.mod (e.g. github.com/user/repo)
#     3. Empty string — falls back to bare "tools" / "tools/<name>"
resolve_module_prefix() {
    # Explicit override takes priority.
    if [[ -n "${GOTOOLS_MODULE_PREFIX:-}" ]]; then
        echo "$GOTOOLS_MODULE_PREFIX"
        return
    fi
    # Auto-detect from root go.mod.
    local root_mod="go.mod"
    if [[ -f "$root_mod" ]]; then
        local mod
        mod=$(awk '$1 == "module" { print $2; exit }' "$root_mod")
        if [[ -n "$mod" ]]; then
            echo "$mod"
            return
        fi
    fi
    # No parent module found.
    echo ""
}

# tool_module_path [tool_name]
#   Builds the full module path for a tool go.mod using $GOTOOLS_DIR.
#   No args:  module path for the tools dir itself (unified strategy).
#   One arg:  module path for a specific tool.
#
#   Examples (GOTOOLS_DIR=tools, parent=github.com/user/repo):
#     tool_module_path              -> "github.com/user/repo/tools"
#     tool_module_path mockgen      -> "github.com/user/repo/tools/mockgen"
#
#   Examples (GOTOOLS_DIR=build/tools, parent=github.com/user/repo):
#     tool_module_path              -> "github.com/user/repo/build/tools"
#     tool_module_path mockgen      -> "github.com/user/repo/build/tools/mockgen"
#
#   Examples (GOTOOLS_DIR=tools, no parent go.mod):
#     tool_module_path              -> "tools"
#     tool_module_path mockgen      -> "tools/mockgen"
tool_module_path() {
    local prefix
    prefix=$(resolve_module_prefix)
    local dir_path="$GOTOOLS_DIR"
    if [[ $# -ge 1 && -n "$1" ]]; then
        dir_path="${dir_path}/${1}"
    fi
    if [[ -n "$prefix" ]]; then
        echo "${prefix}/${dir_path}"
    else
        echo "$dir_path"
    fi
}

# ---------------------------------------------------------------------------
# Parsing helpers for go.mod files
#
# extract_go_version_from_mod <modfile>
#   Prints the Go version from the `go` directive in a go.mod file.
extract_go_version_from_mod() {
    local modfile="$1"
    awk '$1 == "go" { print $2; exit }' "$modfile"
}

# relative_path <absolute_or_relative>
#   Prints the path relative to the current working directory.
#   Pure-bash implementation — no python3 or GNU realpath required.
relative_path() {
    local target="$1"

    # If already relative and doesn't need resolving, just clean it up.
    # Convert to absolute so the algorithm below always works.
    [[ "$target" != /* ]] && target="$PWD/$target"

    # Normalise both paths: resolve /./, remove trailing slashes,
    # collapse repeated slashes.  We avoid readlink/realpath so this
    # works on macOS and Linux without extra tools.
    local abs_target abs_base
    abs_target=$(cd "$(dirname "$target")" 2>/dev/null && echo "$PWD/$(basename "$target")") \
        || { echo "$1"; return; }
    abs_base="$PWD"

    # Split into arrays on '/'.
    local IFS='/'
    read -ra t_parts <<< "$abs_target"
    read -ra b_parts <<< "$abs_base"

    # Find the length of the common prefix.
    local i=0
    while [[ $i -lt ${#t_parts[@]} && $i -lt ${#b_parts[@]} && "${t_parts[$i]}" == "${b_parts[$i]}" ]]; do
        (( i++ ))
    done

    # Build the relative path: one ".." for each remaining base component,
    # then append the remaining target components.
    local rel=""
    local j
    for (( j=i; j<${#b_parts[@]}; j++ )); do
        rel="${rel}../"
    done
    for (( j=i; j<${#t_parts[@]}; j++ )); do
        rel="${rel}${t_parts[$j]}/"
    done

    # Strip trailing slash, default to "." for identical paths.
    rel="${rel%/}"
    echo "${rel:-.}"
}
# ---------------------------------------------------------------------------

# extract_tools_from_mod <modfile>
#   Prints one package path per line from `tool` directives.
#   Handles both:
#     tool pkg
#     tool (
#       pkg1
#       pkg2
#     )
extract_tools_from_mod() {
    local modfile="$1"
    awk '
        /^tool[[:space:]]+\(/ { in_block=1; next }
        in_block && /^\)/ { in_block=0; next }
        in_block {
            gsub(/^[[:space:]]+/, "")
            gsub(/[[:space:]]+$/, "")
            if ($0 != "") print $0
            next
        }
        $1 == "tool" && $2 != "(" { print $2 }
    ' "$modfile"
}

# extract_version_for_pkg <modfile> <pkg>
#   Prints the version string for <pkg> from the require block(s) of <modfile>.
#   Handles subpackage paths like github.com/foo/bar/v2/cmd/baz by trying
#   progressively shorter prefixes until it finds a match in require.
extract_version_for_pkg() {
    local modfile="$1" pkg="$2"
    local candidate="$pkg"
    while [[ -n "$candidate" ]]; do
        local ver
        ver=$(awk -v p="$candidate" '
            /^require[[:space:]]+\(/ { in_req=1; next }
            in_req && /^\)/ { in_req=0; next }
            in_req {
                gsub(/^[[:space:]]+/, "")
                if ($1 == p) { gsub(/\/\/.*/, "", $2); print $2 }
                next
            }
            $1 == "require" && $2 == p { gsub(/\/\/.*/, "", $3); print $3 }
        ' "$modfile")
        if [[ -n "$ver" ]]; then
            echo "$ver"
            return
        fi
        # Strip the last path component and try again.
        local parent="${candidate%/*}"
        [[ "$parent" == "$candidate" ]] && break
        candidate="$parent"
    done
}

# extract_pkg_from_mod <modfile>
#   Returns the FIRST tool package from a go.mod (convenience for single-tool mods).
extract_pkg_from_mod() {
    extract_tools_from_mod "$1" | head -n1
}

# infer_binary_name_from_pkg <package-path>
#   Converts a Go package path (without @version) into the expected binary name.
#   Examples:
#     github.com/goreleaser/goreleaser/v2           -> goreleaser
#     github.com/goreleaser/goreleaser/v2/cmd/goreleaser -> goreleaser
#     golang.org/x/tools/cmd/goimports              -> goimports
#     honnef.co/go/tools/cmd/staticcheck            -> staticcheck
infer_binary_name_from_pkg() {
    local pkg="$1"
    # Remove version suffix: /v2, /v3, /v10, etc.
    pkg="${pkg%/v[0-9]*}"
    # If it ends with /cmd/<name>, take <name>
    if [[ "$pkg" =~ /cmd/([^/]+)$ ]]; then
        echo "${BASH_REMATCH[1]}"
        return
    fi
    # Fallback to basename
    basename "$pkg"
}

# resolve_binary_name <tool_name> <modfile>
#   Dereferences a tool alias to the actual Go binary name.
#   Reads the tool directive from the modfile and returns the inferred binary name.
#   Falls back to the tool_name itself if no directive is found.
resolve_binary_name() {
    local tool_name="$1" modfile="$2"
    local pkg
    pkg=$(extract_pkg_from_mod "$modfile")
    if [[ -n "$pkg" ]]; then
        infer_binary_name_from_pkg "$pkg"
    else
        echo "$tool_name"
    fi
}

# pkg_for_tool <tool-name>
#   Prints the full package path (e.g., github.com/.../cmd/tool) for the given tool name,
#   based on the current strategy. Returns 1 if not found.
pkg_for_tool() {
    local tool_name="$1"
    case "$GOTOOLS_STRATEGY" in
        unified)
            local modfile="$GOTOOLS_DIR/go.mod"
            [[ -f "$modfile" ]] || return 1
            while IFS= read -r pkg; do
                if [[ "$(infer_binary_name_from_pkg "$pkg")" == "$tool_name" ]]; then
                    echo "$pkg"
                    return 0
                fi
            done < <(extract_tools_from_mod "$modfile")
            ;;
        split)
            local modfile="$GOTOOLS_DIR/${tool_name}.mod"
            [[ -f "$modfile" ]] || return 1
            extract_pkg_from_mod "$modfile"
            ;;
        module)
            local modfile="$GOTOOLS_DIR/${tool_name}/go.mod"
            [[ -f "$modfile" ]] || return 1
            extract_pkg_from_mod "$modfile"
            ;;
    esac
    return 1
}

# ---------------------------------------------------------------------------
# extract_tools_with_versions
#   Outputs lines of: name pkg@version
#   Strategy-aware.
# ---------------------------------------------------------------------------
extract_tools_with_versions() {
    case "$GOTOOLS_STRATEGY" in
        unified)
            local modfile="$GOTOOLS_DIR/go.mod"
            [[ -f "$modfile" ]] || return 0
            while IFS= read -r pkg; do
                [[ -z "$pkg" ]] && continue
                local name ver
                name=$(infer_binary_name_from_pkg "$pkg")
                ver=$(extract_version_for_pkg "$modfile" "$pkg")
                if [[ -n "$ver" ]]; then
                    echo "$name ${pkg}@${ver}"
                else
                    echo "$name ${pkg}@latest"
                fi
            done < <(extract_tools_from_mod "$modfile")
            ;;

        split)
            for f in "$GOTOOLS_DIR"/*.mod; do
                [[ -f "$f" ]] || continue
                local name pkg ver
                name=$(basename "$f" .mod)
                pkg=$(extract_pkg_from_mod "$f")
                [[ -z "$pkg" ]] && continue
                ver=$(extract_version_for_pkg "$f" "$pkg")
                if [[ -n "$ver" ]]; then
                    echo "$name ${pkg}@${ver}"
                else
                    echo "$name ${pkg}@latest"
                fi
            done
            ;;

        module)
            for d in "$GOTOOLS_DIR"/*/; do
                [[ -d "$d" ]] || continue
                local modfile="$d/go.mod"
                [[ -f "$modfile" ]] || continue
                local name pkg ver
                name=$(basename "$d")
                pkg=$(extract_pkg_from_mod "$modfile")
                [[ -z "$pkg" ]] && continue
                ver=$(extract_version_for_pkg "$modfile" "$pkg")
                if [[ -n "$ver" ]]; then
                    echo "$name ${pkg}@${ver}"
                else
                    echo "$name ${pkg}@latest"
                fi
            done
            ;;

        *)
            echo "❌ Unknown strategy: $GOTOOLS_STRATEGY" >&2
            return 1
            ;;
    esac
}

# ---------------------------------------------------------------------------
# Commands
# ---------------------------------------------------------------------------

cmd_version() {
    echo "gotools.sh $VERSION"
}

# ---- check ----------------------------------------------------------------
cmd_check() {
    _require_go
    load_config
    local ok=0 fail=0
    echo "🔍 Checking managed tools..."
    echo ""
    while IFS='|' read -r _n _s _p _v; do
        [[ -z "$_n" ]] && continue
        local status="✅"
        if ! "$0" exec "$_n" --version >/dev/null 2>&1; then
            if ! "$0" exec "$_n" --help >/dev/null 2>&1; then
                if ! "$0" exec "$_n" version >/dev/null 2>&1; then
                    status="❌"
                fi
            fi
        fi
        if [[ "$status" == "✅" ]]; then
            echo "  ✅ $_n ($_p@$_v)"
            ok=$((ok + 1))
        else
            echo "  ❌ $_n ($_p@$_v) — not runnable"
            fail=$((fail + 1))
        fi
    done <<< "$_MANIFEST_TOOLS"
    echo ""
    echo "  $ok passed, $fail failed"
    [[ $fail -eq 0 ]] || return 1
}

# ---- completion ----------------------------------------------------------
_generate_bash_completion() {
    cat <<'COMPLETION'
_gotools_completion() {
    local cur prev words cword
    _init_completion || return
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    if [[ $COMP_CWORD -eq 1 ]]; then
        COMPREPLY=($(compgen -W "init install sync exec list upgrade remove migrate config purge info check version self-update uninstall help" -- "$cur"))
        return
    fi

    case "$prev" in
        exec|info|remove|upgrade)
            local tools
            tools=$(gotools.sh list 2>/dev/null | awk 'NR>2 && $1!="" {print $1}')
            COMPREPLY=($(compgen -W "$tools" -- "$cur"))
            ;;
        migrate)
            COMPREPLY=($(compgen -W "unified split module" -- "$cur"))
            ;;
        config)
            COMPREPLY=($(compgen -W "GOTOOLS_STRATEGY GOTOOLS_DIR GOTOOLS_GO_VERSION GOTOOLS_MODULE_PREFIX" -- "$cur"))
            ;;
        init)
            COMPREPLY=($(compgen -W "--strategy= --dir= --go= --prefix=" -- "$cur"))
            ;;
        install)
            COMPREPLY=($(compgen -W "--force" -- "$cur"))
            ;;
        sync|upgrade|remove|migrate|purge)
            COMPREPLY=($(compgen -W "--dry-run" -- "$cur"))
            ;;
        list|info)
            COMPREPLY=($(compgen -W "--json" -- "$cur"))
            ;;
    esac
}
complete -F _gotools_completion gotools.sh gotools
COMPLETION
}

_generate_zsh_completion() {
    cat <<'COMPLETION'
#compdef gotools.sh gotools
_gotools() {
    local -a commands
    commands=(init install sync exec list upgrade remove migrate config purge info check version self-update uninstall help)
    _describe 'command' commands
}
_gotools
COMPLETION
}

_generate_fish_completion() {
    echo "complete -c gotools.sh -f"
    echo "complete -c gotools.sh -a 'init install sync exec list upgrade remove migrate config purge info check version self-update uninstall help'"
    echo "complete -c gotools -f"
    echo "complete -c gotools -a 'init install sync exec list upgrade remove migrate config purge info check version self-update uninstall help'"
}

cmd_completion() {
    local shell="${1:-bash}"

    # install subcommand
    if [[ "$shell" == "install" ]]; then
        cmd_completion_install "${2:-}"
        return
    fi

    case "$shell" in
        bash) _generate_bash_completion ;;
        zsh)  _generate_zsh_completion ;;
        fish) _generate_fish_completion ;;
        *)
            echo "Usage: gotools.sh completion <bash|zsh|fish|install>" >&2
            return 1
            ;;
    esac
    echo ""
    echo "# To activate:" >&2
    case "$shell" in
        bash) echo "#   source <(gotools.sh completion bash)" >&2 ;;
        zsh)  echo "#   source <(gotools.sh completion zsh)"  >&2 ;;
        fish) echo "#   gotools.sh completion fish | source"   >&2 ;;
    esac
}

cmd_completion_install() {
    local shell="${1:-}"

    # auto-detect shell from $SHELL if not specified
    if [[ -z "$shell" ]]; then
        case "$SHELL" in
            */bash) shell=bash ;;
            */zsh)  shell=zsh ;;
            */fish) shell=fish ;;
            *)
                echo "❌ Error: Could not detect shell from \$SHELL." >&2
                echo "   Please specify: gotools.sh completion install <bash|zsh|fish>" >&2
                return 1
                ;;
        esac
    fi

    local install_path
    case "$shell" in
        bash)
            if [[ -n "${BASH_COMPLETION_USER_DIR:-}" ]]; then
                install_path="$BASH_COMPLETION_USER_DIR/completions/gotools"
            elif [[ -n "${XDG_DATA_HOME:-}" ]]; then
                install_path="$XDG_DATA_HOME/bash-completion/completions/gotools"
            else
                install_path="$HOME/.local/share/bash-completion/completions/gotools"
            fi
            ;;
        zsh)
            install_path="$HOME/.zsh/completions/_gotools"
            ;;
        fish)
            install_path="$HOME/.config/fish/completions/gotools.fish"
            ;;
        *)
            echo "Usage: gotools.sh completion install <bash|zsh|fish>" >&2
            return 1
            ;;
    esac

    local dir
    dir=$(dirname "$install_path")
    if [[ ! -d "$dir" ]]; then
        mkdir -p "$dir" || {
            echo "❌ Error: Could not create directory: $dir" >&2
            return 1
        }
    fi

    case "$shell" in
        bash) _generate_bash_completion > "$install_path" ;;
        zsh)  _generate_zsh_completion > "$install_path" ;;
        fish) _generate_fish_completion > "$install_path" ;;
    esac

    echo "✅ Completion installed for $shell: $install_path"

    # post-install hints
    case "$shell" in
        bash)
            echo ""
            echo "💡 To activate, add this to your ~/.bashrc:"
            echo "   source \"$install_path\""
            ;;
        zsh)
            echo ""
            echo "💡 To activate, add this to your ~/.zshrc:"
            echo "   fpath=(\$HOME/.zsh/completions \$fpath)"
            echo "   autoload -Uz compinit && compinit"
            ;;
        fish)
            echo ""
            echo "💡 Fish auto-loads completions from ~/.config/fish/completions/"
            echo "   No further setup needed. Restart your shell or run:"
            echo "   source \"$install_path\""
            ;;
    esac
}

# ---- init ----------------------------------------------------------------
cmd_init() {
    local strategy=$DEFAULT_STRATEGY dir=$DEFAULT_DIR go_v=$DEFAULT_GO_VERSION prefix=""
    for arg in "$@"; do
        case $arg in
            --strategy=*) strategy="${arg#*=}" ;;
            --dir=*)      dir="${arg#*=}" ;;
            --go=*)       go_v="${arg#*=}" ;;
            --prefix=*)   prefix="${arg#*=}" ;;
            *)            echo "❓ Unknown flag: $arg"; usage ;;
        esac
    done

    # Validate strategy.
    case "$strategy" in
        unified|split|module) ;;
        *) echo "❌ Invalid strategy: $strategy (must be unified, split, or module)" >&2; exit 1 ;;
    esac

    # Set config vars in memory, then flush manifest.
    GOTOOLS_STRATEGY="$strategy"
    GOTOOLS_DIR="$dir"
    GOTOOLS_GO_VERSION="$go_v"
    GOTOOLS_MODULE_PREFIX="$prefix"
    _MANIFEST_TOOLS=""
    _manifest_flush

    mkdir -p "$dir"
    echo "✅ Initialized $MANIFEST_FILE (strategy=$strategy, dir=$dir)"

    # Reload so cmd_sync picks up the new values.
    load_config
    cmd_sync
}

# ---- sync ----------------------------------------------------------------
cmd_sync() {
    _require_go
    _acquire_lock
    _DRY_RUN=false
    for a in "$@"; do [[ "$a" == "--dry-run" ]] && _DRY_RUN=true; done

    load_config
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
            exit 1
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
                cmd_install "$_n" "${_p}@${_v}"
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

# ---- install -------------------------------------------------------------
cmd_install() {
    _require_go
    load_config
    _acquire_lock

    # Strip --force from args before parsing name/pkg.
    local force=false filtered=()
    for a in "$@"; do
        if [[ "$a" == "--force" ]]; then force=true
        else filtered+=("$a"); fi
    done
    set -- "${filtered[@]}"

    local name="" pkg=""
    if [[ $# -eq 0 ]]; then
        echo "❌ Usage: $(basename "$0") install [name] <pkg> [--force]" >&2
        exit 1
    elif [[ $# -eq 1 ]]; then
        pkg="$1"
        name=$(infer_binary_name_from_pkg "${pkg%%@*}")
    else
        name="$1"
        pkg="$2"
    fi

    local target_v
    target_v=$(resolve_go_version)

    # refuse duplicate tool names unless --force is given.
    if _manifest_tool_exists "$name" && ! $force; then
        echo "❌ Tool '$name' already exists in manifest. Use --force to overwrite." >&2
        exit 1
    fi

    echo "📦 Installing $name ($pkg) [strategy=$GOTOOLS_STRATEGY]..."

    case "$GOTOOLS_STRATEGY" in
        unified)
            mkdir -p "$GOTOOLS_DIR"
            if [[ ! -f "$GOTOOLS_DIR/go.mod" ]]; then
                (cd "$GOTOOLS_DIR" && go mod init "$(tool_module_path)" && go mod edit -go="$target_v")
            fi
            (cd "$GOTOOLS_DIR" && go get -tool "$pkg")
            ;;

        split)
            mkdir -p "$GOTOOLS_DIR"
            local modfile="${name}.mod"
            if [[ ! -f "$GOTOOLS_DIR/$modfile" ]]; then
                local mod_path
                mod_path=$(tool_module_path "$name")
                cat > "$GOTOOLS_DIR/$modfile" <<MODEOF
module $mod_path

go $target_v
MODEOF
            fi
            (cd "$GOTOOLS_DIR" && go get -tool -modfile="$modfile" "$pkg")
            ;;

        module)
            mkdir -p "$GOTOOLS_DIR/$name"
            if [[ ! -f "$GOTOOLS_DIR/$name/go.mod" ]]; then
                (cd "$GOTOOLS_DIR/$name" && go mod init "$(tool_module_path "$name")" && go mod edit -go="$target_v")
            fi
            (cd "$GOTOOLS_DIR/$name" && go get -tool "$pkg")
            ;;

        *)
            echo "❌ Unknown strategy: $GOTOOLS_STRATEGY" >&2
            exit 1
            ;;
    esac

    # Resolve the actual installed version and update manifest.
    local resolved_ver="" pkg_base="${pkg%%@*}"
    case "$GOTOOLS_STRATEGY" in
        unified) resolved_ver=$(_resolve_installed_version "$GOTOOLS_DIR/go.mod" "$pkg_base") ;;
        split)   resolved_ver=$(_resolve_installed_version "$GOTOOLS_DIR/$modfile" "$pkg_base") ;;
        module)  resolved_ver=$(_resolve_installed_version "$GOTOOLS_DIR/$name/go.mod" "$pkg_base") ;;
    esac
    _manifest_tool_set "$name" "go" "$pkg_base" "${resolved_ver:-latest}"
    _manifest_flush

    echo "✅ Installed $name"

    # warn when @latest is used — floating versions break reproducibility.
    if [[ "$pkg" == *@latest ]]; then
        echo "⚠️  Installed at @latest (resolved: ${resolved_ver:-latest})."
        echo "   Pin this version for reproducibility:"
        echo "     gotools.sh install $name ${pkg_base}@${resolved_ver:-latest}"
    fi
}

# ---- exec ----------------------------------------------------------------
cmd_exec() {
    _require_go
    load_config
    local tool_name="${1:?tool name is required}"
    shift

    case "$GOTOOLS_STRATEGY" in
        unified)
            if [[ ! -f "$GOTOOLS_DIR/go.mod" ]]; then
                echo "❌ Error: No $GOTOOLS_DIR/go.mod found. Run 'init' first." >&2
                exit 1
            fi
            local binary
            binary=$(resolve_binary_name "$tool_name" "$GOTOOLS_DIR/go.mod")
            (cd "$GOTOOLS_DIR" && exec go tool "$binary" "$@")
            ;;

        split)
            local mod_file="$GOTOOLS_DIR/${tool_name}.mod"
            if [[ ! -f "$mod_file" ]]; then
                echo "❌ Error: Tool '$tool_name' not found ($mod_file missing). Run 'install' first." >&2
                exit 1
            fi
            local binary
            binary=$(resolve_binary_name "$tool_name" "$mod_file")
            exec go tool -modfile="$mod_file" "$binary" "$@"
            ;;

        module)
            if [[ ! -d "$GOTOOLS_DIR/$tool_name" ]]; then
                echo "❌ Error: Tool '$tool_name' not found ($GOTOOLS_DIR/$tool_name missing). Run 'install' first." >&2
                exit 1
            fi
            local binary
            binary=$(resolve_binary_name "$tool_name" "$GOTOOLS_DIR/$tool_name/go.mod")
            (cd "$GOTOOLS_DIR/$tool_name" && exec go tool "$binary" "$@")
            ;;

        *)
            echo "❌ Unknown strategy: $GOTOOLS_STRATEGY" >&2
            exit 1
            ;;
    esac
}

# ---- list ----------------------------------------------------------------
cmd_list() {
    load_config
    local as_json=false
    [[ "${1:-}" == "--json" ]] && as_json=true

    if $as_json; then
        _list_json
        return
    fi

    printf "  %-18s %-6s %-10s %-8s %-30s %s\n" "TOOL" "SOURCE" "STRATEGY" "GO" "MODFILE" "PACKAGE@VERSION"
    printf "  %-18s %-6s %-10s %-8s %-30s %s\n" "----" "------" "--------" "--" "-------" "---------------"

    case "$GOTOOLS_STRATEGY" in
        unified)
            local modfile="$GOTOOLS_DIR/go.mod"
            if [[ -f "$modfile" ]]; then
                local go_ver rel_mod
                go_ver=$(extract_go_version_from_mod "$modfile")
                rel_mod=$(relative_path "$modfile")
                while IFS= read -r pkg; do
                    [[ -z "$pkg" ]] && continue
                    local name ver src
                    name=$(infer_binary_name_from_pkg "$pkg")
                    ver=$(extract_version_for_pkg "$modfile" "$pkg")
                    src=$(_manifest_tool_entry "$name" 2>/dev/null | cut -d'|' -f1)
                    printf "  %-18s %-6s %-10s %-8s %-30s %s\n" "$name" "${src:-go}" "unified" "${go_ver:-?}" "$rel_mod" "${pkg}@${ver:-unknown}"
                done < <(extract_tools_from_mod "$modfile")
            fi
            ;;

        split)
            for f in "$GOTOOLS_DIR"/*.mod; do
                [[ -f "$f" ]] || continue
                local name pkg ver go_ver rel_mod src
                name=$(basename "$f" .mod)
                pkg=$(extract_pkg_from_mod "$f")
                [[ -z "$pkg" ]] && continue
                ver=$(extract_version_for_pkg "$f" "$pkg")
                go_ver=$(extract_go_version_from_mod "$f")
                rel_mod=$(relative_path "$f")
                src=$(_manifest_tool_entry "$name" 2>/dev/null | cut -d'|' -f1)
                printf "  %-18s %-6s %-10s %-8s %-30s %s\n" "$name" "${src:-go}" "split" "${go_ver:-?}" "$rel_mod" "${pkg}@${ver:-unknown}"
            done
            ;;

        module)
            for d in "$GOTOOLS_DIR"/*/; do
                [[ -d "$d" ]] || continue
                local modfile="$d/go.mod"
                [[ -f "$modfile" ]] || continue
                local name pkg ver go_ver rel_mod src
                name=$(basename "$d")
                pkg=$(extract_pkg_from_mod "$modfile")
                [[ -z "$pkg" ]] && continue
                ver=$(extract_version_for_pkg "$modfile" "$pkg")
                go_ver=$(extract_go_version_from_mod "$modfile")
                rel_mod=$(relative_path "$modfile")
                src=$(_manifest_tool_entry "$name" 2>/dev/null | cut -d'|' -f1)
                printf "  %-18s %-6s %-10s %-8s %-30s %s\n" "$name" "${src:-go}" "module" "${go_ver:-?}" "$rel_mod" "${pkg}@${ver:-unknown}"
            done
            ;;

        *)
            echo "❌ Unknown strategy: $GOTOOLS_STRATEGY" >&2
            exit 1
            ;;
    esac

    # If nothing was printed from disk, show what's in the manifest.
    if [[ -z "$(extract_tools_with_versions 2>/dev/null)" ]]; then
        local _n _s _p _v
        while IFS='|' read -r _n _s _p _v; do
            [[ -z "$_n" ]] && continue
            printf "  %-18s %-6s %-10s %-8s %-30s %s\n" "$_n" "$_s" "$GOTOOLS_STRATEGY" "?" "(manifest)" "${_p}@${_v}"
        done <<< "$_MANIFEST_TOOLS"
    fi
}

# ---- info ----------------------------------------------------------------
cmd_info() {
    load_config
    if [[ $# -eq 0 ]]; then
        echo "❌ Usage: $(basename "$0") info <tool-name> [--json]" >&2
        exit 1
    fi

    local tool_name="$1" as_json=false
    [[ "${2:-}" == "--json" ]] && as_json=true

    if $as_json; then
        _info_json "$tool_name"
        return
    fi

    local modfile="" pkg="" ver="" go_ver="" strategy="$GOTOOLS_STRATEGY"

    case "$GOTOOLS_STRATEGY" in
        unified)
            modfile="$GOTOOLS_DIR/go.mod"
            if [[ ! -f "$modfile" ]]; then
                echo "❌ No $modfile found. Run 'init' first." >&2
                exit 1
            fi
            # Find the package whose inferred name matches the tool name.
            pkg=$(extract_tools_from_mod "$modfile" | while IFS= read -r p; do
                if [[ "$(infer_binary_name_from_pkg "$p")" == "$tool_name" ]]; then
                    echo "$p"
                    break
                fi
            done)
            if [[ -z "$pkg" ]]; then
                echo "❌ Tool '$tool_name' not found in $modfile." >&2
                exit 1
            fi
            ver=$(extract_version_for_pkg "$modfile" "$pkg")
            go_ver=$(extract_go_version_from_mod "$modfile")
            ;;

        split)
            modfile="$GOTOOLS_DIR/${tool_name}.mod"
            if [[ ! -f "$modfile" ]]; then
                echo "❌ Tool '$tool_name' not found ($modfile missing)." >&2
                exit 1
            fi
            pkg=$(extract_pkg_from_mod "$modfile")
            ver=$(extract_version_for_pkg "$modfile" "$pkg")
            go_ver=$(extract_go_version_from_mod "$modfile")
            ;;

        module)
            modfile="$GOTOOLS_DIR/$tool_name/go.mod"
            if [[ ! -f "$modfile" ]]; then
                echo "❌ Tool '$tool_name' not found ($modfile missing)." >&2
                exit 1
            fi
            pkg=$(extract_pkg_from_mod "$modfile")
            ver=$(extract_version_for_pkg "$modfile" "$pkg")
            go_ver=$(extract_go_version_from_mod "$modfile")
            ;;

        *)
            echo "❌ Unknown strategy: $GOTOOLS_STRATEGY" >&2
            exit 1
            ;;
    esac

    local rel_mod src
    rel_mod=$(relative_path "$modfile")
    src=$(_manifest_tool_entry "$tool_name" 2>/dev/null | cut -d'|' -f1)

    echo ""
    echo "  Tool:       $tool_name"
    echo "  Source:     ${src:-go}"
    echo "  Package:    ${pkg:-unknown}"
    echo "  Version:    ${ver:-unknown}"
    echo "  Go:         ${go_ver:-unknown}"
    echo "  Strategy:   $strategy"
    echo "  Modfile:    $rel_mod"
    echo ""
}

# ---- upgrade -------------------------------------------------------------
cmd_upgrade() {
    _require_go
    _acquire_lock
    _DRY_RUN=false
    for a in "$@"; do [[ "$a" == "--dry-run" ]] && _DRY_RUN=true; done
    load_config
    if [[ $# -eq 0 ]]; then
        echo "❌ Usage: $(basename "$0") upgrade <name|all> [--dry-run]" >&2
        exit 1
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
                (cd "$GOTOOLS_DIR" && go get -tool "${pkg}@latest")
                ;;
            split)
                (cd "$GOTOOLS_DIR" && go get -tool -modfile="$t.mod" "${pkg}@latest")
                ;;
            module)
                (cd "$GOTOOLS_DIR/$t" && go get -tool "${pkg}@latest")
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

# ---- remove --------------------------------------------------------------
cmd_remove() {
    _require_go
    _acquire_lock
    _DRY_RUN=false
    for a in "$@"; do [[ "$a" == "--dry-run" ]] && _DRY_RUN=true; done
    load_config
    if [[ $# -eq 0 ]]; then
        echo "❌ Usage: $(basename "$0") remove <name1> [name2...] [--dry-run]" >&2
        exit 1
    fi

    for name in "$@"; do
        [[ "$name" == "--dry-run" ]] && continue

        if $_DRY_RUN; then
            echo "  🔍 [dry-run] Would remove $name"
            continue
        fi
        echo "🗑️  Removing $name..."
        case "$GOTOOLS_STRATEGY" in
            unified)
                local modfile="$GOTOOLS_DIR/go.mod"
                if [[ -f "$modfile" ]]; then
                    local pkg
                    if pkg=$(pkg_for_tool "$name"); then
                        (cd "$GOTOOLS_DIR" && go mod edit -drop-tool="$pkg" && go mod tidy)
                        echo "  ✅ Dropped $name from $modfile"
                    else
                        echo "  ⚠️  Tool $name not found in $modfile"
                    fi
                fi
                ;;

            split)
                if [[ -f "$GOTOOLS_DIR/$name.mod" ]]; then
                    rm -f "$GOTOOLS_DIR/$name.mod" "$GOTOOLS_DIR/$name.sum"
                    echo "  ✅ Removed $name.mod / $name.sum"
                else
                    echo "  ⚠️  $name.mod not found."
                fi
                ;;

            module)
                if [[ -d "$GOTOOLS_DIR/$name" ]]; then
                    rm -rf "${GOTOOLS_DIR:?}/${name:?}"
                    echo "  ✅ Removed $GOTOOLS_DIR/$name/"
                else
                    echo "  ⚠️  $GOTOOLS_DIR/$name/ not found."
                fi
                ;;

            *)
                echo "❌ Unknown strategy: $GOTOOLS_STRATEGY" >&2
                exit 1
                ;;
        esac

        # Remove from manifest.
        _manifest_tool_remove "$name"
    done

    _manifest_flush
}

# ---- migrate -------------------------------------------------------------
cmd_migrate() {
    _require_go
    _acquire_lock
    _DRY_RUN=false
    for a in "$@"; do [[ "$a" == "--dry-run" ]] && _DRY_RUN=true; done
    if [[ $# -eq 0 ]]; then
        echo "❌ Usage: $(basename "$0") migrate <unified|split|module> [--dry-run]" >&2
        exit 1
    fi

    local target_strategy="$1"

    case "$target_strategy" in
        unified|split|module) ;;
        *) echo "❌ Invalid strategy: $target_strategy (must be unified, split, or module)" >&2; exit 1 ;;
    esac

    load_config

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

# ---- purge ---------------------------------------------------------------
cmd_purge() {
    _acquire_lock
    _DRY_RUN=false
    for a in "$@"; do [[ "$a" == "--dry-run" ]] && _DRY_RUN=true; done
    load_config

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

# ---- uninstall -----------------------------------------------------------
cmd_uninstall() {
    echo "⚠️  WARNING: This will delete the 'gotools.sh' script itself."
    printf "Do you want to uninstall gotools.sh? (y/N): "
    read -r confirmation

    if [[ "$confirmation" =~ ^[Yy]$ ]]; then
        local script_path
        script_path="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"
        rm -f "$script_path"
        echo "✅ gotools.sh has been uninstalled. Goodbye!"
        exit 0
    else
        echo "❌ Uninstall cancelled."
    fi
}

# ---- config --------------------------------------------------------------
cmd_config() {
    if [[ $# -eq 0 ]]; then
        # Show all config.
        if [[ ! -f "$MANIFEST_FILE" ]]; then
            echo "⚠️  No $MANIFEST_FILE found. Run 'init' first."
            return 1
        fi
        cat "$MANIFEST_FILE"
        return 0
    fi

    local key="$1"

    # Validate key name.
    case "$key" in
        GOTOOLS_STRATEGY|GOTOOLS_DIR|GOTOOLS_GO_VERSION|GOTOOLS_MODULE_PREFIX) ;;
        *) echo "❌ Unknown config key: $key" >&2
           echo "   Valid keys: GOTOOLS_STRATEGY, GOTOOLS_DIR, GOTOOLS_GO_VERSION, GOTOOLS_MODULE_PREFIX" >&2
           return 1 ;;
    esac

    if [[ $# -eq 1 ]]; then
        # Show single key.
        if [[ ! -f "$MANIFEST_FILE" ]]; then
            echo "⚠️  No $MANIFEST_FILE found. Run 'init' first."
            return 1
        fi
        local val
        val=$(_manifest_config_get "$key")
        if [[ -n "$val" ]]; then
            echo "$val"
        else
            echo "⚠️  $key is not set. (auto-detected from root go.mod when empty)"
        fi
        return 0
    fi

    local value="$2"

    # Validate value for strategy key.
    if [[ "$key" == "GOTOOLS_STRATEGY" ]]; then
        case "$value" in
            unified|split|module) ;;
            *) echo "❌ Invalid strategy: $value (must be unified, split, or module)" >&2; return 1 ;;
        esac
    fi

    if [[ ! -f "$MANIFEST_FILE" ]]; then
        # Create a new manifest with this one key set.
        GOTOOLS_STRATEGY="${DEFAULT_STRATEGY}"
        GOTOOLS_DIR="${DEFAULT_DIR}"
        GOTOOLS_GO_VERSION="${DEFAULT_GO_VERSION}"
        GOTOOLS_MODULE_PREFIX="${DEFAULT_MODULE_PREFIX}"
        _MANIFEST_TOOLS=""
        _manifest_config_set "$key" "$value"
        echo "✅ Created $MANIFEST_FILE with $key=$value"
        return 0
    fi

    _manifest_config_set "$key" "$value"
    echo "✅ Set $key=$value"

    # Warn if strategy was changed — sync is needed to migrate tools.
    if [[ "$key" == "GOTOOLS_STRATEGY" ]]; then
        echo "⚠️  Strategy changed. Run 'gotools.sh sync' to migrate the tools directory."
    fi
}

# ---- self-update ---------------------------------------------------------
cmd_self_update() {
    echo "🔍 Checking for updates..."
    local latest_tag
    latest_tag=$(curl -sL "$API_URL" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/') || true

    if [[ -z "$latest_tag" ]]; then
        echo "❌ Error: Could not fetch latest version from GitHub." >&2
        return 1
    fi

    if [[ "$latest_tag" == "$VERSION" ]]; then
        echo "✅ You are already on the latest version ($VERSION)."
        return 0
    fi

    echo "🚀 New version found: $latest_tag (Current: $VERSION)"

    # If $0 doesn't end in .sh, we're running inside the Go binary wrapper.
    # We can't overwrite a compiled binary with a shell script — use go install.
    if [[ "$0" != *.sh ]]; then
        echo "📥 Updating via go install..."
        go install "github.com/piusalfred/gotools/cmd/gotools@$latest_tag" || {
            echo "❌ Error: go install failed." >&2
            return 1
        }
        echo "✨ Successfully updated to $latest_tag!"
        return 0
    fi

    echo "📥 Downloading update..."

    local tmp_file
    tmp_file=$(mktemp)
    local tag_url="https://raw.githubusercontent.com/$REPO/$latest_tag/gotools.sh"

    if curl -sL "$tag_url" -o "$tmp_file"; then
        mv "$tmp_file" "$0"
        chmod +x "$0"
        echo "✨ Successfully updated to $latest_tag!"
    else
        echo "❌ Error: Update download failed." >&2
        rm -f "$tmp_file"
        return 1
    fi
}

# ---- test (signal / cancellation helper) ---------------------------------
cmd_test() {
    if [[ $# -eq 0 ]]; then
        echo "❌ Usage: $(basename "$0") test <seconds>" >&2
        exit 1
    fi

    local seconds="$1"

    # Validate that the argument is a positive number.
    if ! [[ "$seconds" =~ ^[0-9]+(\.[0-9]+)?$ ]] || [[ "$seconds" == "0" ]]; then
        echo "❌ Error: <seconds> must be a positive number, got '$seconds'" >&2
        exit 1
    fi

    # Trap SIGINT and SIGTERM so we can report the signal before exiting.
    trap 'echo ""; echo "⚡ Caught SIGINT (Ctrl-C) — exiting."; exit 130' INT
    trap 'echo ""; echo "⚡ Caught SIGTERM — exiting."; exit 143' TERM

    echo "⏳ Sleeping for ${seconds}s — press Ctrl-C to test signal handling..."

    local elapsed=0
    while (( $(echo "$elapsed < $seconds" | bc -l) )); do
        sleep 1 &
        wait $! 2>/dev/null || true  # wait on bg sleep so traps fire immediately
        elapsed=$(echo "$elapsed + 1" | bc -l)
        printf "\r  ⏱  %g / %s seconds" "$elapsed" "$seconds"
    done

    echo ""
    echo "✅ Finished — no interruption."
}

# ---------------------------------------------------------------------------
# Main dispatch — only runs when executed as a script, not when sourced.
# Uses the standard `return 2>/dev/null` trick: return succeeds only inside
# a sourced script (or function), fails at the top level of an executed script.
# This correctly handles direct execution, `bash -c` (Go wrapper), and source.
# ---------------------------------------------------------------------------
if ! (return 0 2>/dev/null); then
    [[ $# -lt 1 ]] && usage
    action="$1"
    shift

    # If the first argument after the command is --help or -h,
    # show per-command help instead of dispatching normally.
    if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
        _cmd_help "$action"
        exit 0
    fi

    case "$action" in
        init)                   cmd_init "$@" ;;
        install)                cmd_install "$@" ;;
        sync)                   cmd_sync "$@" ;;
        exec)                   cmd_exec "$@" ;;
        list)                   cmd_list "$@" ;;
        upgrade|update)         cmd_upgrade "$@" ;;
        remove)                 cmd_remove "$@" ;;
        info)                   cmd_info "$@" ;;
        migrate)                cmd_migrate "$@" ;;
        config)                 cmd_config "$@" ;;
        purge)                  cmd_purge "$@" ;;
        check)                  cmd_check ;;
        completion)             cmd_completion "$@" ;;
        version)                cmd_version ;;
        self-update|self-upgrade) cmd_self_update ;;
        uninstall)              cmd_uninstall ;;
        test)                   cmd_test "$@" ;;
        help|--help|-h)         usage ;;
        *)                      echo "❌ Unknown command: $action" >&2; usage ;;
    esac
fi
