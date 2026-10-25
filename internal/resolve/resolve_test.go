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

package resolve_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/manifest"
	"github.com/piusalfred/gotools/internal/modparser"
	"github.com/piusalfred/gotools/internal/resolve"
	"github.com/piusalfred/gotools/internal/strategy"
)

type fakeGo struct {
	calls []gocmd.Call
	fn    func(gocmd.Call) (gocmd.Result, error)
}

func (g *fakeGo) Run(_ context.Context, c gocmd.Call) (gocmd.Result, error) {
	g.calls = append(g.calls, c)

	if g.fn == nil {
		return gocmd.Result{}, nil
	}

	return g.fn(c)
}

func write(t *testing.T, path, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const (
	fmtPkg  = "mvdan.cc/gofumpt"
	impPkg  = "golang.org/x/tools/cmd/goimports"
	twoMods = "module tools\n\ngo 1.24\n\ntool " + fmtPkg + "\n\ntool " + impPkg + "\n"
	oneMod  = "module tools/x\n\ngo 1.24\n\ntool " + impPkg + "\n"
)

type manifestStub map[string]manifest.Tool

func (m manifestStub) Tool(name string) (manifest.Tool, bool) { t, ok := m[name]; return t, ok }

func resolver(root string, st strategy.Strategy, m resolve.Manifest) *resolve.Resolver {
	return &resolve.Resolver{Root: root, Layout: strategy.Layout{Strategy: st, Dir: "tools"}, ModFiles: modparser.XParser{}, Manifest: m}
}

func TestResolveEachStrategy(t *testing.T) {
	cases := []struct {
		st   strategy.Strategy
		file string
	}{
		{strategy.Split, "tools/goimports.mod"},
		{strategy.Module, "tools/goimports/go.mod"},
		{strategy.Unified, "tools/go.mod"},
	}

	for _, tc := range cases {
		t.Run(string(tc.st), func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, tc.file), oneMod)

			ref, err := resolver(root, tc.st, nil).Resolve("goimports")
			if err != nil {
				t.Fatal(err)
			}

			if ref.Package != impPkg || ref.Binary != "goimports" || ref.ModFileRel != tc.file || ref.ModFile != filepath.Join(root, tc.file) {
				t.Fatalf("ref = %+v", ref)
			}
		})
	}
}

// With two directives in the shared go.mod, a name picks its own: by the
// manifest when it knows the name (an alias), else by the inferred binary.
func TestResolveUnifiedPicksTheNamedDirective(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "tools/go.mod"), twoMods)

	byBinary, err := resolver(root, strategy.Unified, nil).Resolve("goimports")
	if err != nil || byBinary.Package != impPkg || byBinary.Binary != "goimports" {
		t.Fatalf("by binary: %+v, %v", byBinary, err)
	}

	m := manifestStub{"fmt": {Name: "fmt", Package: fmtPkg}}

	alias, err := resolver(root, strategy.Unified, m).Resolve("fmt")
	if err != nil || alias.Package != fmtPkg || alias.Binary != "gofumpt" {
		t.Fatalf("alias: %+v, %v", alias, err)
	}

	// The manifest names a package the file does not declare: nothing serves it.
	gone := manifestStub{"fmt": {Name: "fmt", Package: "example.com/gone"}}

	ref, err := resolver(root, strategy.Unified, gone).Resolve("fmt")
	if err != nil || ref.Package != "" || ref.Binary != "fmt" {
		t.Fatalf("gone: %+v, %v", ref, err)
	}

	none, err := resolver(root, strategy.Unified, nil).Resolve("nope")
	if err != nil || none.Package != "" {
		t.Fatalf("none: %+v, %v", none, err)
	}
}

func TestResolveMissingModfile(t *testing.T) {
	ref, err := resolver(t.TempDir(), strategy.Split, nil).Resolve("goimports")

	var missing *resolve.MissingError
	if !errors.As(err, &missing) || missing.Path != "tools/goimports.mod" || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}

	if ref.ModFileRel != "tools/goimports.mod" {
		t.Errorf("ref = %+v", ref)
	}
}

