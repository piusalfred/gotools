# Copyright (c) 2026 Pius Alfred
# License: MIT


# _go_version_raw — print the bare go version string (e.g. "1.24.3"), empty
# if go is missing or `go env GOVERSION` fails. Non-fatal, never exits.
_go_version_raw() {
    command -v go >/dev/null 2>&1 || return 1
    go env GOVERSION 2>/dev/null | sed 's/^go//'
}

# _version_meets_min <actual> <required> — compare X.Y versions numerically.
#   rc 0: actual >= required
#   rc 1: actual < required
#   rc 2: undetectable (actual empty / no X.Y pair in either version)
# Pure: no output, no exit. Extracts the first [0-9]+\.[0-9]+ pair so inputs
# like "1.24.3", "go1.24", or "devel go1.26-abc123" all work.
_version_meets_min() {
    local actual="$1" required="$2"
    local amaj amin rmaj rmin
    [[ "$actual" =~ ([0-9]+)\.([0-9]+) ]] || return 2
    amaj="${BASH_REMATCH[1]}"
    amin="${BASH_REMATCH[2]}"
    [[ "$required" =~ ([0-9]+)\.([0-9]+) ]] || return 2
    rmaj="${BASH_REMATCH[1]}"
    rmin="${BASH_REMATCH[2]}"
    if (( amaj > rmaj )); then return 0; fi
    if (( amaj < rmaj )); then return 1; fi
    if (( amin >= rmin )); then return 0; fi
    return 1
}

# _require_go — verify Go is installed and >= 1.24 (minimum for tool directives).
_require_go() {
    if ! command -v go &>/dev/null; then
        echo "❌ Go is not installed. Please install Go 1.24 or higher." >&2
        echo "   https://go.dev/dl/" >&2
        exit $E_ENVIRONMENT
    fi
    local go_ver
    go_ver=$(_go_version_raw) || true
    if [[ -z "$go_ver" ]]; then
        echo "❌ Could not determine Go version." >&2
        exit $E_ENVIRONMENT
    fi
    if ! _version_meets_min "$go_ver" "$MIN_GO_VERSION"; then
        echo "❌ Go $go_ver is too old. Go 1.24 or higher is required." >&2
        exit $E_ENVIRONMENT
    fi
}

# ---- Lockfile (concurrent operation safety) -------------------------
_LOCK_FILE=""
_LOCK_HELD=false

# _file_mtime <path> — epoch seconds of <path>'s mtime. BSD (macOS) stat
# uses -f, GNU (Linux) uses -c. Shared by lock staleness and doctor.
_file_mtime() {
    case "$(uname -s)" in
        Darwin) stat -f %m "$1" ;;
        *)      stat -c %Y "$1" ;;
    esac
}

# _lock_pid_live <pid> — liveness probe for lock holders. ps -p reports
# other users' processes too, where kill -0 would return EPERM and look
# like a dead pid.
_lock_pid_live() {
    ps -p "$1" >/dev/null 2>&1
}

# _lock_detect_stale <lock_dir> — decide whether an existing lock is stale
# and remove it. rc 0: stale, removed; rc 1: held (or undecidable).
#
# A lock dir carrying a pid file is stale only when that process is dead —
# a live holder is NEVER stale, no matter how old the lock is. Legacy locks
# (no pid file: older gotools, manual mkdir) fall back to an age check
# (GOTOOLS_LOCK_STALE_TIMEOUT seconds, default 300).
_lock_detect_stale() {
    local lock_dir="$1"
    [[ -d "$lock_dir" ]] || return 1
    local pid=""
    if [[ -f "$lock_dir/pid" ]]; then
        pid=$(cat "$lock_dir/pid" 2>/dev/null || true)
        if [[ "$pid" =~ ^[0-9]+$ ]] && _lock_pid_live "$pid"; then
            return 1
        fi
        echo "  ⚠️  Stale lock detected (process $pid is gone). Removing $lock_dir..." >&2
        rm -f "$lock_dir/pid"
        rmdir "$lock_dir" 2>/dev/null || true
        return 0
    fi
    local now mtime age
    now=$(date +%s)
    mtime=$(_file_mtime "$lock_dir" 2>/dev/null) || true
    [[ -z "$mtime" ]] && return 1
    age=$((now - mtime))
    local stale_timeout="${_LOCK_STALE_TIMEOUT:-300}"
    [[ "$stale_timeout" =~ ^[0-9]+$ ]] || stale_timeout=300
    if [[ $age -gt "$stale_timeout" ]]; then
        echo "  ⚠️  Stale lock detected (age ${age}s). Removing $lock_dir..." >&2
        rmdir "$lock_dir" 2>/dev/null || true
        return 0
    fi
    return 1
}

