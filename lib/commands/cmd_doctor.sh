# Copyright (c) 2026 Pius Alfred
# License: MIT


# ---------------------------------------------------------------------------
# doctor — systematic environment diagnostics (issue #28)
#
# Seven checks: Go installation, module proxy, configuration, managed tools,
# lock file, module integrity, and disk usage. Read-only: doctor never
# acquires the lock (it inspects it), never writes the manifest, the sync
# fingerprint, or a trace record. Probes set GOTOOLCHAIN=local (no toolchain
# auto-download) and GOPROXY=off in offline mode (fail fast, no network).
#
# Exit code is ALWAYS 0 (pure diagnostics) — invalid flags still exit
# E_USAGE. CI gates on `doctor --format=json` → healthy/issues.
# ---------------------------------------------------------------------------

# Result store: pipe-delimited name|status|detail lines, one per check.
# _doctor_add_result appends; single-result checks go through
# _doctor_run_check (subshell capture), the multi-result tools check runs in
# the main shell and appends directly.
_doctor_add_result() {
    local name="$1" status="$2" detail="$3"
    _DOCTOR_RESULTS+="${name}|${status}|${detail}"$'\n'
}

# _doctor_run_check <name> <check-fn>
#   Runs check-fn in a subshell: it prints a one-line detail to stdout and
#   returns 0=pass, 1=warn, 2=fail, anything else=skip. Check fns must be
#   pure — mutations made inside the subshell are lost. OR-list capture keeps
#   set -e from killing the script on a failing check. Always returns 0.
_doctor_run_check() {
    local name="$1" fn="$2"
    local detail rc=0
    detail=$("$fn") || rc=$?
    local status
    case "$rc" in
        0) status="pass" ;;
        1) status="warn" ;;
        2) status="fail" ;;
        *) status="skip" ;;
    esac
    detail="${detail%%$'\n'*}"
    _doctor_add_result "$name" "$status" "$detail"
}

# ---- check: Go installation ---------------------------------------------
_doctor_check_go() {
    local go_path
    go_path=$(command -v go 2>/dev/null) || { echo "Go not installed. Install Go $MIN_GO_VERSION or higher (https://go.dev/dl/)."; return 2; }
    local ver
    ver=$(_go env GOVERSION 2>/dev/null | sed 's/^go//') || true
    if [[ -z "$ver" ]]; then
        echo "Could not determine Go version."
        return 2
    fi
    if ! _version_meets_min "$ver" "$MIN_GO_VERSION"; then
        echo "Go $ver is too old. Go $MIN_GO_VERSION or higher is required."
        return 2
    fi
    echo "Go $ver at $go_path — meets minimum ($MIN_GO_VERSION+)"
    return 0
}

