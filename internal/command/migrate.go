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
	"github.com/piusalfred/gotools/internal/lock"
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

	usageMigrate     = "Migrate to a different strategy or tools directory."
	argsUsageMigrate = "<unified|split|module>"

	// Each paragraph is one line: help wraps it.
	descriptionMigrate = `Migrate all tools to a different isolation strategy, or move the tools directory with --dir. Preserves exact pinned versions across the migration.`
)

// Migrate moves the tools directory to another isolation strategy, another
// directory, or both, keeping every tool at its pinned version.
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
	toDir  string
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
			"migrate split --dir=gotools",
		},
		Flags: []urfave.Flag{
			&urfave.StringFlag{
				Name:      FlagDir,
				Usage:     "Move the tools directory to `PATH`, keeping the strategy",
				Validator: config.ValidateDir,
			},
			&urfave.BoolFlag{Name: FlagDryRun, Usage: "Show what would be migrated without making changes"},
		},
		Args: []urfave.Argument{&urfave.StringArgs{Name: argStrategy, Min: 0, Max: 1}},
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

	if len(args) > 1 {
		return ierrors.New(
			ierrors.Usage,
			"Usage: %s migrate [%s] [--dir=<path>] [--dry-run]",
			m.c.Program,
			argsUsageMigrate,
		)
	}

	if len(args) == 1 {
		m.to = strategy.Strategy(args[0])
		if !m.to.Valid() {
			return ierrors.New(ierrors.Usage, "Invalid strategy: %s (must be unified, split, or module)", args[0])
		}
	}

	m.toDir = cmd.String(FlagDir)
	m.dryRun = cmd.Bool(FlagDryRun)

	if m.to == "" && m.toDir == "" {
		return ierrors.New(
			ierrors.Usage,
			"Usage: %s migrate [%s] [--dir=<path>] [--dry-run]",
			m.c.Program,
			argsUsageMigrate,
		)
	}

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
		return m.c.Migrate(ctx, MigrateParams{To: m.to, ToDir: m.toDir, DryRun: m.dryRun, PersistStrategy: true})
	})
}

// MigrateParams says how Migrate moves the tools directory.
type MigrateParams struct {
	// To is the strategy to move to. "" keeps the one in force.
	To strategy.Strategy
	// ToDir is the tools directory to move to, relative to the project root.
	// "" keeps the one in force.
	ToDir  string
	DryRun bool
	// PersistStrategy writes To and ToDir to the manifest. An automatic
	// migration (sync) leaves it off: its target is the resolved strategy,
	// which may come from the environment, and that must not be written to a
	// committed file.
	PersistStrategy bool
}

// Migrate moves the tools directory to the layout p names. The project must be
// loaded and the caller holds the project lock.
//
// The old layout is moved aside first and put back if anything fails, so a
// failure midway loses nothing: not the old layout, not its go.sum files and
// not an unrelated file kept in the directory. Only gotools' own files are
// touched. A directory move renames the directory itself, so unrelated files
// come along.
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

	to := p.To
	if to == "" {
		to = l.Strategy
	}

	toDir := p.ToDir
	if toDir == "" {
		toDir = l.Dir
	}

	target := strategy.Layout{Strategy: to, Dir: toDir}

	if from == to && !det.Hybrid() && toDir == l.Dir {
		return c.adoptStrategy(p)
	}

	if toDir != l.Dir {
		if err := c.checkDirTarget(toDir); err != nil {
			return err
		}
	}

	_, _ = fmt.Fprintf(out, "🔀 Migrating from '%s' to '%s'...\n", from, to)

	specs, err := c.toolsToMigrate(det, from)
	if err != nil {
		if !p.DryRun {
			return err
		}

		_, _ = fmt.Fprintf(c.Stderr, "⚠️  %v\n", err)
	}

	c.printMigrationPlan(specs)

	if p.DryRun {
		c.printDryRun(l.Dir, toDir, from, to)

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

	st, err := stashLayout(toolsDir, target.ToolsDir(c.Root), toDir != l.Dir)
	if err != nil {
		return err
	}

	if toDir != l.Dir {
		_, _ = fmt.Fprintf(out, "🚚 Moved %s/ to %s/.\n", l.Dir, toDir)
	} else {
		_, _ = fmt.Fprintf(out, "🧹 Moved the old layout aside (%s/).\n", l.Dir)
	}

	if err := c.rebuild(ctx, p, specs); err != nil {
		if rerr := st.restore(); rerr != nil {
			return errors.Join(err, rerr)
		}

		return err
	}

	st.discard()

	_, _ = fmt.Fprintf(out, "✅ Migration from '%s' to '%s' complete.\n", from, to)

	return nil
}

// printDryRun says what a migration would do, writing nothing.
func (c *Context) printDryRun(fromDir, toDir string, from, to strategy.Strategy) {
	if toDir != fromDir {
		_, _ = fmt.Fprintf(
			c.Stdout,
			"🔍 [dry-run] Would move %s/ to %s/ and migrate from '%s' to '%s'.\n",
			fromDir,
			toDir,
			from,
			to,
		)
	} else {
		_, _ = fmt.Fprintf(c.Stdout, "🔍 [dry-run] Would migrate %s/ from '%s' to '%s'.\n", fromDir, from, to)
	}
}

