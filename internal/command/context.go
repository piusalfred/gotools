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
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/fingerprint"
	"github.com/piusalfred/gotools/internal/fsx"
	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/goversion"
	"github.com/piusalfred/gotools/internal/lock"
	"github.com/piusalfred/gotools/internal/manifest"
	"github.com/piusalfred/gotools/internal/modparser"
	"github.com/piusalfred/gotools/internal/resolve"
	"github.com/piusalfred/gotools/internal/selfupdate"
	"github.com/piusalfred/gotools/internal/strategy"
	"github.com/piusalfred/gotools/internal/tools"
	"github.com/piusalfred/gotools/internal/tracer"
	"github.com/piusalfred/gotools/internal/verify"
)

// Context is what a command works on. The first group is handed in by the
// caller; the second is filled by Load. It grows as commands move over.
type Context struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Environ        []string // NAME=value pairs, like os.Environ
	Program        string   // the name used in messages, e.g. "gotools"
	Version        string   // the gotools version, e.g. "v0.6.7"
	WorkDir        string   // where the command was started
	Go             gocmd.Runner
	// Process runs a tool with the caller's streams; the real program when nil.
	Process gocmd.ProcessRunner
	// Interactive reports that the user can be asked a question: stdin is a
	// terminal.
	Interactive bool
	// Completing reports that the shell is asking for completions: the commands
	// answer with words and run nothing else.
	Completing bool
	// ReadOnly makes Load change nothing on disk: it leaves the temp files a
	// crashed write left behind. A command that promises to write nothing sets it.
	ReadOnly bool
	// Exe locates the running executable; os.Executable when nil.
	Exe func() (string, error)
	// Releases is where self-update looks for releases; the project's GitHub
	// releases when its APIURL is empty.
	Releases selfupdate.Source

	Root     string             // the project root
	Config   config.Config      // layout and settings, after every layer
	Manifest *manifest.Manifest // the file's values; New() when there is no file
	// HasManifest reports whether the manifest file exists.
	HasManifest bool
	// Warnings are problems found while loading that did not stop it.
	Warnings []string
	Locker   lock.Locker
	ModFiles modparser.Parser
	// Fingerprint records the state a sync reconciled.
	Fingerprint fingerprint.Fingerprinter
}

// Load finds the project root, reads the manifest, resolves the configuration
// (default < manifest < environment < opts) and builds the seams that depend
// on it. A seam the caller already set is kept.
func (c *Context) Load(ctx context.Context, opts ...config.Option) error {
	dir, err := c.workDir()
	if err != nil {
		return err
	}

	c.Root, _ = manifest.FindRoot(dir)
	if !c.ReadOnly {
		manifest.CleanupTemps(c.ManifestPath())
	}

	res, err := manifest.Load(c.ManifestPath())

	switch {
	case err == nil:
		c.Manifest, c.Warnings, c.HasManifest = res.Manifest, res.Warnings, true
	case errors.Is(err, manifest.ErrMissing):
		c.Manifest, c.Warnings, c.HasManifest = manifest.New(), nil, false
	default:
		return err
	}

	cfg, err := config.Load(c.Root, c.Environ, opts...)
	if err != nil {
		return err
	}

	c.Config = cfg

	if err := c.rejectSymlinkedToolsDir(); err != nil {
		return err
	}

	c.buildSeams()

	return nil
}

// buildSeams fills the seams that depend on the configuration. A seam the caller
// already set is kept.
func (c *Context) buildSeams() {
	if c.Locker == nil {
		c.Locker = lock.NewFileLocker(c.ToolsDir(),
			lock.WithTimeout(time.Duration(c.Config.Settings.LockTimeout)*time.Second),
			lock.WithDisabled(c.Config.Settings.NoLock),
		)
	}

	if c.ModFiles == nil {
		c.ModFiles = modparser.XParser{}
	}

	if c.Fingerprint == nil {
		c.Fingerprint = fingerprint.NewFile(c.ToolsDir())
	}
}

