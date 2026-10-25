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
	"fmt"
	"os"
	"strings"

	urfave "github.com/urfave/cli/v3"

	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
)

// The uninstall command's name and help text.
const (
	NameUninstall = "uninstall"

	usageUninstall = "Remove gotools from your system."

	descriptionUninstall = `Remove gotools itself from your system.`
)

// Uninstall deletes the gotools executable.
//
//   - Run names what it would delete, asks for confirmation on stdin, then
//     removes the running binary and the symlink it was started through, so no
//     dangling link is left behind. No answer at all, as in a script, is a no.
//
// Projects keep their manifest and tools directory; only the program goes.
type Uninstall struct{ c *Context }

// NewUninstall returns the uninstall command.
func NewUninstall() *Uninstall { return &Uninstall{} }

// Name implements Command.
func (*Uninstall) Name() string { return NameUninstall }

// Build implements Command.
func (base *Uninstall) Build(c *Context) *urfave.Command {
	u := *base
	u.c = c

	return Spec{
		Name:        NameUninstall,
		Usage:       usageUninstall,
		Description: descriptionUninstall,
		Examples:    []string{"uninstall", "uninstall --dry-run"},
		Flags:       []urfave.Flag{&urfave.BoolFlag{Name: FlagDryRun, Usage: "Show what would be removed without deleting"}},
		Run:         u.run,
	}.Build(c)
}

func (u *Uninstall) run(_ context.Context, cmd *urfave.Command) error {
	if err := noExtraArgs(cmd); err != nil {
		return err
	}

	c := u.c

	started, resolved, err := c.ExecutablePaths()
	if err != nil {
		return err
	}

	targets := uninstallTargets(started, resolved)

	if cmd.Bool(FlagDryRun) {
		_, _ = fmt.Fprintf(c.Stdout, "🔍 [dry-run] Would remove %s\n", strings.Join(targets, " and "))

		return nil
	}

	_, _ = fmt.Fprintf(c.Stdout, "⚠️  WARNING: This will delete the %s executable (%s).\n", ProductName, strings.Join(targets, ", "))
	_, _ = fmt.Fprintf(c.Stdout, "   Projects keep their %s and tools directory; only the program goes.\n", config.ManifestFile)
	_, _ = fmt.Fprintf(c.Stdout, "Do you want to uninstall %s? (y/N): ", ProductName)

	if !c.confirmed() {
		_, _ = fmt.Fprintln(c.Stdout, "❌ Uninstall cancelled.")

		return nil
	}

	for _, t := range targets {
		if err := os.Remove(t); err != nil && !errors.Is(err, os.ErrNotExist) {
			return ierrors.New(ierrors.Environment, "Could not remove %s: %v", t, err)
		}
	}

	_, _ = fmt.Fprintf(c.Stdout, "✅ %s has been uninstalled. Goodbye!\n", ProductName)

	return nil
}

// uninstallTargets lists what to delete: the real binary, and the symlink it
// was started through when there is one. The link comes first.
func uninstallTargets(started, resolved string) []string {
	if resolved == started {
		return []string{started}
	}

	if fi, err := os.Lstat(started); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return []string{started, resolved}
	}

	return []string{resolved}
}

// confirmed reads one line from stdin and reports whether it is y or yes. No
// input at all counts as no.
func (c *Context) confirmed() bool {
	switch strings.ToLower(strings.TrimSpace(c.readLine())) {
	case "y", "yes":
		return true
	}

	return false
}
