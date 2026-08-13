# Copyright (c) 2026 Pius Alfred
# License: MIT

# ---- exec ----------------------------------------------------------------
cmd_exec() {
    _require_go
    load_config
    local tool_name="${1:?tool name is required}"
    shift

    case "$GOTOOLS_STRATEGY" in
        unified)
            if [[ ! -f "$GOTOOLS_DIR/go.mod" ]]; then
                _trace_fatal "No $GOTOOLS_DIR/go.mod found" "$tool_name"
                echo "❌ Error: No $GOTOOLS_DIR/go.mod found. Run 'init' first." >&2
                exit $E_TOOL_NOT_FOUND
            fi
            local binary
            binary=$(resolve_binary_name "$tool_name" "$GOTOOLS_DIR/go.mod")
            local _ec=0
            (cd "$GOTOOLS_DIR" && go tool "$binary" "$@") || _ec=$?
            _trace_exec "$binary" "$_ec" "$tool_name" "$@"
            exit $_ec
            ;;

        split)
            local mod_file="$GOTOOLS_DIR/${tool_name}.mod"
            if [[ ! -f "$mod_file" ]]; then
                _trace_fatal "Tool '$tool_name' not found ($mod_file missing)" "$tool_name"
                echo "❌ Error: Tool '$tool_name' not found ($mod_file missing). Run 'install' first." >&2
                exit $E_TOOL_NOT_FOUND
            fi
            local binary
            binary=$(resolve_binary_name "$tool_name" "$mod_file")
            local _ec=0
            go tool -modfile="$mod_file" "$binary" "$@" || _ec=$?
            _trace_exec "$binary" "$_ec" "$tool_name" "$@"
            exit $_ec
            ;;

        module)
            if [[ ! -d "$GOTOOLS_DIR/$tool_name" ]]; then
                _trace_fatal "Tool '$tool_name' not found ($GOTOOLS_DIR/$tool_name missing)" "$tool_name"
                echo "❌ Error: Tool '$tool_name' not found ($GOTOOLS_DIR/$tool_name missing). Run 'install' first." >&2
                exit $E_TOOL_NOT_FOUND
            fi
            local binary
            binary=$(resolve_binary_name "$tool_name" "$GOTOOLS_DIR/$tool_name/go.mod")
            local _ec=0
            (cd "$GOTOOLS_DIR/$tool_name" && go tool "$binary" "$@") || _ec=$?
            _trace_exec "$binary" "$_ec" "$tool_name" "$@"
            exit $_ec
            ;;

        *)
            _trace_fatal "Unknown strategy: $GOTOOLS_STRATEGY" "$tool_name"
            echo "❌ Unknown strategy: $GOTOOLS_STRATEGY" >&2
            exit $E_ENVIRONMENT
            ;;
    esac
}