# _acquire_lock — try to acquire the gotools lockfile. Reentrant: if this
# process already holds the lock, returns immediately. Stale locks (dead
# holder, or legacy locks older than the stale timeout) are removed and the
# acquisition retried; otherwise the wait is bounded by
# GOTOOLS_LOCK_TIMEOUT (default 10s, 0.5s poll). GOTOOLS_NO_LOCK=1 skips
# acquisition entirely — only for CI with serial workspace guarantees.
_acquire_lock() {
    if $_LOCK_HELD; then return; fi
    if [[ "${_NO_LOCK:-0}" == "true" || "${_NO_LOCK:-0}" == "1" ]]; then
        _LOCK_HELD=true
        return
    fi
    local lock_dir="${GOTOOLS_DIR:-tools}"
    mkdir -p "$lock_dir"
    _LOCK_FILE="$lock_dir/.gotools.lock"
    local timeout="${_LOCK_TIMEOUT:-10}"
    [[ "$timeout" =~ ^[0-9]+$ ]] || timeout=10
    local waited=0
    while ! mkdir "$_LOCK_FILE" 2>/dev/null; do
        # Crashed processes leave their lock behind: detect and remove
        # stale locks before waiting.
        if _lock_detect_stale "$_LOCK_FILE"; then
            continue
        fi
        if [[ $waited -ge $timeout ]]; then
            echo "❌ Another gotools process is running. Try again later." >&2
            exit $E_LOCK
        fi
        sleep 0.5
        waited=$((waited + 1))
    done
    _LOCK_HELD=true
    # Record our PID so other processes can distinguish a held lock from a
    # stale one — age alone cannot (a live process may legitimately hold
    # the lock longer than the stale threshold).
    echo $$ > "$_LOCK_FILE/pid"
    trap '_release_lock' EXIT
}

# _release_lock — remove the pid record and the lock directory. rmdir only
# removes empty dirs, so the pid file must go first.
_release_lock() {
    if $_LOCK_HELD; then
        if [[ -n "${_LOCK_FILE:-}" && -d "${_LOCK_FILE:-}" ]]; then
            rm -f "$_LOCK_FILE/pid"
            rmdir "$_LOCK_FILE" 2>/dev/null || true
        fi
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

# _parse_offline [args...] — set _OFFLINE from a --offline flag in the args
# or the GOTOOLS_OFFLINE=1 env var. Call at the top of commands that support
# offline mode. Always returns 0 (the env check must not trip set -e).
_parse_offline() {
    for arg in "$@"; do
        case "$arg" in --offline) _OFFLINE=true ;; esac
    done
    if [[ "${GOTOOLS_OFFLINE:-0}" == "1" ]]; then
        _OFFLINE=true
    fi
}

