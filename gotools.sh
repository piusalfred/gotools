#!/usr/bin/env bash
# Copyright (c) 2026 Pius Alfred
# License: MIT

set -euo pipefail


VERSION="v0.1.10"
REPO="piusalfred/gotools.sh"
API_URL="https://api.github.com/repos/$REPO/releases/latest"

ENV_FILE=".gotools.env"
DEFAULT_STRATEGY="workspace"
DEFAULT_DIR="tools"
DEFAULT_GO_VERSION="inherit"
DEFAULT_USE_WORK="true"


usage() {
    cat <<EOF
🧰 Go Tool Manager (Version: $VERSION)

Usage: $(basename "$0") <command> [arguments]

Commands:
  init [flags]          Bootstrap the project.
  install [name] <pkg>  Install a new tool.
  sync                  Force state to match .gotools.env.
  exec <name> [args]    Run a managed tool.
  list                  List tools, versions, and strategies.
  upgrade <name|all>    Update tools to @latest.
  remove <name1>...     Remove tools.
  version               Show script version.
  self-update           Update gotools.sh to the latest version.

Examples:
  ./gotools.sh self-update
  ./gotools.sh version
EOF
    exit 1
}

load_config() {
    if [[ -f "$ENV_FILE" ]]; then
        # shellcheck source=/dev/null
        source "$ENV_FILE"
    fi
    GOTOOLS_STRATEGY="${GOTOOLS_STRATEGY:-$DEFAULT_STRATEGY}"
    GOTOOLS_DIR="${GOTOOLS_DIR:-$DEFAULT_DIR}"
    GOTOOLS_GO_VERSION="${GOTOOLS_GO_VERSION:-$DEFAULT_GO_VERSION}"
    GOTOOLS_USE_WORK="${GOTOOLS_USE_WORK:-$DEFAULT_USE_WORK}"
}


cmd_version() {
    echo "gotools.sh $VERSION"
}

cmd_self_update() {
    echo "🔍 Checking for updates..."

    # Fetch latest tag from GitHub API without requiring jq
    local latest_tag;
    latest_tag=$(curl -s "$API_URL" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

    if [[ -z "$latest_tag" ]]; then
        echo "❌ Error: Could not fetch latest version from GitHub."
        return 1
    fi

    if [[ "$latest_tag" == "$VERSION" ]]; then
        echo "✅ You are already on the latest version ($VERSION)."
        return 0
    fi

    echo "🚀 New version found: $latest_tag (Current: $VERSION)"
    echo "📥 Downloading update..."

    # Use a temporary file to ensure we don't break the current script if download fails
    local tmp_file; tmp_file=$(mktemp)
    # We download from the specific tag to ensure we get that release version
    local tag_url="https://raw.githubusercontent.com/$REPO/$latest_tag/gotools.sh"

    if curl -sL "$tag_url" -o "$tmp_file"; then
        # Replace the current script ($0) with the new one
        mv "$tmp_file" "$0"
        chmod +x "$0"
        echo "✨ Successfully updated to $latest_tag!"
    else
        echo "❌ Error: Update download failed."
        rm -f "$tmp_file"
        return 1
    fi
}


resolve_go_version() {
    if [[ "$GOTOOLS_GO_VERSION" != "inherit" ]]; then
        echo "$GOTOOLS_GO_VERSION"
        return
    fi
    local root_mod="go.mod"
    if [[ -f "$root_mod" ]]; then
        local v; v=$(awk '$1 == "go" { print $2; exit }' "$root_mod")
        [[ -n "$v" ]] && { echo "$v"; return; }
    fi
    go env GOVERSION | sed 's/go//' | awk -F. '{ print $1"."$2 }'
}

extract_pkg_from_mod() {
    awk '$1 == "tool" { if ($3 == "") print $2; else print $3; exit }' "$1"
}

cmd_init() {
    local strategy=$DEFAULT_STRATEGY dir=$DEFAULT_DIR go_v=$DEFAULT_GO_VERSION work=$DEFAULT_USE_WORK
    for arg in "$@"; do
        case $arg in
            --strategy=*) strategy="${arg#*=}" ;;
            --dir=*)      dir="${arg#*=}" ;;
            --go=*)       go_v="${arg#*=}" ;;
            --work=*)     work="${arg#*=}" ;;
            *)            echo "❓ Unknown flag: $arg"; usage ;;
        esac
    done

    cat > "$ENV_FILE" <<EOF
GOTOOLS_STRATEGY=$strategy
GOTOOLS_DIR=$dir
GOTOOLS_GO_VERSION=$go_v
GOTOOLS_USE_WORK=$work
EOF
    mkdir -p "$dir"
    echo "✅ Initialized $ENV_FILE"
    cmd_sync
}

