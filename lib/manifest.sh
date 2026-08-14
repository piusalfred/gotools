# Copyright (c) 2026 Pius Alfred
# License: MIT


# ---------------------------------------------------------------------------
# Manifest helpers (.gotools.json)
# ---------------------------------------------------------------------------

# _MANIFEST_SCHEMA_VERSION — the manifest schema this gotools understands.
# Bump ONLY when load_config would misinterpret a newer format. Additive
# changes (new optional key, new strategy value) do not require a bump.
readonly _MANIFEST_SCHEMA_VERSION=1

# _manifest_read_version [file] — echo the integer schema version (default 1).
# Shape-aware: the top-level version is unquoted JSON ("version": 1,) and
# lands in $3 as ": 1,"; a quoted value (e.g. a tool entry's "version":
# "v0.8.0") lands in $4. Pre-version manifests and non-numeric matches
# (the "version" key of a tool entry) default to v1.
_manifest_read_version() {
    local mf="${1:-$MANIFEST_FILE}"
    local version
    version=$(awk -F'"' '/"version":/ { if (NF >= 4) print $4; else print $3; exit }' "$mf")
    version="${version#: }"
    version="${version%,}"
    [[ "$version" =~ ^[0-9]+$ ]] || version=1
    echo "$version"
}

# _manifest_validate [file] — non-fatal structural validation for read-only
# diagnostics (the doctor command). rc 0 valid; 1 missing file; 2 written by
# a newer gotools (prints the same 3-line message _manifest_check_version
# prints, but returns instead of exiting); 3 unparseable (no top-level
# "strategy" key, or the last non-blank line is not "}" — truncated JSON).
_manifest_validate() {
    local mf="${1:-$MANIFEST_FILE}"
    [[ -f "$mf" ]] || return 1
    local version
    version=$(_manifest_read_version "$mf")
    if [[ "$version" -gt "$_MANIFEST_SCHEMA_VERSION" ]]; then
        echo "❌ This project's $MANIFEST_FILE requires schema version ${version}." >&2
        echo "   Your gotools (${VERSION}) only understands version ${_MANIFEST_SCHEMA_VERSION}." >&2
        echo "   Upgrade: https://github.com/$REPO/releases" >&2
        return 2
    fi
    local strategy last
    strategy=$(awk -F'"' '/"strategy":/ {print $4; exit}' "$mf")
    last=$(awk 'NF { line=$0 } END { print line }' "$mf")
    if [[ -z "$strategy" || "$last" != "}" ]]; then
        echo "❌ $MANIFEST_FILE is not parseable: truncated or malformed JSON." >&2
        return 3
    fi
    return 0
}

# _SETTINGS — in-memory runtime-settings store, same pipe-delimited shape
# as _MANIFEST_TOOLS: one "key|value" line per setting. Populated by
# _manifest_parse from the manifest's "settings" block (which may be
# absent — defaults then apply), mutated by _manifest_config_set.
_SETTINGS=""

# _settings_default <key> — the built-in default for a settings key.
_settings_default() {
    case "$1" in
        offline)           echo "false" ;;
        jobs)              echo "1" ;;
        lock_stale_timeout) echo "300" ;;
        lock_timeout)      echo "10" ;;
        no_lock)           echo "false" ;;
        operation_timeout) echo "120" ;;
        trace)             echo "false" ;;
        verbose)           echo "false" ;;
        *)                 return 1 ;;
    esac
}

# _settings_get <key> — echo the manifest value for a settings key.
# rc 1 when the manifest carries no entry for it.
_settings_get() {
    local want="$1" _k _v
    while IFS='|' read -r _k _v; do
        if [[ "$_k" == "$want" ]]; then
            echo "$_v"
            return 0
        fi
    done <<< "$_SETTINGS"
    return 1
}

