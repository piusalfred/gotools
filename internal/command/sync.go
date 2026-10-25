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
	"path"
	"path/filepath"
	"strings"

	urfave "github.com/urfave/cli/v3"

	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/fsx"
	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/goversion"
	"github.com/piusalfred/gotools/internal/manifest"
	"github.com/piusalfred/gotools/internal/modparser"
	"github.com/piusalfred/gotools/internal/strategy"
	"github.com/piusalfred/gotools/internal/tools"
)

// The sync command's name, flags and help text.
const (
	NameSync = "sync"

	FlagJobs = "jobs"

	usageSync = "Sync tool state to match .gotools.json."

	// Each paragraph is one line: help wraps it.
	descriptionSync = `Sync tool state to match .gotools.json. Tidies existing modfiles and reinstalls any tools from the manifest that are missing on disk or whose installed version drifted from the manifest.

If the on-disk strategy doesn't match the manifest, auto-migrates.

Fast path: when nothing changed, a state fingerprint in $GOTOOLS_DIR/.gotools.fingerprint skips the reconciliation entirely. Commit that file so fresh clones are fast too.`
)

// Sync reconciles the tools directory with the manifest.
//
//   - Before checks the request, requires Go and loads the project.
//   - Run syncs: Context.Sync does the work, and init calls it too.
//
// One Sync serves one run: it keeps what Before read from the flags. Build works
// on a copy, so building the command twice never shares state.
type Sync struct {
	c      *Context
	params SyncParams
}

// NewSync returns the sync command.
func NewSync() *Sync { return &Sync{} }

// Name implements Command.
func (*Sync) Name() string { return NameSync }

// Build implements Command.
func (base *Sync) Build(c *Context) *urfave.Command {
	s := *base
	s.c = c

	spec := Spec{
		Name:        NameSync,
		Usage:       usageSync,
		Description: descriptionSync,
		Examples: []string{
			"sync",
			"sync --dry-run",
			"sync --jobs 4",
			"sync --offline",
		},
		Flags: []urfave.Flag{
			&urfave.BoolFlag{Name: FlagDryRun, Usage: "Show what would be done without making changes"},
			&urfave.BoolFlag{Name: FlagOffline, Usage: "Hermetic: fingerprint match only, exit 6 otherwise (GOTOOLS_OFFLINE=1 does the same)"},
			&urfave.IntFlag{Name: FlagJobs, Usage: "Reinstall N tools at a time; split and module only (GOTOOLS_JOBS=N does the same)", Value: 1, Validator: validJobs},
		},
		Before: s.before,
		Run:    s.run,
	}

	return spec.Build(c)
}

func validJobs(n int) error {
	if n < 1 {
		return ierrors.New(ierrors.Usage, "must be a positive number, not %d", n)
	}

	return nil
}

func (s *Sync) before(ctx context.Context, cmd *urfave.Command) error {
	if err := noExtraArgs(cmd); err != nil {
		return err
	}

	s.params = SyncParams{DryRun: cmd.Bool(FlagDryRun), Offline: cmd.Bool(FlagOffline)}

	// A jobs flag that was not given leaves the jobs setting in charge.
	if cmd.IsSet(FlagJobs) {
		s.params.Jobs = cmd.Int(FlagJobs)
	}

	if err := s.c.requireGo(ctx); err != nil {
		return err
	}

	if err := s.c.Load(ctx); err != nil {
		return err
	}

	for _, w := range s.c.Warnings {
		_, _ = fmt.Fprintf(s.c.Stderr, "  ⚠️  %s\n", w)
	}

	return nil
}

func (s *Sync) run(ctx context.Context, _ *urfave.Command) error {
	return s.c.Sync(ctx, s.params)
}

// SyncParams says how Sync runs.
type SyncParams struct {
	DryRun bool
	// Offline takes only the fingerprint fast path. The offline setting does
	// the same.
	Offline bool
	// Jobs is how many tools reinstall at once; 0 leaves it to the jobs
	// setting.
	Jobs int
	// Unlocked syncs without taking the project lock: the caller holds it, or
	// this is a dry run that must not create the lock file in a tools
	// directory that does not exist yet.
	Unlocked bool
}

// syncRun is what the steps of one sync share.
type syncRun struct {
	dryRun bool
	target string // the Go version modfiles declare
	jobs   int
}

