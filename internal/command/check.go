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

	urfave "github.com/urfave/cli/v3"

	ierrors "github.com/piusalfred/gotools/internal/errors"
)

// The check command's name and help text.
const (
	NameCheck = "check"

	usageCheck = "Verify all managed tools are runnable."

	// Each paragraph is one line: help wraps it.
	descriptionCheck = `Verify all managed tools are installed and runnable. Runs each tool with --version, --help, or version and reports pass/fail. Exits 5 when any tool is not runnable.`
)

// Check confirms that every tool in the manifest runs.
//
//   - Before checks the arguments, requires Go and loads the project, so a bad
//     request stops before any tool is launched.
//   - Run launches each tool once per probe argument until one exits 0 and
//     reports pass or fail per tool.
type Check struct{ c *Context }

// NewCheck returns the check command.
func NewCheck() *Check { return &Check{} }

// Name implements Command.
func (*Check) Name() string { return NameCheck }

// Build implements Command.
func (base *Check) Build(c *Context) *urfave.Command {
	k := *base
	k.c = c

	return Spec{
		Name:        NameCheck,
		Usage:       usageCheck,
		Description: descriptionCheck,
		Examples:    []string{"check"},
		Before:      k.before,
		Run:         k.run,
	}.Build(c)
}

func (k *Check) before(ctx context.Context, cmd *urfave.Command) error {
	if err := noExtraArgs(cmd); err != nil {
		return err
	}

	c := k.c

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

func (k *Check) run(ctx context.Context, _ *urfave.Command) error {
	c := k.c
	prober := c.Prober()

	_, _ = fmt.Fprint(c.Stdout, "🔍 Checking managed tools...\n\n")

	var passed, failed int

	for _, t := range c.Manifest.SortedTools() {
		if prober.Runs(ctx, t.Name) {
			_, _ = fmt.Fprintf(c.Stdout, "  ✅ %s (%s@%s)\n", t.Name, t.Package, t.Version)
			passed++

			continue
		}

		_, _ = fmt.Fprintf(c.Stdout, "  ❌ %s (%s@%s) — not runnable\n", t.Name, t.Package, t.Version)
		failed++
	}

	_, _ = fmt.Fprintf(c.Stdout, "\n  %d passed, %d failed\n", passed, failed)

	if failed > 0 {
		return ierrors.Wrap(ierrors.ToolNotFound, "", nil)
	}

	return nil
}
