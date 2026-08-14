# Copyright (c) 2026 Pius Alfred
# License: MIT


# _cmd_help <command> — print per-command help.



_cmd_help() {
    local cmd="$1"
    case "$cmd" in
        init)
            echo "Usage: gotools.sh init [flags]"
            echo ""
            echo "  Bootstrap the project with a .gotools.json manifest."
            echo "  If the root go.mod declares tools via 'go get -tool', they are"
            echo "  adopted automatically (installed into gotools, directives stripped,"
            echo "  go.mod tidied) — unless --no-migrate is given."
            echo ""
            echo "  Flags:"
            echo "    --strategy=unified|split|module   Isolation strategy (default: split)"
            echo "    --dir=<path>                      Tools directory (default: tools)"
            echo "    --go=<version|inherit>            Go version for tools (default: inherit)"
            echo "    --prefix=<module-path>            Module prefix (default: auto from go.mod)"
            echo "    --no-migrate                      Leave the root go.mod untouched"
            echo "    --dry-run                         Print the plan without writing anything"
            echo ""
            echo "  Examples:"
            echo "    gotools.sh init"
            echo "    gotools.sh init --strategy=module --dir=.tools"
            echo "    gotools.sh init --go=1.24 --prefix=github.com/me/repo"
            ;;
        install)
            echo "Usage: gotools.sh install [name] <package@version> [--force]"
            echo ""
            echo "  Install a Go tool via 'go get -tool'. If only a package is given,"
            echo "  the tool name is inferred from the package path."
            echo ""
            echo "  Tool aliases: You can give the tool a custom name (alias). The"
            echo "  alias is used in gotools.sh (list, info, remove, exec, upgrade)"
            echo "  but the underlying Go binary name stays the same."
            echo ""
            echo "  Examples:"
            echo "    gotools.sh install golang.org/x/tools/cmd/goimports@latest"
            echo "    gotools.sh install mylint golang.org/x/tools/cmd/goimports@v0.38.0"
            echo "    gotools.sh exec mylint -w .   # runs goimports binary"
            echo ""
            echo "  Pipe mode — feed 'go install' commands via stdin:"
            echo "    echo 'go install pkg@latest' | gotools.sh       # pipe"
            echo "    gotools.sh <<< 'go install pkg@latest'          # herestring"
            echo "    gotools.sh <<EOF                               # heredoc"
            echo "    go install pkg1@v1"
            echo "    go install pkg2@v2"
            echo "    EOF"
            echo "    gotools.sh < tools.txt                         # file redirect"
            echo "    # The 'go install' prefix is optional — bare pkg@version works too"
            echo ""
            echo "  Options:"
            echo "    --force   Overwrite an existing tool with the same name"
            ;;
        sync)
            echo "Usage: gotools.sh sync [--dry-run]"
            echo ""
            echo "  Sync tool state to match .gotools.json. Tidies existing modfiles"
            echo "  and reinstalls any tools from the manifest that are missing on disk"
            echo "  or whose installed version drifted from the manifest."
            echo ""
            echo "  If the on-disk strategy doesn't match the manifest, auto-migrates."
            echo ""
            echo "  Fast path: when nothing changed, a state fingerprint in"
            echo "  \$GOTOOLS_DIR/.gotools.fingerprint skips the reconciliation"
            echo "  entirely. Commit that file so fresh clones are fast too."
            echo ""
            echo "  Options:"
            echo "    --dry-run   Show what would be done without making changes"
            ;;
        exec)
            echo "Usage: gotools.sh exec <tool-name> [args...]"
            echo ""
            echo "  Run a managed tool with the given arguments. The tool name must"
            echo "  match a name in 'gotools.sh list'. If you installed a tool with an"
            echo "  alias, use that alias here."
            echo ""
            echo "  Examples:"
            echo "    gotools.sh exec gofumpt -w -extra ."
            echo "    gotools.sh exec golangci-lint run ./..."
            ;;
        list)
            echo "Usage: gotools.sh list [--json]"
            echo ""
            echo "  List all managed tools with their versions and strategy info."
            echo ""
            echo "  Options:"
            echo "    --json   Output as JSON array for scripting/CI"
            ;;
        info)
            echo "Usage: gotools.sh info <tool-name> [--json]"
            echo ""
            echo "  Show detailed information about a specific tool."
            echo ""
            echo "  Options:"
            echo "    --json   Output as JSON object for scripting/CI"
            ;;
        upgrade)
            echo "Usage: gotools.sh upgrade <name|all> [--dry-run]"
            echo ""
            echo "  Upgrade tools to their latest versions."
            echo ""
            echo "  Options:"
            echo "    --dry-run   Show what would be upgraded without making changes"
            ;;
        remove)
            echo "Usage: gotools.sh remove <name1> [name2...] [--dry-run]"
            echo ""
            echo "  Remove specific tools from the project."
            echo ""
            echo "  Options:"
            echo "    --dry-run   Show what would be removed without making changes"
            ;;
        migrate)
            echo "Usage: gotools.sh migrate <unified|split|module> [--dry-run]"
            echo ""
            echo "  Migrate all tools to a different isolation strategy."
            echo "  Preserves exact pinned versions across the migration."
            echo ""
            echo "  Options:"
            echo "    --dry-run   Show what would be migrated without making changes"
            ;;
        config)
            echo "Usage: gotools.sh config [key [value]]"
            echo ""
            echo "  View or edit .gotools.json configuration."
            echo ""
            echo "  No args:    Show the full manifest (cat .gotools.json)"
            echo "  One arg:    Show the value of <key>"
            echo "  Two args:   Set <key>=<value>"
            echo ""
            echo "  Valid keys: GOTOOLS_STRATEGY, GOTOOLS_DIR,"
            echo "              GOTOOLS_GO_VERSION, GOTOOLS_MODULE_PREFIX"
            ;;
        purge)
            echo "Usage: gotools.sh purge [--restore] [--dry-run]"
            echo ""
            echo "  Remove all tools and the .gotools.json manifest."
            echo "  With --restore, the managed tools are added back to the root"
            echo "  go.mod at their pinned versions (go get -tool) before the wipe."
            echo "  Interactive: requires typing YES to confirm."
            echo ""
            echo "  Options:"
            echo "    --restore   Put the managed tools back into the root go.mod"
            echo "    --dry-run   Show what would be removed without deleting"
            ;;
        check)
            echo "Usage: gotools.sh check"
            echo ""
            echo "  Verify all managed tools are installed and runnable."
            echo "  Runs each tool with --help or --version and reports pass/fail."
            ;;
        version)
            echo "Usage: gotools.sh version"
            echo ""
            echo "  Print the gotools.sh version."
            ;;
        self-update)
            echo "Usage: gotools.sh self-update"
            echo ""
            echo "  Update gotools.sh to the latest release from GitHub."
            echo "  Verifies the release SHA-256 checksum before installing."
            ;;
        uninstall)
            echo "Usage: gotools.sh uninstall"
            echo ""
            echo "  Remove gotools.sh itself from your system."
            ;;
        *)
            usage
            ;;
    esac
}

