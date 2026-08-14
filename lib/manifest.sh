# Copyright (c) 2026 Pius Alfred
# License: MIT


# ---------------------------------------------------------------------------
# Manifest helpers (.gotools.json)
# ---------------------------------------------------------------------------

# _MANIFEST_SCHEMA_VERSION — the manifest schema this gotools understands.
# Bump ONLY when load_config would misinterpret a newer format. Additive
# changes (new optional key, new strategy value) do not require a bump.
readonly _MANIFEST_SCHEMA_VERSION=1

# _manifest_check_version — refuse manifests written by a NEWER gotools so
# old versions fail loudly instead of silently misparsing. Pre-version
# manifests (no top-level "version" key) default to v1. A non-numeric match
# means the "version" key belongs to a tool entry, not the top level.
_manifest_check_version() {
    local version
    # Shape-aware: the top-level version is unquoted JSON ("version": 1,)
    # and lands in $3 as ": 1,"; a quoted value (e.g. a tool entry's
    # "version": "v0.8.0") lands in $4.
    version=$(awk -F'"' '/"version":/ { if (NF >= 4) print $4; else print $3; exit }' "$MANIFEST_FILE")
    version="${version#: }"
    version="${version%,}"
    [[ "$version" =~ ^[0-9]+$ ]] || version=1
    if [[ "$version" -gt "$_MANIFEST_SCHEMA_VERSION" ]]; then
        echo "❌ This project's $MANIFEST_FILE requires schema version ${version}." >&2
        echo "   Your gotools (${VERSION}) only understands version ${_MANIFEST_SCHEMA_VERSION}." >&2
        echo "   Upgrade: https://github.com/$REPO/releases" >&2
        exit $E_ENVIRONMENT
    fi
}

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
    # Deterministic order in the committed manifest: source, then package,
    # then version, then name (final tiebreak for aliases) — so diffs are
    # easy to read when tools are added or removed.
    local tools_sorted
    tools_sorted=$(printf '%s\n' "$_MANIFEST_TOOLS" | LC_ALL=C sort -t'|' -k2,2 -k3,3 -k4,4 -k1,1)

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
    done <<< "$tools_sorted"

    # Atomic write: build in a temp file, then rename over the target.
    # A crash mid-write leaves the previous manifest intact; the stray
    # temp file is removed by _manifest_flush_cleanup on the next run.
    # The PID suffix keeps concurrent writes from colliding even if the
    # lock was bypassed.
    local tmpfile="${mf}.tmp.$$"
    cat > "$tmpfile" <<MANIFEST_EOF
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
    mv "$tmpfile" "$mf"
}

# _manifest_flush_cleanup — remove temp files left behind by a crash during
# an atomic manifest write. They are never valid across invocations.
_manifest_flush_cleanup() {
    rm -f "${MANIFEST_FILE}.tmp."* 2>/dev/null || true
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
