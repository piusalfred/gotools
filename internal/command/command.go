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

// Package command holds the gotools commands.
//
// A [Command] declares itself to urfave/cli: Build returns the
// *urfave.Command, with its flags, validators, positional arguments, prose and
// examples, and its Before, Action and After steps (written as a [Spec]) bound to the [Context] it
// works on. urfave/cli parses and validates and renders help; the command
// supplies only what is its own. The Context carries the streams, environment
// and seams (go runner, lock, go.mod parser, tool manager), so a test builds a
// Context with in-memory streams and fakes, builds the command, and runs it
// through urfave/cli exactly as the binary does.
package command

import (
	"context"
	"errors"
	"strings"

	urfave "github.com/urfave/cli/v3"

	ierrors "github.com/piusalfred/gotools/internal/errors"
)

// unknownValue is what a table or document shows for a value that cannot be
// determined.
const unknownValue = "unknown"

type Commander struct {
	root     *urfave.Command
	context  *Context
	Commands []Command
}

// Run runs the commander's root command with args, as RunRoot does.
func (c *Commander) Run(ctx context.Context, args []string) error {
	return RunRoot(ctx, c.root, args)
}

// RunRoot runs root with args and returns its failure as an *errors.Error. A
// command's own failure already is one and passes through untouched, keeping
// its exit code. Anything else comes from urfave/cli itself and has no code of
// its own, so it is a generic failure (exit 1) that keeps its message.
func RunRoot(ctx context.Context, root *urfave.Command, args []string) error {
	return withExitCode(root.Run(ctx, args))
}

func withExitCode(err error) error {
	if err == nil {
		return nil
	}

	if _, ok := errors.AsType[*ierrors.Error](err); ok {
		return err
	}

	return ierrors.Wrap(ierrors.Generic, "", err)
}

func (c *Commander) RegisterCommand(cmd Command) {
	c.root.Commands = append(c.root.Commands, cmd.Build(c.context))
}

// Command is one gotools command.
type Command interface {
	// Name is what the user types.
	Name() string
	// Build declares the command to urfave/cli, bound to the Context it works
	// on.
	Build(c *Context) *urfave.Command
}

// Piped is implemented by the one command that handles data piped to gotools
// when no command is named, as in `echo pkg@v | gotools`.
type Piped interface {
	Command
	// Pipe handles the data read from stdin.
	Pipe(ctx context.Context, c *Context, data []byte) error
}

// Step is one step of a command: its Before, Run or After.
type Step func(ctx context.Context, cmd *urfave.Command) error

// Spec is everything a command declares: its names, prose, examples, flags and
// positional arguments, and its behavior as three steps. Build turns it into the
// urfave/cli command, so a command is written as a Spec and nothing else needs
// to know how urfave/cli is wired.
type Spec struct {
	Name      string
	Aliases   []string
	Usage     string // one-line summary, shown in the overview
	ArgsUsage string // positional arguments in the usage line, e.g. "[name] <pkg>"
	// Description is the prose of the help; {prog} stands for the program
	// name. Write each paragraph as one line: help wraps it. A paragraph with
	// an indented line is kept as written.
	Description string
	// Examples are invocations without the program name.
	Examples []string
	Flags    []urfave.Flag
	Args     []urfave.Argument
	// SkipFlagParsing hands every argument to the command untouched, for a
	// command whose arguments belong to another program.
	SkipFlagParsing bool

	// Before checks the request and loads the project, so a bad request stops
	// before anything is touched. Run does the work. After finishes up, and
	// runs only when Run succeeded: urfave/cli's own After runs even when the
	// action failed, which would sync after a failed adoption. Any may be nil.
	Before, Run, After Step
	// Complete offers the values the shell may complete an argument to. Nil
	// leaves urfave/cli's default: the subcommands and the flags.
	Complete urfave.ShellCompleteFunc
}