# ---- check: module proxy -------------------------------------------------
_doctor_check_proxy() {
    command -v go >/dev/null 2>&1 || { echo "go not installed"; return 3; }
    if $_OFFLINE; then echo "offline mode — reachability not checked"; return 3; fi
    local goproxy
    goproxy=$(_go env GOPROXY 2>/dev/null) || { echo "could not read GOPROXY (go env failed)"; return 1; }
    # First declared tool (string slicing — a pipeline could trip pipefail).
    local entry="${_MANIFEST_TOOLS%%$'\n'*}" tname="" src="" pkg="" probe=""
    if [[ -n "$entry" ]]; then
        IFS='|' read -r tname src pkg _ <<< "$entry"
        if [[ "$src" == "go" && -n "$pkg" ]]; then
            # Resolve the module root from the tool's modfile require block
            # (no network): many tool packages are subpackages of a module
            # (golang.org/x/tools/cmd/goimports → golang.org/x/tools), and
            # `go list -m` only accepts module paths. @latest, not @version:
            # the pinned version may be cache-served, but @latest always
            # queries the proxy — a real reachability probe.
            local modfile=""
            case "$GOTOOLS_STRATEGY" in
                unified) modfile="$GOTOOLS_DIR/go.mod" ;;
                split)   modfile="$GOTOOLS_DIR/${tname}.mod" ;;
                module)  modfile="$GOTOOLS_DIR/${tname}/go.mod" ;;
            esac
            if [[ -f "$modfile" ]]; then
                local module_root
                module_root=$(extract_module_for_pkg "$modfile" "$pkg" 2>/dev/null) || true
                [[ -n "$module_root" ]] && probe="${module_root}@latest"
            fi
        fi
    fi
    if [[ -z "$probe" ]]; then
        echo "GOPROXY=${goproxy:-<unset — Go default>} (no tool to probe — reachability not checked)"
        return 0
    fi
    # `go list -m` accepts no -timeout flag, so bound the probe with a 5s
    # watchdog: kill the go process if it outlives the budget.
    local out_file rc=0
    out_file=$(mktemp "${TMPDIR:-/tmp}/gotools-doctor-proxy.XXXXXX") || { echo "could not create probe temp file"; return 1; }
    ( export GOTOOLCHAIN=local; _go list -m "$probe" >"$out_file" 2>&1 ) &
    local pid=$! waited=0
    while kill -0 "$pid" 2>/dev/null; do
        if [[ $waited -ge 10 ]]; then
            kill "$pid" 2>/dev/null
            echo "GOPROXY=$goproxy — unreachable (no answer within 5s)"
            rm -f "$out_file"
            return 1
        fi
        sleep 0.5
        waited=$((waited + 1))
    done
    # `wait` inherits the child's exit status — OR-guard it (set -e).
    wait "$pid" 2>/dev/null || rc=$?
    local first=""
    first=$(head -n1 "$out_file" 2>/dev/null || true)
    rm -f "$out_file"
    if [[ $rc -ne 0 ]]; then
        echo "GOPROXY=$goproxy — unreachable: ${first:-unknown error}"
        return 1
    fi
    echo "GOPROXY=$goproxy — reachable"
    return 0
}

# ---- check: configuration -------------------------------------------------
_doctor_check_config() {
    if [[ $_DOCTOR_BAD_MANIFEST -eq 1 ]]; then
        echo "${_DOCTOR_MANIFEST_REASON:-$MANIFEST_FILE is invalid}"
        return 2
    fi
    if [[ ! -f "$MANIFEST_FILE" ]]; then
        echo "no $MANIFEST_FILE — run 'gotools init'"
        return 2
    fi
    echo "$MANIFEST_FILE exists and is valid (schema v$_MANIFEST_SCHEMA_VERSION)"
    return 0
}

# ---- check: strategy matches disk -----------------------------------------
_doctor_check_strategy() {
    if [[ $_DOCTOR_BAD_MANIFEST -eq 1 || ! -f "$MANIFEST_FILE" ]]; then
        echo "cannot compare strategy — manifest missing or invalid"
        return 3
    fi
    local detected
    detected=$(detect_strategy "$GOTOOLS_DIR" 2>/dev/null) || true
    if [[ ! -d "$GOTOOLS_DIR" || -z "$detected" ]]; then
        echo "tools not synced — run 'gotools sync'"
        return 1
    fi
    if [[ "$detected" != "$GOTOOLS_STRATEGY" ]]; then
        echo "Strategy: $GOTOOLS_STRATEGY — disk looks like $detected (run 'gotools sync' to migrate)"
        return 1
    fi
    echo "Strategy: $GOTOOLS_STRATEGY — matches disk"
    return 0
}

