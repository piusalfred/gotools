# Copyright (c) 2026 Pius Alfred
# License: MIT


# _stdin_install — read "go install <tool>" lines from stdin and install each.
# Strips the "go install " prefix, trims whitespace, and delegates to
# cmd_install for each non-empty line. Lines without the prefix are passed
# through as-is, so bare "pkg@version" also works.



_stdin_install() {
    local line count=0
    while IFS= read -r line || [[ -n "$line" ]]; do
        # Trim leading / trailing whitespace
        line="${line#"${line%%[![:space:]]*}"}"
        line="${line%"${line##*[![:space:]]}"}"
        [[ -z "$line" ]] && continue

        # Strip "go install " prefix if present
        if [[ "$line" == "go install "* ]]; then
            line="${line#go install }"
        fi

        # Trim again after stripping the prefix
        line="${line#"${line%%[![:space:]]*}"}"
        [[ -z "$line" ]] && continue

        # shellcheck disable=SC2086
        cmd_install $line
        count=$((count + 1))
    done </dev/stdin
    if [[ $count -eq 0 ]]; then
        echo "❌ No tool specifications found on stdin." >&2
        echo "   Pipe one or more lines such as: go install <package>@<version>" >&2
        exit $E_USAGE
    fi
}

# ---------------------------------------------------------------------------
# Main dispatch — only runs when executed as a script, not when sourced.
# Uses the standard `return 2>/dev/null` trick: return succeeds only inside
# a sourced script (or function), fails at the top level of an executed script.
# This correctly handles direct execution, `bash -c` (Go wrapper), and source.
# ---------------------------------------------------------------------------
if ! (return 0 2>/dev/null); then
    if [[ $# -lt 1 ]]; then
        # When stdin is not a terminal AND has data waiting, read
        # "go install <tool>" lines from it.  Otherwise show usage.
        if [[ ! -t 0 ]]; then
            _first_byte=""
            _first_byte=$(dd bs=1 count=1 2>/dev/null)
            if [[ -n "$_first_byte" ]]; then
                {
                    printf '%s' "$_first_byte"
                    cat
                } | _stdin_install
                exit ${PIPESTATUS[0]}
            fi
        fi
        usage
    fi
    action="$1"
    shift

    # If the first argument after the command is --help or -h,
    # show per-command help instead of dispatching normally.
    if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
        _cmd_help "$action"
        exit 0
    fi

    case "$action" in
        init)                     cmd_init "$@" ;;
        install)                  cmd_install "$@" ;;
        sync)                     cmd_sync "$@" ;;
        exec)                     cmd_exec "$@" ;;
        list)                     cmd_list "$@" ;;
        upgrade|update)           cmd_upgrade "$@" ;;
        remove)                   cmd_remove "$@" ;;
        info)                     cmd_info "$@" ;;
        migrate)                  cmd_migrate "$@" ;;
        config)                   cmd_config "$@" ;;
        purge)                    cmd_purge "$@" ;;
        check)                    cmd_check ;;
        completion)               cmd_completion "$@" ;;
        version)                  cmd_version ;;
        self-update|self-upgrade) cmd_self_update ;;
        uninstall)                cmd_uninstall ;;
        test)                     cmd_test "$@" ;;
        help|--help|-h)           usage ;;
        *)                        echo "❌ Unknown command: $action" >&2; usage ;;
    esac
fi
