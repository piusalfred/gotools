# Copyright (c) 2026 Pius Alfred
# License: MIT


# _info_json <tool-name> — output a single tool as a JSON object with a
# runnable flag (probed only when the tool's modfile exists; otherwise false
# without invoking go).

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
    local runnable=false
    local modfile=""
    case "$GOTOOLS_STRATEGY" in
        unified) modfile="$GOTOOLS_DIR/go.mod" ;;
        split)   modfile="$GOTOOLS_DIR/${name}.mod" ;;
        module)  modfile="$GOTOOLS_DIR/${name}/go.mod" ;;
    esac
    if [[ -f "$modfile" ]] && tool_runnable "$name" >/dev/null 2>&1; then
        runnable=true
    fi
    printf '{"schema_version":1,"name":"%s","source":"%s","strategy":"%s","go":"%s","package":"%s","version":"%s","runnable":%s}\n' \
        "$(_json_escape "$name")" "$(_json_escape "$_s")" "$(_json_escape "$GOTOOLS_STRATEGY")" \
        "$(_json_escape "$go_ver")" "$(_json_escape "$_p")" "$(_json_escape "$_v")" "$runnable"
}

# ---- info ----------------------------------------------------------------
cmd_info() {
    load_config
    _parse_output_format "$@"
    local -a positional
    positional=()
    local arg
    for arg in "$@"; do
        case "$arg" in
            --json|--text|--format=json|--format=text) ;;
            *) positional+=("$arg") ;;
        esac
    done

    if [[ ${#positional[@]} -eq 0 || ${#positional[@]} -gt 1 ]]; then
        echo "❌ Usage: $(basename "$0") info <tool-name> [--format=json|text]" >&2
        exit $E_USAGE
    fi

    local tool_name="${positional[0]}"

    if [[ "$_OUTPUT_FORMAT" == "json" ]]; then
        _info_json "$tool_name" || exit $E_TOOL_NOT_FOUND
        return
    fi

    local modfile="" pkg="" ver="" go_ver="" strategy="$GOTOOLS_STRATEGY"

    case "$GOTOOLS_STRATEGY" in
        unified)
            modfile="$GOTOOLS_DIR/go.mod"
            if [[ ! -f "$modfile" ]]; then
                echo "❌ No $modfile found. Run 'init' first." >&2
                exit $E_TOOL_NOT_FOUND
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
                exit $E_TOOL_NOT_FOUND
            fi
            ver=$(extract_version_for_pkg "$modfile" "$pkg")
            go_ver=$(extract_go_version_from_mod "$modfile")
            ;;

        split)
            modfile="$GOTOOLS_DIR/${tool_name}.mod"
            if [[ ! -f "$modfile" ]]; then
                echo "❌ Tool '$tool_name' not found ($modfile missing)." >&2
                exit $E_TOOL_NOT_FOUND
            fi
            pkg=$(extract_pkg_from_mod "$modfile")
            ver=$(extract_version_for_pkg "$modfile" "$pkg")
            go_ver=$(extract_go_version_from_mod "$modfile")
            ;;

        module)
            modfile="$GOTOOLS_DIR/$tool_name/go.mod"
            if [[ ! -f "$modfile" ]]; then
                echo "❌ Tool '$tool_name' not found ($modfile missing)." >&2
                exit $E_TOOL_NOT_FOUND
            fi
            pkg=$(extract_pkg_from_mod "$modfile")
            ver=$(extract_version_for_pkg "$modfile" "$pkg")
            go_ver=$(extract_go_version_from_mod "$modfile")
            ;;

        *)
            echo "❌ Unknown strategy: $GOTOOLS_STRATEGY" >&2
            exit $E_ENVIRONMENT
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
