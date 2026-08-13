# Copyright (c) 2026 Pius Alfred
# License: MIT


# _resolve_installed_version — after go get -tool, read back the version from the
# modfile's require block for the given package.



_resolve_installed_version() {
    local modfile="$1" pkg="$2"
    extract_version_for_pkg "$modfile" "$pkg"
}

# ---------------------------------------------------------------------------
# Parsing helpers for go.mod files
#
# extract_go_version_from_mod <modfile>
#   Prints the Go version from the `go` directive in a go.mod file.
extract_go_version_from_mod() {
    local modfile="$1"
    awk '$1 == "go" { print $2; exit }' "$modfile"
}
# ---------------------------------------------------------------------------

# extract_tools_from_mod <modfile>
#   Prints one package path per line from `tool` directives.
#   Handles both:
#     tool pkg
#     tool (
#       pkg1
#       pkg2
#     )
extract_tools_from_mod() {
    local modfile="$1"
    awk '
        /^tool[[:space:]]+\(/ { in_block=1; next }
        in_block && /^\)/ { in_block=0; next }
        in_block {
            gsub(/^[[:space:]]+/, "")
            gsub(/[[:space:]]+$/, "")
            if ($0 != "") print $0
            next
        }
        $1 == "tool" && $2 != "(" { print $2 }
    ' "$modfile"
}

# extract_version_for_pkg <modfile> <pkg>
#   Prints the version string for <pkg> from the require block(s) of <modfile>.
#   Handles subpackage paths like github.com/foo/bar/v2/cmd/baz by trying
#   progressively shorter prefixes until it finds a match in require.
extract_version_for_pkg() {
    local modfile="$1" pkg="$2"
    local candidate="$pkg"
    while [[ -n "$candidate" ]]; do
        local ver
        ver=$(awk -v p="$candidate" '
            /^require[[:space:]]+\(/ { in_req=1; next }
            in_req && /^\)/ { in_req=0; next }
            in_req {
                gsub(/^[[:space:]]+/, "")
                if ($1 == p) { gsub(/\/\/.*/, "", $2); print $2 }
                next
            }
            $1 == "require" && $2 == p { gsub(/\/\/.*/, "", $3); print $3 }
        ' "$modfile")
        if [[ -n "$ver" ]]; then
            echo "$ver"
            return
        fi
        # Strip the last path component and try again.
        local parent="${candidate%/*}"
        [[ "$parent" == "$candidate" ]] && break
        candidate="$parent"
    done
}

# extract_pkg_from_mod <modfile>
#   Returns the FIRST tool package from a go.mod (convenience for single-tool mods).
extract_pkg_from_mod() {
    extract_tools_from_mod "$1" | head -n1
}

# infer_binary_name_from_pkg <package-path>
#   Converts a Go package path (without @version) into the expected binary name.
#   Examples:
#     github.com/goreleaser/goreleaser/v2           -> goreleaser
#     github.com/goreleaser/goreleaser/v2/cmd/goreleaser -> goreleaser
#     golang.org/x/tools/cmd/goimports              -> goimports
#     honnef.co/go/tools/cmd/staticcheck            -> staticcheck
infer_binary_name_from_pkg() {
    local pkg="$1"
    # Remove version suffix: /v2, /v3, /v10, etc.
    pkg="${pkg%/v[0-9]*}"
    # If it ends with /cmd/<name>, take <name>
    if [[ "$pkg" =~ /cmd/([^/]+)$ ]]; then
        echo "${BASH_REMATCH[1]}"
        return
    fi
    # Fallback to basename
    basename "$pkg"
}

# resolve_binary_name <tool_name> <modfile>
#   Dereferences a tool alias to the actual Go binary name.
#   Reads the tool directive from the modfile and returns the inferred binary name.
#   Falls back to the tool_name itself if no directive is found.
resolve_binary_name() {
    local tool_name="$1" modfile="$2"
    local pkg
    pkg=$(extract_pkg_from_mod "$modfile")
    if [[ -n "$pkg" ]]; then
        infer_binary_name_from_pkg "$pkg"
    else
        echo "$tool_name"
    fi
}
