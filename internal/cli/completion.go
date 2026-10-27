// Copyright (c) 2026 Pius Alfred
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	urfave "github.com/urfave/cli/v3"

	"github.com/piusalfred/gotools/internal/command"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/fsx"
)

// completionName is the name of urfave/cli's shell completion command, which
// the root declares through EnableShellCompletion.
const completionName = "completion"

// completionFlag is the argument urfave/cli's completion scripts append when
// they ask the program for the words to offer.
const completionFlag = "--generate-shell-completion"

// The completion command's help text.
const (
	usageCompletion     = "Print or install shell completions."
	argsUsageCompletion = "<bash|zsh|fish|pwsh|install>"

	// Each paragraph is one line: help wraps it. The indented block is kept as
	// written.
	descriptionCompletion = `Print the completion script of a shell, or install it where the shell loads it from. The scripts ask {prog} for the commands, flags and tool names as you type, so they never go stale.

  source <({prog} completion bash)    # bash: put it in ~/.bashrc
  source <({prog} completion zsh)     # zsh: put it in ~/.zshrc
  {prog} completion fish | source     # fish
  {prog} completion install           # write it where the shell loads it from`
)

// backupSuffix names the copy `completion install` keeps of a script it
// replaces.
const backupSuffix = ".bak"

// The shells `completion install` knows a place for.
const (
	shellBash = "bash"
	shellZsh  = "zsh"
	shellFish = "fish"
	shellPwsh = "pwsh"
)

// installShells are the shells `completion install` knows a place for.
func installShells() []string { return []string{shellBash, shellZsh, shellFish, shellPwsh} }

// shellChoices is installShells as the usage text spells it.
func shellChoices() string { return strings.Join(installShells(), "|") }

// configureCompletion makes urfave/cli's completion command a part of the
// overview and adds `install`, which writes the script where the shell loads it
// from. The scripts themselves are urfave/cli's: they ask the program for the
// commands, flags and tool names as the user types, so they never go stale.
func (a *App) configureCompletion(c *command.Context) func(*urfave.Command) {
	return func(cmd *urfave.Command) {
		cmd.Hidden = false
		cmd.Usage = usageCompletion
		cmd.ArgsUsage = argsUsageCompletion
		cmd.Description = command.Prose(descriptionCompletion, a.Program)
		cmd.CustomHelpTemplate = command.HelpTemplate
		cmd.OnUsageError = nil
		cmd.Metadata = map[string]any{"examples": a.examples(
			"completion bash",
			"completion install",
			"completion install zsh",
		)}

		// Anything but a shell, or install, is a usage error; no argument at all
		// shows the shells.
		cmd.Action = func(_ context.Context, cmd *urfave.Command) error {
			if cmd.Args().Present() {
				return ierrors.New(
					ierrors.Usage,
					"Unknown shell: %s\n   Usage: %s completion %s",
					cmd.Args().First(),
					a.Program,
					argsUsageCompletion,
				)
			}

			urfave.HelpPrinter(cmd.Root().Writer, command.HelpTemplate, cmd)

			return nil
		}

		install := &urfave.Command{
			Name:      "install",
			Usage:     "Write the completion script where the shell loads it from",
			ArgsUsage: "[" + shellChoices() + "]",
			Action:    a.installCompletion(c),
		}

		cmd.Commands = append(cmd.Commands, install)
	}
}

// installCompletion writes the script of the named shell, else the shell in
// $SHELL, to the place that shell loads completions from.
func (a *App) installCompletion(c *command.Context) urfave.ActionFunc {
	return func(ctx context.Context, cmd *urfave.Command) error {
		env := lookup(c.Environ)

		shell, err := a.installShell(cmd.Args().First(), env)
		if err != nil {
			return err
		}

		path := a.completionPath(shell, env)
		if path == "" {
			return ierrors.New(ierrors.Usage, "Usage: %s completion install <%s>", a.Program, shellChoices())
		}

		script, err := a.completionScript(ctx, shell)
		if err != nil {
			return err
		}

		if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return ierrors.New(ierrors.Generic, "Could not create directory: %s", filepath.Dir(path))
		}

		out := a.stdout()

		if err = writeCompletion(out, path, script); err != nil {
			return err
		}

		_, _ = fmt.Fprintf(out, "✅ Completion installed for %s: %s\n", shell, path)
		printActivation(out, shell, path)

		return nil
	}
}

