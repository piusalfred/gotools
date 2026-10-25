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

package tools_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/manifest"
	"github.com/piusalfred/gotools/internal/strategy"
	"github.com/piusalfred/gotools/internal/tools"
)

// fakeGo answers go calls from a handler and records them. The default handler
// plays go's part in an install: `go get -tool` writes the directive and the
// requirement into the modfile, and every other call succeeds.
type fakeGo struct {
	mu     sync.Mutex
	calls  []gocmd.Call
	handle func(gocmd.Call) (gocmd.Result, error)
}

func (g *fakeGo) Run(_ context.Context, c gocmd.Call) (gocmd.Result, error) {
	g.mu.Lock()
	g.calls = append(g.calls, c)
	g.mu.Unlock()

	if g.handle != nil {
		return g.handle(c)
	}

	return installing(c)
}

func (g *fakeGo) args() []string {
	g.mu.Lock()
	defer g.mu.Unlock()

	var out []string
	for _, c := range g.calls {
		out = append(out, strings.Join(c.Args, " "))
	}

	return out
}

// installing is the default go: it edits the modfile `go get -tool` names.
func installing(c gocmd.Call) (gocmd.Result, error) {
	if len(c.Args) < 3 || c.Args[0] != "get" {
		return gocmd.Result{}, nil
	}

	modfile := filepath.Join(c.Dir, "go.mod")
	pkg := c.Args[len(c.Args)-1]

	for _, a := range c.Args {
		if name, ok := strings.CutPrefix(a, "-modfile="); ok {
			modfile = filepath.Join(c.Dir, name)
		}
	}

	base, ver, _ := strings.Cut(pkg, "@")
	if ver == "" || ver == "latest" {
		ver = "v1.9.9"
	}

	f, err := os.OpenFile(modfile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return gocmd.Result{Code: 1, Stderr: []byte(err.Error())}, nil
	}

	defer f.Close()

	_, _ = f.WriteString("\ntool " + base + "\n\nrequire " + base + " " + ver + "\n")

	return gocmd.Result{}, nil
}

// store keeps the manifest in a file, so a test can read back what was
// persisted.
type store struct {
	m     *manifest.Manifest
	path  string
	fails bool
}

func (s *store) Manifest() *manifest.Manifest { return s.m }

func (s *store) Flush() error {
	if s.fails {
		return errors.New("disk full")
	}

	return s.m.Flush(s.path)
}

type versions struct {
	v   string
	err error
}

func (v versions) Resolve(context.Context) (string, error) { return v.v, v.err }

type recorder struct{ n int }

func (r *recorder) Record(context.Context) error { r.n++; return nil }

type advisor struct {
	mu      sync.Mutex
	modules []string
}

func (a *advisor) Warnings(_ context.Context, _, modulePath string) []string {
	a.mu.Lock()
	a.modules = append(a.modules, modulePath)
	a.mu.Unlock()

	return []string{"GOPRIVATE covers " + modulePath}
}

type fixture struct {
	t      *testing.T
	root   string
	store  *store
	goes   *fakeGo
	rec    *recorder
	adv    *advisor
	out    *bytes.Buffer
	errb   *bytes.Buffer
	mgr    *tools.GoManager
	layout strategy.Layout
}

func newFixture(t *testing.T, s strategy.Strategy, existing ...manifest.Tool) *fixture {
	t.Helper()

	root := t.TempDir()
	m := manifest.New()
	m.Strategy = string(s)

	for _, tl := range existing {
		m.SetTool(tl)
	}

	f := &fixture{
		t:      t,
		root:   root,
		store:  &store{m: m, path: filepath.Join(root, config.ManifestFile)},
		goes:   &fakeGo{},
		rec:    &recorder{},
		adv:    &advisor{},
		out:    &bytes.Buffer{},
		errb:   &bytes.Buffer{},
		layout: strategy.Layout{Strategy: s, Dir: "tools"},
	}

	if err := m.Flush(f.store.path); err != nil {
		t.Fatal(err)
	}

	f.build(f.goes)

	return f
}

func (f *fixture) build(g gocmd.Runner) {
	f.mgr = tools.New(tools.Config{
		Root:     f.root,
		Layout:   f.layout,
		Prefix:   "example.com/proj",
		Store:    f.store,
		Versions: versions{v: "1.24"},
		Go:       g,
		Recorder: f.rec,
		Advisor:  f.adv,
		Out:      f.out,
		Err:      f.errb,
		Program:  "gotools",
	})
}

func (f *fixture) dir() string { return filepath.Join(f.root, "tools") }

func (f *fixture) write(rel, content string) string {
	f.t.Helper()

	file := filepath.Join(f.dir(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		f.t.Fatal(err)
	}

	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}

	return file
}

func (f *fixture) read(rel string) string {
	b, _ := os.ReadFile(filepath.Join(f.dir(), filepath.FromSlash(rel)))

	return string(b)
}

