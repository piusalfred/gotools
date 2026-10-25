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
	"fmt"
	"io"
	"slices"
	"strings"

	urfave "github.com/urfave/cli/v3"

	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/strategy"
	"github.com/piusalfred/gotools/internal/tools"
)

// The remove command's name, argument and help text.
const (
	NameRemove = "remove"

	// argNames names the positional arguments: <name...>.
	argNames = "name"

	usageRemove     = "Remove specific tools."
	argsUsageRemove = "<name...>"

	descriptionRemove = `Remove specific tools from the project.`
)

// Remove deletes tools from the tools directory and the manifest.
//
//   - Before checks every name, requires Go and loads the project, so a bad
//     name stops the run before the first tool is touched.
//   - Run removes each tool under the project lock. A tool that is neither on
//     disk nor in the manifest is reported once the others have been removed.
//
// One Remove serves one run: it keeps what Before read. Build works on a copy,
// so building the command twice never shares state.
type Remove struct {
	c      *Context
	names  []string
	dryRun bool
}

// NewRemove returns the remove command.
func NewRemove() *Remove { return &Remove{} }

// Name implements Command.
func (*Remove) Name() string { return NameRemove }

// Build implements Command.
func (base *Remove) Build(c *Context) *urfave.Command {
	r := *base
	r.c = c

	return Spec{
		Name:        NameRemove,
		Usage:       usageRemove,
		ArgsUsage:   argsUsageRemove,
		Description: descriptionRemove,
		Examples:    []string{"remove goimports", "remove staticcheck goimports --dry-run"},
		Flags:       []urfave.Flag{&urfave.BoolFlag{Name: FlagDryRun, Usage: "Show what would be removed without making changes"}},
		Args:        []urfave.Argument{&urfave.StringArgs{Name: argNames, Min: 1, Max: -1}},
		Complete:    r.c.completeArguments(-1, r.c.toolNames),
		Before:      r.before,
		Run:         r.run,
	}.Build(c)
}

func (r *Remove) before(ctx context.Context, cmd *urfave.Command) error {
	args, err := positional(cmd, 1, -1)
	if err != nil {
		return err
	}

	names, err := validNames(args)
	if err != nil {
		return err
	}

	r.names, r.dryRun = names, cmd.Bool(FlagDryRun)

	return r.c.prepare(ctx)
}

func (r *Remove) run(ctx context.Context, _ *urfave.Command) error {
	return r.c.WithLock(ctx, func(ctx context.Context) error {
		return removeTools(ctx, r.c.Stdout, r.c.Tools(), r.names, r.dryRun)
	})
}

// validNames checks every name before anything changes, so a bad third name
// cannot leave the first two already removed, and drops repeated names.
func validNames(names []string) ([]string, error) {
	for _, n := range names {
		if err := strategy.ValidateName(n); err != nil {
			return nil, err
		}
	}

	var out []string

	for _, n := range names {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}

	return out, nil
}

// prepare is what a command that changes tools does first: require Go, load the
// project and show what the manifest warned about.
func (c *Context) prepare(ctx context.Context) error {
	if err := c.requireGo(ctx); err != nil {
		return err
	}

	if err := c.Load(ctx); err != nil {
		return err
	}

	for _, w := range c.Warnings {
		_, _ = fmt.Fprintf(c.Stderr, "  ⚠️  %s\n", w)
	}

	return nil
}

// removeTools removes each named tool. A name that is neither on disk nor in the
// manifest is a tool-not-found error once the other names have been processed,
// so a typo is never silent but does not stop the rest.
func removeTools(ctx context.Context, out io.Writer, manager tools.Manager, names []string, dryRun bool) error {
	var unknown []string

	for _, name := range names {
		if dryRun {
			_, _ = fmt.Fprintf(out, "  🔍 [dry-run] Would remove %s\n", name)

			continue
		}

		_, _ = fmt.Fprintf(out, "🗑️  Removing %s...\n", name)

		res, err := manager.Remove(ctx, name)
		if err != nil {
			return err
		}

		if !res.OnDisk && !res.InManifest {
			unknown = append(unknown, name)
		}
	}

	if len(unknown) > 0 {
		return ierrors.New(ierrors.ToolNotFound, "Tool not found: %s", strings.Join(unknown, ", "))
	}

	return nil
}