// installShell is the shell to install for: the one named, else the one in
// $SHELL.
func (a *App) installShell(named string, env func(string) string) (string, error) {
	if named != "" {
		return named, nil
	}

	if base := filepath.Base(env("SHELL")); slices.Contains(installShells(), base) {
		return base, nil
	}

	return "", ierrors.New(
		ierrors.Usage,
		"Could not detect shell from $SHELL.\n   Please specify: %s completion install <%s>",
		a.Program,
		shellChoices(),
	)
}

// writeCompletion writes script to path and says what it did. A different
// script already there is kept next to it under backupSuffix.
func writeCompletion(out io.Writer, path, script string) error {
	switch previous, err := os.ReadFile(path); {
	case err != nil:
		_, _ = fmt.Fprintf(out, "✅ Installing completion to %s\n", path)
	case string(previous) == script:
		_, _ = fmt.Fprintf(out, "📝 Completion at %s is already up to date\n", path)
	default:
		// What was there may have been edited by hand: keep it.
		if err = fsx.WriteFileAtomic(path+backupSuffix, previous, 0o644); err != nil {
			return ierrors.New(ierrors.Generic, "Could not back up %s: %v", path, err)
		}

		_, _ = fmt.Fprintf(
			out,
			"📝 Updating existing completion at %s (the previous one is kept as %s%s)\n",
			path,
			filepath.Base(path),
			backupSuffix,
		)
	}

	if err := fsx.WriteFileAtomic(path, []byte(script), 0o644); err != nil {
		return ierrors.New(ierrors.Generic, "Could not write %s: %v", path, err)
	}

	return nil
}

// printActivation tells the user what their shell needs besides the file.
func printActivation(out io.Writer, shell, path string) {
	switch shell {
	case shellBash:
		_, _ = fmt.Fprintf(out, "\n💡 To activate, add this to your ~/.bashrc:\n   source %q\n", path)
	case shellZsh:
		_, _ = fmt.Fprint(
			out,
			"\n💡 To activate, add this to your ~/.zshrc:\n   fpath=($HOME/.zsh/completions $fpath)\n   autoload -Uz compinit && compinit\n",
		)
	case shellPwsh:
		_, _ = fmt.Fprintf(
			out,
			"\n💡 To activate, add this line to your PowerShell profile ($PROFILE):\n   . %q\n",
			path,
		)
	case shellFish:
		_, _ = fmt.Fprintf(
			out,
			"\n💡 Fish auto-loads completions from ~/.config/fish/completions/\n   No further setup needed. Restart your shell or run:\n   source %q\n",
			path,
		)
	}
}

// completionScript is what `completion <shell>` prints, obtained by running that
// command on a root of its own, so the script is urfave/cli's and nothing here
// reproduces it.
func (a *App) completionScript(ctx context.Context, shell string) (string, error) {
	var out bytes.Buffer

	inner := *a
	inner.Stdout, inner.Stderr, inner.Stdin = &out, io.Discard, strings.NewReader("")

	// urfave/cli finds a command's parent through a value in the context, so the
	// inner root runs on a context that has the cancellation of ctx and none of
	// its values.
	innerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer context.AfterFunc(ctx, cancel)()

	var unknown string

	root := inner.root(inner.context(), &unknown)

	if err := command.RunRoot(innerCtx, root, []string{a.Program, completionName, shell}); err != nil {
		return "", err
	}

	return out.String(), nil
}

// completionPath is where shell loads its completion for this program from, or
// "" when the shell has no such place.
func (a *App) completionPath(shell string, env func(string) string) string {
	home := env("HOME")

	switch shell {
	case shellBash:
		if d := env("BASH_COMPLETION_USER_DIR"); d != "" {
			return filepath.Join(d, "completions", a.Program)
		}

		if d := env("XDG_DATA_HOME"); d != "" {
			return filepath.Join(d, "bash-completion", "completions", a.Program)
		}

		return filepath.Join(home, ".local/share/bash-completion/completions", a.Program)
	case shellZsh:
		return filepath.Join(home, ".zsh/completions", "_"+a.Program)
	case shellFish:
		return filepath.Join(home, ".config/fish/completions", a.Program+".fish")
	case shellPwsh:
		// The script registers itself under the name of the file that holds it.
		config := env("XDG_CONFIG_HOME")
		if config == "" {
			config = filepath.Join(home, ".config")
		}

		return filepath.Join(config, "powershell/completions", a.Program+".ps1")
	}

	return ""
}

// lookup is os.Getenv over a list of NAME=value pairs.
func lookup(environ []string) func(string) string {
	return func(name string) string {
		for _, e := range slices.Backward(environ) {
			if v, ok := strings.CutPrefix(e, name+"="); ok {
				return v
			}
		}

		return ""
	}
}
