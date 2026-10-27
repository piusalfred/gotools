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

package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/fsx"
	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/manifest"
	"github.com/piusalfred/gotools/internal/modparser"
	"github.com/piusalfred/gotools/internal/strategy"
)

// AddOptions describes one tool to add.
type AddOptions struct {
	Name string
	Pkg  string // package path, optionally @version
	// NoFlush leaves the manifest alone and only returns the tool, for
	// callers (parallel sync, migrate) that merge and write it once at the end.
	NoFlush bool
	// Quiet drops the "Installing" and "Installed" lines and the @latest
	// pinning hint, for callers (upgrade) that report in their own words.
	// Warnings on the error stream are never suppressed.
	Quiet bool
	// RejectTwin refuses a tool that is already managed under another name
	// at the same version (any version in the unified strategy). Only an
	// explicit install asks for it: sync, migrate and init must still be able
	// to restore a manifest that already has such entries.
	RejectTwin bool
}

// addPlan is where one tool lives, worked out before anything is written.
type addPlan struct {
	pkgBase  string // the package path without its @version
	target   string // the go version the tool's modfile declares
	modfile  string
	sumfile  string
	toolsDir string
	toolDir  string // the module directory in the module strategy
}

// Add adds one tool to the tools directory and, unless NoFlush, to the
// manifest. Every failure restores the modfile and sum exactly as they were
// and removes the module directory only if it ended up empty, and a tool that
// fails to build leaves nothing behind that the manifest does not know about.
func (m *GoManager) Add(ctx context.Context, o AddOptions) (manifest.Tool, error) {
	p, err := m.planAdd(ctx, o)
	if err != nil {
		return manifest.Tool{}, err
	}

	l := m.cfg.Layout

	if !o.Quiet {
		_, _ = fmt.Fprintf(m.cfg.Out, "📦 Installing %s (%s) [strategy=%s]...\n", o.Name, o.Pkg, l.Strategy)
	}

	for _, f := range symlinkGuard(l.Strategy, p.modfile, p.sumfile, p.toolDir) {
		if err = fsx.RejectSymlink(f, "install "+o.Name); err != nil {
			return manifest.Tool{}, err
		}
	}

	snap := fsx.TakeSnapshot(p.modfile, p.sumfile)

	tool, err := m.installTool(ctx, o, p)
	if err == nil && !o.NoFlush {
		err = m.recordTool(tool)
	}

	if err != nil {
		snap.Restore()

		if l.Strategy == strategy.Module {
			_ = os.Remove(p.toolDir) // only succeeds when empty
		}

		return manifest.Tool{}, err
	}

	if !o.Quiet {
		m.reportInstalled(o, tool)
	}

	return tool, nil
}

// planAdd validates the package and resolves the files the tool will occupy.
func (m *GoManager) planAdd(ctx context.Context, o AddOptions) (addPlan, error) {
	cfg := m.cfg
	l := cfg.Layout

	if err := CheckPackageSpec(o.Pkg); err != nil {
		return addPlan{}, err
	}

	target, err := cfg.Versions.Resolve(ctx)
	if err != nil {
		return addPlan{}, err
	}

	modfile, err := l.ModFile(cfg.Root, o.Name)
	if err != nil {
		return addPlan{}, err
	}

	sumfile, err := l.SumFile(cfg.Root, o.Name)
	if err != nil {
		return addPlan{}, err
	}

	pkgBase, _, _ := strings.Cut(o.Pkg, "@")

	return addPlan{
		pkgBase:  pkgBase,
		target:   target,
		modfile:  modfile,
		sumfile:  sumfile,
		toolsDir: l.ToolsDir(cfg.Root),
		toolDir:  filepath.Dir(modfile),
	}, nil
}

// installTool writes the modfile, runs go get and proves the tool builds. It
// leaves undoing a failure to the caller, which holds the snapshot.
func (m *GoManager) installTool(ctx context.Context, o AddOptions, p addPlan) (manifest.Tool, error) {
	if err := os.MkdirAll(p.toolDir, 0o755); err != nil {
		return manifest.Tool{}, ierrors.New(ierrors.Generic, "Could not create %s: %v", p.toolDir, err)
	}

	if err := m.prepareModfile(o, p.modfile, p.target, p.pkgBase); err != nil {
		return manifest.Tool{}, err
	}

	if err := m.goGetTool(ctx, o, p.toolsDir, p.modfile); err != nil {
		return manifest.Tool{}, err
	}

	mod, err := m.cfg.ModFiles.Read(p.modfile)
	if err != nil {
		return manifest.Tool{}, ierrors.New(ierrors.Generic, "Could not read %s after install: %v", p.modfile, err)
	}

	resolved := mod.VersionFor(p.pkgBase)
	if resolved == "" {
		resolved = "latest"
	}

	if o.RejectTwin {
		if err = m.refuseTwin(o.Name, p.pkgBase, resolved); err != nil {
			return manifest.Tool{}, err
		}
	}

	if err = m.verifyBuilt(ctx, o.Name, p.pkgBase, p.modfile); err != nil {
		return manifest.Tool{}, err
	}

	m.advise(ctx, mod, p.pkgBase, p.toolsDir)

	return manifest.Tool{Name: o.Name, Source: "go", Package: p.pkgBase, Version: resolved}, nil
}

