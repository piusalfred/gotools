# Copyright (c) 2026 Pius Alfred
# License: MIT


# ---- init ----------------------------------------------------------------



cmd_init() {
    local strategy=$DEFAULT_STRATEGY dir=$DEFAULT_DIR go_v=$DEFAULT_GO_VERSION prefix=""
    for arg in "$@"; do
        case $arg in
            --strategy=*) strategy="${arg#*=}" ;;
            --dir=*)      dir="${arg#*=}" ;;
            --go=*)       go_v="${arg#*=}" ;;
            --prefix=*)   prefix="${arg#*=}" ;;
            *)            echo "❓ Unknown flag: $arg"; usage ;;
        esac
    done

    # Validate strategy.
    case "$strategy" in
        unified|split|module) ;;
        *) echo "❌ Invalid strategy: $strategy (must be unified, split, or module)" >&2; exit $E_USAGE ;;
    esac

    # Set config vars in memory, then flush manifest.
    GOTOOLS_STRATEGY="$strategy"
    GOTOOLS_DIR="$dir"
    GOTOOLS_GO_VERSION="$go_v"
    GOTOOLS_MODULE_PREFIX="$prefix"
    _MANIFEST_TOOLS=""
    _manifest_flush

    mkdir -p "$dir"
    echo "✅ Initialized $MANIFEST_FILE (strategy=$strategy, dir=$dir)"

    # Reload so cmd_sync picks up the new values.
    load_config
    cmd_sync
}
