# Copyright (c) 2026 Pius Alfred
# License: MIT


# ---- config --------------------------------------------------------------



cmd_config() {
    # Load the project config first: _manifest_config_set flushes the whole
    # manifest from in-memory state, so without this a write would reset the
    # strategy to the default and wipe the entire tools list.
    load_config

    if [[ $# -eq 0 ]]; then
        # Show all config.
        if [[ ! -f "$MANIFEST_FILE" ]]; then
            echo "⚠️  No $MANIFEST_FILE found. Run 'init' first."
            return $E_ENVIRONMENT
        fi
        cat "$MANIFEST_FILE"
        return 0
    fi

    # Accept both `config KEY VALUE` and `config KEY=VALUE`.
    if [[ $# -eq 1 && "$1" == *=* ]]; then
        set -- "${1%%=*}" "${1#*=}"
    fi

    local key="$1"

    # Validate key name.
    case "$key" in
        GOTOOLS_STRATEGY|GOTOOLS_DIR|GOTOOLS_GO_VERSION|GOTOOLS_MODULE_PREFIX) ;;
        GOTOOLS_OFFLINE|GOTOOLS_JOBS|GOTOOLS_OPERATION_TIMEOUT|GOTOOLS_LOCK_TIMEOUT) ;;
        GOTOOLS_LOCK_STALE_TIMEOUT|GOTOOLS_NO_LOCK|GOTOOLS_TRACE|GOTOOLS_VERBOSE) ;;
        *) echo "❌ Unknown config key: $key" >&2
           echo "   Valid keys: GOTOOLS_STRATEGY, GOTOOLS_DIR, GOTOOLS_GO_VERSION, GOTOOLS_MODULE_PREFIX," >&2
           echo "               GOTOOLS_OFFLINE, GOTOOLS_JOBS, GOTOOLS_OPERATION_TIMEOUT, GOTOOLS_LOCK_TIMEOUT," >&2
           echo "               GOTOOLS_LOCK_STALE_TIMEOUT, GOTOOLS_NO_LOCK, GOTOOLS_TRACE, GOTOOLS_VERBOSE" >&2
           return $E_USAGE ;;
    esac

    if [[ $# -eq 1 ]]; then
        # Show single key.
        if [[ ! -f "$MANIFEST_FILE" ]]; then
            echo "⚠️  No $MANIFEST_FILE found. Run 'init' first."
            return $E_ENVIRONMENT
        fi
        local val
        val=$(_manifest_config_get "$key")
        if [[ -n "$val" ]]; then
            echo "$val"
        else
            echo "⚠️  $key is not set. (auto-detected from root go.mod when empty)"
        fi
        return 0
    fi

    local value="$2"

    # Validate value for strategy key.
    if [[ "$key" == "GOTOOLS_STRATEGY" ]]; then
        case "$value" in
            unified|split|module) ;;
            *) echo "❌ Invalid strategy: $value (must be unified, split, or module)" >&2; return $E_USAGE ;;
        esac
    fi

    # Validate the tools directory: absolute paths and ".." escapes would
    # redirect every rm/mkdir/cd to outside the project.
    if [[ "$key" == "GOTOOLS_DIR" ]] && ! _validate_tools_dir "$value"; then
        return $E_USAGE
    fi

    # Validate values for the runtime settings.
    case "$key" in
        GOTOOLS_OFFLINE|GOTOOLS_NO_LOCK|GOTOOLS_VERBOSE)
            case "$value" in true|false|0|1) ;; *) echo "❌ Invalid value: $value (must be true or false)" >&2; return $E_USAGE ;; esac
            ;;
        GOTOOLS_TRACE)
            case "$value" in true|false|0|1|stdout) ;; *) echo "❌ Invalid value: $value (must be true, false, or stdout)" >&2; return $E_USAGE ;; esac
            ;;
        GOTOOLS_JOBS|GOTOOLS_OPERATION_TIMEOUT|GOTOOLS_LOCK_TIMEOUT|GOTOOLS_LOCK_STALE_TIMEOUT)
            if ! [[ "$value" =~ ^[0-9]+$ ]]; then
                echo "❌ Invalid value: $value (must be a positive number)" >&2
                return $E_USAGE
            fi
            ;;
    esac

    if [[ ! -f "$MANIFEST_FILE" ]]; then
        # Create a new manifest with this one key set.
        GOTOOLS_STRATEGY="${DEFAULT_STRATEGY}"
        GOTOOLS_DIR="${DEFAULT_DIR}"
        GOTOOLS_GO_VERSION="${DEFAULT_GO_VERSION}"
        GOTOOLS_MODULE_PREFIX="${DEFAULT_MODULE_PREFIX}"
        _MANIFEST_TOOLS=""
        _manifest_config_set "$key" "$value"
        echo "✅ Created $MANIFEST_FILE with $key=$value"
        return 0
    fi

    _manifest_config_set "$key" "$value"
    echo "✅ Set $key=$value"

    # Warn if strategy was changed — sync is needed to migrate tools.
    if [[ "$key" == "GOTOOLS_STRATEGY" ]]; then
        echo "⚠️  Strategy changed. Run 'gotools.sh sync' to migrate the tools directory."
    fi
}