// checkDirTarget refuses a directory move that cannot be safe: the target
// must stay inside the project, must not contain the source (a rename into
// its own subtree cannot work), must not exist yet (an existing directory may
// hold files that are not gotools', and the source must not land inside it),
// and must not be a symlink.
func (c *Context) checkDirTarget(toDir string) error {
	if err := config.ValidateDir(toDir); err != nil {
		return err
	}

	target := filepath.Join(c.Root, filepath.FromSlash(toDir))

	if rel, rerr := filepath.Rel(c.ToolsDir(), target); rerr == nil && !strings.HasPrefix(rel, "..") {
		return ierrors.New(ierrors.Usage, "tools directory cannot move into itself: %s", toDir)
	}

	if err := fsx.RejectSymlink(target, "migrate"); err != nil {
		return err
	}

	if _, err := os.Lstat(target); err == nil {
		return ierrors.New(ierrors.Usage, "tools directory %s already exists: remove it or choose another --dir", toDir)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return ierrors.New(ierrors.Environment, "Could not inspect %s: %v", target, err)
	}

	return nil
}

// adoptStrategy handles "the disk already is the target". The manifest follows
// the disk, so a manifest that names another strategy does not keep sync
// refusing.
func (c *Context) adoptStrategy(p MigrateParams) error {
	out := c.Stdout

	to := p.To
	if to == "" {
		to = c.Layout().Strategy
	}

	if !p.PersistStrategy || c.manifestStrategy() == to {
		_, _ = fmt.Fprintf(out, "ℹ️  Already using strategy '%s'. Nothing to do.\n", to)

		return nil
	}

	if p.DryRun {
		_, _ = fmt.Fprintf(out, "🔍 [dry-run] Would set strategy to '%s' in %s.\n", to, config.ManifestFile)

		return nil
	}

	c.Manifest.Strategy = string(to)

	if err := c.Flush(); err != nil {
		return ierrors.Wrap(ierrors.Generic, "Could not write the manifest", err)
	}

	_, _ = fmt.Fprintf(
		out,
		"✅ %s now says strategy '%s' (the tools directory already matches).\n",
		config.ManifestFile,
		to,
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

// rebuild installs every tool at its pinned version under the target layout,
// then records the result in the manifest.
func (c *Context) rebuild(ctx context.Context, p MigrateParams, specs []modparser.Spec) error {
	out := c.Stdout

	// A copy of the Context that works in the target layout.
	target := *c
	if p.To != "" {
		target.Config.Strategy = p.To
	}

	if p.ToDir != "" {
		target.Config.Dir = p.ToDir
		target.Fingerprint = fingerprint.NewFile(target.ToolsDir())
	}

	_, _ = fmt.Fprintf(out, "🔧 Initializing new strategy (%s)...\n", target.Config.Strategy)

	if len(specs) > 0 {
		_, _ = fmt.Fprintf(out, "📦 Re-installing tools under '%s' strategy...\n", target.Config.Strategy)
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
		if p.To != "" {
			c.Manifest.Strategy = string(p.To)
		}

		if p.ToDir != "" {
			c.Manifest.Dir = p.ToDir
		}
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
	target   string          // the directory the new layout is built in
	move     bool            // the whole directory was renamed to target
	existed  bool            // the tools directory existed before
	backup   string          // directory holding the moved entries ("" if none)
	moved    []string        // names moved into backup
	kept     map[string]bool // names that stayed in toolsDir
	lockHeld bool            // the lock file stayed at toolsDir for the move
}

// stashLayout clears the way for the new layout. A strategy change in place
// (move false) moves gotools' own files (modfiles, sums, module directories,
// the fingerprint) out of toolsDir; anything else stays where it is, which
// includes the lock file. A directory move (move true) renames the whole
// directory to target, unrelated files and all: the lock file moves with it,
// so the project lock this command holds is released and retaken there.
func stashLayout(toolsDir, target string, move bool) (*stash, error) {
	st := &stash{toolsDir: toolsDir, target: target, move: move, kept: map[string]bool{}}

	if move {
		if err := stashMove(st); err != nil {
			return nil, err
		}

		return st, nil
	}

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

// stashMove renames the tools directory to the target, after copying it to a
// backup. The rename alone is not the restore: the rebuild rewrites the files
// where they are, so a failure needs the original content, not the directory.
// A symlinked entry is refused first: it would be carried into the new
// directory, where gotools rewrites what it manages. A missing source only
// needs the target's parents.
func stashMove(st *stash) error {
	entries, err := os.ReadDir(st.toolsDir)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return mkdirParent(st.target)
	case err != nil:
		return ierrors.New(ierrors.Generic, "Could not read %s: %v", st.toolsDir, err)
	}

	st.existed = true

	for _, e := range entries {
		if rerr := fsx.RejectSymlink(filepath.Join(st.toolsDir, e.Name()), "migrate"); rerr != nil {
			return rerr
		}
	}

	st.backup, err = os.MkdirTemp(filepath.Dir(st.toolsDir), ".gotools-migrate-")
	if err != nil {
		return ierrors.New(ierrors.Generic, "Could not create a backup directory: %v", err)
	}

	if err := copyDir(st.toolsDir, filepath.Join(st.backup, "dir")); err != nil {
		_ = os.RemoveAll(st.backup)
		st.backup = ""

		return err
	}

	if err := mkdirParent(st.target); err != nil {
		_ = os.RemoveAll(st.backup)
		st.backup = ""

		return err
	}

	// The lock file stays at the old path: the project lock this command holds
	// is an open description of it, and a nested lock looks for it there.
	// Carrying it with the directory would make the nested lock another holder
	// and wait on itself.
	lockPath := filepath.Join(st.toolsDir, lock.FileName)
	if st.lockHeld = fsx.Lexists(lockPath); st.lockHeld {
		if lerr := os.Rename(lockPath, filepath.Join(st.backup, lock.FileName)); lerr != nil {
			_ = os.RemoveAll(st.backup)
			st.backup = ""

			return ierrors.New(ierrors.Generic, "Could not move the lock file aside: %v", lerr)
		}
	}

	if rerr := os.Rename(st.toolsDir, st.target); rerr != nil {
		_ = os.RemoveAll(st.backup)
		st.backup = ""

		return ierrors.New(ierrors.Generic, "Could not move %s to %s: %v", st.toolsDir, st.target, rerr)
	}

	if st.lockHeld {
		if lerr := keepLock(st, lockPath); lerr != nil {
			rerr := st.restore()

			return errors.Join(lerr, rerr)
		}
	}

	return nil
}

// keepLock puts the lock file back at the old path after the directory moved.
func keepLock(st *stash, lockPath string) error {
	if err := os.MkdirAll(st.toolsDir, 0o755); err != nil {
		return ierrors.New(ierrors.Generic, "Could not keep the lock file at %s: %v", lockPath, err)
	}

	if err := os.Rename(filepath.Join(st.backup, lock.FileName), lockPath); err != nil {
		return ierrors.New(ierrors.Generic, "Could not keep the lock file at %s: %v", lockPath, err)
	}

	return nil
}

// copyDir copies the directory tree at src to dst, which must not exist.
// Regular files and directories only: stashMove refuses symlinks first.
func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return ierrors.New(ierrors.Generic, "Could not create %s: %v", dst, err)
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return ierrors.New(ierrors.Generic, "Could not read %s: %v", src, err)
	}

	for _, e := range entries {
		s, d := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())

		fi, serr := os.Stat(s)
		if serr != nil {
			return ierrors.New(ierrors.Generic, "Could not read %s: %v", s, serr)
		}

		if fi.IsDir() {
			if err := copyDir(s, d); err != nil {
				return err
			}

			continue
		}

		data, rerr := os.ReadFile(s)
		if rerr != nil {
			return ierrors.New(ierrors.Generic, "Could not read %s: %v", s, rerr)
		}

		// d is under the backup directory gotools created, from an entry of a
		// directory it already validated; there is no untrusted path here.
		perm := fi.Mode().Perm()
		if werr := os.WriteFile(d, data, perm); werr != nil { //nolint:gosec // G703: d is gotools' own backup path
			return ierrors.New(ierrors.Generic, "Could not write %s: %v", d, werr)
		}
	}

	return nil
}

