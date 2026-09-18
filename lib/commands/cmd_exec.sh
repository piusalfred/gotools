# Copyright (c) 2026 Pius Alfred
# License: MIT

# ---- exec ----------------------------------------------------------------
# The tool runs in _INVOCATION_DIR — the directory the user ran gotools in —
# so `gotools exec <tool> <args>` puts the tool where `go tool <tool> <args>`
# would put it. That matters for every tool that resolves a relative path, a
# package pattern such as ./..., or a config file, and for `go:generate`,
# which runs each directive in the package's own source directory.
#
# The modfile that declares the tool is passed as an absolute -modfile path,
# which works from any directory inside the module and in all three
# strategies. `go tool -modfile=` refuses to run outside a main module, so
# when the invocation directory is not inside one, each strategy falls back
# to running the tool from its own module directory.
cmd_exec() {
    _require_go
    load_config
    local tool_name="${1:?tool name is required}"
    shift

    # The name is path-joined into modfile/dir paths below — reject
    # anything that could escape the tools directory.
    if ! _validate_tool_name "$tool_name"; then
        exit $E_USAGE
    fi

    # mod_file      — declares the tool, relative to the project root.
    # fallback_dir  — where to run the tool when -modfile cannot be used.
    # fallback_mod  — -modfile value for that fallback, empty when the
    #                 fallback directory holds the tool's own go.mod.
    local mod_file fallback_dir fallback_mod
    case "$GOTOOLS_STRATEGY" in
        unified)
            mod_file="$GOTOOLS_DIR/go.mod"
            fallback_dir="$GOTOOLS_DIR"
            fallback_mod=""
            if [[ ! -f "$mod_file" ]]; then
                _trace_fatal "No $mod_file found" "$tool_name"
                echo "❌ Error: No $mod_file found. Run 'init' first." >&2
                exit $E_TOOL_NOT_FOUND
            fi
            _reject_symlink "$mod_file" "exec $tool_name"
            ;;

        split)
            mod_file="$GOTOOLS_DIR/${tool_name}.mod"
            fallback_dir="."
            fallback_mod="$mod_file"
            if [[ ! -f "$mod_file" ]]; then
                _trace_fatal "Tool '$tool_name' not found ($mod_file missing)" "$tool_name"
                echo "❌ Error: Tool '$tool_name' not found ($mod_file missing). Run 'install' first." >&2
                exit $E_TOOL_NOT_FOUND
            fi
            _reject_symlink "$mod_file" "exec $tool_name"
            ;;

        module)
            mod_file="$GOTOOLS_DIR/$tool_name/go.mod"
            fallback_dir="$GOTOOLS_DIR/$tool_name"
            fallback_mod=""
            if [[ ! -d "$GOTOOLS_DIR/$tool_name" ]]; then
                _trace_fatal "Tool '$tool_name' not found ($GOTOOLS_DIR/$tool_name missing)" "$tool_name"
                echo "❌ Error: Tool '$tool_name' not found ($GOTOOLS_DIR/$tool_name missing). Run 'install' first." >&2
                exit $E_TOOL_NOT_FOUND
            fi
            _reject_symlink "$GOTOOLS_DIR/$tool_name" "exec $tool_name"
            ;;

        *)
            _trace_fatal "Unknown strategy: $GOTOOLS_STRATEGY" "$tool_name"
            echo "❌ Unknown strategy: $GOTOOLS_STRATEGY" >&2
            exit $E_ENVIRONMENT
            ;;
    esac

    local binary
    binary=$(resolve_binary_name "$tool_name" "$mod_file")

    local _ec=0
    if _dir_in_module "$_INVOCATION_DIR"; then
        (cd "$_INVOCATION_DIR" && go tool -modfile="$_PROJECT_ROOT/$mod_file" "$binary" "$@") || _ec=$?
    elif [[ -n "$fallback_mod" ]]; then
        (cd "$fallback_dir" && go tool -modfile="$fallback_mod" "$binary" "$@") || _ec=$?
    else
        (cd "$fallback_dir" && go tool "$binary" "$@") || _ec=$?
    fi

    _trace_exec "$binary" "$_ec" "$tool_name" "$@"
    exit $_ec
}