// Sync reconciles the tools directory with the manifest. The project must be
// loaded. Offline takes only the fingerprint fast path and no lock: it cannot
// write, so concurrent CI jobs sharing a workspace never contend.
func (c *Context) Sync(ctx context.Context, p SyncParams) error {
	jobs := max(p.Jobs, 1)
	if p.Jobs == 0 {
		jobs = max(c.Config.Settings.Jobs, 1)
	}

	target, err := goversion.Resolve(ctx, c.Runner(), c.Config.GoVersion, c.Root)
	if err != nil {
		return err
	}

	if p.Offline || c.Config.Settings.Offline {
		if c.syncUpToDate(target) {
			_, _ = fmt.Fprintln(c.Stdout, "✅ Tools up to date (fingerprint match).")

			return nil
		}

		return ierrors.New(ierrors.Offline, "Offline mode: manifest has changed. Network required.\n   Run '%s sync' locally and commit the updated files.", c.Program)
	}

	if len(c.Manifest.Tools) > 0 {
		if err := c.requireRootModule(); err != nil {
			return err
		}
	}

	r := syncRun{dryRun: p.DryRun, target: target, jobs: jobs}

	if p.Unlocked {
		return c.syncLocked(ctx, r)
	}

	return c.WithLock(ctx, func(ctx context.Context) error { return c.syncLocked(ctx, r) })
}

func (c *Context) syncLocked(ctx context.Context, r syncRun) error {
	l := c.Layout()
	out := c.Stdout

	if disk := strategy.Detect(l.ToolsDir(c.Root)).Strategy; disk != "" && disk != l.Strategy {
		if r.dryRun {
			_, _ = fmt.Fprintf(out, "🔍 [dry-run] Would auto-migrate from '%s' to '%s'\n", disk, l.Strategy)

			return nil
		}

		_, _ = fmt.Fprintf(out, "⚠️  Strategy mismatch: %s says '%s' but %s/ looks like '%s'.\n", config.ManifestFile, l.Strategy, l.Dir, disk)
		_, _ = fmt.Fprintf(out, "🔀 Auto-migrating to '%s'...\n", l.Strategy)

		return c.Migrate(ctx, MigrateParams{To: l.Strategy})
	}

	if !r.dryRun && c.syncUpToDate(r.target) {
		_, _ = fmt.Fprintln(out, "✅ Tools up to date.")

		return nil
	}

	if r.dryRun {
		_, _ = fmt.Fprintf(out, "🔍 [dry-run] Would sync (strategy=%s, go=%s, dir=%s)\n", l.Strategy, r.target, l.Dir)
	} else {
		_, _ = fmt.Fprintf(out, "🔄 Syncing (strategy=%s, go=%s, dir=%s)...\n", l.Strategy, r.target, l.Dir)
	}

	if err := c.tidyAll(ctx, r); err != nil {
		return err
	}

	if err := c.discoverFromDisk(r); err != nil {
		return err
	}

	work := c.reinstallList(r)

	if r.dryRun {
		_, _ = fmt.Fprintf(out, "🔍 [dry-run] Would restore %d tool(s) from manifest.\n", len(work))

		return nil
	}

	if err := c.Tools().Restore(ctx, work, tools.RestoreOptions{Jobs: r.jobs}); err != nil {
		return err
	}

	if err := c.Record(ctx); err != nil {
		return ierrors.New(ierrors.Generic, "Could not write the fingerprint: %v", err)
	}

	if len(work) > 0 {
		_, _ = fmt.Fprintf(out, "✅ Sync restored %d tool(s) from manifest.\n", len(work))
	} else {
		_, _ = fmt.Fprintln(out, "✅ Sync complete.")
	}

	return nil
}

// syncUpToDate is the fast path: the fingerprint matches and every modfile
// still exists with the manifest's version, which catches hand edits that
// bypassed the manifest.
func (c *Context) syncUpToDate(target string) bool {
	recorded, err := c.Fingerprint.Read()
	if err != nil || recorded != c.Fingerprint.Generate(c.fingerprintParams(target)) {
		return false
	}

	for _, t := range c.Manifest.SortedTools() {
		if c.diskVersion(t) != t.Version || t.Version == "" {
			return false
		}
	}

	return true
}

// diskVersion is the version of t in its modfile, or "" if absent.
func (c *Context) diskVersion(t manifest.Tool) string {
	modfile, err := c.Layout().ModFile(c.Root, t.Name)
	if err != nil {
		return ""
	}

	mod, err := c.ModFiles.Read(modfile)
	if err != nil {
		return ""
	}

	return mod.VersionFor(t.Package)
}