func mkdirParent(dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return ierrors.New(ierrors.Generic, "Could not create %s: %v", filepath.Dir(dir), err)
	}

	return nil
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
	if s.move {
		return s.restoreMove()
	}

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

// restoreMove undoes the directory rename: the backup goes back to the old
// path, so the directory is byte-for-byte what it was and a tool the rebuild
// installed is not kept. The lock file, kept at the old path for the move, is
// put back inside the directory first.
func (s *stash) restoreMove() error {
	if s.backup == "" {
		return nil // the rename never happened
	}

	if s.lockHeld {
		lockPath := filepath.Join(s.toolsDir, lock.FileName)
		if fsx.Lexists(lockPath) {
			if err := os.Rename(lockPath, filepath.Join(s.backup, "dir", lock.FileName)); err != nil {
				return ierrors.New(ierrors.Generic, "Could not put the lock file back: %v", err)
			}

			_ = os.Remove(s.toolsDir) // the empty directory the move left
		}
	}

	_ = os.RemoveAll(s.target)

	if err := os.Rename(filepath.Join(s.backup, "dir"), s.toolsDir); err != nil {
		return ierrors.New(
			ierrors.Generic,
			"Could not move the tools directory back; your files are preserved in %s: %v",
			s.backup,
			err,
		)
	}

	_ = os.RemoveAll(s.backup)
	s.backup = ""

	return nil
}

func (s *stash) discard() {
	if s.backup != "" {
		_ = os.RemoveAll(s.backup)
	}
}
