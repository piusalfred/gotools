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
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	urfave "github.com/urfave/cli/v3"

	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/fingerprint"
	"github.com/piusalfred/gotools/internal/fsx"
	"github.com/piusalfred/gotools/internal/manifest"
	"github.com/piusalfred/gotools/internal/modparser"
	"github.com/piusalfred/gotools/internal/strategy"
	"github.com/piusalfred/gotools/internal/tools"
)

// The migrate command's name, arguments and help text.
const (
	NameMigrate = "migrate"

	// argStrategy names the positional argument: the strategy to move to.
	argStrategy = "strategy"

	usageMigrate     = "Migrate to a different strategy."
	argsUsageMigrate = "<unified|split|module>"

	// Each paragraph is one line: help wraps it.
	descriptionMigrate = `Migrate all tools to a different isolation strategy. Preserves exact pinned versions across the migration.`
)

// Migrate moves the tools directory to another isolation strategy, keeping
// every tool at its pinned version.
//
//   - Before checks the arguments, requires Go and loads the project.
//   - Run migrates under the project lock; Context.Migrate does the work, and
//     sync calls it too for an automatic migration.
//
// One Migrate serves one run. Build works on a copy, so building the command
// twice never shares state.
type Migrate struct {
	c      *Context
	to     strategy.Strategy
	dryRun bool
}

// NewMigrate returns the migrate command.
func NewMigrate() *Migrate { return &Migrate{} }

// Name implements Command.
func (*Migrate) Name() string { return NameMigrate }

// Build implements Command.
func (base *Migrate) Build(c *Context) *urfave.Command {
	m := *base
	m.c = c

	spec := Spec{
		Name:        NameMigrate,
		Usage:       usageMigrate,
		ArgsUsage:   argsUsageMigrate,
		Description: descriptionMigrate,
		Examples: []string{
			"migrate unified",
			"migrate module --dry-run",
		},
		Flags: []urfave.Flag{
			&urfave.BoolFlag{Name: FlagDryRun, Usage: "Show what would be migrated without making changes"},
		},
		Args: []urfave.Argument{&urfave.StringArgs{Name: argStrategy, Min: 1, Max: 1}},
		Complete: m.c.completeArguments(1, func(context.Context) []string {
			return []string{string(strategy.Unified), string(strategy.Split), string(strategy.Module)}
		}),
		Before: m.before,
		Run:    m.run,
	}

	return spec.Build(c)
}

func (m *Migrate) before(ctx context.Context, cmd *urfave.Command) error {
	// urfave/cli does not stop at the maximum: what is left over is in Args.
	args := append(cmd.StringArgs(argStrategy), cmd.Args().Slice()...)

	if len(args) != 1 {
		return ierrors.New(ierrors.Usage, "Usage: %s migrate %s [--dry-run]", m.c.Program, argsUsageMigrate)
	}

	m.to = strategy.Strategy(args[0])
	if !m.to.Valid() {
		return ierrors.New(ierrors.Usage, "Invalid strategy: %s (must be unified, split, or module)", args[0])
	}

	m.dryRun = cmd.Bool(FlagDryRun)

	if err := m.c.requireGo(ctx); err != nil {
		return err
	}

	if err := m.c.Load(ctx); err != nil {
		return err
	}

	for _, w := range m.c.Warnings {
		_, _ = fmt.Fprintf(m.c.Stderr, "  ⚠️  %s\n", w)
	}

	return nil
}

func (m *Migrate) run(ctx context.Context, _ *urfave.Command) error {
	return m.c.WithLock(ctx, func(ctx context.Context) error {
		return m.c.Migrate(ctx, MigrateParams{To: m.to, DryRun: m.dryRun, PersistStrategy: true})
	})
}

// MigrateParams says how Migrate moves the tools directory.
type MigrateParams struct {
	// To is the strategy to move to.
	To     strategy.Strategy
	DryRun bool
	// PersistStrategy writes To to the manifest. An automatic migration (sync)
	// leaves it off: its target is the resolved strategy, which may come from
	// the environment, and that must not be written to a committed file.
	PersistStrategy bool
}

