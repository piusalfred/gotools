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
	"os"
	"path/filepath"
	"strings"

	urfave "github.com/urfave/cli/v3"

	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/fsx"
	"github.com/piusalfred/gotools/internal/tools"
	"github.com/piusalfred/gotools/internal/tracer"
)

// The purge command's name, flags and help text.
const (
	NamePurge = "purge"

	FlagRestore = "restore"

	usagePurge = "Remove all tools and the .gotools.json file."

	// Each paragraph is one line: help wraps it.
	descriptionPurge = `Remove all tools and the .gotools.json manifest. With --restore, the managed tools are added back to the root go.mod at their pinned versions (go get -tool) before the wipe, and the init command that recreates this project is printed.

Interactive: requires typing YES to confirm.`
)

// Purge deletes the tools directory and the manifest, optionally giving the
// tools back to the root go.mod first.
//
//   - Before checks the arguments and loads the project. A project with no
//     manifest is not a gotools project, so nothing in it is ours to delete.
//   - Run restores first when asked (a failed restore leaves gotools intact),
//     asks for the word YES before it takes the lock, then deletes under the
//     lock. The manifest goes last: if the directory cannot be removed, it
//     still says what is there.
type Purge struct {
	c       *Context
	restore bool
	dryRun  bool
}

// NewPurge returns the purge command.
func NewPurge() *Purge { return &Purge{} }

// Name implements Command.
func (*Purge) Name() string { return NamePurge }

// Build implements Command.
func (base *Purge) Build(c *Context) *urfave.Command {
	p := *base
	p.c = c

	return Spec{
		Name:        NamePurge,
		Usage:       usagePurge,
		Description: descriptionPurge,
		Examples:    []string{"purge", "purge --restore", "purge --restore --dry-run"},
		Flags: []urfave.Flag{
			&urfave.BoolFlag{Name: FlagRestore, Usage: "Put the managed tools back into the root go.mod"},
			&urfave.BoolFlag{Name: FlagDryRun, Usage: "Show what would be removed without deleting"},
		},
		Before: p.before,
		Run:    p.run,
	}.Build(c)
}

func (p *Purge) before(ctx context.Context, cmd *urfave.Command) error {
	// A mistyped `purge --restor` must not turn into a purge that wipes
	// everything without giving the tools back.
	if err := noExtraArgs(cmd); err != nil {
		return err
	}

	p.restore, p.dryRun = cmd.Bool(FlagRestore), cmd.Bool(FlagDryRun)

	c := p.c

	if err := c.Load(ctx); err != nil {
		return err
	}

	for _, w := range c.Warnings {
		_, _ = fmt.Fprintf(c.Stderr, "  ⚠️  %s\n", w)
	}

	return nil
}

func (p *Purge) run(ctx context.Context, _ *urfave.Command) error {
	c := p.c

	if !c.HasManifest {
		_, _ = fmt.Fprintf(c.Stdout, "ℹ️  No %s found — nothing to purge.\n", config.ManifestFile)

		return nil
	}

	if p.dryRun {
		return p.dry(ctx)
	}

	if p.restore {
		if err := c.requireGo(ctx); err != nil {
			return err
		}
	}

	if !p.confirmed() {
		_, _ = fmt.Fprintln(c.Stdout, "❌ Purge cancelled.")

		return nil
	}

	return c.WithLock(ctx, p.wipe)
}

func (p *Purge) dry(ctx context.Context) error {
	c := p.c

	if p.restore {
		if err := c.RestoreToRoot(ctx, RestoreToRootParams{DryRun: true}); err != nil {
			return err
		}

		_, _ = fmt.Fprintf(c.Stdout, "🔍 [dry-run] To come back: %s\n", c.RecreateCommand())
	}

	_, _ = fmt.Fprintln(c.Stdout, "🔍 [dry-run] Would delete:")
	_, _ = fmt.Fprintf(c.Stdout, "  - %s/ (tools directory)\n", c.Config.Dir)
	_, _ = fmt.Fprintf(c.Stdout, "  - %s (manifest)\n", config.ManifestFile)

	return nil
}

// confirmed asks for the word YES. Anything else, including no input at all,
// cancels. It runs before the lock is taken, so nobody waits on a prompt while
// holding it.
func (p *Purge) confirmed() bool {
	c := p.c

	_, _ = fmt.Fprintf(c.Stdout, "⚠️  WARNING: This will delete the tools directory ('%s') and '%s'.\n", c.Config.Dir, config.ManifestFile)

	if p.restore {
		_, _ = fmt.Fprintln(c.Stdout, "  It will also add the managed tools back to your go.mod.")
	}

	_, _ = fmt.Fprintln(c.Stdout, "This action cannot be undone.")
	_, _ = fmt.Fprint(c.Stdout, "Are you sure you want to proceed? (type YES to confirm): ")

	return strings.TrimSpace(c.readLine()) == "YES"
}

