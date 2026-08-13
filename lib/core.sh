# Copyright (c) 2026 Pius Alfred
# License: MIT


# _require_go — verify Go is installed and >= 1.24 (minimum for tool directives).



_require_go() {
    if ! command -v go &>/dev/null; then
        echo "❌ Go is not installed. Please install Go 1.24 or higher." >&2
        echo "   https://go.dev/dl/" >&2
        exit $E_ENVIRONMENT
    fi
    local go_ver
    go_ver=$(go env GOVERSION 2>/dev/null | sed 's/^go//')
    if [[ -z "$go_ver" ]]; then
        echo "❌ Could not determine Go version." >&2
        exit $E_ENVIRONMENT
    fi
    local major minor
    major=$(echo "$go_ver" | cut -d. -f1)
    minor=$(echo "$go_ver" | cut -d. -f2)
    if [[ "$major" -lt 1 ]] || { [[ "$major" -eq 1 ]] && [[ "${minor:-0}" -lt 24 ]]; }; then
        echo "❌ Go $go_ver is too old. Go 1.24 or higher is required." >&2
        exit $E_ENVIRONMENT
    fi
}

# ---- Lockfile (concurrent operation safety) -------------------------
_LOCK_FILE=""
_LOCK_HELD=false

# _acquire_lock — try to acquire the gotools lockfile. Reentrant: if this
# process already holds the lock, returns immediately.
_acquire_lock() {
    if $_LOCK_HELD; then return; fi
    local lock_dir="${GOTOOLS_DIR:-tools}"
    mkdir -p "$lock_dir"
    _LOCK_FILE="$lock_dir/.gotools.lock"
    local timeout=10 waited=0
    while ! mkdir "$_LOCK_FILE" 2>/dev/null; do
        if [[ $waited -ge $timeout ]]; then
            echo "❌ Another gotools process is running. Try again later." >&2
            exit $E_LOCK
        fi
        sleep 0.5
        waited=$((waited + 1))
    done
    _LOCK_HELD=true
    trap '_release_lock' EXIT
}

# _release_lock — remove the lockfile.
_release_lock() {
    if $_LOCK_HELD; then
        [[ -n "${_LOCK_FILE:-}" && -d "${_LOCK_FILE:-}" ]] && rmdir "$_LOCK_FILE" 2>/dev/null || true
        _LOCK_HELD=false
    fi
}

# _go — run a go command, printing it first if verbose mode is on.
# Usage: _go [args...]  (calls `go` with the same args)
_go() {
    if [[ "$_VERBOSE" == "1" || "$_VERBOSE" == "true" ]]; then
        echo "  ↳ go $*" >&2
    fi
    go "$@"
}

# _parse_dry_run — check remaining args for --dry-run, remove it, set _DRY_RUN.
# Call this at the top of destructive commands after load_config.
_parse_dry_run() {
    local filtered=()
    for arg in "$@"; do
        if [[ "$arg" == "--dry-run" ]]; then
            _DRY_RUN=true
        else
            filtered+=("$arg")
        fi
    done
    # Return filtered args via a global (bash can't return arrays).
    # ${filtered[@]+...} keeps the expansion legal when filtered is empty
    # under `set -u` on bash < 4.4.
    _PARSED_ARGS=(${filtered[@]+"${filtered[@]}"})
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

# _sha256 <file> — print the SHA-256 hash of a file.
# Uses sha256sum (Linux) or shasum -a 256 (macOS); returns 1 if neither exists.
_sha256() {
    if command -v sha256sum &>/dev/null; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum &>/dev/null; then
        shasum -a 256 "$1" | awk '{print $1}'
    else
        return 1
    fi
}

# _json_escape — escape a string for inclusion in a JSON string value.
# Escapes: backslash, double-quote, and control characters (0x00-0x1f).
_json_escape() {
    local s="$1"
    local i c escaped=""
    for ((i = 0; i < ${#s}; i++)); do
        c="${s:i:1}"
        case "$c" in
            $'\\') escaped+='\\' ;;
            '"')   escaped+='\"'  ;;
            $'\n') escaped+='\n'  ;;
            $'\r') escaped+='\r'  ;;
            $'\t') escaped+='\t'  ;;
            *)     escaped+="$c"  ;;
        esac
    done
    printf '%s' "$escaped"
}