// recordTool writes tool to the manifest. When the write fails, the manifest
// in memory goes back to what it was, so a later flush does not persist a tool
// whose install was rolled back.
func (m *GoManager) recordTool(tool manifest.Tool) error {
	mf := m.manifest()
	prev, had := mf.Tool(tool.Name)

	mf.SetTool(tool)

	err := m.flush()
	if err == nil {
		return nil
	}

	if had {
		mf.SetTool(prev)
	} else {
		mf.RemoveTool(tool.Name)
	}

	return err
}

// reportInstalled says the tool is installed and, for @latest, that the
// version it resolved to should be pinned.
func (m *GoManager) reportInstalled(o AddOptions, tool manifest.Tool) {
	cfg := m.cfg

	_, _ = fmt.Fprintf(cfg.Out, "✅ Installed %s\n", o.Name)

	// A floating version breaks reproducibility.
	if strings.HasSuffix(o.Pkg, "@latest") {
		_, _ = fmt.Fprintf(cfg.Out, "⚠️  Installed at @latest (resolved: %s).\n", tool.Version)
		_, _ = fmt.Fprint(cfg.Out, "   Pin this version for reproducibility:\n")
		_, _ = fmt.Fprintf(cfg.Out, "     %s install %s %s@%s\n", cfg.Program, o.Name, tool.Package, tool.Version)
	}
}

// advise prints the advisor's warnings for the module that provides pkg. They
// say Go's own integrity checks are weakened for it; nothing here sets those
// checks or refuses an install over them.
func (m *GoManager) advise(ctx context.Context, mod *modparser.File, pkg, toolsDir string) {
	if m.cfg.Advisor == nil {
		return
	}

	root := pkg
	if r, ok := mod.ModuleFor(pkg); ok {
		root = r.Path
	}

	for _, w := range m.cfg.Advisor.Warnings(ctx, toolsDir, root) {
		_, _ = fmt.Fprintf(m.cfg.Err, "⚠️  %s\n", w)
	}
}

// refuseTwin fails when the tool just installed is already managed under
// another name. The caller's rollback then puts the modfile back.
//
// Two names for one tool drift apart, double the disk use and the sync work,
// and in unified a sync would discover the alias as a third entry. One tool
// gets one name, and rename changes it. A different version in split or module
// is a different tool and is allowed.
func (m *GoManager) refuseTwin(name, pkg, version string) error {
	cfg := m.cfg
	unified := cfg.Layout.Strategy == strategy.Unified

	other, ok := m.manifest().Twin(name, pkg, version, unified)
	if !ok {
		return nil
	}

	why := "at the same version"
	if unified {
		why = "(the unified strategy keeps one version of each module)"
	}

	return ierrors.New(
		ierrors.Usage,
		"%s is already managed as '%s' (%s) %s, so '%s' would be a second name for the same tool.\n   Rename it:   %s rename %s %s\n   Or change it: %s install --force %s %s",
		pkg,
		other.Name,
		other.Version,
		why,
		name,
		cfg.Program,
		other.Name,
		name,
		cfg.Program,
		other.Name,
		pkg+"@<version>",
	)
}

// symlinkGuard lists the paths that must not be symlinks for strategy s.
func symlinkGuard(s strategy.Strategy, modfile, sumfile, toolDir string) []string {
	if s == strategy.Module {
		return []string{toolDir, modfile, sumfile}
	}

	return []string{modfile, sumfile}
}

// prepareModfile creates the tool's modfile when it is missing and drops
// directives left over from an earlier package, so a forced reinstall with a
// different package does not leave two tools in one modfile: the first one
// would win in exec and in the verify probe.
func (m *GoManager) prepareModfile(o AddOptions, modfile, target, pkgBase string) error {
	cfg := m.cfg
	data, err := os.ReadFile(modfile)

	switch {
	case errors.Is(err, os.ErrNotExist):
		name := o.Name
		if cfg.Layout.Strategy == strategy.Unified {
			name = ""
		}

		fresh := fmt.Sprintf("module %s\n\ngo %s\n", cfg.Layout.ModulePath(cfg.Prefix, name), target)
		if err = fsx.WriteFileAtomic(modfile, []byte(fresh), 0o644); err != nil {
			return ierrors.New(ierrors.Generic, "Could not create %s: %v", modfile, err)
		}

		return nil
	case err != nil:
		return ierrors.New(ierrors.Generic, "Could not read %s: %v", modfile, err)
	}

	stale, err := m.staleDirectives(o.Name, pkgBase, data)
	if err != nil {
		return ierrors.New(ierrors.Environment, "Could not parse %s: %v", modfile, err)
	}

	if len(stale) == 0 {
		return nil
	}

	out, err := m.dropTools(data, stale)
	if err != nil {
		return ierrors.New(ierrors.Generic, "Could not update %s: %v", modfile, err)
	}

	return fsx.WriteFileAtomic(modfile, out, 0o644)
}

