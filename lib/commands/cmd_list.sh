# Copyright (c) 2026 Pius Alfred
# License: MIT


# _list_json — output the tool list as a JSON object: schema_version,
# strategy/dir/go_version metadata, and a "tools" array of per-tool records.
# Single compact line; values escaped via _json_escape.

_list_json() {
    load_config
    printf '{"schema_version":1,"strategy":"%s","dir":"%s","go_version":"%s","tools":[' \
        "$(_json_escape "$GOTOOLS_STRATEGY")" "$(_json_escape "$GOTOOLS_DIR")" "$(_json_escape "$GOTOOLS_GO_VERSION")"
    local first=true _n _s _p _v
    while IFS='|' read -r _n _s _p _v; do
        [[ -z "$_n" ]] && continue
        local go_ver="?"
        case "$GOTOOLS_STRATEGY" in
            unified) go_ver=$(extract_go_version_from_mod "$GOTOOLS_DIR/go.mod" 2>/dev/null || echo "?") ;;
            split)   go_ver=$(extract_go_version_from_mod "$GOTOOLS_DIR/${_n}.mod" 2>/dev/null || echo "?") ;;
            module)  go_ver=$(extract_go_version_from_mod "$GOTOOLS_DIR/${_n}/go.mod" 2>/dev/null || echo "?") ;;
        esac
        $first && first=false || printf ','
        printf '{"name":"%s","source":"%s","package":"%s","version":"%s","go":"%s"}' \
            "$(_json_escape "$_n")" "$(_json_escape "$_s")" "$(_json_escape "$_p")" "$(_json_escape "$_v")" "$(_json_escape "$go_ver")"
    done <<< "$_MANIFEST_TOOLS"
    printf ']}\n'
}

# ---- list ----------------------------------------------------------------
cmd_list() {
    load_config
    _parse_output_format "$@"
    local arg
    for arg in "$@"; do
        case "$arg" in
            --json|--text|--format=json|--format=text) ;;
            *)
                echo "❌ Unknown argument: $arg" >&2
                echo "   Usage: $(basename "$0") list [--format=json|text]" >&2
                exit $E_USAGE
                ;;
        esac
    done

    if [[ "$_OUTPUT_FORMAT" == "json" ]]; then
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
            exit $E_ENVIRONMENT
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
