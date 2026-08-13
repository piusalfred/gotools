# Copyright (c) 2026 Pius Alfred
# License: MIT


# ---- test (signal / cancellation helper) ---------------------------------



cmd_test() {
    if [[ $# -eq 0 ]]; then
        echo "❌ Usage: $(basename "$0") test <seconds>" >&2
        exit $E_USAGE
    fi

    local seconds="$1"

    # Validate that the argument is a positive number.
    if ! [[ "$seconds" =~ ^[0-9]+(\.[0-9]+)?$ ]] || [[ "$seconds" == "0" ]]; then
        echo "❌ Error: <seconds> must be a positive number, got '$seconds'" >&2
        exit $E_USAGE
    fi

    # Trap SIGINT and SIGTERM so we can report the signal before exiting.
    trap 'echo ""; echo "⚡ Caught SIGINT (Ctrl-C) — exiting."; exit 130' INT
    trap 'echo ""; echo "⚡ Caught SIGTERM — exiting."; exit 143' TERM

    echo "⏳ Sleeping for ${seconds}s — press Ctrl-C to test signal handling..."

    local elapsed=0
    while (( $(echo "$elapsed < $seconds" | bc -l) )); do
        sleep 1 &
        wait $! 2>/dev/null || true  # wait on bg sleep so traps fire immediately
        elapsed=$(echo "$elapsed + 1" | bc -l)
        printf "\r  ⏱  %g / %s seconds" "$elapsed" "$seconds"
    done

    echo ""
    echo "✅ Finished — no interruption."
}