func (f *fixture) onDisk(rel string) bool {
	_, err := os.Lstat(filepath.Join(f.dir(), filepath.FromSlash(rel)))

	return err == nil
}

// persisted reads the manifest file back.
func (f *fixture) persisted() []string {
	f.t.Helper()

	res, err := manifest.Load(f.store.path)
	if err != nil {
		f.t.Fatal(err)
	}

	return res.Manifest.ToolNames()
}

var (
	toolX = manifest.Tool{Name: "x", Source: "go", Package: "example.com/a/x", Version: "v1.0.0"}
	toolY = manifest.Tool{Name: "y", Source: "go", Package: "example.com/b/y", Version: "v1.2.0"}
)

func TestAddInstallsAndRecordsInEveryStrategy(t *testing.T) {
	cases := []struct {
		strategy strategy.Strategy
		modfile  string
		getArgs  string
		toolArgs string
	}{
		{strategy.Split, "x.mod", "get -tool -modfile=x.mod example.com/a/x@v1.0.0", "tool -n -modfile=%s/tools/x.mod x"},
		{strategy.Module, "x/go.mod", "get -tool example.com/a/x@v1.0.0", "tool -n x"},
		{strategy.Unified, "go.mod", "get -tool example.com/a/x@v1.0.0", "tool -n x"},
	}

	for _, c := range cases {
		t.Run(string(c.strategy), func(t *testing.T) {
			f := newFixture(t, c.strategy)

			tool, err := f.mgr.Add(context.Background(), tools.AddOptions{Name: "x", Pkg: "example.com/a/x@v1.0.0"})
			if err != nil || tool != toolX {
				t.Fatalf("tool=%+v err=%v", tool, err)
			}

			if !strings.Contains(f.read(c.modfile), "tool example.com/a/x") {
				t.Fatalf("modfile:\n%s", f.read(c.modfile))
			}

			wantTool := strings.Replace(c.toolArgs, "%s", f.root, 1)

			if got := f.goes.args(); !slices.Equal(got, []string{c.getArgs, wantTool}) {
				t.Fatalf("go calls:\n%q\nwant\n%q", got, []string{c.getArgs, wantTool})
			}

			if got := f.persisted(); !slices.Equal(got, []string{"x"}) {
				t.Fatalf("persisted %v", got)
			}

			for _, want := range []string{"Installing x (example.com/a/x@v1.0.0) [strategy=" + string(c.strategy) + "]", "Installed x"} {
				if !strings.Contains(f.out.String(), want) {
					t.Errorf("output lacks %q:\n%s", want, f.out)
				}
			}
		})
	}
}

func TestAddCreatesTheModfileFromTheLayout(t *testing.T) {
	f := newFixture(t, strategy.Split)

	if _, err := f.mgr.Add(context.Background(), tools.AddOptions{Name: "x", Pkg: "example.com/a/x@v1.0.0"}); err != nil {
		t.Fatal(err)
	}

	if got := f.read("x.mod"); !strings.HasPrefix(got, "module example.com/proj/tools/x\n\ngo 1.24\n") {
		t.Fatalf("modfile:\n%s", got)
	}

	f = newFixture(t, strategy.Unified)

	if _, err := f.mgr.Add(context.Background(), tools.AddOptions{Name: "x", Pkg: "example.com/a/x@v1.0.0"}); err != nil {
		t.Fatal(err)
	}

	if got := f.read("go.mod"); !strings.HasPrefix(got, "module example.com/proj/tools\n") {
		t.Fatalf("unified modfile:\n%s", got)
	}
}

func TestAddAtLatestResolvesAndHintsToPin(t *testing.T) {
	f := newFixture(t, strategy.Split)

	tool, err := f.mgr.Add(context.Background(), tools.AddOptions{Name: "x", Pkg: "example.com/a/x@latest"})
	if err != nil || tool.Version != "v1.9.9" {
		t.Fatalf("tool=%+v err=%v", tool, err)
	}

	for _, want := range []string{"Installed at @latest (resolved: v1.9.9)", "gotools install x example.com/a/x@v1.9.9"} {
		if !strings.Contains(f.out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, f.out)
		}
	}
}

func TestAddQuietAndNoFlush(t *testing.T) {
	f := newFixture(t, strategy.Split)

	tool, err := f.mgr.Add(context.Background(), tools.AddOptions{Name: "x", Pkg: "example.com/a/x@latest", Quiet: true, NoFlush: true})
	if err != nil || tool.Name != "x" {
		t.Fatalf("tool=%+v err=%v", tool, err)
	}

	if f.out.Len() != 0 {
		t.Errorf("a quiet add printed %q", f.out)
	}

	if len(f.persisted()) != 0 || len(f.store.m.Tools) != 0 {
		t.Fatal("NoFlush recorded the tool")
	}
}