// Build declares s to urfave/cli, bound to c. Every command gets what they
// share: the help layout, the usage-error mapping, the program name in the
// prose, and prose wrapped for the terminal.
func (s Spec) Build(c *Context) *urfave.Command {
	uc := &urfave.Command{
		Name:               s.Name,
		Aliases:            s.Aliases,
		Usage:              s.Usage,
		ArgsUsage:          s.ArgsUsage,
		Description:        Prose(s.Description, c.Program),
		Flags:              s.Flags,
		Arguments:          s.Args,
		SkipFlagParsing:    s.SkipFlagParsing,
		Metadata:           map[string]any{"examples": prefixed(c.Program, s.Examples)},
		CustomHelpTemplate: helpTemplate,
		OnUsageError:       UsageError,
		ShellComplete:      s.Complete,
	}

	s.bind(uc, c)

	return uc
}

// bind sets the Before, Action and After of uc from the steps.
func (s Spec) bind(uc *urfave.Command, c *Context) {
	ran := false

	if s.Before != nil {
		uc.Before = func(ctx context.Context, cmd *urfave.Command) (context.Context, error) {
			// urfave/cli runs Before while the shell is completing too, but
			// completion only asks for words: it must not load a project,
			// require Go or refuse a half typed request.
			if c.Completing {
				return ctx, nil
			}

			return ctx, s.Before(ctx, cmd)
		}
	}

	uc.Action = func(ctx context.Context, cmd *urfave.Command) error {
		if s.Run != nil {
			if err := s.Run(ctx, cmd); err != nil {
				return err
			}
		}

		ran = true

		return nil
	}

	if s.After != nil {
		uc.After = func(ctx context.Context, cmd *urfave.Command) error {
			if !ran {
				return nil
			}

			return s.After(ctx, cmd)
		}
	}
}

// All lists every command, in overview order.
func All() []Command {
	return []Command{
		NewInit(),
		NewInstall(),
		NewSync(),
		NewMigrate(),
		NewVersion(),
		NewInfo(),
		NewCheck(),
		NewConfig(),
		NewRemove(),
		NewRename(),
		NewUpgrade(),
		NewList(),
		NewExec(),
		NewUninstall(),
		NewPurge(),
		NewDoctor(),
		NewSelfUpdate(),
	}
}

func prefixed(prog string, lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = prog + " " + l
	}

	return out
}

// usageLine is the synopsis help prints after "Usage: ": the command, an
// [options] marker when it declares flags (urfave/cli always adds --help), and
// its arguments. helpTemplate builds the same line.
func usageLine(c *urfave.Command) string {
	line := c.FullName()

	if len(c.VisibleFlags()) > 1 {
		line += " [options]"
	}

	if c.ArgsUsage != "" {
		line += " " + c.ArgsUsage
	}

	return line
}

// UsageError turns a parse or validation failure into a usage error, exit 2.
// Without it urfave/cli prints its own message and the whole help, and a
// caller sees a plain error that exits 1. It is every command's OnUsageError.
func UsageError(_ context.Context, c *urfave.Command, err error, _ bool) error {
	msg := err.Error()

	if name, ok := strings.CutPrefix(msg, "flag provided but not defined: -"); ok {
		dash := "--"
		if len(name) == 1 {
			dash = "-"
		}

		msg = "Unknown argument: " + dash + name
	}

	if strings.HasPrefix(msg, "sufficient count of arg ") {
		msg = "Missing argument"
	}

	return ierrors.New(ierrors.Usage, "%s\n   Usage: %s", msg, usageLine(c))
}

// noExtraArgs rejects positional arguments a command does not take.
func noExtraArgs(c *urfave.Command) error {
	if c.Args().Present() {
		return ierrors.New(ierrors.Usage, "Unknown argument: %s\n   Usage: %s", c.Args().First(), usageLine(c))
	}

	return nil
}

// positional returns the positional arguments for a Before step. urfave/cli
// assigns them to the declared names only after Before has run, so a step that
// checks them first reads them here, and refuses a count outside minArgs..maxArgs
// (maxArgs -1 is any number) as a usage error.
func positional(c *urfave.Command, minArgs, maxArgs int) ([]string, error) {
	args := c.Args().Slice()

	switch {
	case len(args) < minArgs:
		return nil, ierrors.New(ierrors.Usage, "Missing argument\n   Usage: %s", usageLine(c))
	case maxArgs >= 0 && len(args) > maxArgs:
		return nil, ierrors.New(ierrors.Usage, "Unknown argument: %s\n   Usage: %s", args[maxArgs], usageLine(c))
	}

	return args, nil
}
