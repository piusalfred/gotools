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

package doctor_test

import (
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
	"github.com/piusalfred/gotools/internal/doctor"
	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/lock"
	"github.com/piusalfred/gotools/internal/manifest"
	"github.com/piusalfred/gotools/internal/modparser"
	"github.com/piusalfred/gotools/internal/strategy"
)

// goOpts shapes the fake go the doctor talks to.
type goOpts struct {
	version       string // `go env GOVERSION`; default go1.24.3
	notFound      bool
	goproxy       string // default https://proxy.golang.org,direct
	gonoproxy     string
	listStderr    string // makes `go list -m` fail
	listBlocks    bool   // `go list -m` never answers
	toolStderr    string // makes `go tool` fail (exit 1) with this output
	verifyStderr  string // makes `go mod verify` fail
	verifyNoFlag  bool   // `go mod verify -modfile` is "not defined" (older go)
	versionStderr string // makes `go env GOVERSION` fail
}

type fakeGo struct {
	o     goOpts
	mu    sync.Mutex
	calls []gocmd.Call
}

func (g *fakeGo) Run(ctx context.Context, c gocmd.Call) (gocmd.Result, error) {
	g.mu.Lock()
	g.calls = append(g.calls, c)
	g.mu.Unlock()

	if g.o.notFound {
		return gocmd.Result{}, gocmd.ErrNotFound
	}

	if len(c.Args) == 0 {
		return gocmd.Result{}, nil
	}

	switch c.Args[0] {
	case "env":
		switch c.Args[1] {
		case "GOVERSION":
			if g.o.versionStderr != "" {
				return gocmd.Result{Code: 1, Stderr: []byte(g.o.versionStderr)}, nil
			}

			return gocmd.Result{Stdout: []byte(or(g.o.version, "go1.24.3") + "\n")}, nil
		case "GOPROXY":
			return gocmd.Result{Stdout: []byte(or(g.o.goproxy, "https://proxy.golang.org,direct") + "\n" + g.o.gonoproxy + "\n")}, nil
		}
	case "list":
		if g.o.listBlocks {
			<-ctx.Done()

			return gocmd.Result{}, ctx.Err()
		}

		if g.o.listStderr != "" {
			return gocmd.Result{Code: 1, Stderr: []byte(g.o.listStderr)}, nil
		}
	case "mod":
		if g.o.verifyNoFlag && slices.ContainsFunc(c.Args, func(a string) bool { return strings.HasPrefix(a, "-modfile=") }) {
			return gocmd.Result{Code: 2, Stderr: []byte("flag provided but not defined: -modfile")}, nil
		}

		if g.o.verifyStderr != "" {
			return gocmd.Result{Code: 1, Stderr: []byte(g.o.verifyStderr)}, nil
		}
	case "tool":
		if g.o.toolStderr != "" {
			return gocmd.Result{Code: 1, Stderr: []byte(g.o.toolStderr)}, nil
		}
	}

	return gocmd.Result{}, nil
}

func (g *fakeGo) called(first string) []gocmd.Call {
	var out []gocmd.Call

	for _, c := range g.calls {
		if len(c.Args) > 0 && c.Args[0] == first {
			out = append(out, c)
		}
	}

	return out
}

func or(a, b string) string {
	if a != "" {
		return a
	}

	return b
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
	modX = "module tools/x\n\ngo 1.24\n\ntool example.com/a/x\n\nrequire example.com/a v1.0.0\n"
	modY = "module tools/y\n\ngo 1.24\n\ntool example.com/b/y\n\nrequire example.com/b v1.2.0\n"
)

