# Copyright (c) 2026 Pius Alfred
# License: MIT


# _install_failed <name> <output> — report a failed `go get -tool` and exit
# with a structured exit code. Network problems (proxy unreachable, DNS
# failure, timeout) exit $E_NETWORK so CI can retry with backoff; invalid
# package paths / versions are usage errors and exit $E_USAGE; anything
# else exits $E_GENERIC.



_install_failed() {
    local name="$1" output="$2"
    echo "❌ Failed to install $name" >&2
    [[ -n "$output" ]] && echo "$output" >&2
    if echo "$output" | grep -qiE 'dial tcp|i/o timeout|connection timed out|operation timed out|no such host|connection refused|network is unreachable|could not resolve host|fetch failed|timeout exceeded'; then
        exit $E_NETWORK
    fi
    if echo "$output" | grep -qiE 'invalid module version syntax|malformed module path|invalid package path|can.t request version'; then
        exit $E_USAGE
    fi
    exit $E_GENERIC
}

# ---- install -------------------------------------------------------------
cmd_install() {
    _require_go
    load_config
    _acquire_lock

    # Strip --force/--offline from args before parsing name/pkg.
    local force=false filtered=()
    for a in "$@"; do
        if [[ "$a" == "--force" ]]; then force=true
        elif [[ "$a" == "--offline" ]]; then _OFFLINE=true
        else filtered+=("$a"); fi
    done
    _parse_offline "$@"
    # ${filtered[@]+...} keeps the expansion legal when filtered is empty
    # under `set -u` on bash < 4.4 (e.g. plain `gotools.sh install`).
    set -- ${filtered[@]+"${filtered[@]}"}

    local name="" pkg="" _get_out=""
    if [[ $# -eq 0 ]]; then
        echo "❌ Usage: $(basename "$0") install [name] <pkg> [--force]" >&2
        exit $E_USAGE
    elif [[ $# -eq 1 ]]; then
        pkg="$1"
        name=$(infer_binary_name_from_pkg "${pkg%%@*}")
    else
        name="$1"
        pkg="$2"
    fi

    # Installing a new tool needs the module proxy — refuse in offline mode.
    if ${_OFFLINE:-false}; then
        echo "❌ Offline mode: cannot install new tools without network." >&2
        exit $E_OFFLINE
    fi

    local target_v
    target_v=$(resolve_go_version)

    # refuse duplicate tool names unless --force is given.
    if _manifest_tool_exists "$name" && ! $force; then
        echo "❌ Tool '$name' already exists in manifest. Use --force to overwrite." >&2
        exit $E_USAGE
    fi

    echo "📦 Installing $name ($pkg) [strategy=$GOTOOLS_STRATEGY]..."

    local _created_mod=false  # track whether we just created a mod file/dir
    case "$GOTOOLS_STRATEGY" in
        unified)
            mkdir -p "$GOTOOLS_DIR"
            if [[ ! -f "$GOTOOLS_DIR/go.mod" ]]; then
                (cd "$GOTOOLS_DIR" && _go mod init "$(tool_module_path)" && go mod edit -go="$target_v")
            fi
            if ! _get_out=$(cd "$GOTOOLS_DIR" && _go get -tool "$pkg" 2>&1); then
                _install_failed "$name" "$_get_out"
            fi
            ;;

        split)
            mkdir -p "$GOTOOLS_DIR"
            local modfile="${name}.mod"
            if [[ ! -f "$GOTOOLS_DIR/$modfile" ]]; then
                _created_mod=true
                local mod_path
                mod_path=$(tool_module_path "$name")
                cat > "$GOTOOLS_DIR/$modfile" <<MODEOF
module $mod_path

go $target_v
MODEOF
            fi
            if ! _get_out=$(cd "$GOTOOLS_DIR" && _go get -tool -modfile="$modfile" "$pkg" 2>&1); then
                if $_created_mod; then
                    rm -f "$GOTOOLS_DIR/$modfile" "$GOTOOLS_DIR/${modfile%.mod}.sum"
                fi
                _install_failed "$name" "$_get_out"
            fi
            ;;

        module)
            mkdir -p "$GOTOOLS_DIR/$name"
            if [[ ! -f "$GOTOOLS_DIR/$name/go.mod" ]]; then
                _created_mod=true
                (cd "$GOTOOLS_DIR/$name" && _go mod init "$(tool_module_path "$name")" && go mod edit -go="$target_v")
            fi
            if ! _get_out=$(cd "$GOTOOLS_DIR/$name" && _go get -tool "$pkg" 2>&1); then
                if $_created_mod; then
                    rm -rf "${GOTOOLS_DIR:?}/$name"
                fi
                _install_failed "$name" "$_get_out"
            fi
            ;;

        *)
            echo "❌ Unknown strategy: $GOTOOLS_STRATEGY" >&2
            exit $E_ENVIRONMENT
            ;;
    esac

    # Resolve the actual installed version and update manifest.
    local resolved_ver="" pkg_base="${pkg%%@*}"
    case "$GOTOOLS_STRATEGY" in
        unified) resolved_ver=$(_resolve_installed_version "$GOTOOLS_DIR/go.mod" "$pkg_base") ;;
        split)   resolved_ver=$(_resolve_installed_version "$GOTOOLS_DIR/$modfile" "$pkg_base") ;;
        module)  resolved_ver=$(_resolve_installed_version "$GOTOOLS_DIR/$name/go.mod" "$pkg_base") ;;
    esac
    # Verify the binary was actually compiled and is runnable.
    # go get -tool may exit 0 even if compilation fails (the tool directive
    # is written to go.mod before the binary is built).  Run the tool with
    # /dev/null on stdin and check whether the go command itself can find the
    # binary.  A "go: "-prefixed error means the go tool couldn't find/resolve
    # the binary; any other output (or even a non-zero exit from the tool
    # itself) confirms the binary exists and is executable.
    local _verify_binary _verify_err
    case "$GOTOOLS_STRATEGY" in
        unified)
            _verify_binary=$(resolve_binary_name "$name" "$GOTOOLS_DIR/go.mod")
            _verify_err=$(cd "$GOTOOLS_DIR" && _go tool "$_verify_binary" </dev/null 2>&1 >/dev/null) || true
            ;;
        split)
            _verify_binary=$(resolve_binary_name "$name" "$GOTOOLS_DIR/$modfile")
            _verify_err=$(cd "$GOTOOLS_DIR" && _go tool -modfile="$modfile" "$_verify_binary" </dev/null 2>&1 >/dev/null) || true
            ;;
        module)
            _verify_binary=$(resolve_binary_name "$name" "$GOTOOLS_DIR/$name/go.mod")
            _verify_err=$(cd "$GOTOOLS_DIR/$name" && _go tool "$_verify_binary" </dev/null 2>&1 >/dev/null) || true
            ;;
    esac
    if echo "$_verify_err" | grep -qiE '^(go: )?(no such tool|unknown command|tool not found)'; then
        echo "❌ Tool installed but not runnable: $name" >&2
        echo "   go tool error: $_verify_err" >&2
        exit $E_ENVIRONMENT
    fi

    # _INSTALL_NO_FLUSH=1: internal hook used by parallel sync — the parent
    # process merges the manifest after all jobs complete, so individual
    # background jobs must not write it (they would race and drop tools).
    if [[ "${_INSTALL_NO_FLUSH:-0}" != "1" ]]; then
        _manifest_tool_set "$name" "go" "$pkg_base" "${resolved_ver:-latest}"
        _manifest_flush
    fi

    echo "✅ Installed $name"

    # warn when @latest is used — floating versions break reproducibility.
    if [[ "$pkg" == *@latest ]]; then
        echo "⚠️  Installed at @latest (resolved: ${resolved_ver:-latest})."
        echo "   Pin this version for reproducibility:"
        echo "     gotools.sh install $name ${pkg_base}@${resolved_ver:-latest}"
    fi
}