func (c *Context) modfileExists(name string) bool {
	modfile, err := c.Layout().ModFile(c.Root, name)
	if err != nil {
		return false
	}

	fi, err := os.Stat(modfile)

	return err == nil && fi.Mode().IsRegular()
}

// tidyAll runs `go mod tidy` on every modfile in the tools directory.
func (c *Context) tidyAll(ctx context.Context, r syncRun) error {
	l := c.Layout()
	dir := l.ToolsDir(c.Root)

	switch l.Strategy {
	case strategy.Unified:
		if r.dryRun {
			_, _ = fmt.Fprintf(c.Stdout, "  [dry-run] Would go mod tidy in %s/\n", l.Dir)

			return nil
		}

		modfile := filepath.Join(dir, "go.mod")

		if err := os.MkdirAll(dir, 0o755); err != nil {
			return ierrors.New(ierrors.Generic, "Could not create %s: %v", dir, err)
		}

		if err := fsx.RejectSymlink(modfile, "sync (unified)"); err != nil {
			return err
		}

		if _, err := os.Stat(modfile); errors.Is(err, fs.ErrNotExist) {
			fresh := fmt.Sprintf("module %s\n\ngo %s\n", l.ModulePath(c.ModulePrefix(), ""), r.target)
			if err := fsx.WriteFileAtomic(modfile, []byte(fresh), 0o644); err != nil {
				return ierrors.New(ierrors.Generic, "Could not create %s: %v", modfile, err)
			}
		}

		return c.editAndTidy(ctx, dir, r.target, "go mod tidy in "+l.Dir)
	case strategy.Split:
		return c.tidySplit(ctx, r)
	case strategy.Module:
		return c.tidyModules(ctx, r)
	}

	return ierrors.New(ierrors.Environment, "Unknown strategy: %s", l.Strategy)
}

// editAndTidy sets the go directive and tidies the module in dir.
func (c *Context) editAndTidy(ctx context.Context, dir, target, what string) error {
	res, err := c.Runner().Run(ctx, gocmd.Call{Dir: dir, Args: []string{"mod", "edit", "-go=" + target}})
	if err != nil {
		return timeoutOr(err, what)
	}

	if res.Code != 0 {
		return ierrors.New(ierrors.Generic, "go mod edit failed in %s\n%s", dir, strings.TrimSpace(string(res.Stderr)))
	}

	res, err = c.Network().Run(ctx, gocmd.Call{Dir: dir, Args: []string{"mod", "tidy"}})
	if err != nil {
		return timeoutOr(err, what)
	}

	if res.Code != 0 {
		output := strings.TrimSpace(string(res.Stderr) + string(res.Stdout))

		return ierrors.New(tidyFailureCode(output), "%s failed\n%s", what, output)
	}

	return nil
}

// tidySplit tidies each tools/<name>.mod in a scratch module. Go cannot
// reconcile two module identities when run with -modfile from inside another
// module's tree, so the modfile is copied out, given a replace to the local
// parent module (which has all the packages, unlike a stale published
// version), tidied, and copied back.
func (c *Context) tidySplit(ctx context.Context, r syncRun) error {
	dir := c.ToolsDir()

	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return ierrors.New(ierrors.Generic, "Could not read %s: %v", dir, err)
	}

	for _, e := range entries {
		n := e.Name()
		// A stray tools/go.mod is not a split modfile.
		if n == "go.mod" || !strings.HasSuffix(n, ".mod") || strings.HasPrefix(n, ".") {
			continue
		}

		modfile := filepath.Join(dir, n)

		if fi, err := os.Stat(modfile); err != nil || !fi.Mode().IsRegular() {
			continue
		}

		if r.dryRun {
			_, _ = fmt.Fprintf(c.Stdout, "  [dry-run] Would tidy %s\n", n)

			continue
		}

		sumfile := strings.TrimSuffix(modfile, ".mod") + ".sum"

		for _, f := range []string{modfile, sumfile} {
			if err := fsx.RejectSymlink(f, "sync (split: "+n+")"); err != nil {
				return err
			}
		}

		_, _ = fmt.Fprintf(c.Stdout, "  ↻ %s\n", n)

		if err := c.tidyScratch(ctx, r, modfile, sumfile, n); err != nil {
			return err
		}
	}

	return nil
}

