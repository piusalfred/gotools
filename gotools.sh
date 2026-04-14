#!/usr/bin/env bash
# Copyright (c) 2026 Pius Alfred
# License: MIT

set -euo pipefail

VERSION="v0.3.0"
REPO="piusalfred/gotools.sh"
API_URL="https://api.github.com/repos/$REPO/releases/latest"

ENV_FILE=".gotools.env"
DEFAULT_STRATEGY="split"
DEFAULT_DIR="tools"
DEFAULT_GO_VERSION="inherit"
DEFAULT_MODULE_PREFIX=""

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
  sync                    Force state to match .gotools.env (sync Go version, tidy, etc.).
  exec <name> [args]      Run a managed tool.
  list                    List tools, versions, and strategies.
  upgrade <name|all>      Update tools to @latest.
  remove <name1> ...      Remove specific tools.
  migrate <strategy>      Migrate all tools to a different strategy.
  config [key [value]]    View or edit .gotools.env configuration.
                            No args: show all config.
                            One arg: show value of <key>.
                            Two args: set <key>=<value>.
  purge                   Remove all tools and the .gotools.env file.
  info <name>             Show detailed information about a specific tool.
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
# Config helpers
# ---------------------------------------------------------------------------
_CONFIG_LOADED=false

load_config() {
    if [[ "$_CONFIG_LOADED" == "true" ]]; then
        return
    fi

    if [[ -f "$ENV_FILE" ]]; then
        # shellcheck source=/dev/null
        source "$ENV_FILE"
    fi

    # Startup env overrides > config file > defaults.
    GOTOOLS_STRATEGY="${_ORIG_ENV_STRATEGY:-${GOTOOLS_STRATEGY:-$DEFAULT_STRATEGY}}"
    GOTOOLS_DIR="${_ORIG_ENV_DIR:-${GOTOOLS_DIR:-$DEFAULT_DIR}}"
    GOTOOLS_GO_VERSION="${_ORIG_ENV_GO_VERSION:-${GOTOOLS_GO_VERSION:-$DEFAULT_GO_VERSION}}"
    GOTOOLS_MODULE_PREFIX="${_ORIG_ENV_MODULE_PREFIX:-${GOTOOLS_MODULE_PREFIX:-$DEFAULT_MODULE_PREFIX}}"
    _CONFIG_LOADED=true
}

reload_config() {
    _CONFIG_LOADED=false
    load_config
}