// LoadOrDefaults is Load for a command that must report on a project even when it
// cannot be loaded: a manifest from a newer gotools, malformed JSON, no
// strategy, a bad tools directory. It returns why the project is unusable, or ""
// when it loaded and is valid. When it could not load, the Context holds a
// stand-in: no manifest, and a layout from the environment, else the defaults.
func (c *Context) LoadOrDefaults(ctx context.Context, opts ...config.Option) string {
	err := c.Load(ctx, opts...)
	if err == nil {
		// A manifest without a top-level strategy loads, but is not valid.
		if c.HasManifest {
			if status, verr := manifest.Check(c.ManifestPath()); status != manifest.Valid {
				return config.ManifestFile + ": " + c.reason(verr)
			}
		}

		return ""
	}

	dir, _ := c.workDir()
	c.Root, _ = manifest.FindRoot(dir)
	c.Manifest, c.HasManifest, c.Warnings = manifest.New(), false, nil

	cfg, cerr := config.Load(c.Root, c.Environ, append(opts, config.WithoutManifest())...)
	if cerr != nil {
		cfg, _ = config.Load(c.Root, nil, config.WithoutManifest())
	}

	c.Config = cfg
	c.buildSeams()

	return c.reason(err)
}

// reason is the first line of err, with the manifest named as the user knows it
// instead of by its absolute path.
func (c *Context) reason(err error) string {
	return strings.ReplaceAll(firstLine(err.Error()), c.ManifestPath(), config.ManifestFile)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")

	return line
}

func (c *Context) rejectSymlinkedToolsDir() error {
	fi, err := os.Lstat(c.ToolsDir())
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return nil
	}

	return ierrors.New(ierrors.Environment, "Tools directory must not be a symlink: %s\n   Remove the symlink and retry.", c.Config.Dir)
}

// requireGo checks that go is installed and new enough, from the directory the
// command was started in.
func (c *Context) requireGo(ctx context.Context) error {
	dir, _ := c.workDir()

	return goversion.Require(ctx, c.Runner(), dir)
}

// workDir is where the command was started: WorkDir, else the process's own.
func (c *Context) workDir() (string, error) {
	if c.WorkDir != "" {
		return c.WorkDir, nil
	}

	wd, err := os.Getwd()
	if err != nil {
		return "", ierrors.Wrap(ierrors.Environment, "Could not determine the working directory", err)
	}

	return wd, nil
}

// ManifestPath is where the project's manifest lives.
func (c *Context) ManifestPath() string { return filepath.Join(c.Root, config.ManifestFile) }

// Layout is the strategy and tools directory in force.
func (c *Context) Layout() strategy.Layout {
	return strategy.Layout{Strategy: c.Config.Strategy, Dir: c.Config.Dir}
}

// ToolsDir is the tools directory under the project root.
func (c *Context) ToolsDir() string { return c.Layout().ToolsDir(c.Root) }

// ModulePrefix is the configured module path prefix, else the module path of
// the root go.mod, else "".
func (c *Context) ModulePrefix() string {
	if c.Config.ModulePrefix != "" {
		return c.Config.ModulePrefix
	}

	return modparser.ModulePath(c.Root)
}

// Runner is the go runner commands use: the injected one, else the real go
// binary with GOWORK=off, echoing each call to the error stream when verbose is
// on.
func (c *Context) Runner() gocmd.Runner {
	if c.Go != nil {
		return c.Go
	}

	// The tool modules are their own modules, kept apart from the project's:
	// a go.work file that happens to be above them must not pull them into a
	// workspace, where go refuses -modfile.
	r := gocmd.ExecRunner{Env: []string{"GOWORK=off"}}
	if c.Config.Settings.Verbose {
		r.Verbose = c.Stderr
	}

	return r
}

// Network is Runner bounded by the operation timeout, for network-bound calls
// such as go get and mod tidy.
func (c *Context) Network() gocmd.Runner {
	return gocmd.WithTimeout(c.Runner(), time.Duration(c.Config.Settings.OperationTimeout)*time.Second)
}

// WithLock runs fn while holding the project lock. It is re-entrant through
// the context it passes to fn.
func (c *Context) WithLock(ctx context.Context, fn func(context.Context) error) error {
	return lock.With(ctx, c.Locker, c.ToolsDir(), fn)
}