func (c *Context) tidyScratch(ctx context.Context, r syncRun, modfile, sumfile, n string) error {
	tmp, err := os.MkdirTemp("", "gotools-tidy-")
	if err != nil {
		return ierrors.New(ierrors.Generic, "Could not create a scratch directory: %v", err)
	}

	defer func() { _ = os.RemoveAll(tmp) }()

	mod, err := os.ReadFile(modfile)
	if err != nil {
		return ierrors.New(ierrors.Generic, "Could not read %s: %v", modfile, err)
	}

	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), mod, 0o600); err != nil {
		return ierrors.New(ierrors.Generic, "Could not write the scratch module: %v", err)
	}

	if sum, err := os.ReadFile(sumfile); err == nil {
		if err := os.WriteFile(filepath.Join(tmp, "go.sum"), sum, 0o600); err != nil {
			return ierrors.New(ierrors.Generic, "Could not write the scratch module: %v", err)
		}
	}

	parent := c.ModulePrefix()

	if parent != "" {
		if err := c.goModEdit(ctx, tmp, "-replace="+parent+"="+c.Root); err != nil {
			return err
		}
	}

	if err := c.editAndTidy(ctx, tmp, r.target, "go mod tidy for "+n); err != nil {
		return err
	}

	if parent != "" {
		if err := c.goModEdit(ctx, tmp, "-dropreplace="+parent); err != nil {
			return err
		}
	}

	return copyBack(tmp, modfile, sumfile)
}

func (c *Context) goModEdit(ctx context.Context, dir, flag string) error {
	res, err := c.Runner().Run(ctx, gocmd.Call{Dir: dir, Args: []string{"mod", "edit", flag}})
	if err != nil {
		return timeoutOr(err, "go mod edit")
	}

	if res.Code != 0 {
		return ierrors.New(ierrors.Generic, "go mod edit %s failed\n%s", flag, strings.TrimSpace(string(res.Stderr)))
	}

	return nil
}

// copyBack writes the tidied scratch module over the real modfile and sum.
func copyBack(tmp, modfile, sumfile string) error {
	b, err := os.ReadFile(filepath.Join(tmp, "go.mod"))
	if err != nil {
		return ierrors.New(ierrors.Generic, "Could not read the tidied module: %v", err)
	}

	if err := fsx.WriteFileAtomic(modfile, b, 0o644); err != nil {
		return ierrors.New(ierrors.Generic, "Could not write %s: %v", modfile, err)
	}

	sum, err := os.ReadFile(filepath.Join(tmp, "go.sum"))

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil // a tool module with no dependencies has no sum
	case err != nil:
		return ierrors.New(ierrors.Generic, "Could not read the tidied sum: %v", err)
	}

	if err := fsx.WriteFileAtomic(sumfile, sum, 0o644); err != nil {
		return ierrors.New(ierrors.Generic, "Could not write %s: %v", sumfile, err)
	}

	return nil
}

func (c *Context) tidyModules(ctx context.Context, r syncRun) error {
	dir := c.ToolsDir()

	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return ierrors.New(ierrors.Generic, "Could not read %s: %v", dir, err)
	}

	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, ".") {
			continue
		}

		sub := filepath.Join(dir, n)
		modfile := filepath.Join(sub, "go.mod")

		if fi, err := os.Stat(sub); err != nil || !fi.IsDir() {
			continue
		}

		if fi, err := os.Stat(modfile); err != nil || !fi.Mode().IsRegular() {
			continue
		}

		if r.dryRun {
			_, _ = fmt.Fprintf(c.Stdout, "  [dry-run] Would tidy %s\n", n)

			continue
		}

		for _, f := range []string{sub, modfile} {
			if err := fsx.RejectSymlink(f, "sync (module)"); err != nil {
				return err
			}
		}

		_, _ = fmt.Fprintf(c.Stdout, "  ↻ %s\n", n)

		if err := c.editAndTidy(ctx, sub, r.target, "go mod tidy for "+n); err != nil {
			return err
		}
	}

	return nil
}

