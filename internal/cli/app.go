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

// Package cli is the gotools command line on urfave/cli v3: an App with one
// entry point, Run(ctx, args), that serves the commands declared in package
// command.
//
// The App turns each command's declaration (name, summary, prose, examples,
// flags) into a urfave/cli command and its help, hands the command a Context
// built from the App's streams, environment and seams, and runs its PreRun,
// Run and PostRun. Nothing here is global, so a test builds its own App with
// in-memory streams and fakes.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"

	urfave "github.com/urfave/cli/v3"

	"github.com/piusalfred/gotools/internal/command"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/gocmd"
)

//nolint:gochecknoinits // urfave/cli's version printer and help template are package-level; see AGENTS.md
func init() {
	// --version and -v are urfave/cli's own flag; only what it prints is ours,
	// so that it reads like the version command: "gotools v1.2.3".
	//nolint:reassign // the only hook urfave/cli offers for what --version prints
	urfave.VersionPrinter = func(cmd *urfave.Command) {
		_, _ = fmt.Fprintf(cmd.Root().Writer, "%s %s\n", command.ProductName, cmd.Root().Version)
	}

	// The only command with subcommands is completion. urfave/cli prints such a
	// command's --help with this template and ignores the command's own, so it
	// is set here to read like every other command's help.
	//nolint:reassign // urfave/cli reads this template from the package, not from the command
	urfave.SubcommandHelpTemplate = command.HelpTemplate
}

// App is the command line. The zero value of every field but Program has a
// default.
type App struct {
	Program string // the name used in usage text, e.g. "gotools"
	Version string

	Stdin          io.Reader // os.Stdin when nil
	Stdout, Stderr io.Writer // os.Stdout and os.Stderr when nil
	Environ        []string  // NAME=value pairs; os.Environ when nil
	WorkDir        string    // os.Getwd when empty
	Go             gocmd.Runner

	// Commands are the commands served; command.All when nil.
	Commands []command.Command

	completing bool
}

func (a *App) commands() []command.Command {
	if a.Commands != nil {
		return a.Commands
	}

	return command.All()
}

// Has reports whether the App serves a command called name, or an alias of it.
func (a *App) Has(name string) bool {
	if name == completionName {
		return true
	}

	return slices.ContainsFunc(a.commands(), func(cmd command.Command) bool {
		return cmd.Build(&command.Context{}).HasName(name)
	})
}

// Run is the entry point. args excludes the program name. A failure comes back
// as an error, usually an *errors.Error carrying the exit code; Report turns it
// into output and that code.
func (a *App) Run(ctx context.Context, args []string) error {
	var unknown string

	a.completing = len(args) > 0 && args[len(args)-1] == completionFlag

	root := a.root(a.context(), &unknown)

	if err := command.RunRoot(ctx, root, append([]string{a.Program}, args...)); err != nil {
		return err
	}

	if unknown != "" {
		return ierrors.New(ierrors.Usage, "Unknown command: %s\n   Run '%s help' for the commands.", unknown, a.Program)
	}

	return nil
}

// Report prints err in the house style (a "❌" line on stderr) and returns the
// exit code: 0 for nil, the error's own code, or 1.
func (a *App) Report(err error) int {
	if err != nil {
		if msg := err.Error(); msg != "" {
			_, _ = fmt.Fprintf(a.stderr(), "❌ %s\n", msg)
		}
	}

	return int(ierrors.CodeOf(err))
}

func (a *App) stdin() io.Reader {
	if a.Stdin != nil {
		return a.Stdin
	}

	return os.Stdin
}

func (a *App) stdout() io.Writer {
	if a.Stdout != nil {
		return a.Stdout
	}

	return os.Stdout
}

func (a *App) stderr() io.Writer {
	if a.Stderr != nil {
		return a.Stderr
	}

	return os.Stderr
}

func (a *App) environ() []string {
	if a.Environ != nil {
		return a.Environ
	}

	return os.Environ()
}

func (a *App) root(c *command.Context, unknown *string) *urfave.Command {
	return &urfave.Command{
		Name:   a.Program,
		Usage:  "Go Tool Manager",
		Reader: a.stdin(), Writer: a.stdout(), ErrWriter: a.stderr(),
		Version:                         a.Version,
		Commands:                        a.urfaveCommands(c),
		EnableShellCompletion:           true,
		ConfigureShellCompletionCommand: a.configureCompletion(c),
		CustomRootCommandHelpTemplate:   rootHelpTemplate,
		Metadata:                        a.rootMetadata(),
		OnUsageError:                    command.UsageError,
		Action:                          a.noCommand(c),
		// urfave/cli would exit 3 here, which gotools uses for network errors.
		CommandNotFound: func(_ context.Context, _ *urfave.Command, name string) { *unknown = name },
		// A failure must reach the caller as an error, not os.Exit.
		ExitErrHandler: func(context.Context, *urfave.Command, error) {},
	}
}

// urfaveCommands builds every command, bound to c.
func (a *App) urfaveCommands(c *command.Context) []*urfave.Command {
	commands := a.commands()
	out := make([]*urfave.Command, 0, len(commands))

	for _, cmd := range commands {
		out = append(out, cmd.Build(c))
	}

	return out
}

// noCommand is what runs when no command is named: data piped to stdin goes to
// the command that takes it (install), and anything else shows the overview and
// is a usage error.
func (a *App) noCommand(c *command.Context) urfave.ActionFunc {
	return func(ctx context.Context, root *urfave.Command) error {
		if root.Args().Present() {
			return ierrors.New(
				ierrors.Usage,
				"Unknown command: %s\n   Run '%s help' for the commands.",
				root.Args().First(),
				a.Program,
			)
		}

		data, ok, err := readPiped(ctx, a.stdin())
		if err != nil {
			return err
		}

		if ok {
			for _, cmd := range a.commands() {
				if p, isPiped := cmd.(command.Piped); isPiped {
					return p.Pipe(ctx, c, data)
				}
			}
		}

		root.Writer = a.stderr()
		_ = urfave.ShowRootCommandHelp(root)

		return ierrors.Wrap(ierrors.Usage, "", nil)
	}
}

// context is the Context a command starts from.
func (a *App) context() *command.Context {
	return &command.Context{
		Stdin:   a.stdin(),
		Stdout:  a.stdout(),
		Stderr:  a.stderr(),
		Environ: a.environ(),
		Program: a.Program,
		Version: a.Version,
		WorkDir: a.WorkDir,
		Go:      a.Go,

		Interactive: isTerminal(a.stdin()),
		Completing:  a.completing,
	}
}