cmd_sync() {
    load_config
    local target_v; target_v=$(resolve_go_version)
    echo "🔄 Syncing (Strategy: $GOTOOLS_STRATEGY, Go: $target_v)..."

    if [[ "$GOTOOLS_STRATEGY" == "workspace" ]]; then
        [[ ! -f "$GOTOOLS_DIR/go.mod" ]] && (cd "$GOTOOLS_DIR" && go mod init "tools")
        (cd "$GOTOOLS_DIR" && go mod edit -go="$target_v" && go mod tidy)
        [[ "$GOTOOLS_USE_WORK" == "true" ]] && { [[ ! -f "go.work" ]] && go work init; go work use "$GOTOOLS_DIR"; }
    else
        for f in "$GOTOOLS_DIR"/*.mod; do
            [[ -f "$f" ]] || continue
            (cd "$GOTOOLS_DIR" && go mod edit -go="$target_v" -modfile="$(basename "$f")" && go mod download -modfile="$(basename "$f")")
        done
    fi
}

cmd_install() {
    load_config
    local name="" pkg=""
    [[ $# -eq 1 ]] && { pkg="$1"; name=$(basename "$pkg"); } || { name="$1"; pkg="$2"; }

    if [[ "$GOTOOLS_STRATEGY" == "isolated" ]]; then
        local modfile="${name}.mod"
        if [[ ! -f "$GOTOOLS_DIR/$modfile" ]]; then
            echo -e "module tools/$name\n\ngo $(resolve_go_version)" > "$GOTOOLS_DIR/$modfile"
        fi
        (cd "$GOTOOLS_DIR" && go get -tool -modfile="$modfile" "$pkg")
    else
        (cd "$GOTOOLS_DIR" && go get -tool "$pkg")
    fi
    echo "✅ Installed $name"
}

cmd_remove() {
    load_config
    for name in "$@"; do
        if [[ "$GOTOOLS_STRATEGY" == "isolated" ]]; then
            rm -f "$GOTOOLS_DIR/$name.mod" "$GOTOOLS_DIR/$name.sum"
            echo "🗑️  Removed $name.mod"
        else
            local pkg; pkg=$(awk -v t="$name" '$1 == "tool" && $2 ~ t {print $2}' "$GOTOOLS_DIR/go.mod")
            if [[ -n "$pkg" ]]; then
                (cd "$GOTOOLS_DIR" && go mod edit -drop-tool="$pkg" && go mod tidy)
                echo "🗑️  Dropped $name from go.mod"
            fi
        fi
    done
}

cmd_upgrade() {
    load_config
    local targets=()
    if [[ "${1:-}" == "all" ]]; then
        if [[ "$GOTOOLS_STRATEGY" == "isolated" ]]; then
            for f in "$GOTOOLS_DIR"/*.mod; do [[ -f "$f" ]] && targets+=("$(basename "$f" .mod)"); done
        else
            mapfile -t targets < <(awk '$1 == "tool" {print $2}' "$GOTOOLS_DIR/go.mod")
        fi
    else
        targets=("$@")
    fi

    for t in "${targets[@]}"; do
        echo "🚀 Upgrading $t..."
        if [[ "$GOTOOLS_STRATEGY" == "isolated" ]]; then
            local pkg; pkg=$(extract_pkg_from_mod "$GOTOOLS_DIR/$t.mod")
            (cd "$GOTOOLS_DIR" && go get -tool -modfile="$t.mod" "${pkg}@latest")
        else
            (cd "$GOTOOLS_DIR" && go get -tool "${t%@*}@latest")
        fi
    done
}

cmd_list() {
    load_config
    printf "  %-20s %-10s %-15s\n" "TOOL" "STRATEGY" "PACKAGE"
    printf "  %-20s %-10s %-15s\n" "----" "--------" "-------"
    if [[ "$GOTOOLS_STRATEGY" == "workspace" && -f "$GOTOOLS_DIR/go.mod" ]]; then
        while read -r p; do printf "  %-20s %-10s %-15s\n" "$(basename "$p")" "workspace" "$p"; done < <(awk '$1 == "tool" {print $2}' "$GOTOOLS_DIR/go.mod")
    else
        for f in "$GOTOOLS_DIR"/*.mod; do
            [[ -f "$f" ]] || continue
            printf "  %-20s %-10s %-15s\n" "$(basename "$f" .mod)" "isolated" "$(extract_pkg_from_mod "$f")"
        done
    fi
}

cmd_exec() {
    load_config
    local tool_name="${1:?tool-name is required}"
    shift

    if [[ "$GOTOOLS_STRATEGY" == "isolated" ]]; then
        local mod_file="${GOTOOLS_DIR}/${tool_name}.mod"
        [[ ! -f "$mod_file" ]] && { echo "❌ Error: Tool '${tool_name}' not found. Run 'install' first." >&2; exit 1; }
        exec go tool -modfile="$mod_file" "$tool_name" "$@"
    else
        [[ ! -f "$GOTOOLS_DIR/go.mod" ]] && { echo "❌ Error: No tools go.mod found. Run 'init' first." >&2; exit 1; }
        (cd "$GOTOOLS_DIR" && exec go tool "$tool_name" "$@")
    fi
}

[[ $# -lt 1 ]] && usage
action="$1"; shift

case "$action" in
    init) cmd_init "$@" ;;
    install) cmd_install "$@" ;;
    sync) cmd_sync ;;
    exec) cmd_exec "$@" ;;
    list) cmd_list ;;
    remove) cmd_remove "$@" ;;
    upgrade|update) cmd_upgrade "$@" ;;
    version) cmd_version ;;
    self-update|self-upgrade) cmd_self_update ;;
    *) usage ;;
esac