# detect_strategy <dir>
#   Inspects the tools directory on disk and returns which strategy it
#   actually matches: unified, split, module, or empty (unknown).
detect_strategy() {
    local dir="${1:-$GOTOOLS_DIR}"
    [[ -d "$dir" ]] || return 0

    # unified: single go.mod at the tools root with tool directives
    if [[ -f "$dir/go.mod" ]] && grep -q '^tool ' "$dir/go.mod" 2>/dev/null; then
        echo "unified"
        return
    fi

    # module: subdirectories each containing go.mod
    local has_subdirs=false
    for d in "$dir"/*/; do
        if [[ -d "$d" && -f "$d/go.mod" ]]; then
            has_subdirs=true
            break
        fi
    done
    if [[ "$has_subdirs" == "true" ]]; then
        echo "module"
        return
    fi

    # split: flat *.mod files (not go.mod)
    for f in "$dir"/*.mod; do
        if [[ -f "$f" && "$(basename "$f")" != "go.mod" ]]; then
            echo "split"
            return
        fi
    done
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
#     1. GOTOOLS_MODULE_PREFIX from .gotools.env (if non-empty)
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

# resolve_binary_name <tool_name> <modfile>
#   Dereferences a tool alias to the actual Go binary name.
#   Reads the tool directive from the modfile and returns basename of the
#   package path. Falls back to the tool_name itself if no directive is found.
#
#   Example: tool alias "golangci" with tool directive
#     github.com/golangci/golangci-lint/v2/cmd/golangci-lint
#   resolves to binary name "golangci-lint".
resolve_binary_name() {
    local tool_name="$1" modfile="$2"
    local pkg
    pkg=$(extract_pkg_from_mod "$modfile")
    if [[ -n "$pkg" ]]; then
        basename "$pkg"
    else
        echo "$tool_name"
    fi
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
                name=$(basename "$pkg")
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
# ---------------------------------------------------------------------------
# Commands
# ---------------------------------------------------------------------------

cmd_version() {
    echo "gotools.sh $VERSION"
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

    cat > "$ENV_FILE" <<EOF
GOTOOLS_STRATEGY=$strategy
GOTOOLS_DIR=$dir
GOTOOLS_GO_VERSION=$go_v
GOTOOLS_MODULE_PREFIX=$prefix
EOF

    mkdir -p "$dir"
    echo "✅ Initialized $ENV_FILE (strategy=$strategy, dir=$dir)"

    # Reload so cmd_sync picks up the new values.
    load_config
    cmd_sync
}

# ---- sync ----------------------------------------------------------------
cmd_sync() {
    load_config
    local target_v
    target_v=$(resolve_go_version)

    local disk_strategy
    disk_strategy=$(detect_strategy "$GOTOOLS_DIR")

    if [[ -n "$disk_strategy" && "$disk_strategy" != "$GOTOOLS_STRATEGY" ]]; then
        echo "⚠️  Strategy mismatch: .gotools.env says '$GOTOOLS_STRATEGY' but $GOTOOLS_DIR/ looks like '$disk_strategy'."
        echo "🔀 Auto-migrating to '$GOTOOLS_STRATEGY'..."
        cmd_migrate "$GOTOOLS_STRATEGY"
        return
    fi

    echo "🔄 Syncing (strategy=$GOTOOLS_STRATEGY, go=$target_v, dir=$GOTOOLS_DIR)..."

    case "$GOTOOLS_STRATEGY" in
        unified)
            mkdir -p "$GOTOOLS_DIR"
            if [[ ! -f "$GOTOOLS_DIR/go.mod" ]]; then
                (cd "$GOTOOLS_DIR" && go mod init "$(tool_module_path)")
            fi
            (cd "$GOTOOLS_DIR" && go mod edit -go="$target_v" && go mod tidy)
            ;;

        split)
            mkdir -p "$GOTOOLS_DIR"
            for f in "$GOTOOLS_DIR"/*.mod; do
                [[ -f "$f" ]] || continue
                local base
                base=$(basename "$f")
                echo "  ↻ $base"
                (cd "$GOTOOLS_DIR" && go mod edit -go="$target_v" -modfile="$base" && go mod download -modfile="$base")
            done
            ;;

        module)
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
            ;;

        *)
            echo "❌ Unknown strategy: $GOTOOLS_STRATEGY" >&2
            exit 1
            ;;
    esac

    echo "✅ Sync complete."
}

# ---- install -------------------------------------------------------------
cmd_install() {
    load_config
    local name="" pkg=""

    if [[ $# -eq 0 ]]; then
        echo "❌ Usage: $(basename "$0") install [name] <pkg>" >&2
        exit 1
    elif [[ $# -eq 1 ]]; then
        pkg="$1"
        # Strip @version suffix to get the base package path, then take basename.
        name=$(basename "${pkg%%@*}")
    else
        name="$1"
        pkg="$2"
    fi

    local target_v
    target_v=$(resolve_go_version)

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

    echo "✅ Installed $name"
}

# ---- exec ----------------------------------------------------------------
cmd_exec() {
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
    printf "  %-18s %-10s %-8s %-30s %s\n" "TOOL" "STRATEGY" "GO" "MODFILE" "PACKAGE@VERSION"
    printf "  %-18s %-10s %-8s %-30s %s\n" "----" "--------" "--" "-------" "---------------"

    case "$GOTOOLS_STRATEGY" in
        unified)
            local modfile="$GOTOOLS_DIR/go.mod"
            if [[ -f "$modfile" ]]; then
                local go_ver rel_mod
                go_ver=$(extract_go_version_from_mod "$modfile")
                rel_mod=$(relative_path "$modfile")
                while IFS= read -r pkg; do
                    [[ -z "$pkg" ]] && continue
                    local name ver
                    name=$(basename "$pkg")
                    ver=$(extract_version_for_pkg "$modfile" "$pkg")
                    printf "  %-18s %-10s %-8s %-30s %s\n" "$name" "unified" "${go_ver:-?}" "$rel_mod" "${pkg}@${ver:-unknown}"
                done < <(extract_tools_from_mod "$modfile")
            fi
            ;;

        split)
            for f in "$GOTOOLS_DIR"/*.mod; do
                [[ -f "$f" ]] || continue
                local name pkg ver go_ver rel_mod
                name=$(basename "$f" .mod)
                pkg=$(extract_pkg_from_mod "$f")
                [[ -z "$pkg" ]] && continue
                ver=$(extract_version_for_pkg "$f" "$pkg")
                go_ver=$(extract_go_version_from_mod "$f")
                rel_mod=$(relative_path "$f")
                printf "  %-18s %-10s %-8s %-30s %s\n" "$name" "split" "${go_ver:-?}" "$rel_mod" "${pkg}@${ver:-unknown}"
            done
            ;;

        module)
            for d in "$GOTOOLS_DIR"/*/; do
                [[ -d "$d" ]] || continue
                local modfile="$d/go.mod"
                [[ -f "$modfile" ]] || continue
                local name pkg ver go_ver rel_mod
                name=$(basename "$d")
                pkg=$(extract_pkg_from_mod "$modfile")
                [[ -z "$pkg" ]] && continue
                ver=$(extract_version_for_pkg "$modfile" "$pkg")
                go_ver=$(extract_go_version_from_mod "$modfile")
                rel_mod=$(relative_path "$modfile")
                printf "  %-18s %-10s %-8s %-30s %s\n" "$name" "module" "${go_ver:-?}" "$rel_mod" "${pkg}@${ver:-unknown}"
            done
            ;;

        *)
            echo "❌ Unknown strategy: $GOTOOLS_STRATEGY" >&2
            exit 1
            ;;
    esac
}