func TestAddAdvisoryWarningsGoToTheErrorStream(t *testing.T) {
	f := newFixture(t, strategy.Split)

	if _, err := f.mgr.Add(context.Background(), tools.AddOptions{Name: "x", Pkg: "example.com/a/x@v1.0.0", Quiet: true}); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(f.adv.modules, []string{"example.com/a/x"}) || !strings.Contains(f.errb.String(), "GOPRIVATE covers example.com/a/x") {
		t.Fatalf("modules=%v stderr=%q", f.adv.modules, f.errb)
	}
}

func TestAddRejectsABadPackageBeforeAnyWork(t *testing.T) {
	f := newFixture(t, strategy.Split)

	for _, pkg := range []string{"-flag", "not a path", "../x"} {
		_, err := f.mgr.Add(context.Background(), tools.AddOptions{Name: "x", Pkg: pkg})
		if ierrors.CodeOf(err) != ierrors.Environment {
			t.Errorf("%q: %v", pkg, err)
		}
	}

	if len(f.goes.calls) != 0 || f.onDisk("x.mod") {
		t.Fatal("work started for an invalid package")
	}
}

func TestAddRejectsUnsafeNames(t *testing.T) {
	f := newFixture(t, strategy.Split)

	if _, err := f.mgr.Add(context.Background(), tools.AddOptions{Name: "../x", Pkg: "example.com/a/x"}); ierrors.CodeOf(err) != ierrors.Usage {
		t.Fatalf("err = %v", err)
	}
}

func TestAddVersionResolutionFailureStopsEarly(t *testing.T) {
	f := newFixture(t, strategy.Split)
	f.mgr = tools.New(tools.Config{
		Root: f.root, Layout: f.layout, Store: f.store, Go: f.goes,
		Versions: versions{err: ierrors.New(ierrors.Environment, "no go version")},
	})

	if _, err := f.mgr.Add(context.Background(), tools.AddOptions{Name: "x", Pkg: "example.com/a/x"}); ierrors.CodeOf(err) != ierrors.Environment {
		t.Fatalf("err = %v", err)
	}
}

func TestAddFailureRollsBackFilesAndManifest(t *testing.T) {
	cases := []struct {
		name   string
		handle func(gocmd.Call) (gocmd.Result, error)
		code   ierrors.Code
		say    string
	}{
		{"network", func(gocmd.Call) (gocmd.Result, error) {
			return gocmd.Result{Code: 1, Stderr: []byte("dial tcp: i/o timeout")}, nil
		}, ierrors.Network, "Failed to install x"},
		{"bad version", func(gocmd.Call) (gocmd.Result, error) {
			return gocmd.Result{Code: 1, Stderr: []byte("invalid module version syntax")}, nil
		}, ierrors.Usage, "Failed to install x"},
		{"other", func(gocmd.Call) (gocmd.Result, error) {
			return gocmd.Result{Code: 1, Stderr: []byte("boom")}, nil
		}, ierrors.Generic, "boom"},
		{"go missing", func(gocmd.Call) (gocmd.Result, error) { return gocmd.Result{}, gocmd.ErrNotFound }, ierrors.Environment, "go is not installed"},
		{"timeout", func(gocmd.Call) (gocmd.Result, error) {
			return gocmd.Result{}, &gocmd.TimeoutError{After: 2 * time.Second}
		}, ierrors.Network, "GOTOOLS_OPERATION_TIMEOUT"},
		{"does not build", func(c gocmd.Call) (gocmd.Result, error) {
			if c.Args[0] == "tool" {
				return gocmd.Result{Code: 1, Stderr: []byte("compile error")}, nil
			}

			return installing(c)
		}, ierrors.Environment, "failed to build"},
		{"not runnable", func(c gocmd.Call) (gocmd.Result, error) {
			if c.Args[0] == "tool" {
				return gocmd.Result{Code: 1, Stderr: []byte("go: no such tool \"x\"")}, nil
			}

			return installing(c)
		}, ierrors.Environment, "not runnable"},
	}

	for _, s := range []strategy.Strategy{strategy.Split, strategy.Module} {
		for _, c := range cases {
			t.Run(string(s)+"/"+c.name, func(t *testing.T) {
				f := newFixture(t, s)
				f.build(&fakeGo{handle: c.handle})

				_, err := f.mgr.Add(context.Background(), tools.AddOptions{Name: "x", Pkg: "example.com/a/x@v1.0.0"})
				if ierrors.CodeOf(err) != c.code || !strings.Contains(err.Error(), c.say) {
					t.Fatalf("err = %v (code %d)", err, ierrors.CodeOf(err))
				}

				if f.onDisk("x.mod") || f.onDisk("x") || len(f.persisted()) != 0 {
					t.Fatalf("a failed install left files or a manifest entry behind")
				}
			})
		}
	}
}