// project is an installed project of strategy with tools x and y and the sums
// the doctor looks for.
func project(t *testing.T, st strategy.Strategy) *doctor.Doctor {
	t.Helper()

	root := t.TempDir()
	m := manifest.New()
	m.Strategy, m.ModulePrefix = string(st), "tools"
	m.SetTool(manifest.Tool{Name: "x", Source: "go", Package: "example.com/a/x", Version: "v1.0.0"})
	m.SetTool(manifest.Tool{Name: "y", Source: "go", Package: "example.com/b/y", Version: "v1.2.0"})

	switch st {
	case strategy.Split:
		for n, mod := range map[string]string{"x": modX, "y": modY} {
			write(t, filepath.Join(root, "tools", n+".mod"), mod)
			write(t, filepath.Join(root, "tools", n+".sum"), "sum\n")
		}
	case strategy.Module:
		for n, mod := range map[string]string{"x": modX, "y": modY} {
			write(t, filepath.Join(root, "tools", n, "go.mod"), mod)
			write(t, filepath.Join(root, "tools", n, "go.sum"), "sum\n")
		}
	case strategy.Unified:
		write(t, filepath.Join(root, "tools", "go.mod"), "module tools\n\ngo 1.24\n\ntool example.com/a/x\n\ntool example.com/b/y\n\nrequire (\n\texample.com/a v1.0.0\n\texample.com/b v1.2.0\n)\n")
		write(t, filepath.Join(root, "tools", "go.sum"), "sum\n")
	}

	layout := strategy.Layout{Strategy: st, Dir: "tools"}

	return &doctor.Doctor{
		Program: "gotools", Root: root, Layout: layout, Manifest: m, HasManifest: true,
		ModFiles: modparser.XParser{}, Locker: lock.NewFileLocker(layout.ToolsDir(root)),
		Go: &fakeGo{}, Scratch: t.TempDir(),
		LookGo:       func() (string, error) { return "/usr/local/go/bin/go", nil },
		ProxyTimeout: 200 * time.Millisecond,
	}
}

// row finds a check by name.
func row(t *testing.T, r doctor.Report, name string) doctor.Check {
	t.Helper()

	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}

	t.Fatalf("no check %q in %+v", name, r.Checks)

	return doctor.Check{}
}

func wantRow(t *testing.T, r doctor.Report, name string, status doctor.Status, detail string) {
	t.Helper()

	c := row(t, r, name)
	if c.Status != status || !strings.Contains(c.Detail, detail) {
		t.Fatalf("%s = %s %q, want %s containing %q", name, c.Status, c.Detail, status, detail)
	}
}

func TestHealthyEachStrategy(t *testing.T) {
	for _, st := range []strategy.Strategy{strategy.Split, strategy.Module, strategy.Unified} {
		t.Run(string(st), func(t *testing.T) {
			r := project(t, st).Run(context.Background())

			if !r.Healthy() || r.Issues() != 0 {
				t.Fatalf("unhealthy: %+v", r.Checks)
			}

			wantRow(t, r, "go", doctor.Pass, "Go 1.24.3 at /usr/local/go/bin/go — meets minimum (1.24+)")
			wantRow(t, r, "proxy", doctor.Pass, "reachable (probed example.com/a@latest)")
			wantRow(t, r, "config", doctor.Pass, ".gotools.json exists and is valid (schema v1)")
			wantRow(t, r, "config.strategy", doctor.Pass, "Strategy: "+string(st)+" — matches disk")
			wantRow(t, r, "tools.x", doctor.Pass, "x@v1.0.0 — runnable")
			wantRow(t, r, "tools.y", doctor.Pass, "y@v1.2.0 — runnable")
			wantRow(t, r, "lock", doctor.Pass, "no lock detected")
			wantRow(t, r, "disk", doctor.Pass, "tools/ uses")

			want := "go mod verify passed for all 2 modules"
			if st == strategy.Unified {
				want = "go mod verify passed for all 1 modules"
			}

			wantRow(t, r, "integrity", doctor.Pass, want)
		})
	}
}

func TestReportOrderIsStable(t *testing.T) {
	r := project(t, strategy.Split).Run(context.Background())

	var names []string
	for _, c := range r.Checks {
		names = append(names, c.Name)
	}

	want := []string{"go", "proxy", "config", "config.strategy", "tools.x", "tools.y", "lock", "integrity", "disk"}
	if !slices.Equal(names, want) {
		t.Fatalf("names = %v", names)
	}
}

