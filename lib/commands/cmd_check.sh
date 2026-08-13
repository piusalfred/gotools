# Copyright (c) 2026 Pius Alfred
# License: MIT


# ---- check ----------------------------------------------------------------



cmd_check() {
    _require_go
    load_config
    local ok=0 fail=0
    echo "🔍 Checking managed tools..."
    echo ""
    while IFS='|' read -r _n _s _p _v; do
        [[ -z "$_n" ]] && continue
        local status="✅"
        if ! "$0" exec "$_n" --version >/dev/null 2>&1; then
            if ! "$0" exec "$_n" --help >/dev/null 2>&1; then
                if ! "$0" exec "$_n" version >/dev/null 2>&1; then
                    status="❌"
                fi
            fi
        fi
        if [[ "$status" == "✅" ]]; then
            echo "  ✅ $_n ($_p@$_v)"
            ok=$((ok + 1))
        else
            echo "  ❌ $_n ($_p@$_v) — not runnable"
            fail=$((fail + 1))
        fi
    done <<< "$_MANIFEST_TOOLS"
    echo ""
    echo "  $ok passed, $fail failed"
    [[ $fail -eq 0 ]] || return $E_TOOL_NOT_FOUND
}
