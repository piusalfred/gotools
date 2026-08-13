# Copyright (c) 2026 Pius Alfred
# License: MIT


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
