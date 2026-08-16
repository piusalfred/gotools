# Copyright (c) 2026 Pius Alfred
# License: MIT

VERSION="v0.6.7"
REPO="piusalfred/gotools"
API_URL="https://api.github.com/repos/$REPO/releases/latest"

# Structured exit codes — let CI pipelines react intelligently to different
# failure modes instead of a flat `exit 1` everywhere. The user-facing table
# lives in usage(); keep it in sync when changing these values.
readonly E_GENERIC=1
readonly E_USAGE=2
readonly E_NETWORK=3
readonly E_LOCK=4
readonly E_TOOL_NOT_FOUND=5
readonly E_OFFLINE=6
# Reserved for the documented public contract.
# shellcheck disable=SC2034
readonly E_POLICY=7
readonly E_ENVIRONMENT=8

MANIFEST_FILE=".gotools.json"
DEFAULT_STRATEGY="split"
DEFAULT_DIR="tools"
DEFAULT_GO_VERSION="inherit"
DEFAULT_MODULE_PREFIX=""

# Minimum Go version for tool directives. Shared by _require_go and the
# doctor command's Go check — change in one place.
MIN_GO_VERSION="1.24"

# In-memory tool store. Each line is: name|source|package|version
# Populated by _manifest_parse() and mutated by _manifest_tool_set/remove.
# Flushed to .gotools.json by _manifest_flush().
_MANIFEST_TOOLS=""

# Dry-run mode — set by _parse_dry_run(). When true, destructive commands
# print what they would do but make no changes.
_DRY_RUN=false

# Offline mode — set via the --offline flag or GOTOOLS_OFFLINE=1. When true,
# commands refuse to do anything that would need the network: sync only takes
# the fingerprint fast path, install/upgrade refuse outright.
_OFFLINE=false

# Verbose mode — set via GOTOOLS_VERBOSE=1 env var or --verbose flag.
# When true, print every go command before executing it.
_VERBOSE="${GOTOOLS_VERBOSE:-0}"

# Trace mode — set via GOTOOLS_TRACE=1 env var.
# When true, log each tool execution as a JSON Lines record to
# .gotools_trace.log in the project root. Each record includes a
# "level" field: "info" (exit 0), "error" (tool ran but failed),
# or "fatal" (gotools itself errored before executing the tool).
_TRACE="${GOTOOLS_TRACE:-0}"

# Output format — set by _parse_output_format(). "text" unless a --format/
# --json/--text flag selects json. The helper resets it before scanning so
# repeated in-process calls (unit tests source the bundle) never leak state.
_OUTPUT_FORMAT="text"

# _PROJECT_ROOT — set by _find_project_root(). The directory containing
# .gotools.json, found by walking up from $PWD (like git finds .git).
# If not found, defaults to $PWD. Commands operate relative to this root.
_PROJECT_ROOT=""

# _find_project_root — walk up the directory tree looking for .gotools.json.
# If found, cd to that directory and set _PROJECT_ROOT. Otherwise, stay in $PWD.



_find_project_root() {
    local dir="$PWD"
    while [[ "$dir" != "/" ]]; do
        if [[ -f "$dir/$MANIFEST_FILE" ]]; then
            _PROJECT_ROOT="$dir"
            cd "$dir" 2>/dev/null || true
            return 0
        fi
        dir="$(dirname "$dir")"
    done
    # Check root directory too
    if [[ -f "/$MANIFEST_FILE" ]]; then
        _PROJECT_ROOT="/"
        cd "/" 2>/dev/null || true
        return 0
    fi
    _PROJECT_ROOT="$PWD"
    return 0
}

# Capture the original environment once at startup, before any load_config
# call sets these variables in the current shell. This is the only reliable
# way to distinguish "user passed GOTOOLS_DIR=x ./gotools ..." from
# "load_config already ran and set GOTOOLS_DIR earlier in this process".
_ORIG_ENV_STRATEGY="${GOTOOLS_STRATEGY:-}"
_ORIG_ENV_DIR="${GOTOOLS_DIR:-}"
_ORIG_ENV_GO_VERSION="${GOTOOLS_GO_VERSION:-}"
_ORIG_ENV_MODULE_PREFIX="${GOTOOLS_MODULE_PREFIX:-}"