// Flush writes the manifest. An empty module prefix is first resolved from the
// root go.mod, then the tools directory, so the file never stores an empty one.
// Values that came from the environment are never in the manifest, so they are
// never written.
func (c *Context) Flush() error {
	m := c.Manifest

	if m.ModulePrefix == "" {
		m.ModulePrefix = modparser.ModulePath(c.Root)
	}

	if m.ModulePrefix == "" {
		m.ModulePrefix = orDefault(m.Dir, config.DefaultDir)
	}

	if err := m.Flush(c.ManifestPath()); err != nil {
		return err
	}

	c.HasManifest = true

	return nil
}

// Tools is the tool manager for this project. Build it per command: the go
// runners and the streams are the command's.
func (c *Context) Tools() *tools.GoManager {
	return tools.New(tools.Config{
		Root:     c.Root,
		Layout:   c.Layout(),
		Prefix:   c.ModulePrefix(),
		Store:    store{c},
		Versions: versions{c},
		ModFiles: c.ModFiles,
		Go:       c.Runner(),
		Network:  c.Network(),
		Recorder: c,
		Advisor:  verify.Advisor{Runner: c.Runner()},
		Out:      c.Stdout,
		Err:      c.Stderr,
		Program:  c.Program,
	})
}

// Record writes the sync fingerprint of the manifest as it stands, so the next
// sync can take its fast path. Callers record it only when disk and manifest
// agree.
func (c *Context) Record(ctx context.Context) error {
	goVersion, err := goversion.Resolve(ctx, c.Runner(), c.Config.GoVersion, c.Root)
	if err != nil {
		return err
	}

	return c.Fingerprint.Write(c.Fingerprint.Generate(c.fingerprintParams(goVersion)))
}

// fingerprintParams is the state a fingerprint covers, as the manifest and
// configuration stand now, for the given resolved Go version.
func (c *Context) fingerprintParams(goVersion string) fingerprint.Params {
	var list []fingerprint.Tool
	for _, t := range c.Manifest.SortedTools() {
		list = append(list, fingerprint.Tool{Name: t.Name, Source: t.Source, Package: t.Package, Version: t.Version})
	}

	return fingerprint.Params{
		Tools:        list,
		Files:        c.toolFiles(),
		Strategy:     string(c.Config.Strategy),
		GoVersion:    goVersion,
		ModulePrefix: c.ModulePrefix(),
	}
}

// toolFiles are the modfiles and sums in the tools directory, by content, so a
// hand edit to one is not taken for a project that is in sync. Dot-files (the
// fingerprint, the lock) and symlinks are not part of it.
func (c *Context) toolFiles() []fingerprint.File {
	root := c.ToolsDir()

	var files []fingerprint.File

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // what cannot be read is not covered
		}

		switch {
		case path == root:
			return nil
		case strings.HasPrefix(d.Name(), "."):
			if d.IsDir() {
				return filepath.SkipDir
			}

			return nil
		case !d.Type().IsRegular():
			return nil
		}

		digest, derr := fingerprint.FileDigest(path)
		if derr != nil {
			return nil
		}

		rel, _ := filepath.Rel(root, path)
		files = append(files, fingerprint.File{Path: filepath.ToSlash(rel), Digest: digest})

		return nil
	})

	return files
}

// store lets tools persist the manifest through the Context.
type store struct{ c *Context }

func (s store) Manifest() *manifest.Manifest { return s.c.Manifest }
func (s store) Flush() error                 { return s.c.Flush() }

// versions resolves the Go version a new tool's modfile declares.
type versions struct{ c *Context }