func TestResolveRefusesAnUnsafeNameAndABrokenModfile(t *testing.T) {
	root := t.TempDir()

	if _, err := resolver(root, strategy.Split, nil).Resolve("../x"); err == nil {
		t.Error("a path in the name was accepted")
	}

	write(t, filepath.Join(root, "tools/bad.mod"), "this is not a modfile\n")

	if _, err := resolver(root, strategy.Split, nil).Resolve("bad"); err == nil || !strings.Contains(err.Error(), "parse tools/bad.mod") {
		t.Errorf("err = %v", err)
	}
}

func TestCallPerStrategy(t *testing.T) {
	root := t.TempDir()

	cases := []struct {
		st   strategy.Strategy
		file string
		dir  string
		args []string
	}{
		{strategy.Unified, "tools/go.mod", filepath.Join(root, "tools"), []string{"tool", "goimports", "-w"}},
		{strategy.Split, "tools/goimports.mod", root, []string{"tool", "-modfile=" + filepath.Join(root, "tools/goimports.mod"), "goimports", "-w"}},
		{strategy.Module, "tools/goimports/go.mod", filepath.Join(root, "tools/goimports"), []string{"tool", "goimports", "-w"}},
	}

	for _, tc := range cases {
		t.Run(string(tc.st), func(t *testing.T) {
			write(t, filepath.Join(root, tc.file), oneMod)

			r := resolver(root, tc.st, nil)

			ref, err := r.Resolve("goimports")
			if err != nil {
				t.Fatal(err)
			}

			call := r.Call(ref, "-w")
			if call.Dir != tc.dir || !slices.Equal(call.Args, tc.args) {
				t.Fatalf("call = %+v", call)
			}
		})
	}
}

func prober(t *testing.T, g *fakeGo, offline bool) *resolve.Prober {
	t.Helper()

	root := t.TempDir()
	write(t, filepath.Join(root, "tools/goimports.mod"), oneMod)

	return &resolve.Prober{Resolver: resolver(root, strategy.Split, nil), Go: g, Offline: offline}
}