# ---- check: managed tools --------------------------------------------------
# Multi-result: runs in the MAIN shell (subshell captures would lose the
# result-store mutations), so it appends directly via _doctor_add_result.
_doctor_check_tools() {
    local _n _s _p _v count=0
    while IFS='|' read -r _n _s _p _v; do
        [[ -z "$_n" ]] && continue
        count=$((count + 1))
        if [[ "$_s" != "go" ]]; then
            _doctor_add_result "tools.$_n" "skip" "$_n@$_v — source '$_s' not probed"
            continue
        fi
        local reason rc=0
        reason=$(tool_runnable "$_n" 2>/dev/null) || rc=$?
        if [[ $rc -eq 0 ]]; then
            _doctor_add_result "tools.$_n" "pass" "$_n@$_v — runnable"
        else
            _doctor_add_result "tools.$_n" "warn" "$_n@$_v — not runnable: ${reason:-unknown} (run 'gotools sync')"
        fi
    done <<< "$_MANIFEST_TOOLS"
    if [[ $count -eq 0 ]]; then
        _doctor_add_result "tools" "pass" "no tools declared"
    fi
}

# ---- check: lock file ------------------------------------------------------
# stat flags differ per platform: BSD (macOS) uses -f, GNU (Linux) uses -c.
_file_mtime() {
    case "$(uname -s)" in
        Darwin) stat -f %m "$1" ;;
        *)      stat -c %Y "$1" ;;
    esac
}

_doctor_check_lock() {
    local lock_dir="$GOTOOLS_DIR/.gotools.lock"
    [[ -d "$lock_dir" ]] || { echo "no lock detected"; return 0; }
    local now mtime age
    now=$(date +%s)
    mtime=$(_file_mtime "$lock_dir" 2>/dev/null) || true
    if [[ -z "$mtime" ]]; then
        echo "unable to read lock age"
        return 1
    fi
    age=$((now - mtime))
    if [[ $age -gt 300 ]]; then
        echo "stale lock directory (age ${age}s) — remove $lock_dir if no gotools process is running"
        return 1
    fi
    echo "lock is held or was released recently (age ${age}s) — another gotools process may be running"
    return 0
}

# ---- check: module integrity ------------------------------------------------
_doctor_check_integrity() {
    command -v go >/dev/null 2>&1 || { echo "go not installed"; return 3; }
    local out rc=0 count=0
    case "$GOTOOLS_STRATEGY" in
        unified)
            local mf="$GOTOOLS_DIR/go.mod"
            if [[ ! -f "$mf" ]]; then echo "no $mf — run 'gotools sync'"; return 3; fi
            if [[ ! -f "$GOTOOLS_DIR/go.sum" ]]; then echo "missing $GOTOOLS_DIR/go.sum — run 'gotools sync'"; return 2; fi
            out=$( (export GOTOOLCHAIN=local; $_OFFLINE && export GOPROXY=off; cd "$GOTOOLS_DIR" && _go mod verify) 2>&1 ) || rc=$?
            if [[ $rc -ne 0 ]]; then
                echo "go mod verify failed for $mf: ${out%%$'\n'*}"
                return 2
            fi
            echo "go mod verify passed for all 1 modules"
            return 0
            ;;
        split)
            local f name sumf
            for f in "$GOTOOLS_DIR"/*.mod; do
                [[ -f "$f" ]] || continue
                count=$((count + 1))
                name=$(basename "$f" .mod)
                sumf="$GOTOOLS_DIR/${name}.sum"
                if [[ ! -f "$sumf" ]]; then
                    echo "missing $sumf — run 'gotools sync'"
                    return 2
                fi
                out=$( (export GOTOOLCHAIN=local; $_OFFLINE && export GOPROXY=off; _go mod verify -modfile="$f") 2>&1 ) || rc=$?
                if [[ $rc -ne 0 ]] && [[ "$out" == *"flag provided but not defined"* ]]; then
                    # Older go: `go mod verify` may not know -modfile — retry
                    # through GOFLAGS, which every go command honors.
                    rc=0
                    out=$( (export GOTOOLCHAIN=local GOFLAGS="-modfile=$f"; $_OFFLINE && export GOPROXY=off; _go mod verify) 2>&1 ) || rc=$?
                fi
                if [[ $rc -ne 0 ]]; then
                    echo "go mod verify failed for $f: ${out%%$'\n'*}"
                    return 2
                fi
            done
            if [[ $count -eq 0 ]]; then echo "no tool modfiles in $GOTOOLS_DIR — run 'gotools sync'"; return 3; fi
            echo "go mod verify passed for all $count modules"
            return 0
            ;;
        module)
            local d mf2
            for d in "$GOTOOLS_DIR"/*/; do
                [[ -d "$d" ]] || continue
                mf2="$d/go.mod"
                [[ -f "$mf2" ]] || continue
                count=$((count + 1))
                if [[ ! -f "$d/go.sum" ]]; then
                    echo "missing ${d}go.sum — run 'gotools sync'"
                    return 2
                fi
                out=$( (export GOTOOLCHAIN=local; $_OFFLINE && export GOPROXY=off; cd "$d" && _go mod verify) 2>&1 ) || rc=$?
                if [[ $rc -ne 0 ]]; then
                    echo "go mod verify failed for $mf2: ${out%%$'\n'*}"
                    return 2
                fi
            done
            if [[ $count -eq 0 ]]; then echo "no tool modules in $GOTOOLS_DIR — run 'gotools sync'"; return 3; fi
            echo "go mod verify passed for all $count modules"
            return 0
            ;;
        *)
            echo "unknown strategy: $GOTOOLS_STRATEGY"
            return 3
            ;;
    esac
}