# _run_with_timeout <seconds> <cmd...> — run cmd, killing it if it exceeds
# <seconds>. Returns the command's exit code, or 124 when the timeout fired.
# Portable (no GNU timeout(1) dependency): backgrounds the command — stdout
# and stderr flow through unchanged — polls, and escalates TERM -> KILL.
# Safe inside command substitutions: killing the command closes the pipe the
# substitution waits on. `wait` needs an OR-guard under set -e (it inherits
# the child's exit status).
_run_with_timeout() {
    local timeout="$1"
    shift
    [[ $# -gt 0 ]] || return 1
    local rc=0
    "$@" &
    local pid=$! waited=0
    while kill -0 "$pid" 2>/dev/null; do
        if [[ $waited -ge $((timeout * 2)) ]]; then
            kill "$pid" 2>/dev/null || true
            # Grace period: escalate to KILL if TERM didn't land.
            sleep 0.5
            kill -0 "$pid" 2>/dev/null && {
                sleep 0.5
                kill -0 "$pid" 2>/dev/null && kill -9 "$pid" 2>/dev/null || true
            }
            wait "$pid" 2>/dev/null || rc=$?
            return 124
        fi
        sleep 0.5
        waited=$((waited + 1))
    done
    wait "$pid" 2>/dev/null || rc=$?
    return $rc
}

# _go_timeout [args...] — run a go command under the operation timeout
# (GOTOOLS_OPERATION_TIMEOUT seconds, default 120; 0 disables). Uses GNU
# timeout(1) when present (Linux, BusyBox); otherwise the portable watchdog.
# Returns go's exit code, or 124 when the timeout fired — the single rc for
# "timed out" across both mechanisms.
_go_timeout() {
    local t="${_OPERATION_TIMEOUT:-120}"
    [[ "$t" =~ ^[0-9]+$ ]] || t=120
    if [[ "$_VERBOSE" == "1" || "$_VERBOSE" == "true" ]]; then
        echo "  ↳ go $*" >&2
    fi
    if [[ "$t" -eq 0 ]]; then
        go "$@"
        return $?
    fi
    if command -v timeout >/dev/null 2>&1; then
        timeout "$t" go "$@"
        return $?
    fi
    _run_with_timeout "$t" go "$@"
}

# _timeout_fatal <rc> <what> — for call sites with no other error handling
# (set -e covers ordinary failures): on a timeout exit E_NETWORK with a clear
# message; otherwise re-exit with rc, preserving set -e semantics.
_timeout_fatal() {
    local rc="$1" what="$2"
    if [[ "$rc" -eq 124 ]]; then
        echo "❌ $what timed out after ${_OPERATION_TIMEOUT:-120}s." >&2
        exit $E_NETWORK
    fi
    exit "$rc"
}

# _parse_output_format [args...] — scan args for output-format flags and set
# _OUTPUT_FORMAT. --format=json | --json → json; --format=text | --text →
# text. Any other --format=<x> → usage error. Always returns 0 otherwise
# (set -e safe). Resets to "text" first so repeated in-process calls never
# leak a previous call's format. Callers re-scan the args to reject any
# leftover positional junk.
_parse_output_format() {
    _OUTPUT_FORMAT="text"
    local arg
    for arg in "$@"; do
        case "$arg" in
            --json)        _OUTPUT_FORMAT="json" ;;
            --text)        _OUTPUT_FORMAT="text" ;;
            --format=json) _OUTPUT_FORMAT="json" ;;
            --format=text) _OUTPUT_FORMAT="text" ;;
            --format=*)
                echo "❌ Unknown output format: ${arg#--format=}" >&2
                echo "   Supported formats: json, text" >&2
                exit $E_USAGE
                ;;
        esac
    done
}

