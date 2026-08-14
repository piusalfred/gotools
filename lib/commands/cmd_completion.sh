# Copyright (c) 2026 Pius Alfred
# License: MIT


# ---- completion ----------------------------------------------------------



_generate_bash_completion() {
    cat <<'COMPLETION'
_gotools_completion() {
    local cur prev words cword
    _init_completion || return
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    if [[ $COMP_CWORD -eq 1 ]]; then
        COMPREPLY=($(compgen -W "init install sync exec list upgrade update remove migrate config purge info check doctor completion version self-update self-upgrade uninstall help" -- "$cur"))
        return
    fi

    case "$prev" in
        exec|info|remove|upgrade)
            local tools
            tools=$(gotools.sh list 2>/dev/null | awk 'NR>2 && $1!="" {print $1}')
            COMPREPLY=($(compgen -W "$tools" -- "$cur"))
            ;;
        migrate)
            COMPREPLY=($(compgen -W "unified split module" -- "$cur"))
            ;;
        config)
            COMPREPLY=($(compgen -W "GOTOOLS_STRATEGY GOTOOLS_DIR GOTOOLS_GO_VERSION GOTOOLS_MODULE_PREFIX" -- "$cur"))
            ;;
        init)
            COMPREPLY=($(compgen -W "--strategy= --dir= --go= --prefix=" -- "$cur"))
            ;;
        install)
            COMPREPLY=($(compgen -W "--force" -- "$cur"))
            ;;
        completion)
            COMPREPLY=($(compgen -W "bash zsh fish install" -- "$cur"))
            ;;
        sync|upgrade|remove|migrate|purge)
            COMPREPLY=($(compgen -W "--dry-run" -- "$cur"))
            ;;
        list|info|version)
            COMPREPLY=($(compgen -W "--format= --json --text" -- "$cur"))
            ;;
        doctor)
            COMPREPLY=($(compgen -W "--format= --json --text --offline" -- "$cur"))
            ;;
    esac
}
complete -F _gotools_completion gotools.sh gotools
COMPLETION
}

_generate_zsh_completion() {
    cat <<'COMPLETION'
#compdef gotools.sh gotools
_gotools() {
    local -a commands
    commands=(init install sync exec list upgrade update remove migrate config purge info check doctor completion version self-update self-upgrade uninstall help)
    _describe 'command' commands
}
_gotools
COMPLETION
}

_generate_fish_completion() {
    echo "complete -c gotools.sh -f"
    echo "complete -c gotools.sh -a 'init install sync exec list upgrade update remove migrate config purge info check doctor completion version self-update self-upgrade uninstall help'"
    echo "complete -c gotools -f"
    echo "complete -c gotools -a 'init install sync exec list upgrade update remove migrate config purge info check doctor completion version self-update self-upgrade uninstall help'"
}

cmd_completion() {
    local shell="${1:-bash}"

    # install subcommand
    if [[ "$shell" == "install" ]]; then
        cmd_completion_install "${2:-}"
        return
    fi

    case "$shell" in
        bash) _generate_bash_completion ;;
        zsh)  _generate_zsh_completion ;;
        fish) _generate_fish_completion ;;
        *)
            echo "Usage: gotools.sh completion <bash|zsh|fish|install>" >&2
            return $E_USAGE
            ;;
    esac
    echo ""
    echo "# To activate:" >&2
    case "$shell" in
        bash) echo "#   source <(gotools.sh completion bash)" >&2 ;;
        zsh)  echo "#   source <(gotools.sh completion zsh)"  >&2 ;;
        fish) echo "#   gotools.sh completion fish | source"   >&2 ;;
    esac
}

cmd_completion_install() {
    local shell="${1:-}"

    # auto-detect shell from $SHELL if not specified
    if [[ -z "$shell" ]]; then
        case "$SHELL" in
            */bash) shell=bash ;;
            */zsh)  shell=zsh ;;
            */fish) shell=fish ;;
            *)
                echo "❌ Error: Could not detect shell from \$SHELL." >&2
                echo "   Please specify: gotools.sh completion install <bash|zsh|fish>" >&2
                return $E_USAGE
                ;;
        esac
    fi

    local install_path
    case "$shell" in
        bash)
            if [[ -n "${BASH_COMPLETION_USER_DIR:-}" ]]; then
                install_path="$BASH_COMPLETION_USER_DIR/completions/gotools"
            elif [[ -n "${XDG_DATA_HOME:-}" ]]; then
                install_path="$XDG_DATA_HOME/bash-completion/completions/gotools"
            else
                install_path="$HOME/.local/share/bash-completion/completions/gotools"
            fi
            ;;
        zsh)
            install_path="$HOME/.zsh/completions/_gotools"
            ;;
        fish)
            install_path="$HOME/.config/fish/completions/gotools.fish"
            ;;
        *)
            echo "Usage: gotools.sh completion install <bash|zsh|fish>" >&2
            return $E_USAGE
            ;;
    esac

    local dir
    dir=$(dirname "$install_path")
    if [[ ! -d "$dir" ]]; then
        mkdir -p "$dir" || {
            echo "❌ Error: Could not create directory: $dir" >&2
            return $E_GENERIC
        }
    fi

    if [[ -f "$install_path" ]]; then
        echo "📝 Updating existing completion at $install_path"
    else
        echo "✅ Installing completion to $install_path"
    fi

    case "$shell" in
        bash) _generate_bash_completion > "$install_path" ;;
        zsh)  _generate_zsh_completion > "$install_path" ;;
        fish) _generate_fish_completion > "$install_path" ;;
    esac

    echo "✅ Completion installed for $shell: $install_path"

    # post-install hints
    case "$shell" in
        bash)
            echo ""
            echo "💡 To activate, add this to your ~/.bashrc:"
            echo "   source \"$install_path\""
            ;;
        zsh)
            echo ""
            echo "💡 To activate, add this to your ~/.zshrc:"
            echo "   fpath=(\$HOME/.zsh/completions \$fpath)"
            echo "   autoload -Uz compinit && compinit"
            ;;
        fish)
            echo ""
            echo "💡 Fish auto-loads completions from ~/.config/fish/completions/"
            echo "   No further setup needed. Restart your shell or run:"
            echo "   source \"$install_path\""
            ;;
    esac
}