func (p *Purge) wipe(ctx context.Context) error {
	c := p.c

	// Before the wipe, so a failed restore leaves gotools intact.
	if p.restore {
		if err := c.RestoreToRoot(ctx, RestoreToRootParams{}); err != nil {
			return err
		}

		_, _ = fmt.Fprintf(c.Stdout, "💡 To come back: %s\n", c.RecreateCommand())
	}

	if err := os.RemoveAll(c.ToolsDir()); err != nil {
		return ierrors.New(ierrors.Generic, "Could not remove %s: %v", c.Config.Dir, err)
	}

	if err := os.Remove(c.ManifestPath()); err != nil {
		return ierrors.New(ierrors.Generic, "Could not remove %s: %v", config.ManifestFile, err)
	}

	_, _ = fmt.Fprintln(c.Stdout, "✅ Purge complete. All tools and configurations have been removed.")

	return nil
}

// RecreateCommand is the init command that recreates this project's
// configuration. The tools come back through the go.mod restore; the strategy,
// directory, Go version and module prefix live only in the manifest.
func (c *Context) RecreateCommand() string {
	m := c.Manifest

	parts := []string{c.Program, "init", "--strategy=" + firstSet(m.Strategy, string(config.DefaultStrategy))}

	if dir := firstSet(m.Dir, config.DefaultDir); dir != config.DefaultDir {
		parts = append(parts, tracer.ShellQuote("--dir="+dir))
	}

	if m.GoVersion != "" && m.GoVersion != config.DefaultGoVersion {
		parts = append(parts, tracer.ShellQuote("--go="+m.GoVersion))
	}

	if m.ModulePrefix != "" {
		parts = append(parts, tracer.ShellQuote("--prefix="+m.ModulePrefix))
	}

	return strings.Join(parts, " ")
}

// RestoreToRootParams are the options of RestoreToRoot.
type RestoreToRootParams struct {
	// DryRun reports what would be added and runs nothing.
	DryRun bool
}

// RestoreToRoot is the reverse of adoption: it adds every managed tool back to
// the root go.mod at its pinned version.
//
// The root go.mod and go.sum are snapshotted and put back on any failure, so a
// failure on tool N of M never leaves go.mod half edited. Package paths come
// from the committed manifest and reach `go get` as arguments, so each is
// checked first: one that looks like a flag never gets there.
func (c *Context) RestoreToRoot(ctx context.Context, p RestoreToRootParams) error {
	rootMod := c.RootGoMod()
	if !isRegularFile(rootMod) {
		return ierrors.New(ierrors.Environment, "Cannot restore tools: no go.mod in project root.")
	}

	if err := fsx.RejectSymlink(rootMod, "purge --restore"); err != nil {
		return err
	}

	snap := fsx.TakeSnapshot(rootMod, filepath.Join(c.Root, "go.sum"))
	count := 0

	for _, t := range c.Manifest.SortedTools() {
		if t.Source != "" && t.Source != "go" {
			_, _ = fmt.Fprintf(c.Stdout, "  ⚠ %s: source '%s' — skipping (only go tools can be restored)\n", t.Name, t.Source)

			continue
		}

		spec := t.Package + "@" + firstSet(t.Version, "latest")

		if err := tools.CheckPackageSpec(spec); err != nil {
			snap.Restore()

			return err
		}

		count++

		if p.DryRun {
			_, _ = fmt.Fprintf(c.Stdout, "  [dry-run] Would restore %s (%s) to go.mod\n", t.Name, spec)

			continue
		}

		_, _ = fmt.Fprintf(c.Stdout, "  ⬆ %s (%s) → go.mod\n", t.Name, spec)

		if err := c.getRootTool(ctx, t.Name, spec); err != nil {
			snap.Restore()

			return err
		}
	}

	switch {
	case p.DryRun:
		_, _ = fmt.Fprintf(c.Stdout, "[dry-run] Would restore %d tool(s) to go.mod.\n", count)
	case count > 0:
		_, _ = fmt.Fprintf(c.Stdout, "✅ Restored %d tool(s) to go.mod.\n", count)
	default:
		_, _ = fmt.Fprintln(c.Stdout, "ℹ No tools to restore.")
	}

	return nil
}

// getRootTool runs `go get -tool spec` in the project root.
func (c *Context) getRootTool(ctx context.Context, name, spec string) error {
	res, err := c.Network().Run(ctx, gocmdCall(c.Root, "get", "-tool", spec))
	if err != nil {
		return timeoutOr(err, "Restoring "+name)
	}

	if res.Code != 0 {
		output := strings.TrimSpace(string(res.Stderr) + string(res.Stdout))

		return ierrors.New(tidyFailureCode(output), "Failed to restore %s\n%s", name, output)
	}

	return nil
}