# _settings_resolve <key> — the effective value with full precedence:
# startup env (explicit, even "0"/"false") > manifest settings > default.
# Booleans come back normalized to true/false; numbers as their digits.
_settings_resolve() {
    local key="$1" env_var=""
    case "$key" in
        offline)           env_var="$_ORIG_ENV_OFFLINE" ;;
        jobs)              env_var="$_ORIG_ENV_JOBS" ;;
        lock_stale_timeout) env_var="$_ORIG_ENV_LOCK_STALE_TIMEOUT" ;;
        lock_timeout)      env_var="$_ORIG_ENV_LOCK_TIMEOUT" ;;
        no_lock)           env_var="$_ORIG_ENV_NO_LOCK" ;;
        operation_timeout) env_var="$_ORIG_ENV_OPERATION_TIMEOUT" ;;
        trace)             env_var="$_ORIG_ENV_TRACE" ;;
        verbose)           env_var="$_ORIG_ENV_VERBOSE" ;;
    esac
    local value=""
    if [[ -n "$env_var" ]]; then
        value="$env_var"
    else
        value=$(_settings_get "$key") || value=$(_settings_default "$key")
    fi
    _settings_normalize "$key" "$value"
}

# _settings_normalize <key> <raw> — validate and normalize a settings
# value: booleans become true/false (accepting 0/1), integers stay as
# digits. Anything else falls back to the default.
_settings_normalize() {
    local key="$1" raw="$2"
    case "$key" in
        offline|no_lock|verbose)
            case "$raw" in
                true|1)  echo "true" ;;
                false|0) echo "false" ;;
                *)       _settings_default "$key" ;;
            esac
            ;;
        trace)
            # "stdout" selects stdout streaming — valid for the env var
            # only, but harmless to accept here too.
            case "$raw" in
                stdout)       echo "stdout" ;;
                true|1)       echo "true" ;;
                false|0|"")   echo "false" ;;
                *)            _settings_default "$key" ;;
            esac
            ;;
        jobs|operation_timeout|lock_timeout|lock_stale_timeout)
            [[ "$raw" =~ ^[0-9]+$ ]] && echo "$raw" || _settings_default "$key"
            ;;
        *) _settings_default "$key" ;;
    esac
}

# _manifest_check_version — refuse manifests written by a NEWER gotools so
# old versions fail loudly instead of silently misparsing. Pre-version
# manifests (no top-level "version" key) default to v1. A non-numeric match
# means the "version" key belongs to a tool entry, not the top level.
_manifest_check_version() {
    local version
    version=$(_manifest_read_version "$MANIFEST_FILE")
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
    _SETTINGS=""
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

    # Second pass: the optional "settings" block ("key": value lines).
    # Unknown keys and malformed values are ignored — defaults apply.
    local in_settings=false _sk="" _sv=""
    while IFS= read -r line; do
        if [[ "$line" =~ ^[[:space:]]*\"settings\"[[:space:]]*:[[:space:]]*\{ ]]; then
            in_settings=true
            continue
        fi
        if $in_settings && [[ "$line" =~ ^[[:space:]]*\} ]]; then
            break
        fi
        if $in_settings && [[ "$line" =~ \"([a-z_]+)\":[[:space:]]*([^,]+) ]]; then
            _sk="${BASH_REMATCH[1]}"
            _sv="${BASH_REMATCH[2]}"
            _sv="${_sv##[[:space:]]}"
            _sv="${_sv%%[[:space:]]}"
            if _settings_default "$_sk" >/dev/null 2>&1; then
                _SETTINGS+="${_sk}|${_sv}"$'\n'
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

    # Build settings JSON block. Every key is emitted — including keys the
    # manifest never carried — so the committed file always shows the full
    # knob surface with its effective values (visibility by default).
    local settings_json="" skey sval
    local settings_sorted
    settings_sorted=$(printf '%s\n' "$_SETTINGS" | LC_ALL=C sort -t'|' -k1,1)
    local seen=""
    local first_setting=true
    while IFS='|' read -r skey sval; do
        [[ -z "$skey" ]] && continue
        seen="${seen} ${skey}"
        if $first_setting; then first_setting=false; else settings_json+=","$'\n'; fi
        settings_json+="    \"${skey}\": ${sval}"
    done <<< "$settings_sorted"
    for skey in jobs lock_stale_timeout lock_timeout no_lock offline operation_timeout trace verbose; do
        if [[ " $seen " != *" $skey "* ]]; then
            if $first_setting; then first_setting=false; else settings_json+=","$'\n'; fi
            settings_json+="    \"${skey}\": $(_settings_default "$skey")"
        fi
    done

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
  "settings": {
${settings_json}
  },
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
        GOTOOLS_OFFLINE)           _settings_get offline || _settings_default offline ;;
        GOTOOLS_JOBS)              _settings_get jobs || _settings_default jobs ;;
        GOTOOLS_OPERATION_TIMEOUT) _settings_get operation_timeout || _settings_default operation_timeout ;;
        GOTOOLS_LOCK_TIMEOUT)      _settings_get lock_timeout || _settings_default lock_timeout ;;
        GOTOOLS_LOCK_STALE_TIMEOUT) _settings_get lock_stale_timeout || _settings_default lock_stale_timeout ;;
        GOTOOLS_NO_LOCK)           _settings_get no_lock || _settings_default no_lock ;;
        GOTOOLS_TRACE)             _settings_get trace || _settings_default trace ;;
        GOTOOLS_VERBOSE)           _settings_get verbose || _settings_default verbose ;;
        *) echo "❌ Unknown config key: $key" >&2; return 1 ;;
    esac
}