// Migrate moves the tools directory to p.To. The project must be loaded and the
// caller holds the project lock.
//
// The old layout is moved aside first and put back if anything fails, so a
// failure midway loses nothing: not the old layout, not its go.sum files and not
// an unrelated file kept in the directory. Only gotools' own files are touched.
func (c *Context) Migrate(ctx context.Context, p MigrateParams) error {
	l := c.Layout()
	toolsDir := l.ToolsDir(c.Root)
	det := strategy.Detect(toolsDir)
	out := c.Stdout

	// The disk, not the config, says what we migrate from: the config may
	// already name the target.
	from := det.Strategy
	if from == "" {
		from = l.Strategy
	}

	if from == p.To && !det.Hybrid() {
		return c.adoptStrategy(p)
	}

	_, _ = fmt.Fprintf(out, "🔀 Migrating from '%s' to '%s'...\n", from, p.To)

	specs, err := c.toolsToMigrate(det, from)
	if err != nil {
		if !p.DryRun {
			return err
		}

		_, _ = fmt.Fprintf(c.Stderr, "⚠️  %v\n", err)
	}

	c.printMigrationPlan(specs)

	if p.DryRun {
		_, _ = fmt.Fprintf(out, "🔍 [dry-run] Would migrate %s/ from '%s' to '%s'.\n", l.Dir, from, p.To)

		return nil
	}

	// Offline mode forbids the reinstall, so it is refused before the old
	// layout is touched.
	if len(specs) > 0 && c.Config.Settings.Offline {
		return ierrors.New(
			ierrors.Offline,
			"Offline mode: migrate must reinstall %d tool(s). Network required.",
			len(specs),
		)
	}

	st, err := stashLayout(toolsDir)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "🧹 Moved the old layout aside (%s/).\n", l.Dir)

	if err := c.rebuild(ctx, p, specs); err != nil {
		if rerr := st.restore(); rerr != nil {
			return errors.Join(err, rerr)
		}

		return err
	}

	st.discard()

	_, _ = fmt.Fprintf(out, "✅ Migration from '%s' to '%s' complete.\n", from, p.To)

	return nil
}

// adoptStrategy handles "the disk already is the target". The manifest follows
// the disk, so a manifest that names another strategy does not keep sync
// refusing.
func (c *Context) adoptStrategy(p MigrateParams) error {
	out := c.Stdout

	if !p.PersistStrategy || c.manifestStrategy() == p.To {
		_, _ = fmt.Fprintf(out, "ℹ️  Already using strategy '%s'. Nothing to do.\n", p.To)

		return nil
	}

	if p.DryRun {
		_, _ = fmt.Fprintf(out, "🔍 [dry-run] Would set strategy to '%s' in %s.\n", p.To, config.ManifestFile)

		return nil
	}

	c.Manifest.Strategy = string(p.To)

	if err := c.Flush(); err != nil {
		return ierrors.Wrap(ierrors.Generic, "Could not write the manifest", err)
	}

	_, _ = fmt.Fprintf(
		out,
		"✅ %s now says strategy '%s' (the tools directory already matches).\n",
		config.ManifestFile,
		p.To,
	)

	return nil
}

func (c *Context) manifestStrategy() strategy.Strategy {
	if c.Manifest.Strategy == "" {
		return config.DefaultStrategy
	}

	return strategy.Strategy(c.Manifest.Strategy)
}

func (c *Context) printMigrationPlan(specs []modparser.Spec) {
	if len(specs) == 0 {
		_, _ = fmt.Fprintln(c.Stdout, "⚠️  No tools found to migrate.")

		return
	}

	_, _ = fmt.Fprintln(c.Stdout, "📋 Tools to migrate:")

	for _, t := range specs {
		_, _ = fmt.Fprintf(c.Stdout, "   %s %s@%s\n", t.Name, t.Package, t.Version)
	}
}

// toolsToMigrate lists what the new layout must contain: the tools of every
// layout present on disk, then manifest entries whose files are missing, so a
// manifest entry without files is not dropped. A hybrid directory has all of
// its layouts read, and a modfile that cannot be read stops the migration (the
// error is returned together with the tools that did load, for the dry run).
func (c *Context) toolsToMigrate(det strategy.Detection, from strategy.Strategy) ([]modparser.Spec, error) {
	layouts := det.Found
	if len(layouts) == 0 {
		layouts = []strategy.Strategy{from}
	}

	var (
		specs []modparser.Spec
		errs  []error
	)

	seen := func(name string) bool {
		return slices.ContainsFunc(specs, func(t modparser.Spec) bool { return t.Name == name })
	}

	for _, s := range layouts {
		view := *c
		view.Config.Strategy = s

		found, err := view.ToolsOnDisk()
		if err != nil {
			errs = append(errs, err)
		}

		for _, t := range found {
			if !seen(t.Name) {
				specs = append(specs, t)
			}
		}
	}

	for _, t := range c.Manifest.SortedTools() {
		if !seen(t.Name) {
			specs = append(specs, modparser.Spec{Name: t.Name, Package: t.Package, Version: t.Version})
		}
	}

	slices.SortFunc(specs, func(a, b modparser.Spec) int { return strings.Compare(a.Name, b.Name) })

	if len(errs) > 0 {
		return specs, ierrors.New(
			ierrors.Environment,
			"Refusing to migrate: some files in the tools directory could not be read and would be lost.\n   %v\n   Fix or remove them and retry.",
			errors.Join(errs...),
		)
	}

	return specs, nil
}

