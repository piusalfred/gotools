# Copyright (c) 2026 Pius Alfred
# License: MIT


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

# tool_runnable <tool-name>
#   Probes whether `go tool <binary>` can run the tool under the current
#   strategy, mirroring the invocation forms cmd_exec uses. Prints a one-line
#   reason to stdout when NOT runnable. rc 0 runnable, 1 not runnable.
#
#   "Not runnable" means a GO-level failure: go missing, modfile missing, or
#   stderr matching a go error (^go( tool)?:, "no such tool", "is not a
#   tool", "no go.mod file found"). A tool that starts and then exits
#   non-zero on empty stdin (its own usage error, e.g. staticcheck) IS
#   runnable — the go toolchain found and launched it.
#
#   Read-only: stdin is /dev/null, GOTOOLCHAIN=local always (no toolchain
#   auto-download), plus GOPROXY=off in offline mode so a not-yet-downloaded
#   tool fails fast instead of touching the network.
tool_runnable() {
    local tool_name="$1"
    command -v go >/dev/null 2>&1 || { echo "go not installed"; return 1; }
    # The probe cds into "$GOTOOLS_DIR/$tool_name" for the module strategy —
    # an unsafe name would escape the project. Report, never follow.
    _validate_tool_name "$tool_name" 2>/dev/null || { echo "invalid tool name: $tool_name"; return 1; }
    local modfile binary out rc=0
    case "$GOTOOLS_STRATEGY" in
        unified)
            modfile="$GOTOOLS_DIR/go.mod"
            [[ -f "$modfile" ]] || { echo "missing $modfile"; return 1; }
            binary=$(resolve_binary_name "$tool_name" "$modfile")
            # GOTOOLCHAIN=local: never auto-download a toolchain during
            # diagnostics. GOPROXY=off in offline mode: fail fast instead of
            # touching the network. Exported inside the subshell — bash 3.2
            # cannot apply array-expanded "VAR=val" words before a function
            # call ("command not found").
            out=$( (export GOTOOLCHAIN=local; $_OFFLINE && export GOPROXY=off; cd "$GOTOOLS_DIR" && _go tool "$binary" </dev/null) 2>&1 ) || rc=$?
            ;;
        split)
            modfile="$GOTOOLS_DIR/${tool_name}.mod"
            [[ -f "$modfile" ]] || { echo "missing $modfile"; return 1; }
            binary=$(resolve_binary_name "$tool_name" "$modfile")
            out=$( (export GOTOOLCHAIN=local; $_OFFLINE && export GOPROXY=off; _go tool -modfile="$modfile" "$binary" </dev/null) 2>&1 ) || rc=$?
            ;;
        module)
            modfile="$GOTOOLS_DIR/$tool_name/go.mod"
            [[ -f "$modfile" ]] || { echo "missing $modfile"; return 1; }
            binary=$(resolve_binary_name "$tool_name" "$modfile")
            out=$( (export GOTOOLCHAIN=local; $_OFFLINE && export GOPROXY=off; cd "$GOTOOLS_DIR/$tool_name" && _go tool "$binary" </dev/null) 2>&1 ) || rc=$?
            ;;
        *)
            echo "unknown strategy: $GOTOOLS_STRATEGY"
            return 1
            ;;
    esac
    [[ $rc -eq 0 ]] && return 0
    # Non-zero: only a go-level failure makes it NOT runnable. The verbosity
    # echo ("↳ go ...") goes to stderr first; it never matches this pattern.
    local reason
    reason=$(grep -E '^go( tool)?:|no such tool|is not a tool|no go\.mod file found' <<< "$out" | head -n1 || true)
    if [[ -n "$reason" ]]; then
        echo "$reason"
        return 1
    fi
    return 0
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