func (v versions) Resolve(ctx context.Context) (string, error) {
	return goversion.Resolve(ctx, v.c.Runner(), v.c.Config.GoVersion, v.c.Root)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

func gocmdCall(dir string, args ...string) gocmd.Call { return gocmd.Call{Dir: dir, Args: args} }

// timeoutOr turns a bounded run's timeout into a Network error naming what
// was running; other errors pass through.
func timeoutOr(err error, what string) error {
	if te, ok := errors.AsType[*gocmd.TimeoutError](err); ok {
		return ierrors.New(ierrors.Network, "%s timed out after %s. Check your network or raise %s.", what, te.After, config.EnvVarOperationTimeout)
	}

	return err
}

// prepareInstall requires Go, loads the project and refuses offline mode:
// installing a new tool needs the module proxy.
func (c *Context) prepareInstall(ctx context.Context, offline bool) error {
	if err := c.requireGo(ctx); err != nil {
		return err
	}

	if err := c.Load(ctx); err != nil {
		return err
	}

	for _, w := range c.Warnings {
		_, _ = fmt.Fprintf(c.Stderr, "  ⚠️  %s\n", w)
	}

	if offline || c.Config.Settings.Offline {
		return ierrors.New(ierrors.Offline, "Offline mode: cannot install new tools without network.")
	}

	return c.requireRootModule()
}

// requireRootModule refuses the split strategy in a project that has no go.mod
// in its root. Split runs go from the root with -modfile pointing at the tool's
// own modfile, and go cannot use -modfile outside a module ("cannot find main
// module, but -modfile was set"). Say so before go does, and say what to do.
func (c *Context) requireRootModule() error {
	if c.Config.Strategy != strategy.Split || isRegularFile(c.RootGoMod()) {
		return nil
	}

	return ierrors.New(ierrors.Environment, "The split strategy needs a go.mod in the project root (%s): go cannot use a tool's modfile outside a module.\n   Run 'go mod init' there, or use the module or unified strategy: '%s migrate module'.", c.Root, c.Program)
}

// InstallParams says what Install installs and how.
type InstallParams struct {
	// Specs are the tools to install, one after the other. Each is atomic: a
	// failure stops the run and leaves the earlier ones in place.
	Specs []tools.AddOptions
	// Force overwrites a tool that is already managed under the same name.
	Force bool
}

// Install installs p.Specs under one lock. A name or package the manifest
// already has is reconciled first: refused, or, when it is the same package
// under another name, offered as a choice on a terminal. The project must be
// loaded and prepared (see prepareInstall).
func (c *Context) Install(ctx context.Context, p InstallParams) error {
	return c.WithLock(ctx, func(ctx context.Context) error {
		manager := c.Tools()

		for _, s := range p.Specs {
			pkgBase, _, _ := strings.Cut(s.Pkg, "@")

			name, done, err := c.reconcileExisting(ctx, manager, s.Name, s.Pkg, pkgBase, p.Force)
			if err != nil {
				return err
			}

			if done {
				continue
			}

			s.Name = name

			if _, err := manager.Add(ctx, s); err != nil {
				return err
			}
		}

		return nil
	})
}

// reconcileExisting handles a name or package the manifest already has. done
// reports that the request was fully handled (rename or skip). Choosing to
// upgrade installs over the existing entry, which needs no --force.
func (c *Context) reconcileExisting(ctx context.Context, manager tools.Manager, name, pkg, pkgBase string, force bool) (string, bool, error) {
	if force {
		return name, false, nil
	}

	if _, exists := c.Manifest.Tool(name); exists {
		return "", false, ierrors.New(ierrors.Usage, "Tool '%s' already exists in manifest. Use --force to overwrite.", name)
	}

	// The same package managed under a different name: the user may not know
	// it is already here. Never refuse: inform, and on a terminal offer to
	// install another copy, upgrade the existing entry, or rename it.
	managedAs, ok := c.Manifest.FindByPackage(pkgBase)
	if !ok || managedAs == name {
		return name, false, nil
	}

	managed, _ := c.Manifest.Tool(managedAs)
	out, prog := c.Stdout, c.Program

	if !c.Interactive {
		_, _ = fmt.Fprintf(out, "ℹ️  %s is already installed as '%s' (version=%s). Installing another entry under '%s' (allowed only at a different version).\n", pkgBase, managedAs, managed.Version, name)
		_, _ = fmt.Fprintf(out, "   Rename instead:   %s rename %s %s\n", prog, managedAs, name)
		_, _ = fmt.Fprintf(out, "   Upgrade instead:  %s install --force %s %s\n", prog, managedAs, pkg)

		return name, false, nil
	}

	_, _ = fmt.Fprintf(out, "ℹ️  %s is already installed as '%s' (version=%s).\n", pkgBase, managedAs, managed.Version)
	_, _ = fmt.Fprint(out, "   Do you want to install another version of this tool?\n")
	_, _ = fmt.Fprintf(out, "     1) install     install '%s' as another entry\n", name)
	_, _ = fmt.Fprintf(out, "     2) upgrade     update '%s' to the requested version\n", managedAs)
	_, _ = fmt.Fprintf(out, "     3) rename      rename '%s' → '%s' (no new install)\n", managedAs, name)
	_, _ = fmt.Fprintf(out, "     4) skip        do nothing — keep '%s' as it is\n", managedAs)
	_, _ = fmt.Fprint(out, "   Choose [1/2/3/4] (default: 1) ")

	switch strings.TrimSpace(c.readLine()) {
	case "2":
		return managedAs, false, nil
	case "3":
		return "", true, manager.Rename(ctx, managedAs, name, tools.RenameOptions{})
	case "4":
		_, _ = fmt.Fprintf(out, "   Skipped — %s stays managed as '%s'.\n", pkgBase, managedAs)

		return "", true, nil
	}

	return name, false, nil
}

// PlanAdoptionParams says how PlanAdoption reads the root go.mod.
type PlanAdoptionParams struct {
	// NoMigrate plans nothing: the root go.mod is to be left untouched.
	NoMigrate bool
}

// RootGoMod is the go.mod in the project root.
func (c *Context) RootGoMod() string { return filepath.Join(c.Root, "go.mod") }

// PlanAdoption lists the tools the root go.mod declares. It refuses when two of
// them would share one manifest name, because the second would look "already
// managed", be skipped, and then lose its directive when go.mod is stripped.
func (c *Context) PlanAdoption(p PlanAdoptionParams) ([]modparser.Spec, error) {
	rootMod := c.RootGoMod()

	if p.NoMigrate || !isRegularFile(rootMod) {
		return nil, nil
	}

	mod, err := c.ModFiles.Read(rootMod)
	if err != nil {
		return nil, ierrors.New(ierrors.Environment, "Could not read %s: %v\n   Fix it, or run init with --no-migrate.", rootMod, err)
	}

	// The tool paths are about to reach `go get`; refuse a bad one before
	// anything is installed.
	if err := c.ModFiles.Validate(mod); err != nil {
		return nil, ierrors.New(ierrors.Environment, "Invalid %s: %v\n   Fix it, or run init with --no-migrate.", rootMod, err)
	}

	byName := map[string]string{}

	for _, t := range mod.Scan() {
		if other, ok := byName[t.Name]; ok && other != t.Package {
			return nil, collision(t.Name, other, t.Package)
		}

		byName[t.Name] = t.Package

		if have, ok := c.Manifest.Tool(t.Name); ok && have.Package != t.Package {
			return nil, collision(t.Name, have.Package+" (already managed)", t.Package)
		}
	}

	return mod.Scan(), nil
}

func collision(name, a, b string) error {
	return ierrors.New(ierrors.Usage,
		"Two tools would be managed as '%s': %s and %s\n   Install them under distinct names ('install <name> <pkg>'), then run init again.", name, a, b)
}

// ManagedAs is the manifest name already serving t: its own name, or the name
// the same package has under another one. A tool that is already managed gets
// no second name.
func (c *Context) ManagedAs(t modparser.Spec) (string, bool) {
	if _, ok := c.Manifest.Tool(t.Name); ok {
		return t.Name, true
	}

	return c.Manifest.FindByPackage(t.Package)
}

// Unmanaged are the specs the manifest does not serve yet.
func (c *Context) Unmanaged(specs []modparser.Spec) []modparser.Spec {
	var out []modparser.Spec

	for _, s := range specs {
		if _, ok := c.ManagedAs(s); !ok {
			out = append(out, s)
		}
	}

	return out
}

// AdoptParams says what Adopt takes over from the root go.mod.
type AdoptParams struct {
	// Specs are the tools the root go.mod declares (see PlanAdoption).
	Specs []modparser.Spec
	// DryRun reports what would happen and changes nothing.
	DryRun bool
}

// Adopt installs every tool of p.Specs that the manifest does not serve yet,
// then removes the directives from the root go.mod and tidies it. The root
// go.mod is not touched until every install succeeded, so a failure leaves the
// project as it was and a rerun resumes. The caller holds the lock.
func (c *Context) Adopt(ctx context.Context, p AdoptParams) error {
	out := c.Stdout

	if !isRegularFile(c.RootGoMod()) {
		_, _ = fmt.Fprintln(out, "ℹ No go.mod in project root — skipping tool migration.")

		return nil
	}

	if len(p.Specs) == 0 {
		_, _ = fmt.Fprintln(out, "ℹ No tool directives in go.mod — nothing to migrate.")

		return nil
	}

	_, _ = fmt.Fprintf(out, "🔍 Found %d tool(s) in go.mod:\n", len(p.Specs))

	for _, t := range p.Specs {
		_, _ = fmt.Fprintf(out, "     %s  %s@%s\n", t.Name, t.Package, t.Version)
	}

	manager := c.Tools()

	// The root go.mod loses its directives, so the pins in it are gone after
	// this: say when one differs from what the manifest keeps.
	snapshot := fsx.TakeSnapshot(c.RootGoMod(), filepath.Join(c.Root, "go.sum"))

	for _, t := range p.Specs {
		if as, ok := c.ManagedAs(t); ok {
			c.warnPinConflict(t, as)

			if as == t.Name {
				_, _ = fmt.Fprintf(out, "  ⏭ %s — already managed, skipping\n", t.Name)
			} else {
				_, _ = fmt.Fprintf(out, "  ⏭ %s — already managed as '%s', skipping\n", t.Name, as)
			}

			continue
		}

		if p.DryRun {
			_, _ = fmt.Fprintf(out, "  [dry-run] Would install %s (%s@%s)\n", t.Name, t.Package, t.Version)

			continue
		}

		if _, err := manager.Add(ctx, tools.AddOptions{Name: t.Name, Pkg: t.Package + "@" + t.Version}); err != nil {
			return err
		}
	}

	if p.DryRun {
		_, _ = fmt.Fprintln(out, "[dry-run] Would strip tool directives from go.mod and run 'go mod tidy'.")

		return nil
	}

	// Stripping and tidying are one step: if the tidy fails, the root module
	// goes back to what it was, so the next run finds the directives and tries
	// again instead of reporting nothing to migrate.
	if err := c.stripRootTools(); err != nil {
		return err
	}

	if err := c.tidyRoot(ctx); err != nil {
		snapshot.Restore()

		return err
	}

	_, _ = fmt.Fprintf(out, "✅ Migrated %d tool(s) from go.mod into gotools.\n", len(p.Specs))

	return nil
}

// tidyRoot runs `go mod tidy` on the root module.
func (c *Context) tidyRoot(ctx context.Context) error {
	_, _ = fmt.Fprintln(c.Stdout, "🧹 Running 'go mod tidy' on the root module...")

	res, err := c.Network().Run(ctx, gocmdCall(c.Root, "mod", "tidy"))
	if err != nil {
		return timeoutOr(err, "go mod tidy")
	}

	if res.Code != 0 {
		output := strings.TrimSpace(string(res.Stderr) + string(res.Stdout))

		return ierrors.New(tidyFailureCode(output), "go mod tidy failed on the root module\n%s", output)
	}

	return nil
}

// warnPinConflict says when the root go.mod pins a tool at another version than
// the manifest, which keeps its own.
func (c *Context) warnPinConflict(t modparser.Spec, managedAs string) {
	managed, ok := c.Manifest.Tool(managedAs)
	if !ok || managed.Version == "" || t.Version == "" || t.Version == "latest" || managed.Version == t.Version {
		return
	}

	_, _ = fmt.Fprintf(c.Stderr, "  ⚠️  %s: go.mod pins %s, the manifest keeps %s. Run '%s upgrade %s' or '%s install --force %s %s@%s' to change it.\n",
		t.Name, t.Version, managed.Version, c.Program, managedAs, c.Program, managedAs, t.Package, t.Version)
}

func (c *Context) stripRootTools() error {
	rootMod := c.RootGoMod()

	if err := fsx.RejectSymlink(rootMod, "init"); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(c.Stdout, "✂️  Removing tool directives from go.mod...")

	data, err := os.ReadFile(rootMod)
	if err != nil {
		return ierrors.New(ierrors.Environment, "Could not read %s: %v", rootMod, err)
	}

	mod, err := c.ModFiles.Parse(data)
	if err == nil {
		err = mod.StripTools()
	}

	var out []byte
	if err == nil {
		out, err = mod.Format()
	}

	if err != nil {
		return ierrors.New(ierrors.Environment, "Could not edit %s: %v", rootMod, err)
	}

	perm := os.FileMode(0o644)
	if fi, err := os.Stat(rootMod); err == nil {
		perm = fi.Mode().Perm()
	}

	if err := fsx.WriteFileAtomic(rootMod, out, perm); err != nil {
		return ierrors.New(ierrors.Environment, "Could not write %s: %v", rootMod, err)
	}

	return nil
}

// tidyFailureCode classifies a failed `go mod tidy`: the same as any go
// failure, except that a message about a bad version is not the user's usage
// error here.
func tidyFailureCode(output string) ierrors.Code {
	if code := tools.FailureCode(output); code != ierrors.Usage {
		return code
	}

	return ierrors.Generic
}

func isRegularFile(path string) bool {
	fi, err := os.Stat(path)

	return err == nil && fi.Mode().IsRegular()
}

// Resolver resolves tool names against the project's tools directory.
func (c *Context) Resolver() *resolve.Resolver {
	return &resolve.Resolver{Root: c.Root, Layout: c.Layout(), ModFiles: c.ModFiles, Manifest: c.Manifest}
}

// Prober asks go whether the project's tools run. It uses the runner without a
// timeout: a probe never touches the network unless the tool is missing, and
// offline mode turns the proxy off.
func (c *Context) Prober() *resolve.Prober {
	return &resolve.Prober{Resolver: c.Resolver(), Go: c.Runner(), Offline: c.Config.Settings.Offline}
}

// Processes runs a tool with the caller's own streams: the injected runner,
// else the real program.
func (c *Context) Processes() gocmd.ProcessRunner {
	if c.Process != nil {
		return c.Process
	}

	return gocmd.ExecProcessRunner{}
}

// Tracer is the tracer the settings ask for. With tracing off it is a Nop and
// costs nothing, not even a go call. Otherwise records are appended to the
// project's trace log and echoed to stderr, or to stdout when the setting says
// so; a record that cannot be written is reported on stderr and never fails the
// command.
func (c *Context) Tracer(ctx context.Context) tracer.Tracer {
	mode := c.Config.Settings.Trace

	var echo io.Writer

	switch mode {
	case config.TraceStderr:
		echo = c.Stderr
	case config.TraceStdout:
		echo = c.Stdout
	default:
		return tracer.Nop{}
	}

	goVersion, _ := goversion.Resolve(ctx, c.Runner(), c.Config.GoVersion, c.Root)

	verbose := "0"
	if c.Config.Settings.Verbose {
		verbose = "1"
	}

	return tracer.NewFile(tracer.Config{
		Path:    filepath.Join(c.Root, tracer.FileName),
		Echo:    echo,
		Warn:    c.Stderr,
		Program: c.Program,
		Stdin:   tracer.StdinKind(c.Stdin),
		Env: tracer.Env{
			Strategy:     string(c.Config.Strategy),
			Dir:          c.Config.Dir,
			GoVersion:    goVersion,
			ModulePrefix: c.ModulePrefix(),
			Verbose:      verbose,
			Trace:        string(mode),
		},
	})
}

// Executable is the file that holds the running program: the real file behind
// any symlink, which is what an update must replace.
func (c *Context) Executable() (string, error) {
	_, resolved, err := c.ExecutablePaths()

	return resolved, err
}

// ExecutablePaths are the path the program was started through and the resolved
// file behind it. They differ when it was started through a symlink; an
// uninstall removes both.
func (c *Context) ExecutablePaths() (started, resolved string, err error) {
	locate := c.Exe
	if locate == nil {
		locate = os.Executable
	}

	started, err = locate()
	if err != nil {
		return "", "", ierrors.Wrap(ierrors.Environment, "Could not locate the "+ProductName+" executable", err)
	}

	resolved = started
	if r, err := filepath.EvalSymlinks(started); err == nil {
		resolved = r
	}

	return started, resolved, nil
}

// readLine reads one line of the user's answer from stdin. No input at all, as
// in a script, is an empty answer.
func (c *Context) readLine() string {
	if c.Stdin == nil {
		return ""
	}

	answer, _ := bufio.NewReader(c.Stdin).ReadString('\n')

	return answer
}

// ReleaseSource is where releases come from: the injected Releases, else GitHub.
func (c *Context) ReleaseSource() selfupdate.Source {
	if c.Releases.APIURL != "" {
		return c.Releases
	}

	return selfupdate.GitHub(c.Version)
}