# Runtime settings — startup env > manifest "settings" block > defaults
# (resolved in load_config). Captured once at startup, like the layout
# keys, so an explicit GOTOOLS_X=0 in the environment can override a
# manifest setting that is "true". Flags (e.g. --offline) always win.
_ORIG_ENV_OFFLINE="${GOTOOLS_OFFLINE:-}"
_ORIG_ENV_JOBS="${GOTOOLS_JOBS:-}"
_ORIG_ENV_OPERATION_TIMEOUT="${GOTOOLS_OPERATION_TIMEOUT:-}"
_ORIG_ENV_LOCK_TIMEOUT="${GOTOOLS_LOCK_TIMEOUT:-}"
_ORIG_ENV_LOCK_STALE_TIMEOUT="${GOTOOLS_LOCK_STALE_TIMEOUT:-}"
_ORIG_ENV_NO_LOCK="${GOTOOLS_NO_LOCK:-}"
_ORIG_ENV_TRACE="${GOTOOLS_TRACE:-}"
_ORIG_ENV_VERBOSE="${GOTOOLS_VERBOSE:-}"

# Resolved settings globals (set by load_config; consumed by the code).
_JOBS=1
_OPERATION_TIMEOUT=120
_LOCK_TIMEOUT=10
_LOCK_STALE_TIMEOUT=300
_NO_LOCK=0

_CONFIG_LOADED=false

# ---------------------------------------------------------------------------
# Config helpers
# ---------------------------------------------------------------------------

load_config() {
    if [[ "$_CONFIG_LOADED" == "true" ]]; then
        return
    fi

    # find project root by walking up from $PWD.
    _find_project_root

    # Drop stray temp files from a manifest write that crashed mid-flight.
    _manifest_flush_cleanup

    if [[ -f "$MANIFEST_FILE" ]]; then
        # Fail loudly on manifests written by a newer gotools.
        _manifest_check_version
        _manifest_parse "$MANIFEST_FILE"
    fi

    # Startup env overrides > config file > defaults.
    # Need to re-resolve after parsing because the manifest/env may have set these
    # to empty strings, and we need defaults to kick in for empty values.
    GOTOOLS_STRATEGY="${_ORIG_ENV_STRATEGY:-${GOTOOLS_STRATEGY:-$DEFAULT_STRATEGY}}"
    GOTOOLS_DIR="${_ORIG_ENV_DIR:-${GOTOOLS_DIR:-$DEFAULT_DIR}}"
    GOTOOLS_GO_VERSION="${_ORIG_ENV_GO_VERSION:-${GOTOOLS_GO_VERSION:-$DEFAULT_GO_VERSION}}"
    GOTOOLS_MODULE_PREFIX="${_ORIG_ENV_MODULE_PREFIX:-${GOTOOLS_MODULE_PREFIX:-$DEFAULT_MODULE_PREFIX}}"

    # Security: the tools dir feeds rm -rf/mkdir -p/cd in every command. A
    # hostile or mistaken manifest/env must never redirect those outside the
    # project, so refuse absolute paths, ".." components, and symlinks here —
    # before any command can touch the filesystem.
    if ! _validate_tools_dir "${GOTOOLS_DIR:-}"; then
        echo "   Fix the 'dir' field in $MANIFEST_FILE or unset GOTOOLS_DIR." >&2
        exit $E_ENVIRONMENT
    fi
    if [[ -L "${GOTOOLS_DIR:-}" ]]; then
        echo "❌ Tools directory must not be a symlink: $GOTOOLS_DIR" >&2
        echo "   Remove the symlink and retry." >&2
        exit $E_ENVIRONMENT
    fi

    # Normalize: treat empty module_prefix as unset so auto-detection kicks in
    # in resolve_module_prefix.
    [[ -n "${GOTOOLS_MODULE_PREFIX:-}" ]] || GOTOOLS_MODULE_PREFIX=""

    # Runtime settings: startup env > manifest "settings" block > defaults.
    # Flags (e.g. --offline) set _OFFLINE before load_config in some
    # commands — a flag-set true must never be clobbered back to false.
    if ! $_OFFLINE; then
        _OFFLINE=$(_settings_resolve offline) || true
    fi
    _VERBOSE=$(_settings_resolve verbose) || true
    _TRACE=$(_settings_resolve trace) || true
    _JOBS=$(_settings_resolve jobs) || true
    _OPERATION_TIMEOUT=$(_settings_resolve operation_timeout) || true
    _LOCK_TIMEOUT=$(_settings_resolve lock_timeout) || true
    _LOCK_STALE_TIMEOUT=$(_settings_resolve lock_stale_timeout) || true
    _NO_LOCK=$(_settings_resolve no_lock) || true

    _CONFIG_LOADED=true
}

reload_config() {
    _CONFIG_LOADED=false
    load_config
}