func TestAddFailedReinstallRestoresTheOriginalModfile(t *testing.T) {
	f := newFixture(t, strategy.Split, toolX)
	original := "module example.com/proj/tools/x\n\ngo 1.24\n\ntool example.com/a/x\n\nrequire example.com/a/x v1.0.0\n"
	f.write("x.mod", original)
	f.write("x.sum", "sum-before\n")

	f.build(&fakeGo{handle: func(c gocmd.Call) (gocmd.Result, error) {
		if c.Args[0] == "get" {
			_, _ = installing(c) // half-changes the modfile, then fails
			_ = os.WriteFile(filepath.Join(c.Dir, "x.sum"), []byte("garbage"), 0o644)

			return gocmd.Result{Code: 1, Stderr: []byte("boom")}, nil
		}

		return gocmd.Result{}, nil
	}})

	if _, err := f.mgr.Add(context.Background(), tools.AddOptions{Name: "x", Pkg: "example.com/other/x@v1.5.0"}); err == nil {
		t.Fatal("install succeeded")
	}

	if f.read("x.mod") != original || f.read("x.sum") != "sum-before\n" {
		t.Fatalf("files not restored:\n%s\n%s", f.read("x.mod"), f.read("x.sum"))
	}
}

func TestAddReplacesTheStaleDirectiveOfAForcedReinstall(t *testing.T) {
	f := newFixture(t, strategy.Split, toolX)
	f.write("x.mod", "module example.com/proj/tools/x\n\ngo 1.24\n\ntool example.com/old/x\n\nrequire example.com/old/x v0.1.0\n")

	if _, err := f.mgr.Add(context.Background(), tools.AddOptions{Name: "x", Pkg: "example.com/new/x@v1.5.0"}); err != nil {
		t.Fatal(err)
	}

	got := f.read("x.mod")
	if strings.Contains(got, "tool example.com/old/x") || !strings.Contains(got, "tool example.com/new/x") {
		t.Fatalf("modfile:\n%s", got)
	}
}

func TestAddUnifiedKeepsDirectivesOtherToolsUse(t *testing.T) {
	f := newFixture(t, strategy.Unified, toolX, manifest.Tool{Name: "alias", Source: "go", Package: toolX.Package, Version: "v1.0.0"})
	f.write("go.mod", "module example.com/proj/tools\n\ngo 1.24\n\ntool example.com/a/x\n\nrequire example.com/a/x v1.0.0\n")

	if _, err := f.mgr.Add(context.Background(), tools.AddOptions{Name: "x", Pkg: "example.com/new/x@v1.5.0"}); err != nil {
		t.Fatal(err)
	}

	if got := f.read("go.mod"); !strings.Contains(got, "tool example.com/a/x") {
		t.Fatalf("another tool's directive was dropped:\n%s", got)
	}
}

func TestAddRejectTwin(t *testing.T) {
	f := newFixture(t, strategy.Split, toolX)

	_, err := f.mgr.Add(context.Background(), tools.AddOptions{Name: "fmt", Pkg: "example.com/a/x@v1.0.0", RejectTwin: true})
	if ierrors.CodeOf(err) != ierrors.Usage || !strings.Contains(err.Error(), "gotools rename x fmt") {
		t.Fatalf("err = %v", err)
	}

	if f.onDisk("fmt.mod") || len(f.persisted()) != 1 {
		t.Fatal("the refused twin left files or an entry behind")
	}

	// A different version in split is a different tool.
	if _, err := f.mgr.Add(context.Background(), tools.AddOptions{Name: "fmt", Pkg: "example.com/a/x@v1.5.0", RejectTwin: true}); err != nil {
		t.Fatalf("different version refused: %v", err)
	}

	// Without the option a twin is accepted: sync must restore such manifests.
	if _, err := f.mgr.Add(context.Background(), tools.AddOptions{Name: "again", Pkg: "example.com/a/x@v1.0.0"}); err != nil {
		t.Fatalf("twin refused without RejectTwin: %v", err)
	}
}

func TestAddRejectTwinUnifiedIgnoresTheVersion(t *testing.T) {
	f := newFixture(t, strategy.Unified, toolX)

	_, err := f.mgr.Add(context.Background(), tools.AddOptions{Name: "fmt", Pkg: "example.com/a/x@v1.5.0", RejectTwin: true})
	if ierrors.CodeOf(err) != ierrors.Usage || !strings.Contains(err.Error(), "one version of each module") {
		t.Fatalf("err = %v", err)
	}
}

func TestAddManifestWriteFailureRestoresEverything(t *testing.T) {
	f := newFixture(t, strategy.Split)
	f.store.fails = true

	_, err := f.mgr.Add(context.Background(), tools.AddOptions{Name: "x", Pkg: "example.com/a/x@v1.0.0"})
	if err == nil || !strings.Contains(err.Error(), "Could not write the manifest") {
		t.Fatalf("err = %v", err)
	}

	if f.onDisk("x.mod") || len(f.store.m.Tools) != 0 {
		t.Fatal("a failed manifest write left the tool behind")
	}
}