# Trace logging (opt-in via GOTOOLS_TRACE=1) — appends one JSON Lines
# record per tool invocation to .gotools_trace.log in the project root.
# Format:
#   {"ts":"<iso8601>","level":"<info|error>","tool":"<name>","binary":"<path>","cmd":"<reconstructed>","strategy":"<s>","stdin":"<pipe|terminal>","exit_code":<int>,"args":["...",...],"env":{...}}
# _trace_exec BINARY EXIT_CODE TOOL_NAME [ARGS...]
_trace_exec() {
    if [[ "$_TRACE" != "1" && "$_TRACE" != "true" ]]; then
        return
    fi
    local _bin="$1"
    local _exit_code="$2"
    local _tool="$3"
    shift 3

    local _ts _stdin _json_args="" _first=1 _arg _cmd _level
    _ts=$(date '+%Y-%m-%dT%H:%M:%S')
    _stdin=$([ -t 0 ] && echo "terminal" || echo "pipe")

    # Compute level from exit code.
    if [[ "$_exit_code" -eq 0 ]]; then
        _level="info"
    else
        _level="error"
    fi

    # Reconstruct the full gotools invocation — copy-pasteable for reproduction.
    _cmd="gotools exec $_tool"
    for _arg in "$@"; do
        if [[ "$_arg" =~ [[:space:]] ]]; then
            _cmd+=" \"$_arg\""
        else
            _cmd+=" $_arg"
        fi
    done

    for _arg in "$@"; do
        if [[ $_first -eq 1 ]]; then
            _json_args+="\"$(_json_escape "$_arg")\""
            _first=0
        else
            _json_args+=",\"$(_json_escape "$_arg")\""
        fi
    done

    printf '{"ts":"%s","level":"%s","tool":"%s","binary":"%s","cmd":"%s","strategy":"%s","stdin":"%s","exit_code":%d,"args":[%s],"env":{"GOTOOLS_STRATEGY":"%s","GOTOOLS_DIR":"%s","GOTOOLS_GO_VERSION":"%s","GOTOOLS_MODULE_PREFIX":"%s","GOTOOLS_VERBOSE":"%s","GOTOOLS_TRACE":"%s"}}\n' \
        "$_ts" \
        "$_level" \
        "$(_json_escape "$_tool")" \
        "$(_json_escape "$_bin")" \
        "$(_json_escape "$_cmd")" \
        "$(_json_escape "$GOTOOLS_STRATEGY")" \
        "$_stdin" \
        "$_exit_code" \
        "$_json_args" \
        "$(_json_escape "${GOTOOLS_STRATEGY:-}")" \
        "$(_json_escape "${GOTOOLS_DIR:-}")" \
        "$(_json_escape "$(resolve_go_version)")" \
        "$(_json_escape "$(resolve_module_prefix)")" \
        "$(_json_escape "${GOTOOLS_VERBOSE:-0}")" \
        "$(_json_escape "${GOTOOLS_TRACE:-0}")" \
        >> "$_PROJECT_ROOT/.gotools_trace.log"
}

# _trace_fatal — emit a "fatal" trace record when gotools itself errors
# out before a tool is executed (missing go.mod, unknown tool, etc.).
# _trace_fatal MESSAGE [TOOL_NAME]
_trace_fatal() {
    if [[ "$_TRACE" != "1" && "$_TRACE" != "true" ]]; then
        return
    fi
    local _message="$1"
    local _tool="${2:-unknown}"
    local _ts _stdin
    _ts=$(date '+%Y-%m-%dT%H:%M:%S')
    _stdin=$([ -t 0 ] && echo "terminal" || echo "pipe")
    printf '{"ts":"%s","level":"fatal","tool":"%s","error":"%s","strategy":"%s","stdin":"%s","env":{"GOTOOLS_STRATEGY":"%s","GOTOOLS_DIR":"%s","GOTOOLS_GO_VERSION":"%s","GOTOOLS_MODULE_PREFIX":"%s","GOTOOLS_VERBOSE":"%s","GOTOOLS_TRACE":"%s"}}\n' \
        "$_ts" \
        "$(_json_escape "$_tool")" \
        "$(_json_escape "$_message")" \
        "$(_json_escape "${GOTOOLS_STRATEGY:-}")" \
        "$_stdin" \
        "$(_json_escape "${GOTOOLS_STRATEGY:-}")" \
        "$(_json_escape "${GOTOOLS_DIR:-}")" \
        "$(_json_escape "$(resolve_go_version)")" \
        "$(_json_escape "$(resolve_module_prefix)")" \
        "$(_json_escape "${GOTOOLS_VERBOSE:-0}")" \
        "$(_json_escape "${GOTOOLS_TRACE:-0}")" \
        >> "$_PROJECT_ROOT/.gotools_trace.log"
}