# ---- info ----------------------------------------------------------------
cmd_info() {
    load_config
    if [[ $# -eq 0 ]]; then
        echo "❌ Usage: $(basename "$0") info <tool-name>" >&2
        exit 1
    fi

    local tool_name="$1"
    local modfile="" pkg="" ver="" go_ver="" strategy="$GOTOOLS_STRATEGY"

    case "$GOTOOLS_STRATEGY" in
        unified)
            modfile="$GOTOOLS_DIR/go.mod"
            if [[ ! -f "$modfile" ]]; then
                echo "❌ No $modfile found. Run 'init' first." >&2
                exit 1
            fi
            # Find the package whose basename matches the tool name.
            pkg=$(extract_tools_from_mod "$modfile" | while IFS= read -r p; do
                if [[ "$(basename "$p")" == "$tool_name" ]]; then
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

    local rel_mod
    rel_mod=$(relative_path "$modfile")

    echo ""
    echo "  Tool:       $tool_name"
    echo "  Package:    ${pkg:-unknown}"
    echo "  Version:    ${ver:-unknown}"
    echo "  Go:         ${go_ver:-unknown}"
    echo "  Strategy:   $strategy"
    echo "  Modfile:    $rel_mod"
    echo ""
}

# ---- upgrade -------------------------------------------------------------
cmd_upgrade() {
    load_config
    if [[ $# -eq 0 ]]; then
        echo "❌ Usage: $(basename "$0") upgrade <name|all>" >&2
        exit 1
    fi

    local targets=()

    if [[ "$1" == "all" ]]; then
        case "$GOTOOLS_STRATEGY" in
            unified)
                local modfile="$GOTOOLS_DIR/go.mod"
                [[ -f "$modfile" ]] && mapfile -t targets < <(extract_tools_from_mod "$modfile")
                ;;
            split)
                for f in "$GOTOOLS_DIR"/*.mod; do
                    [[ -f "$f" ]] && targets+=("$(basename "$f" .mod)")
                done
                ;;
            module)
                for d in "$GOTOOLS_DIR"/*/; do
                    [[ -d "$d" ]] && targets+=("$(basename "$d")")
                done
                ;;
        esac
    else
        targets=("$@")
    fi

    if [[ ${#targets[@]} -eq 0 ]]; then
        echo "⚠️  No tools found to upgrade."
        return 0
    fi

    for t in "${targets[@]}"; do
        echo "🚀 Upgrading $t..."
        case "$GOTOOLS_STRATEGY" in
            unified)
                # $t here is the full package path from the tool directive.
                (cd "$GOTOOLS_DIR" && go get -tool "${t}@latest")
                ;;
            split)
                local pkg
                pkg=$(extract_pkg_from_mod "$GOTOOLS_DIR/$t.mod")
                if [[ -n "$pkg" ]]; then
                    (cd "$GOTOOLS_DIR" && go get -tool -modfile="$t.mod" "${pkg}@latest")
                else
                    echo "  ⚠️  Could not determine package for $t, skipping."
                fi
                ;;
            module)
                local pkg
                pkg=$(extract_pkg_from_mod "$GOTOOLS_DIR/$t/go.mod")
                if [[ -n "$pkg" ]]; then
                    (cd "$GOTOOLS_DIR/$t" && go get -tool "${pkg}@latest")
                else
                    echo "  ⚠️  Could not determine package for $t, skipping."
                fi
                ;;
        esac
    done

    echo "✅ Upgrade complete."
}

# ---- remove --------------------------------------------------------------
cmd_remove() {
    load_config
    if [[ $# -eq 0 ]]; then
        echo "❌ Usage: $(basename "$0") remove <name1> [name2] ..." >&2
        exit 1
    fi

    for name in "$@"; do
        echo "🗑️  Removing $name..."
        case "$GOTOOLS_STRATEGY" in
            unified)
                local modfile="$GOTOOLS_DIR/go.mod"
                if [[ -f "$modfile" ]]; then
                    # Find the full pkg path matching the tool name.
                    local pkg
                    pkg=$(extract_tools_from_mod "$modfile" | grep "/${name}\$" || true)
                    if [[ -z "$pkg" ]]; then
                        # Try exact match (the tool directive might just be the name).
                        pkg=$(extract_tools_from_mod "$modfile" | grep -x "$name" || true)
                    fi
                    if [[ -n "$pkg" ]]; then
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
    done
}

# ---- migrate -------------------------------------------------------------
cmd_migrate() {
    if [[ $# -eq 0 ]]; then
        echo "❌ Usage: $(basename "$0") migrate <unified|split|module>" >&2
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

    # 2. Capture the Go version.
    local go_ver
    go_ver=$(resolve_go_version)

    # 3. Wipe old tools directory structure.
    echo "🧹 Cleaning old tools directory ($GOTOOLS_DIR)..."

    rm -rf "${GOTOOLS_DIR:?}"
    mkdir -p "$GOTOOLS_DIR"

    # 4. Update .gotools.env and force the live variable so that
    #    subsequent load_config / cmd_install calls in this process
    #    use the target strategy (not the old one).
    sed -i.bak "s/^GOTOOLS_STRATEGY=.*/GOTOOLS_STRATEGY=$target_strategy/" "$ENV_FILE"
    rm -f "$ENV_FILE.bak"

    # Force the live state to the target so cmd_install uses it.
    GOTOOLS_STRATEGY="$target_strategy"
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
    load_config
    echo "⚠️  WARNING: This will delete the tools directory ('$GOTOOLS_DIR') and '$ENV_FILE'."
    echo "This action cannot be undone."
    printf "Are you sure you want to proceed? (type YES to confirm): "
    read -r confirmation

    if [[ "$confirmation" != "YES" ]]; then
        echo "❌ Purge cancelled."
        return 0
    fi

    rm -rf "${GOTOOLS_DIR:?}" "${ENV_FILE:?}"
    echo "✅ Purge complete. All tools and configurations have been removed."
}

# ---- uninstall -----------------------------------------------------------
cmd_uninstall() {
    echo "⚠️  WARNING: This will delete the 'gotools.sh' script itself."
    printf "Do you want to uninstall gotools.sh? (y/N): "
    read -r confirmation

    if [[ "$confirmation" =~ ^[Yy]$ ]]; then
        local script_path
        script_path=$(realpath "$0")
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
        if [[ ! -f "$ENV_FILE" ]]; then
            echo "⚠️  No $ENV_FILE found. Run 'init' first."
            return 1
        fi
        cat "$ENV_FILE"
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
        if [[ ! -f "$ENV_FILE" ]]; then
            echo "⚠️  No $ENV_FILE found. Run 'init' first."
            return 1
        fi
        local val
        val=$(grep "^${key}=" "$ENV_FILE" 2>/dev/null | cut -d'=' -f2- || true)
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

    if [[ ! -f "$ENV_FILE" ]]; then
        echo "$key=$value" > "$ENV_FILE"
        echo "✅ Created $ENV_FILE with $key=$value"
        return 0
    fi

    if grep -q "^${key}=" "$ENV_FILE"; then
        sed -i.bak "s|^${key}=.*|${key}=${value}|" "$ENV_FILE"
        rm -f "$ENV_FILE.bak"
    else
        echo "$key=$value" >> "$ENV_FILE"
    fi
    echo "✅ Set $key=$value"
}

# ---- self-update ---------------------------------------------------------
cmd_self_update() {
    echo "🔍 Checking for updates..."
    local latest_tag
    latest_tag=$(curl -s "$API_URL" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

    if [[ -z "$latest_tag" ]]; then
        echo "❌ Error: Could not fetch latest version from GitHub." >&2
        return 1
    fi

    if [[ "$latest_tag" == "$VERSION" ]]; then
        echo "✅ You are already on the latest version ($VERSION)."
        return 0
    fi

    echo "🚀 New version found: $latest_tag (Current: $VERSION)"
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
# Main dispatch
# ---------------------------------------------------------------------------
[[ $# -lt 1 ]] && usage
action="$1"
shift

case "$action" in
    init)                   cmd_init "$@" ;;
    install)                cmd_install "$@" ;;
    sync)                   cmd_sync ;;
    exec)                   cmd_exec "$@" ;;
    list)                   cmd_list ;;
    upgrade|update)         cmd_upgrade "$@" ;;
    remove)                 cmd_remove "$@" ;;
    info)                   cmd_info "$@" ;;
    migrate)                cmd_migrate "$@" ;;
    config)                 cmd_config "$@" ;;
    purge)                  cmd_purge ;;
    version)                cmd_version ;;
    self-update|self-upgrade) cmd_self_update ;;
    uninstall)              cmd_uninstall ;;
    test)                   cmd_test "$@" ;;
    help|--help|-h)         usage ;;
    *)                      echo "❌ Unknown command: $action" >&2; usage ;;
esac