usage() {
    cat <<EOF
🧰 Go Tool Manager (Version: $VERSION)

Usage: $(basename "$0") <command> [arguments]
       <go install command> | $(basename "$0")     (pipe mode)

Commands:
  init [flags]            Bootstrap the project and adopt tools
                            already declared in the root go.mod.
                            --strategy=unified|split|module  (default: $DEFAULT_STRATEGY)
                            --dir=<tools-dir>                     (default: $DEFAULT_DIR)
                            --go=<version|inherit>                (default: $DEFAULT_GO_VERSION)
                            --prefix=<module-prefix|auto>         (default: auto from root go.mod)
                            --no-migrate  --dry-run
  install [name] <pkg>    Install a new tool.
                            If only <pkg> is given, name is inferred from its basename.
  sync [--dry-run]        Sync tool state to match $MANIFEST_FILE.
  exec <name> [args]      Run a managed tool.
  list [--json]           List tools, versions, and strategies.
  upgrade <name|all> [--dry-run]  Upgrade tools to @latest.
  remove <name...> [--dry-run]    Remove specific tools.
  migrate <strategy> [--dry-run]  Migrate to a different strategy.
  config [key [value]]    View or edit $MANIFEST_FILE configuration.
                            No args: show all config.
                            One arg: show value of <key>.
                            Two args: set <key>=<value>.
  purge [--restore] [--dry-run]  Remove all tools and the $MANIFEST_FILE file;
                            --restore puts the tools back into your go.mod.
  info <name> [--json]    Show detailed information about a specific tool.
  check                   Verify all managed tools are runnable.
  version                 Show script version.
  self-update             Update gotools.sh to the latest version.
  uninstall               Remove this script from your system.
  test <seconds>          Sleep for <seconds> (useful for testing Ctrl-C / signal handling).

Strategies:
  unified     One shared tools/go.mod with all tool directives.
  split       Flat files: tools/<name>.mod and tools/<name>.sum per tool.
  module      Dedicated subdirectories: tools/<name>/go.mod per tool.

Examples:
  gotools.sh init --strategy=module --dir=tools
  gotools.sh install staticcheck honnef.co/go/tools/cmd/staticcheck@latest
  gotools.sh install golang.org/x/tools/cmd/goimports@latest
  gotools.sh exec goimports -w .
  gotools.sh migrate unified
  gotools.sh upgrade all
  gotools.sh remove staticcheck goimports
  gotools.sh config
  gotools.sh config GOTOOLS_STRATEGY
  gotools.sh config GOTOOLS_STRATEGY module
  gotools.sh purge
  gotools.sh uninstall

Exit codes (stable — CI pipelines can key off these):
  $E_GENERIC  Generic failure (catch-all)
  $E_USAGE  Usage error — bad flags, wrong args, invalid input
  $E_NETWORK  Network error — proxy unreachable, DNS failure, timeout
  $E_LOCK  Lock contention — another gotools process is running
  $E_TOOL_NOT_FOUND  Tool not installed — run 'gotools.sh sync' and retry
  $E_OFFLINE  Offline violation — network needed but --offline was set
  $E_POLICY  Policy violation — tool banned, version not pinned
  $E_ENVIRONMENT  Environment error — Go missing/too old, bad manifest
EOF
    exit $E_USAGE
}