# _parallel_for <max-jobs> <fn> [args...]
#   Runs fn concurrently over argument groups delimited by "::", at most
#   max-jobs at a time, then waits for all. Each invocation's output is
#   line-prefixed with [<first group arg>] so interleaved output stays
#   readable. Portable to bash 3.2 (no `wait -n`): the oldest job is reaped
#   whenever the cap is reached. Returns the first non-zero job status.
_parallel_for() {
    local max_jobs="$1" fn="$2"
    shift 2
    # Separate declaration/assignment: bash 3.2 cannot initialize two
    # arrays in a single `local` statement.
    local -a group pids
    group=()
    pids=()
    local running=0 fail_status=0 label
    local arg
    for arg in "$@"; do
        if [[ "$arg" == "::" ]]; then
            label="${group[0]}"
            # The subshell wrapper is required: bash 3.2 returns from a
            # function immediately after backgrounding a pipeline under
            # `set -e` (the classic errexit/& bug).
            (
                "$fn" "${group[@]}" 2>&1 | while IFS= read -r line; do
                    echo "[$label] $line"
                done
            ) &
            pids+=($!)
            running=$((running + 1))
            group=()
            # Only reap once we EXCEED the cap: with max_jobs=1 the second
            # job must launch before the first is waited on, otherwise the
            # semaphore degenerates into serial execution.
            if [[ $running -gt $max_jobs ]]; then
                _parallel_reap
                # Drop the oldest pid. The explicit branch avoids expanding
                # an empty array under `set -u` on bash < 4.4.
                if [[ ${#pids[@]} -gt 1 ]]; then
                    pids=("${pids[@]:1}")
                else
                    pids=()
                fi
                running=$((running - 1))
            fi
        else
            group+=("$arg")
        fi
    done
    local pid
    # Safe expansion: pids may be empty when no job was left running.
    for pid in ${pids[@]+"${pids[@]}"}; do
        if wait "$pid"; then
            fail_status=$fail_status
        else
            _parallel_record_failure $?
        fi
    done
    [[ $fail_status -eq 0 ]] || return $fail_status
}

# _parallel_reap — wait for the oldest running job, recording its status.
# Uses pids[0] and fail_status from the caller's scope.
_parallel_reap() {
    if wait "${pids[0]}"; then
        return 0
    fi
    _parallel_record_failure $?
    return 0
}

# _parallel_record_failure <status> — remember the first non-zero job status.
_parallel_record_failure() {
    local status="$1"
    if [[ $status -ne 0 && $fail_status -eq 0 ]]; then
        fail_status=$status
    fi
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
    if [[ "$_TRACE" != "1" && "$_TRACE" != "true" && "$_TRACE" != "stdout" ]]; then
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

    local _record
    _record=$(printf '{"ts":"%s","level":"%s","tool":"%s","binary":"%s","cmd":"%s","strategy":"%s","stdin":"%s","exit_code":%d,"args":[%s],"env":{"GOTOOLS_STRATEGY":"%s","GOTOOLS_DIR":"%s","GOTOOLS_GO_VERSION":"%s","GOTOOLS_MODULE_PREFIX":"%s","GOTOOLS_VERBOSE":"%s","GOTOOLS_TRACE":"%s"}}\n' \
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
        "$(_json_escape "${GOTOOLS_TRACE:-0}")")
    printf '%s\n' "$_record" >> "$_PROJECT_ROOT/.gotools_trace.log"
    # Live stream: stderr by default, stdout only when explicitly asked.
    # stderr keeps the tool's own stdout clean for pipes and CI logs.
    if [[ "$_TRACE" == "stdout" ]]; then
        printf '%s\n' "$_record"
    else
        printf '%s\n' "$_record" >&2
    fi
}

# _trace_fatal — emit a "fatal" trace record when gotools itself errors
# out before a tool is executed (missing go.mod, unknown tool, etc.).
# _trace_fatal MESSAGE [TOOL_NAME]
_trace_fatal() {
    if [[ "$_TRACE" != "1" && "$_TRACE" != "true" && "$_TRACE" != "stdout" ]]; then
        return
    fi
    local _message="$1"
    local _tool="${2:-unknown}"
    local _ts _stdin
    _ts=$(date '+%Y-%m-%dT%H:%M:%S')
    _stdin=$([ -t 0 ] && echo "terminal" || echo "pipe")
    local _record
    _record=$(printf '{"ts":"%s","level":"fatal","tool":"%s","error":"%s","strategy":"%s","stdin":"%s","env":{"GOTOOLS_STRATEGY":"%s","GOTOOLS_DIR":"%s","GOTOOLS_GO_VERSION":"%s","GOTOOLS_MODULE_PREFIX":"%s","GOTOOLS_VERBOSE":"%s","GOTOOLS_TRACE":"%s"}}\n' \
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
        "$(_json_escape "${GOTOOLS_TRACE:-0}")")
    printf '%s\n' "$_record" >> "$_PROJECT_ROOT/.gotools_trace.log"
    # Live stream: stderr by default, stdout only when explicitly asked.
    if [[ "$_TRACE" == "stdout" ]]; then
        printf '%s\n' "$_record"
    else
        printf '%s\n' "$_record" >&2
    fi
}
