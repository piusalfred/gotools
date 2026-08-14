# Copyright (c) 2026 Pius Alfred
# License: MIT


# ---------------------------------------------------------------------------
# Commands
# ---------------------------------------------------------------------------




cmd_version() {
    _parse_output_format "$@"
    local arg
    for arg in "$@"; do
        case "$arg" in
            --json|--text|--format=json|--format=text) ;;
            *)
                echo "❌ Unknown argument: $arg" >&2
                echo "   Usage: $(basename "$0") version [--format=json|text]" >&2
                exit $E_USAGE
                ;;
        esac
    done

    if [[ "$_OUTPUT_FORMAT" == "json" ]]; then
        printf '{"schema_version":1,"name":"gotools.sh","version":"%s"}\n' "$(_json_escape "$VERSION")"
    else
        echo "gotools.sh $VERSION"
    fi
}