// discoverFromDisk adds tools that the tools directory declares but the
// manifest does not know about. A dry run only reports.
func (c *Context) discoverFromDisk(r syncRun) error {
	specs, err := c.ToolsOnDisk()
	if err != nil {
		_, _ = fmt.Fprintf(c.Stderr, "⚠️  Some tools on disk could not be read: %v\n", err)
	}

	added := 0

	for _, s := range specs {
		if _, ok := c.Manifest.Tool(s.Name); ok {
			continue
		}

		if r.dryRun {
			_, _ = fmt.Fprintf(c.Stdout, "  [dry-run] Would add %s (%s@%s) — discovered from disk\n", s.Name, s.Package, s.Version)

			continue
		}

		c.Manifest.SetTool(manifest.Tool{Name: s.Name, Source: "go", Package: s.Package, Version: s.Version})
		_, _ = fmt.Fprintf(c.Stdout, "  ✚ %s (%s@%s) — discovered from disk\n", s.Name, s.Package, s.Version)
		added++
	}

	if added == 0 {
		return nil
	}

	if err := c.Flush(); err != nil {
		return ierrors.Wrap(ierrors.Generic, "Could not write the manifest", err)
	}

	_, _ = fmt.Fprintf(c.Stdout, "✅ Added %d tool(s) to manifest.\n", added)

	return nil
}

// reinstallList lists manifest tools whose modfile is missing or carries a
// different version than the manifest.
func (c *Context) reinstallList(r syncRun) []manifest.Tool {
	var work []manifest.Tool

	for _, t := range c.Manifest.SortedTools() {
		onDisk := c.modfileExists(t.Name)

		var have string
		if onDisk {
			have = c.diskVersion(t)
		}

		if onDisk && have == t.Version {
			continue
		}

		switch {
		case r.dryRun:
			_, _ = fmt.Fprintf(c.Stdout, "  🔍 [dry-run] Would reinstall %s (%s@%s)\n", t.Name, t.Package, t.Version)
		case !onDisk:
			_, _ = fmt.Fprintf(c.Stdout, "  ⬇ %s (%s@%s) — missing, reinstalling...\n", t.Name, t.Package, t.Version)
		default:
			if have == "" {
				have = "unknown"
			}

			_, _ = fmt.Fprintf(c.Stdout, "  ⚠ %s: manifest %s, disk %s — reinstalling...\n", t.Name, t.Version, have)
		}

		work = append(work, t)
	}

	return work
}

// ToolsOnDisk lists the tools the tools directory declares, for the project's
// strategy, with the manifest's name for a package it already knows. A modfile
// that cannot be read, or whose name is unsafe, is skipped and reported in the
// returned error, alongside the entries that did load.
//
// An aliased tool in the unified strategy keeps its manifest name instead of
// being discovered again under the name inferred from its binary, and a stray
// tools/go.mod in the split strategy is not a tool named "go".
func (c *Context) ToolsOnDisk() ([]modparser.Spec, error) {
	l := c.Layout()
	dir := l.ToolsDir(c.Root)

	switch l.Strategy {
	case strategy.Unified:
		mod, err := c.ModFiles.Read(filepath.Join(dir, "go.mod"))
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}

		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path.Join(path.Clean(filepath.ToSlash(l.Dir)), "go.mod"), err)
		}

		specs := mod.Scan()
		for i := range specs {
			if n, ok := c.Manifest.FindByPackage(specs[i].Package); ok {
				specs[i].Name = n
			}
		}

		return specs, nil
	case strategy.Split, strategy.Module:
	default:
		return nil, ierrors.New(ierrors.Environment, "Unknown strategy: %s", l.Strategy)
	}

	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	var (
		specs []modparser.Spec
		errs  []error
	)

	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, ".") {
			continue
		}

		var name string

		switch fi, err := os.Stat(filepath.Join(dir, n)); {
		case err != nil:
			continue
		case l.Strategy == strategy.Split && fi.Mode().IsRegular() && n != "go.mod" && strings.HasSuffix(n, ".mod"):
			name = strings.TrimSuffix(n, ".mod")
		case l.Strategy == strategy.Module && fi.IsDir() && isRegularFile(filepath.Join(dir, n, "go.mod")):
			name = n
		default:
			continue
		}

		if err := strategy.ValidateName(name); err != nil {
			errs = append(errs, fmt.Errorf("ignoring %s: %w", n, err))

			continue
		}

		file, _ := l.ModFile(c.Root, name)

		mod, err := c.ModFiles.Read(file)
		if err != nil {
			errs = append(errs, fmt.Errorf("ignoring %s: %w", n, err))

			continue
		}

		if len(mod.Tools) == 0 {
			continue
		}

		spec := mod.Scan()[0]
		spec.Name = name
		specs = append(specs, spec)
	}

	return specs, errors.Join(errs...)
}