func TestAddRefusesSymlinks(t *testing.T) {
	f := newFixture(t, strategy.Split)

	victim := filepath.Join(t.TempDir(), "victim")
	_ = os.WriteFile(victim, []byte("keep"), 0o644)
	_ = os.MkdirAll(f.dir(), 0o755)

	if err := os.Symlink(victim, filepath.Join(f.dir(), "x.mod")); err != nil {
		t.Fatal(err)
	}

	_, err := f.mgr.Add(context.Background(), tools.AddOptions{Name: "x", Pkg: "example.com/a/x@v1.0.0"})
	if ierrors.CodeOf(err) != ierrors.Environment {
		t.Fatalf("err = %v", err)
	}

	if b, _ := os.ReadFile(victim); string(b) != "keep" || len(f.goes.calls) != 0 {
		t.Fatal("went through the symlink")
	}
}

func TestUpgradeInstallsLatestQuietly(t *testing.T) {
	f := newFixture(t, strategy.Split, toolX)
	f.write("x.mod", "module example.com/proj/tools/x\n\ngo 1.24\n")

	tool, err := f.mgr.Upgrade(context.Background(), toolX)
	if err != nil || tool.Version != "v1.9.9" || f.out.Len() != 0 {
		t.Fatalf("tool=%+v err=%v out=%q", tool, err, f.out)
	}

	if got := f.goes.args()[0]; got != "get -tool -modfile=x.mod example.com/a/x@latest" {
		t.Fatalf("go call %q", got)
	}

	if got, _ := f.store.m.Tool("x"); got.Version != "v1.9.9" {
		t.Fatalf("manifest has %+v", got)
	}
}

func TestRestoreSerialAndParallel(t *testing.T) {
	work := []manifest.Tool{toolX, toolY, {Name: "z", Source: "go", Package: "example.com/c/z", Version: "v1.3.0"}}

	for _, c := range []struct {
		name string
		s    strategy.Strategy
		jobs int
	}{{"serial split", strategy.Split, 1}, {"parallel split", strategy.Split, 3}, {"parallel module", strategy.Module, 2}} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, c.s)

			if err := f.mgr.Restore(context.Background(), work, tools.RestoreOptions{Jobs: c.jobs}); err != nil {
				t.Fatal(err)
			}

			if got := f.persisted(); len(got) != 3 {
				t.Fatalf("persisted %v", got)
			}

			for _, tl := range work {
				if got, _ := f.store.m.Tool(tl.Name); got.Version != tl.Version {
					t.Errorf("%s: %+v", tl.Name, got)
				}
			}
		})
	}
}

func TestRestoreUnifiedStaysSerialAndSaysSo(t *testing.T) {
	f := newFixture(t, strategy.Unified)

	if err := f.mgr.Restore(context.Background(), []manifest.Tool{toolX, toolY}, tools.RestoreOptions{Jobs: 4}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(f.out.String(), "unified strategy: installs are serial") {
		t.Fatalf("output %q", f.out)
	}
}

func TestRestoreParallelFailureWritesNoManifest(t *testing.T) {
	f := newFixture(t, strategy.Split)
	f.build(&fakeGo{handle: func(c gocmd.Call) (gocmd.Result, error) {
		if strings.Contains(strings.Join(c.Args, " "), "example.com/b/y") {
			return gocmd.Result{Code: 1, Stderr: []byte("boom")}, nil
		}

		return installing(c)
	}})

	err := f.mgr.Restore(context.Background(), []manifest.Tool{toolX, toolY}, tools.RestoreOptions{Jobs: 2})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}

	if len(f.persisted()) != 0 || len(f.store.m.Tools) != 0 {
		t.Fatal("a failed parallel restore wrote the manifest")
	}
}

func TestRestoreNothingIsFine(t *testing.T) {
	f := newFixture(t, strategy.Split)

	if err := f.mgr.Restore(context.Background(), nil, tools.RestoreOptions{Jobs: 4}); err != nil || len(f.goes.calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, f.goes.calls)
	}
}

func TestRemoveDeletesFilesAndManifestEntry(t *testing.T) {
	cases := []struct {
		strategy strategy.Strategy
		files    []string
		gone     []string
		said     string
	}{
		{strategy.Split, []string{"x.mod", "x.sum", "y.mod"}, []string{"x.mod", "x.sum"}, "Removed x.mod / x.sum"},
		{strategy.Module, []string{"x/go.mod", "x/go.sum", "y/go.mod"}, []string{"x"}, "Removed tools/x/"},
	}

	for _, c := range cases {
		t.Run(string(c.strategy), func(t *testing.T) {
			f := newFixture(t, c.strategy, toolX, toolY)
			for _, rel := range c.files {
				f.write(rel, "module m\n")
			}

			res, err := f.mgr.Remove(context.Background(), "x")
			if err != nil || res != (tools.RemoveResult{OnDisk: true, InManifest: true}) {
				t.Fatalf("res=%+v err=%v", res, err)
			}

			for _, rel := range c.gone {
				if f.onDisk(rel) {
					t.Errorf("%s still on disk", rel)
				}
			}

			if !f.onDisk(c.files[len(c.files)-1]) {
				t.Error("another tool's files were removed")
			}

			if got := f.persisted(); !slices.Equal(got, []string{"y"}) {
				t.Errorf("persisted manifest lists %v", got)
			}

			if !strings.Contains(f.out.String(), c.said) {
				t.Errorf("output %q lacks %q", f.out, c.said)
			}
		})
	}
}