func TestRunTwiceGivesTheSameReport(t *testing.T) {
	d := project(t, strategy.Split)

	first, second := d.Run(context.Background()), d.Run(context.Background())
	if len(first.Checks) != len(second.Checks) {
		t.Fatalf("%d then %d checks", len(first.Checks), len(second.Checks))
	}
}

func TestGoProblemsAreRows(t *testing.T) {
	cases := map[string]struct {
		o      goOpts
		status doctor.Status
		want   string
	}{
		"too old":      {goOpts{version: "go1.23.9"}, doctor.Fail, "Go 1.23.9 is too old. Go 1.24 or higher is required."},
		"missing":      {goOpts{notFound: true}, doctor.Fail, "Go not installed. Install Go 1.24 or higher"},
		"undetectable": {goOpts{versionStderr: "go: go.mod requires go >= 1.99 (running go 1.24; GOTOOLCHAIN=local)"}, doctor.Fail, "Could not determine Go version. go: go.mod requires go >= 1.99"},
		"devel":        {goOpts{version: "devel go1.26-abc123 Tue Jan 1 00:00:00 2030"}, doctor.Pass, "meets minimum"},
		"no version":   {goOpts{version: "weird"}, doctor.Fail, "Could not determine Go version."},
	}

	for name, tc := range cases {
		d := project(t, strategy.Split)
		d.Go = &fakeGo{o: tc.o}

		wantRow(t, d.Run(context.Background()), "go", tc.status, tc.want)

		if name == "missing" {
			// Go missing is one issue; the probes that need go are skipped, not failed.
			r := d.Run(context.Background())
			wantRow(t, r, "proxy", doctor.Skip, "go not installed")
			wantRow(t, r, "integrity", doctor.Skip, "go not installed")
		}
	}
}

func TestProxy(t *testing.T) {
	// Unreachable: a warning, and go's message is shown.
	d := project(t, strategy.Split)
	d.Go = &fakeGo{o: goOpts{listStderr: "go: dial tcp 1.2.3.4:443: i/o timeout"}}

	r := d.Run(context.Background())
	wantRow(t, r, "proxy", doctor.Warn, "unreachable: go: dial tcp 1.2.3.4:443: i/o timeout")

	if r.Issues() != 1 {
		t.Fatalf("issues = %d", r.Issues())
	}

	// No answer at all: bounded by the probe timeout.
	d.Go = &fakeGo{o: goOpts{listBlocks: true}}
	wantRow(t, d.Run(context.Background()), "proxy", doctor.Warn, "unreachable (no answer within 200ms)")

	// A private module is passed over, not reported as unreachable.
	g := &fakeGo{o: goOpts{gonoproxy: "example.com/a"}}
	d.Go = g

	wantRow(t, d.Run(context.Background()), "proxy", doctor.Pass, "reachable (probed example.com/b@latest)")

	if list := g.called("list"); len(list) != 1 || list[0].Args[len(list[0].Args)-1] != "example.com/b@latest" {
		t.Fatalf("probe calls: %v", list)
	}

	g = &fakeGo{o: goOpts{gonoproxy: "example.com", listStderr: "must not be called"}}
	d.Go = g

	wantRow(t, d.Run(context.Background()), "proxy", doctor.Pass, "every tool module is private — reachability not checked")

	if len(g.called("list")) != 0 {
		t.Fatal("a private module was sent to the proxy")
	}

	// A proxy that cannot even be read.
	d.Go = &fakeGo{o: goOpts{}}
	d.Go = unreadableProxy{}

	wantRow(t, d.Run(context.Background()), "proxy", doctor.Warn, "could not read GOPROXY")

	// No tools: nothing to probe, so nothing is requested.
	empty := project(t, strategy.Split)
	empty.Manifest = manifest.New()
	g = &fakeGo{}
	empty.Go = g

	wantRow(t, empty.Run(context.Background()), "proxy", doctor.Pass, "no tool to probe — reachability not checked")

	if len(g.called("list")) != 0 {
		t.Fatal("probed with no tools")
	}
}

