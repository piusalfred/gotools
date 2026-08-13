# Copyright (c) 2026 Pius Alfred
# License: MIT

VERSION="v0.5.8"
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
readonly E_POLICY=7
readonly E_ENVIRONMENT=8

MANIFEST_FILE=".gotools.json"
DEFAULT_STRATEGY="split"
DEFAULT_DIR="tools"
DEFAULT_GO_VERSION="inherit"
DEFAULT_MODULE_PREFIX=""

# In-memory tool store. Each line is: name|source|package|version
# Populated by _manifest_parse() and mutated by _manifest_tool_set/remove.
# Flushed to .gotools.json by _manifest_flush().
_MANIFEST_TOOLS=""

# Dry-run mode — set by _parse_dry_run(). When true, destructive commands
# print what they would do but make no changes.
_DRY_RUN=false

# Verbose mode — set via GOTOOLS_VERBOSE=1 env var or --verbose flag.
# When true, print every go command before executing it.
_VERBOSE="${GOTOOLS_VERBOSE:-0}"

# Trace mode — set via GOTOOLS_TRACE=1 env var.
# When true, log each tool execution as a JSON Lines record to
# .gotools_trace.log in the project root. Each record includes a
# "level" field: "info" (exit 0), "error" (tool ran but failed),
# or "fatal" (gotools itself errored before executing the tool).
_TRACE="${GOTOOLS_TRACE:-0}"

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

    # Normalize: treat empty module_prefix as unset so auto-detection kicks in
    # in resolve_module_prefix.
    [[ -n "${GOTOOLS_MODULE_PREFIX:-}" ]] || GOTOOLS_MODULE_PREFIX=""

    _CONFIG_LOADED=true
}

reload_config() {
    _CONFIG_LOADED=false
    load_config
}