func TestRemoveReportsWhatItFound(t *testing.T) {
	f := newFixture(t, strategy.Split, toolX)

	res, err := f.mgr.Remove(context.Background(), "x")
	if err != nil || res != (tools.RemoveResult{InManifest: true}) || len(f.persisted()) != 0 {
		t.Fatalf("manifest-only: res=%+v err=%v", res, err)
	}

	f = newFixture(t, strategy.Split)
	f.write("z.mod", "module m\n")

	if res, err = f.mgr.Remove(context.Background(), "z"); err != nil || res != (tools.RemoveResult{OnDisk: true}) {
		t.Fatalf("disk-only: res=%+v err=%v", res, err)
	}

	f = newFixture(t, strategy.Module)

	if res, err = f.mgr.Remove(context.Background(), "nope"); err != nil || res != (tools.RemoveResult{}) {
		t.Fatalf("unknown: res=%+v err=%v", res, err)
	}
}

func TestRemoveRefusesASymlinkAndKeepsTheManifest(t *testing.T) {
	f := newFixture(t, strategy.Split, toolX)

	target := filepath.Join(t.TempDir(), "victim.mod")
	_ = os.WriteFile(target, []byte("keep"), 0o644)
	_ = os.MkdirAll(f.dir(), 0o755)

	if err := os.Symlink(target, filepath.Join(f.dir(), "x.mod")); err != nil {
		t.Fatal(err)
	}

	if _, err := f.mgr.Remove(context.Background(), "x"); err == nil {
		t.Fatal("removed through a symlink")
	}

	if b, _ := os.ReadFile(target); string(b) != "keep" || len(f.persisted()) != 1 {
		t.Fatal("the symlink target or the manifest changed")
	}
}

func TestRemoveRefusesUnsafeNames(t *testing.T) {
	for _, s := range []strategy.Strategy{strategy.Split, strategy.Module} {
		f := newFixture(t, s)

		if _, err := f.mgr.Remove(context.Background(), "../x"); ierrors.CodeOf(err) != ierrors.Usage {
			t.Errorf("%s: err = %v", s, err)
		}
	}
}

func TestRemoveUnifiedDropsTheDirectiveAndTidies(t *testing.T) {
	f := newFixture(t, strategy.Unified, toolX, toolY)
	f.write("go.mod", "module m\n\ngo 1.24\n\ntool (\n\texample.com/a/x\n\texample.com/b/y\n)\n")

	res, err := f.mgr.Remove(context.Background(), "x")
	if err != nil || res != (tools.RemoveResult{OnDisk: true, InManifest: true}) {
		t.Fatalf("res=%+v err=%v", res, err)
	}

	want := []string{"mod edit -droptool=example.com/a/x", "mod tidy"}
	if got := f.goes.args(); !slices.Equal(got, want) {
		t.Fatalf("go calls %q, want %q", got, want)
	}

	if !strings.Contains(f.out.String(), "Dropped x from tools/go.mod") {
		t.Fatalf("output %q", f.out)
	}
}

func TestRemoveUnifiedFindsAnUntrackedToolByBinaryName(t *testing.T) {
	f := newFixture(t, strategy.Unified)
	f.write("go.mod", "module m\n\ngo 1.24\n\ntool example.com/b/y/cmd/ytool\n")

	res, err := f.mgr.Remove(context.Background(), "ytool")
	if err != nil || !res.OnDisk || res.InManifest {
		t.Fatalf("res=%+v err=%v", res, err)
	}

	if got := f.goes.args(); len(got) == 0 || got[0] != "mod edit -droptool=example.com/b/y/cmd/ytool" {
		t.Fatalf("go calls %q", got)
	}
}

func TestRemoveUnifiedMissingFileOrDirective(t *testing.T) {
	f := newFixture(t, strategy.Unified, toolX)

	if res, err := f.mgr.Remove(context.Background(), "x"); err != nil || res.OnDisk || !res.InManifest {
		t.Fatalf("no go.mod: res=%+v err=%v", res, err)
	}

	f = newFixture(t, strategy.Unified)
	f.write("go.mod", "module m\n\ngo 1.24\n")

	res, err := f.mgr.Remove(context.Background(), "ghost")
	if err != nil || res != (tools.RemoveResult{}) || len(f.goes.calls) != 0 || !strings.Contains(f.out.String(), "Tool ghost not found in tools/go.mod") {
		t.Fatalf("res=%+v err=%v calls=%v out=%q", res, err, f.goes.calls, f.out)
	}
}