type unreadableProxy struct{}

func (unreadableProxy) Run(_ context.Context, c gocmd.Call) (gocmd.Result, error) {
	if len(c.Args) > 1 && c.Args[0] == "env" && c.Args[1] == "GOPROXY" {
		return gocmd.Result{Code: 1}, nil
	}

	return gocmd.Result{Stdout: []byte("go1.24.3\n")}, nil
}

func TestProxyProbeSkipsToolsItCannotLocate(t *testing.T) {
	d := project(t, strategy.Split)
	d.Manifest.SetTool(manifest.Tool{Name: "aaa", Source: "go", Package: "example.com/zzz/aaa", Version: "v1"})
	d.Manifest.SetTool(manifest.Tool{Name: "bin", Source: "binary", Package: "example.com/bin", Version: "v1"})
	d.Manifest.SetTool(manifest.Tool{Name: "nopkg", Source: "go", Version: "v1"})

	// aaa has no modfile, bin is not a go tool, nopkg has no package: x is probed.
	wantRow(t, d.Run(context.Background()), "proxy", doctor.Pass, "probed example.com/a@latest")
}

func TestOfflineNeverTouchesTheNetwork(t *testing.T) {
	d := project(t, strategy.Split)
	g := &fakeGo{}
	d.Go, d.Offline = g, true

	r := d.Run(context.Background())
	wantRow(t, r, "proxy", doctor.Skip, "offline mode — reachability not checked")

	if len(g.called("list")) != 0 {
		t.Fatal("proxy probed")
	}

	for _, c := range append(g.called("mod"), g.called("tool")...) {
		if !slices.Contains(c.Env, "GOPROXY=off") {
			t.Fatalf("%v ran without GOPROXY=off (env %v)", c.Args, c.Env)
		}
	}
}

// The doctor must never let go switch or download a toolchain.
func TestEveryProbeUsesTheLocalToolchainFromTheScratchDirectory(t *testing.T) {
	d := project(t, strategy.Module)
	g := &fakeGo{}
	d.Go = g

	d.Run(context.Background())

	if len(g.calls) < 5 {
		t.Fatalf("only %d go calls", len(g.calls))
	}

	for _, c := range g.calls {
		if !slices.Contains(c.Env, "GOTOOLCHAIN=local") {
			t.Fatalf("%v ran without GOTOOLCHAIN=local (env %v)", c.Args, c.Env)
		}

		if c.Args[0] == "env" || c.Args[0] == "list" {
			if c.Dir != d.Scratch {
				t.Fatalf("%v ran in %s, not the scratch directory", c.Args, c.Dir)
			}
		}
	}
}

func TestToolsNotRunnable(t *testing.T) {
	d := project(t, strategy.Split)
	d.Go = &fakeGo{o: goOpts{toolStderr: `go: no such tool "staticcheck"`}}

	r := d.Run(context.Background())
	wantRow(t, r, "tools.x", doctor.Warn, `x@v1.0.0 — not runnable: go: no such tool "staticcheck" (run 'gotools sync')`)

	if r.Issues() != 2 {
		t.Fatalf("issues = %d", r.Issues())
	}

	// A tool that starts and then complains about its usage is runnable.
	d.Go = &fakeGo{o: goOpts{toolStderr: "usage: x [flags]"}}
	wantRow(t, d.Run(context.Background()), "tools.x", doctor.Pass, "x@v1.0.0 — runnable")

	// No tools at all.
	d.Manifest = manifest.New()
	wantRow(t, d.Run(context.Background()), "tools", doctor.Pass, "no tools declared")

	// A tool of another source is not probed.
	d = project(t, strategy.Split)
	d.Manifest.SetTool(manifest.Tool{Name: "bin", Source: "binary", Package: "example.com/bin", Version: "v1"})

	wantRow(t, d.Run(context.Background()), "tools.bin", doctor.Skip, "source 'binary' not probed")
}