// rebuild installs every tool at its pinned version under the target strategy,
// then records the result in the manifest.
func (c *Context) rebuild(ctx context.Context, p MigrateParams, specs []modparser.Spec) error {
	out := c.Stdout

	// A copy of the Context that works in the target layout.
	target := *c
	target.Config.Strategy = p.To

	_, _ = fmt.Fprintf(out, "🔧 Initializing new strategy (%s)...\n", p.To)

	if len(specs) > 0 {
		_, _ = fmt.Fprintf(out, "📦 Re-installing tools under '%s' strategy...\n", p.To)
	}

	installed := make([]manifest.Tool, 0, len(specs))
	manager := target.Tools()

	for _, t := range specs {
		_, _ = fmt.Fprintf(out, "  → %s (%s@%s)\n", t.Name, t.Package, t.Version)

		tool, err := manager.Add(ctx, tools.AddOptions{Name: t.Name, Pkg: t.Package + "@" + t.Version, NoFlush: true})
		if err != nil {
			return err
		}

		installed = append(installed, tool)
	}

	// The manifest is written once, last: until then a failure leaves it
	// exactly as it was. The fingerprint comes just before it, from the target
	// layout: the stash took the old one, and without a new one the next
	// offline sync refuses a project that is in sync. A failure here still
	// restores the old layout, fingerprint included.
	for _, t := range installed {
		c.Manifest.SetTool(t)
	}

	if p.PersistStrategy {
		c.Manifest.Strategy = string(p.To)
	}

	if err := target.Record(ctx); err != nil {
		return ierrors.Wrap(ierrors.Generic, "Could not write the fingerprint", err)
	}

	if err := c.Flush(); err != nil {
		return ierrors.Wrap(ierrors.Generic, "Could not write the manifest", err)
	}

	return nil
}

// stash holds the files of the old layout while the new one is built.
type stash struct {
	toolsDir string
	existed  bool            // the tools directory existed before
	backup   string          // directory holding the moved entries ("" if none)
	moved    []string        // names moved into backup
	kept     map[string]bool // names that stayed in toolsDir
}

// stashLayout moves gotools' own files (modfiles, sums, module directories, the
// fingerprint) out of toolsDir. Anything else stays where it is, which includes
// the lock file.
func stashLayout(toolsDir string) (*stash, error) {
	st := &stash{toolsDir: toolsDir, kept: map[string]bool{}}

	entries, err := os.ReadDir(toolsDir)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return st, nil
	case err != nil:
		return nil, ierrors.New(ierrors.Generic, "Could not read %s: %v", toolsDir, err)
	}

	st.existed = true

	var owned []string

	for _, e := range entries {
		if ownedEntry(toolsDir, e) {
			if err = fsx.RejectSymlink(filepath.Join(toolsDir, e.Name()), "migrate"); err != nil {
				return nil, err
			}

			owned = append(owned, e.Name())
		} else {
			st.kept[e.Name()] = true
		}
	}

	if len(owned) == 0 {
		return st, nil
	}

	st.backup, err = os.MkdirTemp(filepath.Dir(toolsDir), ".gotools-migrate-")
	if err != nil {
		return nil, ierrors.New(ierrors.Generic, "Could not create a backup directory: %v", err)
	}

	for _, n := range owned {
		if err := os.Rename(filepath.Join(toolsDir, n), filepath.Join(st.backup, n)); err != nil {
			err = ierrors.New(ierrors.Generic, "Could not move %s aside: %v", n, err)
			if rerr := st.restore(); rerr != nil {
				return nil, errors.Join(err, rerr)
			}

			return nil, err
		}

		st.moved = append(st.moved, n)
	}

	return st, nil
}

// ownedEntry reports whether e is a file gotools manages in the tools
// directory, whatever the strategy: *.mod / *.sum, a module directory holding a
// go.mod, or the fingerprint. Dot-files other than the fingerprint (the lock
// file, .gitignore) are never owned.
func ownedEntry(dir string, e fs.DirEntry) bool {
	n := e.Name()
	if n == fingerprint.FileName {
		return true
	}

	if strings.HasPrefix(n, ".") {
		return false
	}

	fi, err := os.Stat(filepath.Join(dir, n))
	if err != nil {
		return false
	}

	if fi.IsDir() {
		return isRegularFile(filepath.Join(dir, n, "go.mod"))
	}

	return fi.Mode().IsRegular() && (strings.HasSuffix(n, ".mod") || strings.HasSuffix(n, ".sum"))
}

// restore removes whatever the new layout created and puts the old files back.
// When it cannot, the backup is left in place and named in the error.
func (s *stash) restore() error {
	if entries, err := os.ReadDir(s.toolsDir); err == nil {
		for _, e := range entries {
			if !s.kept[e.Name()] {
				_ = os.RemoveAll(filepath.Join(s.toolsDir, e.Name()))
			}
		}
	}

	if !s.existed {
		_ = os.Remove(s.toolsDir) // only succeeds when empty

		return nil
	}

	var errs []error

	for _, n := range s.moved {
		if err := os.Rename(filepath.Join(s.backup, n), filepath.Join(s.toolsDir, n)); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return ierrors.New(
			ierrors.Generic,
			"Could not restore the old layout; your files are preserved in %s: %v",
			s.backup,
			errors.Join(errs...),
		)
	}

	if s.backup != "" {
		_ = os.RemoveAll(s.backup)
	}

	return nil
}

func (s *stash) discard() {
	if s.backup != "" {
		_ = os.RemoveAll(s.backup)
	}
}