func TestRemoveUnifiedGoFailure(t *testing.T) {
	f := newFixture(t, strategy.Unified, toolX)
	f.write("go.mod", "module m\n\ngo 1.24\n\ntool example.com/a/x\n")
	f.build(&fakeGo{handle: func(gocmd.Call) (gocmd.Result, error) {
		return gocmd.Result{Code: 1, Stderr: []byte("nope")}, nil
	}})

	_, err := f.mgr.Remove(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "go mod edit -droptool failed (exit 1)") || len(f.persisted()) != 1 {
		t.Fatalf("err = %v, persisted %v", err, f.persisted())
	}
}

func TestRenameMovesFilesRewritesModuleAndUpdatesManifest(t *testing.T) {
	f := newFixture(t, strategy.Split, toolX)
	f.write("x.mod", "module example.com/proj/tools/x\n\ngo 1.24\n\ntool example.com/a/x\n")
	f.write("x.sum", "sum\n")

	if err := f.mgr.Rename(context.Background(), "x", "fmt", tools.RenameOptions{}); err != nil {
		t.Fatal(err)
	}

	if f.onDisk("x.mod") || f.onDisk("x.sum") || !f.onDisk("fmt.mod") || !f.onDisk("fmt.sum") {
		t.Fatal("files not moved")
	}

	if got := f.read("fmt.mod"); !strings.Contains(got, "module example.com/proj/tools/fmt") {
		t.Fatalf("module line not rewritten:\n%s", got)
	}

	if got := f.persisted(); !slices.Equal(got, []string{"fmt"}) {
		t.Fatalf("persisted manifest lists %v", got)
	}

	if f.rec.n != 1 {
		t.Fatalf("fingerprint recorded %d times", f.rec.n)
	}
}

func TestRenameModuleStrategyMovesTheDirectory(t *testing.T) {
	f := newFixture(t, strategy.Module, toolX)
	f.write("x/go.mod", "module example.com/proj/tools/x\n\ngo 1.24\n")

	if err := f.mgr.Rename(context.Background(), "x", "fmt", tools.RenameOptions{}); err != nil {
		t.Fatal(err)
	}

	if f.onDisk("x") || !f.onDisk("fmt/go.mod") || !strings.Contains(f.read("fmt/go.mod"), "module example.com/proj/tools/fmt") {
		t.Fatal("directory not moved")
	}
}

func TestRenameWithoutAModfileChangesTheManifestOnly(t *testing.T) {
	f := newFixture(t, strategy.Split, toolX)

	if err := f.mgr.Rename(context.Background(), "x", "fmt", tools.RenameOptions{}); err != nil {
		t.Fatal(err)
	}

	if got := f.persisted(); !slices.Equal(got, []string{"fmt"}) || f.rec.n != 0 || !strings.Contains(f.out.String(), "renaming in the manifest only") {
		t.Fatalf("persisted=%v recorded=%d out=%q", got, f.rec.n, f.out)
	}
}

func TestRenameDryRunChecksButChangesNothing(t *testing.T) {
	f := newFixture(t, strategy.Split, toolX)
	f.write("x.mod", "module m\n")

	if err := f.mgr.Rename(context.Background(), "x", "fmt", tools.RenameOptions{DryRun: true}); err != nil {
		t.Fatal(err)
	}

	if !f.onDisk("x.mod") || f.onDisk("fmt.mod") || f.persisted()[0] != "x" {
		t.Fatal("dry run changed something")
	}

	if !strings.Contains(f.out.String(), "[dry-run] Would rename x → fmt") {
		t.Fatalf("output %q", f.out)
	}

	if err := f.mgr.Rename(context.Background(), "x", "x", tools.RenameOptions{DryRun: true}); ierrors.CodeOf(err) != ierrors.Usage {
		t.Fatalf("identical names accepted in a dry run: %v", err)
	}
}

func TestRenameRefusals(t *testing.T) {
	cases := []struct {
		name     string
		strategy strategy.Strategy
		prep     func(f *fixture)
		from, to string
		code     ierrors.Code
	}{
		{"unknown tool", strategy.Split, nil, "nope", "z", ierrors.ToolNotFound},
		{"target in manifest", strategy.Split, nil, "x", "y", ierrors.Usage},
		{"unified", strategy.Unified, nil, "x", "z", ierrors.Environment},
		{"unsafe new name", strategy.Module, nil, "x", "../z", ierrors.Usage},
		{"file in the way", strategy.Split, func(f *fixture) {
			f.write("x.mod", "module m\n")
			f.write("z.mod", "someone else's\n")
		}, "x", "z", ierrors.Environment},
		{"directory in the way", strategy.Module, func(f *fixture) {
			f.write("x/go.mod", "module m\n")
			f.write("z/go.mod", "someone else's\n")
		}, "x", "z", ierrors.Environment},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, c.strategy, toolX, toolY)
			if c.prep != nil {
				c.prep(f)
			}

			err := f.mgr.Rename(context.Background(), c.from, c.to, tools.RenameOptions{})
			if ierrors.CodeOf(err) != c.code {
				t.Fatalf("err = %v, want code %d", err, c.code)
			}

			if got := f.persisted(); len(got) != 2 {
				t.Fatalf("manifest changed by a refused rename: %v", got)
			}
		})
	}
}

