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

	urfave "github.com/urfave/cli/v3"

	"github.com/piusalfred/gotools/internal/strategy"
	"github.com/piusalfred/gotools/internal/tools"
)

// The rename command's name, argument and help text.
const (
	NameRename = "rename"

	// argOldNew names the positional arguments: <old> <new>.
	argOldNew = "name"

	usageRename     = "Rename a tool (manifest + modfiles)."
	argsUsageRename = "<old> <new>"

	// Each paragraph is one line: help wraps it.
	descriptionRename = `Rename a managed tool: updates the manifest and moves the tool's modfiles (split) or its module directory (module). The module line inside the moved go.mod is rewritten to the new name.

Not supported with the unified strategy: there the names come from the go.mod tool directives, so rename would desync list/info/exec. Migrate to split or module first.

Refuses when <new-name> is already managed or its files already exist on disk.`
)

// Rename gives a managed tool another name.
//
//   - Before checks both names, requires Go and loads the project.
//   - Run renames under the project lock: the tool manager moves the files and
//     the manifest entry together and undoes every step if a later one fails.
type Rename struct {
	c                *Context
	oldName, newName string
	dryRun           bool
}

// NewRename returns the rename command.
func NewRename() *Rename { return &Rename{} }

// Name implements Command.
func (*Rename) Name() string { return NameRename }

// Build implements Command.
func (base *Rename) Build(c *Context) *urfave.Command {
	r := *base
	r.c = c

	return Spec{
		Name:        NameRename,
		Usage:       usageRename,
		ArgsUsage:   argsUsageRename,
		Description: descriptionRename,
		Examples:    []string{"rename goimports imports", "rename goimports imports --dry-run"},
		Flags:       []urfave.Flag{&urfave.BoolFlag{Name: FlagDryRun, Usage: "Show what would be renamed without making changes"}},
		Args:        []urfave.Argument{&urfave.StringArgs{Name: argOldNew, Min: 2, Max: 2}},
		Complete:    r.c.completeArguments(1, r.c.toolNames),
		Before:      r.before,
		Run:         r.run,
	}.Build(c)
}

func (r *Rename) before(ctx context.Context, cmd *urfave.Command) error {
	names, err := positional(cmd, 2, 2)
	if err != nil {
		return err
	}

	for _, n := range names {
		if err := strategy.ValidateName(n); err != nil {
			return err
		}
	}

	r.oldName, r.newName, r.dryRun = names[0], names[1], cmd.Bool(FlagDryRun)

	return r.c.prepare(ctx)
}

func (r *Rename) run(ctx context.Context, _ *urfave.Command) error {
	return r.c.WithLock(ctx, func(ctx context.Context) error {
		return r.c.Tools().Rename(ctx, r.oldName, r.newName, tools.RenameOptions{DryRun: r.dryRun})
	})
}