func TestConfigProblems(t *testing.T) {
	d := project(t, strategy.Split)
	d.HasManifest = false

	r := d.Run(context.Background())
	wantRow(t, r, "config", doctor.Fail, "no .gotools.json — run 'gotools init'")
	wantRow(t, r, "config.strategy", doctor.Skip, "cannot compare strategy")

	d = project(t, strategy.Split)
	d.Bad = ".gotools.json: requires schema version 2"

	r = d.Run(context.Background())
	wantRow(t, r, "config", doctor.Fail, "requires schema version 2")
	wantRow(t, r, "config.strategy", doctor.Skip, "cannot compare strategy")
	wantRow(t, r, "go", doctor.Pass, "Go 1.24.3")
}

func TestStrategyAgainstDisk(t *testing.T) {
	// Disk is unified, manifest says split.
	d := project(t, strategy.Unified)
	d.Layout.Strategy = strategy.Split

	wantRow(t, d.Run(context.Background()), "config.strategy", doctor.Warn, "Strategy: split — disk looks like unified (run 'gotools sync' to migrate)")

	// An empty tools directory.
	d = project(t, strategy.Split)
	if err := os.RemoveAll(filepath.Join(d.Root, "tools")); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(d.Root, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}

	wantRow(t, d.Run(context.Background()), "config.strategy", doctor.Warn, "tools not synced — run 'gotools sync'")

	// A hybrid directory.
	d = project(t, strategy.Unified)
	write(t, filepath.Join(d.Root, "tools/z.mod"), "module tools/z\n\ngo 1.24\n\ntool example.com/c/z\n")

	wantRow(t, d.Run(context.Background()), "config.strategy", doctor.Warn, "Strategy: unified — tools holds more than one layout (unified, split); run 'gotools migrate unified' to clean up")
}

func TestOneToolUnderSeveralNames(t *testing.T) {
	d := project(t, strategy.Split)
	d.Manifest.SetTool(manifest.Tool{Name: "ex", Source: "go", Package: "example.com/a/x", Version: "v1.0.0"})

	r := d.Run(context.Background())
	wantRow(t, r, "config.duplicates", doctor.Warn, "are one tool (example.com/a/x@v1.0.0) under several names — keep one: 'gotools remove")

	// The row exists only when there is something to say.
	for _, c := range project(t, strategy.Split).Run(context.Background()).Checks {
		if c.Name == "config.duplicates" {
			t.Fatal("a healthy project has a duplicates row")
		}
	}
}

func TestIntegrity(t *testing.T) {
	// A missing go.sum.
	d := project(t, strategy.Split)
	if err := os.Remove(filepath.Join(d.Root, "tools/x.sum")); err != nil {
		t.Fatal(err)
	}

	wantRow(t, d.Run(context.Background()), "integrity", doctor.Fail, "missing tools/x.sum — run 'gotools sync'")

	// A failing verify names the file and go's first line only.
	d = project(t, strategy.Module)
	d.Go = &fakeGo{o: goOpts{verifyStderr: "tools/x: dir has been modified\nsecond line"}}

	r := d.Run(context.Background())
	wantRow(t, r, "integrity", doctor.Fail, "go mod verify failed for tools/x/go.mod: tools/x: dir has been modified")

	if strings.Contains(row(t, r, "integrity").Detail, "second line") {
		t.Fatal("more than the first line was shown")
	}

	// Older go: -modfile unknown to `mod verify`, retried through GOFLAGS.
	d = project(t, strategy.Split)
	g := &fakeGo{o: goOpts{verifyNoFlag: true}}
	d.Go = g

	wantRow(t, d.Run(context.Background()), "integrity", doctor.Pass, "go mod verify passed for all 2 modules")

	if !slices.ContainsFunc(g.called("mod"), func(c gocmd.Call) bool {
		return slices.ContainsFunc(c.Env, func(e string) bool { return strings.HasPrefix(e, "GOFLAGS=-modfile=") })
	}) {
		t.Fatal("no GOFLAGS retry")
	}

	// Nothing to verify.
	d = project(t, strategy.Split)
	if err := os.RemoveAll(filepath.Join(d.Root, "tools")); err != nil {
		t.Fatal(err)
	}

	wantRow(t, d.Run(context.Background()), "integrity", doctor.Skip, "no tool modules in tools — run 'gotools sync'")

	// An unknown strategy cannot be verified.
	d = project(t, strategy.Split)
	d.Layout.Strategy = "bogus"

	wantRow(t, d.Run(context.Background()), "integrity", doctor.Skip, "unknown strategy: bogus")
}