# ---- check: disk usage -------------------------------------------------------
_doctor_check_disk() {
    [[ -d "$GOTOOLS_DIR" ]] || { echo "tools directory not found"; return 3; }
    local size
    size=$(du -sh "$GOTOOLS_DIR" 2>/dev/null | awk '{print $1}') || { echo "unable to measure disk usage"; return 3; }
    echo "tools/ uses $size"
    return 0
}

# ---- renderers ----------------------------------------------------------------
_doctor_heading() {
    case "$1" in
        go)        echo "Go installation" ;;
        proxy)     echo "Module proxy" ;;
        config)    echo "Configuration" ;;
        tools)     echo "Managed tools" ;;
        lock)      echo "Lock file" ;;
        integrity) echo "Module integrity" ;;
        disk)      echo "Disk usage" ;;
        *)         echo "$1" ;;
    esac
}

_doctor_render_text() {
    local name status detail grp="" issues=0 tool_count=0
    while IFS='|' read -r name _ _; do
        [[ "$name" == tools.* ]] && tool_count=$((tool_count + 1))
    done <<< "$_DOCTOR_RESULTS"
    while IFS='|' read -r name status detail; do
        [[ -n "$name" ]] || continue
        local g="${name%%.*}" icon=""
        if [[ "$g" != "$grp" ]]; then
            if [[ -n "$grp" ]]; then echo ""; fi
            local heading
            heading=$(_doctor_heading "$g")
            if [[ "$g" == "tools" && $tool_count -gt 0 ]]; then
                echo "  $heading ($tool_count declared)"
            else
                echo "  $heading"
            fi
            grp="$g"
        fi
        case "$status" in
            pass) icon="✅" ;;
            warn) icon="⚠️" ;;
            fail) icon="❌" ;;
            skip) icon="⏭" ;;
        esac
        if [[ -n "$detail" ]]; then
            echo "  $icon $detail"
        else
            echo "  $icon"
        fi
        if [[ "$status" == "warn" || "$status" == "fail" ]]; then
            issues=$((issues + 1))
        fi
    done <<< "$_DOCTOR_RESULTS"
    echo ""
    echo "──────────────────────────────────────────────────"
    if [[ $issues -eq 0 ]]; then
        echo "✅ All checks passed. Your environment is healthy."
    elif [[ $issues -eq 1 ]]; then
        echo "⚠️ 1 issue found."
    else
        echo "⚠️ $issues issues found."
    fi
}