// dropTools returns data without the tool directives for pkgs.
func (m *GoManager) dropTools(data []byte, pkgs []string) ([]byte, error) {
	mod, err := m.cfg.ModFiles.Parse(data)
	if err != nil {
		return nil, err
	}

	if err := mod.DropTools(pkgs...); err != nil {
		return nil, err
	}

	return mod.Format()
}

// staleDirectives are the tool directives in the modfile that the new install
// replaces. Split and module hold one tool per file, so every other directive
// is stale. Unified shares one go.mod, so only the previous package of this
// same tool is, and only if no other tool uses it.
func (m *GoManager) staleDirectives(name, pkgBase string, data []byte) ([]string, error) {
	mod, err := m.cfg.ModFiles.Parse(data)
	if err != nil {
		return nil, err
	}

	if m.cfg.Layout.Strategy != strategy.Unified {
		return slices.DeleteFunc(slices.Clone(mod.Tools), func(t string) bool { return t == pkgBase }), nil
	}

	mf := m.manifest()

	prev, ok := mf.Tool(name)
	if !ok || prev.Package == pkgBase || !slices.Contains(mod.Tools, prev.Package) {
		return nil, nil
	}

	for _, t := range mf.SortedTools() {
		if t.Name != name && t.Package == prev.Package {
			return nil, nil
		}
	}

	return []string{prev.Package}, nil
}

// goGetTool runs `go get -tool` for the package, bounded by the network
// runner, and turns every failure into a classified error.
func (m *GoManager) goGetTool(ctx context.Context, o AddOptions, toolsDir, modfile string) error {
	call := gocmd.Call{Dir: filepath.Dir(modfile), Args: []string{"get", "-tool"}}

	if m.cfg.Layout.Strategy == strategy.Split {
		call.Dir = toolsDir
		call.Args = append(call.Args, "-modfile="+filepath.Base(modfile))
	}

	call.Args = append(call.Args, o.Pkg)

	res, err := m.cfg.Network.Run(ctx, call)

	switch {
	case errors.Is(err, gocmd.ErrNotFound):
		return ierrors.New(ierrors.Environment, "go is not installed.")
	case err != nil:
		return timeoutToExit(err, "Installing "+o.Name)
	case res.Code != 0:
		output := strings.TrimSpace(string(res.Stderr) + string(res.Stdout))

		return ierrors.New(FailureCode(output), "Failed to install %s\n%s", o.Name, output)
	}

	return nil
}

// toolCall is the `go tool` invocation for the binary of a package, with args
// after the binary:
//
//	unified  go tool <bin>                    in <dir>
//	split    go tool -modfile=<abs> <bin>     in the project root
//	module   go tool <bin>                    in <dir>/<name>
func (m *GoManager) toolCall(modfile, binary string, args ...string) gocmd.Call {
	c := gocmd.Call{Dir: filepath.Dir(modfile), Args: []string{"tool"}}

	if m.cfg.Layout.Strategy == strategy.Split {
		c.Dir = m.cfg.Root
		c.Args = append(c.Args, "-modfile="+modfile)
	}

	c.Args = append(c.Args, binary)

	return gocmd.Call{Dir: c.Dir, Args: append(c.Args, args...)}
}

// verifyBuilt makes sure the tool really builds. `go get -tool` can exit 0
// even when compilation fails, because the directive is written before the
// binary is built, so the install is not trustworthy until the tool has been
// built.
//
// `go tool -n` builds the tool and prints the command it would run without
// running it. That is deliberate: the binary was just downloaded from a module
// proxy, and running it here would execute unverified third-party code as a
// side effect of an install. Nothing runs, so a non-zero exit can only mean
// the tool failed to resolve or build. The probe names the package just
// installed, not the first directive of a shared go.mod.
func (m *GoManager) verifyBuilt(ctx context.Context, name, pkgBase, modfile string) error {
	call := m.toolCall(modfile, modparser.InferBinaryName(pkgBase))
	call.Args = slices.Insert(call.Args, 1, "-n")

	res, err := m.cfg.Network.Run(ctx, call)

	switch {
	case errors.Is(err, gocmd.ErrNotFound):
		return ierrors.New(ierrors.Environment, "go is not installed.")
	case err != nil:
		return timeoutToExit(err, "Verifying "+name)
	case res.Code == 0:
		return nil
	}

	detail := strings.TrimSpace(string(res.Stderr))
	if detail == "" {
		detail = fmt.Sprintf("exit status %d", res.Code)
	}

	if notATool.MatchString(detail) {
		return ierrors.New(
			ierrors.Environment,
			"Tool installed but not runnable: %s\n   go tool error: %s",
			name,
			detail,
		)
	}

	return ierrors.New(
		ierrors.Environment,
		"Tool installed but failed to build: %s\n   go tool error: %s",
		name,
		detail,
	)
}