func TestLock(t *testing.T) {
	d := project(t, strategy.Split)

	wantRow(t, d.Run(context.Background()), "lock", doctor.Pass, "no lock detected")

	// Held by another holder: reported, and the doctor itself does not block.
	held, err := d.Locker.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	r := d.Run(context.Background())
	held.Unlock()

	c := row(t, r, "lock")
	if c.Status != doctor.Pass || !strings.Contains(c.Detail, "lock held") || !strings.Contains(c.Detail, "another gotools process is running") {
		t.Fatalf("held lock not reported: %+v", c)
	}

	// A directory left by an older gotools.
	if err := os.Mkdir(filepath.Join(d.Root, "tools/.gotools.lock"), 0o755); err != nil {
		t.Fatal(err)
	}

	wantRow(t, d.Run(context.Background()), "lock", doctor.Warn, "legacy lock directory tools/.gotools.lock found")
}

func TestLockThatCannotBeInspected(t *testing.T) {
	d := project(t, strategy.Split)
	d.Locker = brokenLocker{}

	wantRow(t, d.Run(context.Background()), "lock", doctor.Skip, "unable to inspect the lock: boom")
}

type brokenLocker struct{}

func (brokenLocker) Lock(context.Context) (*lock.Lock, error) { return nil, errors.New("boom") }
func (brokenLocker) Holder() (lock.Holder, error)             { return lock.Holder{}, errors.New("boom") }

func TestDoctorIsReadOnly(t *testing.T) {
	d := project(t, strategy.Split)

	if err := os.RemoveAll(filepath.Join(d.Root, "tools")); err != nil {
		t.Fatal(err)
	}

	write(t, filepath.Join(d.Root, config.ManifestFile), "{}")

	before := listing(t, d.Root)

	d.Run(context.Background())

	if after := listing(t, d.Root); !slices.Equal(before, after) {
		t.Fatalf("the doctor changed the project:\nbefore %v\nafter  %v", before, after)
	}
}