_doctor_render_json() {
    local name status detail issues=0
    while IFS='|' read -r name status detail; do
        [[ -n "$name" ]] || continue
        if [[ "$status" == "warn" || "$status" == "fail" ]]; then
            issues=$((issues + 1))
        fi
    done <<< "$_DOCTOR_RESULTS"
    if [[ $issues -eq 0 ]]; then
        printf '{"schema_version":1,"healthy":true,"issues":0,"checks":['
    else
        printf '{"schema_version":1,"healthy":false,"issues":%d,"checks":[' "$issues"
    fi
    local sep=""
    while IFS='|' read -r name status detail; do
        [[ -n "$name" ]] || continue
        printf '%s{"name":"%s","status":"%s"' "$sep" "$(_json_escape "$name")" "$status"
        if [[ -n "$detail" ]]; then
            printf ',"detail":"%s"' "$(_json_escape "$detail")"
        fi
        printf '}'
        sep=","
    done <<< "$_DOCTOR_RESULTS"
    printf ']}\n'
}

# ---- doctor -----------------------------------------------------------------
cmd_doctor() {
    _parse_offline "$@"
    _parse_output_format "$@"
    local arg
    for arg in "$@"; do
        case "$arg" in
            --offline|--json|--text|--format=json|--format=text) ;;
            *)
                echo "❌ Unknown argument: $arg" >&2
                echo "   Usage: $(basename "$0") doctor [--format=json|text] [--offline]" >&2
                exit $E_USAGE
                ;;
        esac
    done

    _DOCTOR_RESULTS=""
    _DOCTOR_BAD_MANIFEST=0
    _DOCTOR_MANIFEST_REASON=""

    # load_config EXITS on a manifest written by a newer gotools — pre-validate
    # and bypass it so doctor can still render a full report.
    _find_project_root
    if [[ -f "$MANIFEST_FILE" ]]; then
        local mrc=0 verr=""
        verr=$(_manifest_validate "$MANIFEST_FILE" 2>&1 >/dev/null) || mrc=$?
        if [[ $mrc -ne 0 ]]; then
            _DOCTOR_BAD_MANIFEST=1
            _DOCTOR_MANIFEST_REASON="${verr%%$'\n'*}"
            # Replicate load_config's env > default precedence without parsing
            # the broken file; mark config loaded so nested calls no-op.
            GOTOOLS_STRATEGY="${_ORIG_ENV_STRATEGY:-$DEFAULT_STRATEGY}"
            GOTOOLS_DIR="${_ORIG_ENV_DIR:-$DEFAULT_DIR}"
            GOTOOLS_GO_VERSION="${_ORIG_ENV_GO_VERSION:-$DEFAULT_GO_VERSION}"
            GOTOOLS_MODULE_PREFIX=""
            _CONFIG_LOADED=true
        else
            load_config
        fi
    else
        load_config
    fi

    if [[ "$_OUTPUT_FORMAT" != "json" ]]; then
        echo "🔍 gotools doctor — checking your environment..."
        echo ""
    fi

    _doctor_run_check "go" _doctor_check_go
    _doctor_run_check "proxy" _doctor_check_proxy
    _doctor_run_check "config" _doctor_check_config
    _doctor_run_check "config.strategy" _doctor_check_strategy
    _doctor_check_tools
    _doctor_run_check "lock" _doctor_check_lock
    _doctor_run_check "integrity" _doctor_check_integrity
    _doctor_run_check "disk" _doctor_check_disk

    if [[ "$_OUTPUT_FORMAT" == "json" ]]; then
        _doctor_render_json
    else
        _doctor_render_text
    fi
    # Always exit 0: doctor is pure diagnostics — check results never fail
    # the shell. Invalid flags exited E_USAGE above. CI gates on
    # `doctor --format=json` → healthy/issues.
}
