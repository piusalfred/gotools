# Copyright (c) 2026 Pius Alfred
# License: MIT


# ---- config --------------------------------------------------------------



cmd_config() {
    if [[ $# -eq 0 ]]; then
        # Show all config.
        if [[ ! -f "$MANIFEST_FILE" ]]; then
            echo "⚠️  No $MANIFEST_FILE found. Run 'init' first."
            return $E_ENVIRONMENT
        fi
        cat "$MANIFEST_FILE"
        return 0
    fi

    local key="$1"

    # Validate key name.
    case "$key" in
        GOTOOLS_STRATEGY|GOTOOLS_DIR|GOTOOLS_GO_VERSION|GOTOOLS_MODULE_PREFIX) ;;
        *) echo "❌ Unknown config key: $key" >&2
           echo "   Valid keys: GOTOOLS_STRATEGY, GOTOOLS_DIR, GOTOOLS_GO_VERSION, GOTOOLS_MODULE_PREFIX" >&2
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