func listing(t *testing.T, dir string) []string {
	t.Helper()

	var out []string

	err := filepath.WalkDir(dir, func(p string, _ os.DirEntry, err error) error {
		if err == nil {
			out = append(out, p)
		}

		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	return out
}

func TestDiskUsage(t *testing.T) {
	d := project(t, strategy.Split)
	write(t, filepath.Join(d.Root, "tools/big.bin"), strings.Repeat("x", 3*1024))

	wantRow(t, d.Run(context.Background()), "disk", doctor.Pass, "tools/ uses 3.")

	if c := row(t, d.Run(context.Background()), "disk"); !strings.Contains(c.Detail, "KiB") {
		t.Fatalf("detail = %q", c.Detail)
	}

	if err := os.RemoveAll(filepath.Join(d.Root, "tools")); err != nil {
		t.Fatal(err)
	}

	wantRow(t, d.Run(context.Background()), "disk", doctor.Skip, "tools directory not found")
}

func TestAnUnusableGoRunnerDoesNotCrash(t *testing.T) {
	d := project(t, strategy.Split)
	d.Go = brokenGo{}

	r := d.Run(context.Background())
	if r.Healthy() {
		t.Fatal("a broken go is healthy")
	}

	wantRow(t, r, "go", doctor.Fail, "Could not determine Go version.")
}

type brokenGo struct{}

func (brokenGo) Run(context.Context, gocmd.Call) (gocmd.Result, error) {
	return gocmd.Result{}, errors.New("exec format error")
}

func TestReportCounts(t *testing.T) {
	r := doctor.Report{Checks: []doctor.Check{
		{Name: "a", Status: doctor.Pass},
		{Name: "b", Status: doctor.Skip},
		{Name: "c", Status: doctor.Warn},
		{Name: "d", Status: doctor.Fail},
	}}

	if r.Issues() != 2 || r.Healthy() {
		t.Fatalf("issues=%d healthy=%v", r.Issues(), r.Healthy())
	}

	if !(doctor.Report{}).Healthy() {
		t.Fatal("an empty report is not healthy")
	}
}

func TestDiskUsageUnits(t *testing.T) {
	d := project(t, strategy.Split)

	if err := os.RemoveAll(filepath.Join(d.Root, "tools")); err != nil {
		t.Fatal(err)
	}

	write(t, filepath.Join(d.Root, "tools/a"), "12345")
	wantRow(t, d.Run(context.Background()), "disk", doctor.Pass, "tools/ uses 5 B")

	write(t, filepath.Join(d.Root, "tools/big"), strings.Repeat("x", 3<<20))
	wantRow(t, d.Run(context.Background()), "disk", doctor.Pass, "MiB")
}

// Dot-files and a stray go.mod are not tool modules.
func TestIntegrityIgnoresWhatIsNotAToolModule(t *testing.T) {
	d := project(t, strategy.Split)
	write(t, filepath.Join(d.Root, "tools/.hidden.mod"), "module h\n")
	write(t, filepath.Join(d.Root, "tools/go.mod"), "module stray\n")

	wantRow(t, d.Run(context.Background()), "integrity", doctor.Pass, "all 2 modules")

	m := project(t, strategy.Module)
	write(t, filepath.Join(m.Root, "tools/.cache/go.mod"), "module c\n")

	wantRow(t, m.Run(context.Background()), "integrity", doctor.Pass, "all 2 modules")
}

func TestProxyNamesAnEmptyGoproxy(t *testing.T) {
	d := project(t, strategy.Split)
	d.Go = &fakeGo{o: goOpts{goproxy: "off"}}

	wantRow(t, d.Run(context.Background()), "proxy", doctor.Pass, "GOPROXY=off")
}

type unsetProxy struct{ fakeGo }

func (g *unsetProxy) Run(ctx context.Context, c gocmd.Call) (gocmd.Result, error) {
	if len(c.Args) > 1 && c.Args[0] == "env" && c.Args[1] == "GOPROXY" {
		return gocmd.Result{Stdout: []byte("\n\n")}, nil
	}

	return g.fakeGo.Run(ctx, c)
}

func TestProxyShowsAnUnsetGoproxy(t *testing.T) {
	d := project(t, strategy.Split)
	d.Go = &unsetProxy{}

	wantRow(t, d.Run(context.Background()), "proxy", doctor.Pass, "GOPROXY=<unset — Go default>")
}

// A project that vendors has -mod=vendor in GOFLAGS, which forbids asking the
// proxy; the probe must not inherit it.
func TestProxyProbeOverridesAVendoringGoflags(t *testing.T) {
	d := project(t, strategy.Split)
	g := &fakeGo{}
	d.Go = g

	d.Run(context.Background())

	list := g.called("list")
	if len(list) != 1 || !slices.Contains(list[0].Env, "GOFLAGS=-mod=mod") {
		t.Fatalf("probe calls: %+v", list)
	}

	// Only the proxy probe: the tools are run as the user's GOFLAGS say.
	for _, c := range g.calls {
		if c.Args[0] != "list" && slices.Contains(c.Env, "GOFLAGS=-mod=mod") {
			t.Fatalf("%v overrode GOFLAGS", c.Args)
		}
	}
}
