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

package command

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	urfave "github.com/urfave/cli/v3"

	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/fsx"
	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/resolve"
	"github.com/piusalfred/gotools/internal/strategy"
	"github.com/piusalfred/gotools/internal/tracer"
)

// The exec command's name and help text.
const (
	NameExec = "exec"

	usageExec     = "Run a managed tool."
	argsUsageExec = "<name> [args]"

	// Each paragraph is one line: help wraps it.
	descriptionExec = `Run a managed tool with the given arguments. The tool name must match a name in '{prog} list'. If you installed a tool with an alias, use that alias here.`
)

// Exec runs a managed tool.
//
//   - Before checks the request, requires Go and loads the project.
//   - Run has go build the tool (`go tool -n` prints the binary's path), then
//     runs that binary itself, in the directory exec was started from, with
//     gotools' own stdin, stdout and stderr. The tool's exit code is the exit
//     code.
//
// The tool runs where exec was invoked, in every strategy, so `./...`, relative
// paths and config lookups resolve where the user is. Building in the strategy
// directory and running the binary elsewhere also leaves the environment alone
// (no GOWORK=off reaches the tool) and makes the exit status the tool's own.
//
// Everything after the tool's name goes to the tool as it is: exec has no flags
// of its own.
type Exec struct {
	c        *Context
	name     string
	toolArgs []string
	help     bool
}

// NewExec returns the exec command.
func NewExec() *Exec { return &Exec{} }

// Name implements Command.
func (*Exec) Name() string { return NameExec }

// Build implements Command.
func (base *Exec) Build(c *Context) *urfave.Command {
	e := *base
	e.c = c

	return Spec{
		Name:            NameExec,
		Usage:           usageExec,
		ArgsUsage:       argsUsageExec,
		Description:     descriptionExec,
		Examples:        []string{"exec gofumpt -w -extra .", "exec golangci-lint run ./..."},
		SkipFlagParsing: true,
		Complete:        e.c.completeArguments(1, e.c.toolNames),
		Before:          e.before,
		Run:             e.run,
	}.Build(c)
}

func (e *Exec) before(ctx context.Context, cmd *urfave.Command) error {
	args := cmd.Args().Slice()

	// The tool's own flags pass through, so only a leading --help is exec's.
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		e.help = true

		urfave.HelpPrinter(e.c.Stdout, cmd.CustomHelpTemplate, cmd)

		return nil
	}

	args, err := positional(cmd, 1, -1)
	if err != nil {
		return err
	}

	e.name, e.toolArgs = args[0], args[1:]

	return e.c.prepare(ctx)
}

func (e *Exec) run(ctx context.Context, _ *urfave.Command) error {
	if e.help {
		return nil
	}

	c := e.c
	trace := c.Tracer(ctx)

	// fail records the problem in the trace before reporting it.
	fail := func(err error) error {
		trace.Fatal(tracer.Entry{Tool: e.name, Message: err.Error()})

		return err
	}

	if err := strategy.ValidateName(e.name); err != nil {
		return fail(err)
	}

	ref, err := e.resolve()
	if err != nil {
		return fail(err)
	}

	bin, err := e.build(ctx, ref)
	if err != nil {
		return fail(err)
	}

	dir, err := c.workDir()
	if err != nil {
		return fail(err)
	}

	code, err := c.Processes().Run(ctx, gocmd.Process{
		Bin: bin, Args: e.toolArgs, Dir: dir,
		Stdin: c.Stdin, Stdout: c.Stdout, Stderr: c.Stderr,
	})
	if err != nil {
		return fail(ierrors.New(ierrors.Generic, "Could not run %s: %v", ref.Binary, err))
	}

	entry := tracer.Entry{Tool: e.name, Binary: ref.Binary, Args: e.toolArgs, Code: &code}

	if code != 0 {
		trace.Error(entry)

		return ierrors.Wrap(ierrors.Code(code), "", nil)
	}

	trace.Log(entry)

	return nil
}

// resolve finds the tool's modfile and directive, refusing symlinked tool files
// and tools that are not installed.
func (e *Exec) resolve() (resolve.Ref, error) {
	c := e.c
	layout := c.Layout()

	modfile, err := layout.ModFile(c.Root, e.name)
	if err != nil {
		return resolve.Ref{}, ierrors.New(ierrors.Environment, "%v", err)
	}

	// module keeps the tool in a directory of its own; guard the directory as
	// well as its go.mod.
	guard := []string{modfile}
	if layout.Strategy == strategy.Module {
		guard = append(guard, filepath.Dir(modfile))
	}

	for _, path := range guard {
		if err = fsx.RejectSymlink(path, "exec "+e.name); err != nil {
			return resolve.Ref{}, err
		}
	}

	ref, err := c.Resolver().Resolve(e.name)

	var missing *resolve.MissingError

	switch {
	case errors.As(err, &missing) || errors.Is(err, fs.ErrNotExist):
		if layout.Strategy == strategy.Unified {
			return ref, ierrors.New(ierrors.ToolNotFound, "No %s found. Run 'init' first.", ref.ModFileRel)
		}

		return ref, ierrors.New(
			ierrors.ToolNotFound,
			"Tool '%s' not found (%s missing). Run 'install' first.",
			e.name,
			ref.ModFileRel,
		)
	case err != nil:
		return ref, ierrors.New(ierrors.Environment, "%v", err)
	case ref.Package == "":
		// Taking the first directive of the shared go.mod would run another
		// tool.
		return ref, ierrors.New(
			ierrors.ToolNotFound,
			"Tool '%s' has no tool directive in %s. Run 'install' first.",
			e.name,
			ref.ModFileRel,
		)
	}

	return ref, nil
}

// build has go build the tool and returns the path of its binary.
func (e *Exec) build(ctx context.Context, ref resolve.Ref) (string, error) {
	c := e.c

	call := c.Resolver().Call(ref)
	call.Args = slices.Insert(call.Args, 1, "-n")

	res, err := c.Runner().Run(ctx, call)

	switch {
	case errors.Is(err, gocmd.ErrNotFound):
		return "", ierrors.New(ierrors.Environment, "go is not installed.")
	case err != nil:
		return "", ierrors.New(ierrors.Generic, "go tool -n %s: %v", ref.Binary, err)
	case res.Code != 0:
		_, _ = c.Stderr.Write(res.Stderr)

		return "", ierrors.New(ierrors.Generic, "Could not build tool '%s' (go exited %d).", ref.Name, res.Code)
	}

	// The binary path is the last line; anything before it is build chatter.
	lines := strings.Split(strings.TrimSpace(string(res.Stdout)), "\n")
	bin := strings.TrimSpace(lines[len(lines)-1])

	if bin == "" {
		return "", ierrors.New(ierrors.Generic, "go tool -n %s printed no binary path.", ref.Binary)
	}

	if _, err := os.Stat(bin); err != nil {
		return "", ierrors.New(
			ierrors.Generic,
			"go tool -n %s reported %q, which cannot be run: %v",
			ref.Binary,
			bin,
			err,
		)
	}

	return bin, nil
}