# _manifest_config_set — update a config field in the manifest file.
_manifest_config_set() {
    local key="$1" value="$2"
    local setting_key="" normalized=""
    case "$key" in
        GOTOOLS_STRATEGY)     GOTOOLS_STRATEGY="$value"     ;;
        GOTOOLS_DIR)          GOTOOLS_DIR="$value"          ;;
        GOTOOLS_GO_VERSION)   GOTOOLS_GO_VERSION="$value"    ;;
        GOTOOLS_MODULE_PREFIX) GOTOOLS_MODULE_PREFIX="$value" ;;
        GOTOOLS_OFFLINE)           setting_key="offline" ;;
        GOTOOLS_JOBS)              setting_key="jobs" ;;
        GOTOOLS_OPERATION_TIMEOUT) setting_key="operation_timeout" ;;
        GOTOOLS_LOCK_TIMEOUT)      setting_key="lock_timeout" ;;
        GOTOOLS_LOCK_STALE_TIMEOUT) setting_key="lock_stale_timeout" ;;
        GOTOOLS_NO_LOCK)           setting_key="no_lock" ;;
        GOTOOLS_TRACE)             setting_key="trace" ;;
        GOTOOLS_VERBOSE)           setting_key="verbose" ;;
        *) echo "❌ Unknown config key: $key" >&2; return 1 ;;
    esac
    if [[ -n "$setting_key" ]]; then
        normalized=$(_settings_normalize "$setting_key" "$value")
        _settings_set "$setting_key" "$normalized"
        case "$setting_key" in
            offline) _OFFLINE=$normalized ;;
            trace)   _TRACE=$normalized ;;
            verbose) _VERBOSE=$normalized ;;
            jobs)    _JOBS=$normalized ;;
            operation_timeout) _OPERATION_TIMEOUT=$normalized ;;
            lock_timeout) _LOCK_TIMEOUT=$normalized ;;
            lock_stale_timeout) _LOCK_STALE_TIMEOUT=$normalized ;;
            no_lock) _NO_LOCK=$normalized ;;
        esac
    fi
    _manifest_flush
}

# _settings_set <key> <value> — update a settings entry in memory
# (does NOT flush).
_settings_set() {
    local want="$1" value="$2"
    local new_settings="" found=false _k _v
    while IFS='|' read -r _k _v; do
        [[ -z "$_k" ]] && continue
        if [[ "$_k" == "$want" ]]; then
            new_settings+="${_k}|${value}"$'\n'
            found=true
        else
            new_settings+="${_k}|${_v}"$'\n'
        fi
    done <<< "$_SETTINGS"
    if ! $found; then
        new_settings+="${want}|${value}"$'\n'
    fi
    _SETTINGS="$new_settings"
}