func TestProbe(t *testing.T) {
	fail := func(code int, stderr string) *fakeGo {
		return &fakeGo{fn: func(gocmd.Call) (gocmd.Result, error) {
			return gocmd.Result{Code: code, Stderr: []byte(stderr)}, nil
		}}
	}

	cases := []struct {
		name string
		g    *fakeGo
		want string
	}{
		{"runs", &fakeGo{}, ""},
		{"go level failure", fail(1, "noise\ngo: no such tool \"x\"\n"), `go: no such tool "x"`},
		{"the tool itself exits non-zero", fail(2, "usage: goimports [flags]\n"), ""},
		{"go missing", &fakeGo{fn: func(gocmd.Call) (gocmd.Result, error) { return gocmd.Result{}, gocmd.ErrNotFound }}, "go not installed"},
		{"cannot start", &fakeGo{fn: func(gocmd.Call) (gocmd.Result, error) { return gocmd.Result{}, errors.New("boom") }}, "boom"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := prober(t, tc.g, false).Probe(context.Background(), "goimports"); got != tc.want {
				t.Fatalf("Probe = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProbeIsReadOnlyAndLocal(t *testing.T) {
	g := &fakeGo{}

	if got := prober(t, g, true).Probe(context.Background(), "goimports"); got != "" {
		t.Fatal(got)
	}

	if len(g.calls) != 1 || !slices.Equal(g.calls[0].Env, []string{"GOTOOLCHAIN=local", "GOPROXY=off"}) {
		t.Fatalf("calls = %+v", g.calls)
	}

	g = &fakeGo{}
	prober(t, g, false).Probe(context.Background(), "goimports")

	if !slices.Equal(g.calls[0].Env, []string{"GOTOOLCHAIN=local"}) {
		t.Fatalf("env = %v", g.calls[0].Env)
	}
}

func TestProbeSpendsNoGoCallWithoutADirective(t *testing.T) {
	g := &fakeGo{}
	p := prober(t, g, false)
	write(t, filepath.Join(p.Resolver.Root, "tools/empty.mod"), "module tools/empty\n")

	for name, want := range map[string]string{
		"empty":   "no tool directive for empty in tools/empty.mod",
		"missing": "missing tools/missing.mod",
		"../bad":  "invalid tool name: ../bad",
	} {
		if got := p.Probe(context.Background(), name); got != want {
			t.Errorf("Probe(%q) = %q, want %q", name, got, want)
		}
	}

	if len(g.calls) != 0 {
		t.Errorf("go was called: %+v", g.calls)
	}

	p.Resolver.Layout.Strategy = "bogus"

	if got := p.Probe(context.Background(), "empty"); got != "unknown strategy: bogus" {
		t.Errorf("got %q", got)
	}
}

func TestRunsTriesEachProbeArgument(t *testing.T) {
	g := &fakeGo{fn: func(c gocmd.Call) (gocmd.Result, error) {
		if c.Args[len(c.Args)-1] == "version" {
			return gocmd.Result{}, nil
		}

		return gocmd.Result{Code: 1}, nil
	}}

	if !prober(t, g, false).Runs(context.Background(), "goimports") {
		t.Fatal("not runnable though `version` exits 0")
	}

	var tried []string
	for _, c := range g.calls {
		tried = append(tried, c.Args[len(c.Args)-1])
	}

	if !slices.Equal(tried, []string{"--version", "--help", "version"}) {
		t.Errorf("tried %v", tried)
	}

	never := &fakeGo{fn: func(gocmd.Call) (gocmd.Result, error) { return gocmd.Result{Code: 2}, nil }}
	if prober(t, never, false).Runs(context.Background(), "goimports") {
		t.Error("runnable though every probe failed")
	}

	if prober(t, &fakeGo{}, false).Runs(context.Background(), "missing") {
		t.Error("a tool without a modfile runs")
	}
}

func TestRunsOfflineTurnsTheProxyOff(t *testing.T) {
	g := &fakeGo{}
	prober(t, g, true).Runs(context.Background(), "goimports")

	if len(g.calls) != 1 || !slices.Contains(g.calls[0].Env, "GOPROXY=off") {
		t.Fatalf("calls = %+v", g.calls)
	}
}

// A tool with none of --version, --help or version (goimports exits 2 on all
// three) is still runnable when go can build it.
func TestRunsAcceptsAToolThatBuildsButHasNoProbeFlag(t *testing.T) {
	g := &fakeGo{fn: func(c gocmd.Call) (gocmd.Result, error) {
		if len(c.Args) > 1 && c.Args[1] == "-n" {
			return gocmd.Result{Stdout: []byte("/cache/goimports\n")}, nil
		}

		return gocmd.Result{Code: 2, Stderr: []byte("flag provided but not defined")}, nil
	}}

	if !prober(t, g, false).Runs(context.Background(), "goimports") {
		t.Fatal("a tool that builds was reported not runnable")
	}

	last := g.calls[len(g.calls)-1]
	if last.Args[0] != "tool" || last.Args[1] != "-n" {
		t.Fatalf("the last call was %v, want the build check", last.Args)
	}

	// A tool that does not even build is not.
	broken := &fakeGo{fn: func(gocmd.Call) (gocmd.Result, error) {
		return gocmd.Result{Code: 1, Stderr: []byte("# example.com/x\nx.go:1: syntax error")}, nil
	}}

	if prober(t, broken, false).Runs(context.Background(), "goimports") {
		t.Fatal("a tool that does not build was reported runnable")
	}

	// And offline the build check does not reach for the proxy.
	offline := &fakeGo{fn: g.fn}
	prober(t, offline, true).Runs(context.Background(), "goimports")

	for _, c := range offline.calls {
		if !slices.Contains(c.Env, "GOPROXY=off") {
			t.Fatalf("%v ran without GOPROXY=off", c.Args)
		}
	}
}

// A probe argument that works settles it without the build check.
func TestRunsSkipsTheBuildCheckWhenAProbeArgumentWorks(t *testing.T) {
	g := &fakeGo{}

	if !prober(t, g, false).Runs(context.Background(), "goimports") {
		t.Fatal("not runnable")
	}

	if len(g.calls) != 1 {
		t.Fatalf("calls = %v", g.calls)
	}
}