func TestRenameUndoesEveryStepWhenTheManifestWriteFails(t *testing.T) {
	f := newFixture(t, strategy.Split, toolX)
	original := "module example.com/proj/tools/x\n\ngo 1.24\n\ntool example.com/a/x\n"
	f.write("x.mod", original)
	f.write("x.sum", "sum\n")
	f.store.fails = true

	err := f.mgr.Rename(context.Background(), "x", "fmt", tools.RenameOptions{})
	if err == nil || !strings.Contains(err.Error(), "Could not write the manifest") {
		t.Fatalf("err = %v", err)
	}

	if f.read("x.mod") != original || !f.onDisk("x.sum") || f.onDisk("fmt.mod") || f.onDisk("fmt.sum") {
		t.Fatal("the files were not put back")
	}

	if _, ok := f.store.m.Tool("x"); !ok {
		t.Fatal("the manifest lost the original entry")
	}

	if _, ok := f.store.m.Tool("fmt"); ok {
		t.Fatal("the manifest kept the new entry")
	}
}

func TestFailureCode(t *testing.T) {
	cases := map[string]ierrors.Code{
		"dial tcp 1.2.3.4:443: i/o timeout": ierrors.Network,
		"no such host":                      ierrors.Network,
		"invalid module version syntax":     ierrors.Usage,
		"malformed module path \"x\"":       ierrors.Usage,
		"something else":                    ierrors.Generic,
		"":                                  ierrors.Generic,
		"Get https://proxy: connection refused (x)": ierrors.Network,
	}

	for out, want := range cases {
		if got := tools.FailureCode(out); got != want {
			t.Errorf("FailureCode(%q) = %d, want %d", out, got, want)
		}
	}
}

func TestCheckPackageSpec(t *testing.T) {
	for _, ok := range []string{"example.com/a/x", "example.com/a/x@v1.0.0", "example.com/a/x@latest", "github.com/Foo/Bar"} {
		if err := tools.CheckPackageSpec(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}

	for _, bad := range []string{"", "-x", "-x@v1", "a b", "../x", "example.com/x/"} {
		if err := tools.CheckPackageSpec(bad); ierrors.CodeOf(err) != ierrors.Environment {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

// Callers hold the interface, so a fake manager stands in for the real one.
type fakeManager struct{ removed []string }

func (m *fakeManager) Add(context.Context, tools.AddOptions) (manifest.Tool, error) {
	return manifest.Tool{}, nil
}

func (m *fakeManager) Remove(_ context.Context, name string) (tools.RemoveResult, error) {
	m.removed = append(m.removed, name)

	return tools.RemoveResult{InManifest: true}, nil
}

func (m *fakeManager) Rename(context.Context, string, string, tools.RenameOptions) error { return nil }

func (m *fakeManager) Upgrade(_ context.Context, t manifest.Tool) (manifest.Tool, error) {
	return t, nil
}

func (m *fakeManager) Restore(context.Context, []manifest.Tool, tools.RestoreOptions) error {
	return nil
}

func TestManagerCanBeFaked(t *testing.T) {
	fake := &fakeManager{}

	var m tools.Manager = fake

	if _, err := m.Remove(context.Background(), "x"); err != nil || !slices.Equal(fake.removed, []string{"x"}) {
		t.Fatalf("%v %v", err, fake.removed)
	}
}

// blocking waits for the context, as a long download does, except for the one
// package that fails at once.
type blocking struct{ failing string }

func (b blocking) Run(ctx context.Context, c gocmd.Call) (gocmd.Result, error) {
	if strings.Contains(strings.Join(c.Args, " "), b.failing) {
		return gocmd.Result{Code: 1, Stderr: []byte("boom")}, nil
	}

	<-ctx.Done()

	return gocmd.Result{}, ctx.Err()
}

// The tool that failed is reported, not the cancellation it caused in a tool
// that came earlier in the list.
func TestRestoreReportsTheRealFailureNotTheCancellation(t *testing.T) {
	f := newFixture(t, strategy.Split)
	f.build(blocking{failing: "example.com/b/y"})

	err := f.mgr.Restore(context.Background(), []manifest.Tool{toolX, toolY}, tools.RestoreOptions{Jobs: 2})
	if err == nil || !strings.Contains(err.Error(), "boom") || errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
