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

package command_test

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/piusalfred/gotools/internal/command"
	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/manifest"
)

// fakeGo answers go calls. `go get -tool` plays go's part in an install by
// writing the directive and the requirement into the modfile.
type fakeGo struct {
	mu    sync.Mutex
	calls []gocmd.Call
	// fail answers a call with a failing result when it returns a message.
	fail func(gocmd.Call) string
	// intercept answers a call itself when its last result is true.
	intercept func(gocmd.Call) (gocmd.Result, error, bool)
}

func (g *fakeGo) Run(_ context.Context, c gocmd.Call) (gocmd.Result, error) {
	g.mu.Lock()
	g.calls = append(g.calls, c)
	g.mu.Unlock()

	if g.intercept != nil {
		if res, err, ok := g.intercept(c); ok {
			return res, err
		}
	}

	if g.fail != nil {
		if msg := g.fail(c); msg != "" {
			return gocmd.Result{Code: 1, Stderr: []byte(msg)}, nil
		}
	}

	switch {
	case slices.Equal(c.Args, []string{"env", "GOVERSION"}):
		return gocmd.Result{Stdout: []byte("go1.27.0\n")}, nil
	case len(c.Args) >= 3 && c.Args[0] == "get":
		return installing(c)
	}

	return gocmd.Result{}, nil
}

func (g *fakeGo) count(args ...string) int {
	n := 0

	for _, c := range g.calls {
		if len(c.Args) >= len(args) && slices.Equal(c.Args[:len(args)], args) {
			n++
		}
	}

	return n
}

func (g *fakeGo) rootCalls(root string, args ...string) int {
	n := 0

	for _, c := range g.calls {
		if c.Dir == root && len(c.Args) >= len(args) && slices.Equal(c.Args[:len(args)], args) {
			n++
		}
	}

	return n
}

func installing(c gocmd.Call) (gocmd.Result, error) {
	modfile := filepath.Join(c.Dir, "go.mod")
	pkg := c.Args[len(c.Args)-1]

	for _, a := range c.Args {
		if name, ok := strings.CutPrefix(a, "-modfile="); ok {
			modfile = filepath.Join(c.Dir, name)
		}
	}

	base, ver, _ := strings.Cut(pkg, "@")
	if ver == "" || ver == "latest" {
		ver = "v1.2.3"
	}

	mod := path.Dir(base)

	b, err := os.ReadFile(modfile)
	if err != nil {
		return gocmd.Result{Code: 1, Stderr: []byte(err.Error())}, nil
	}

	// A real go replaces the version of a module it already requires.
	var kept []string

	for ln := range strings.SplitSeq(string(b), "\n") {
		if !strings.HasPrefix(ln, "require "+mod+" ") {
			kept = append(kept, ln)
		}
	}

	out := strings.Join(kept, "\n") + "\ntool " + base + "\nrequire " + mod + " " + ver + "\n"

	if err := os.WriteFile(modfile, []byte(out), 0o644); err != nil {
		return gocmd.Result{}, err
	}

	return gocmd.Result{}, nil
}

type outcome struct {
	out, err string
	runErr   error
	goes     *fakeGo
	ctx      *command.Context
}

type opts struct {
	goes    *fakeGo
	environ []string
	args    []string
}

// argv turns the flags a test gives into a command line: a bool is a switch,
// a string is --name=value.
func argv(flags map[string]any, positional []string) []string {
	var out []string

	for _, name := range slices.Sorted(maps.Keys(flags)) {
		switch v := flags[name].(type) {
		case bool:
			if v {
				out = append(out, "--"+name)
			}
		case string:
			out = append(out, "--"+name+"="+v)
		}
	}

	return append(out, positional...)
}

// run builds the command for c and runs it through urfave/cli, as the binary
// does.
func run(cmd command.Command, c *command.Context, args ...string) error {
	uc := cmd.Build(c)

	return uc.Run(context.Background(), append([]string{uc.Name}, args...))
}

// initIn runs init in dir with the flags the user gave.
func initIn(t *testing.T, dir string, o opts, flags map[string]any) outcome {
	t.Helper()

	if o.goes == nil {
		o.goes = &fakeGo{}
	}

	var out, errb bytes.Buffer

	c := &command.Context{
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb,
		Environ: o.environ, Program: "gotools", WorkDir: dir, Go: o.goes,
	}

	err := run(command.NewInit(), c, argv(flags, o.args)...)

	return outcome{out: out.String(), err: errb.String(), runErr: err, goes: o.goes, ctx: c}
}

func (o outcome) code() ierrors.Code { return ierrors.CodeOf(o.runErr) }

func (o outcome) ok(t *testing.T) {
	t.Helper()

	if o.runErr != nil {
		t.Fatalf("err=%v out=%q stderr=%q", o.runErr, o.out, o.err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

func exists(path string) bool {
	_, err := os.Lstat(path)

	return err == nil
}

func readManifest(t *testing.T, dir string) *manifest.Manifest {
	t.Helper()

	res, err := manifest.Load(filepath.Join(dir, config.ManifestFile))
	if err != nil {
		t.Fatal(err)
	}

	return res.Manifest
}

func files(t *testing.T, dir string) []string {
	t.Helper()

	var out []string

	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, filepath.ToSlash(rel))
		}

		return nil
	})

	return out
}

const rootGoMod = `module example.com/test

go 1.24

// keep this comment

tool (
	github.com/google/addlicense
	golang.org/x/tools/cmd/stringer
)

require (
	github.com/google/addlicense v1.1.1
	golang.org/x/tools v0.30.0
)
`

func TestInitFreshEachStrategy(t *testing.T) {
	for _, st := range []string{"split", "module", "unified"} {
		t.Run(st, func(t *testing.T) {
			dir := t.TempDir()

			r := initIn(t, dir, opts{}, map[string]any{"strategy": st, "dir": ".tools", "go": "1.24", "prefix": "example.com/p"})
			r.ok(t)

			for _, want := range []string{"Initialized .gotools.json (strategy=" + st + ", dir=.tools)", "No go.mod in project root"} {
				if !strings.Contains(r.out, want) {
					t.Fatalf("missing %q in %q", want, r.out)
				}
			}

			m := readManifest(t, dir)
			if m.Strategy != st || m.Dir != ".tools" || m.GoVersion != "1.24" || m.ModulePrefix != "example.com/p" {
				t.Fatalf("manifest = %+v", m)
			}

			if !strings.Contains(r.out, "Syncing (strategy="+st+", go=") || !strings.Contains(r.out, "dir=.tools)") {
				t.Fatalf("the sync did not run under the flags: %q", r.out)
			}
		})
	}
}

func TestInitDefaultsAndPrefixFromRootGoMod(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.24\n")

	initIn(t, dir, opts{}, nil).ok(t)

	m := readManifest(t, dir)
	if m.Strategy != "split" || m.Dir != "tools" || m.GoVersion != "inherit" || m.ModulePrefix != "example.com/test" {
		t.Fatalf("manifest = %+v", m)
	}
}

func TestInitAdoptsRootGoMod(t *testing.T) {
	for _, st := range []string{"split", "module", "unified"} {
		t.Run(st, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "go.mod"), rootGoMod)

			r := initIn(t, dir, opts{}, map[string]any{"strategy": st})
			r.ok(t)

			for _, want := range []string{"Found 2 tool(s) in go.mod", "Migrated 2 tool(s) from go.mod into gotools"} {
				if !strings.Contains(r.out, want) {
					t.Fatalf("missing %q in %q", want, r.out)
				}
			}

			mod := readFile(t, filepath.Join(dir, "go.mod"))
			if strings.Contains(mod, "tool ") || strings.Contains(mod, "\ttool") || !strings.Contains(mod, "// keep this comment") ||
				!strings.Contains(mod, "github.com/google/addlicense v1.1.1") {
				t.Fatalf("root go.mod after strip:\n%s", mod)
			}

			if n := r.goes.rootCalls(dir, "mod", "tidy"); n != 1 {
				t.Fatalf("root tidy ran %d times", n)
			}

			m := readManifest(t, dir)
			if names := m.ToolNames(); !slices.Equal(names, []string{"addlicense", "stringer"}) {
				t.Fatalf("manifest tools = %v", names)
			}

			if tl, _ := m.Tool("stringer"); tl.Version != "v0.30.0" || tl.Package != "golang.org/x/tools/cmd/stringer" {
				t.Fatalf("stringer = %+v", tl)
			}
		})
	}
}

func TestInitInstallFailureLeavesGoModUntouched(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), rootGoMod)

	g := &fakeGo{fail: func(c gocmd.Call) string {
		if len(c.Args) > 0 && c.Args[0] == "get" {
			return "dial tcp: lookup proxy.golang.org: no such host"
		}

		return ""
	}}

	r := initIn(t, dir, opts{goes: g}, nil)
	if r.code() != ierrors.Network {
		t.Fatalf("err=%v out=%q", r.runErr, r.out)
	}

	if got := readFile(t, filepath.Join(dir, "go.mod")); got != rootGoMod {
		t.Fatalf("go.mod changed:\n%s", got)
	}

	if !exists(filepath.Join(dir, config.ManifestFile)) {
		t.Fatal("manifest not bootstrapped, so a rerun cannot resume")
	}

	if strings.Contains(r.out, "Sync") {
		t.Fatalf("sync ran after a failed Run: %q", r.out)
	}
}

func TestInitNoMigrate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), rootGoMod)

	r := initIn(t, dir, opts{}, map[string]any{"no-migrate": true})
	r.ok(t)

	if readFile(t, filepath.Join(dir, "go.mod")) != rootGoMod || r.goes.count("get", "-tool") != 0 {
		t.Fatal("--no-migrate touched the root module")
	}

	if !strings.Contains(r.out, "Sync complete") {
		t.Fatalf("sync did not run: %q", r.out)
	}
}

// The root go.mod is committed input: a tool path go would read as a flag must
// stop init before it installs or strips anything.
func TestInitRefusesAnInvalidToolInRootGoMod(t *testing.T) {
	for _, tool := range []string{"-x", "example.com/x/", "../y"} {
		t.Run(tool, func(t *testing.T) {
			dir := t.TempDir()
			bad := "module example.com/test\n\ngo 1.24\n\ntool " + tool + "\n"
			writeFile(t, filepath.Join(dir, "go.mod"), bad)

			r := initIn(t, dir, opts{}, nil)
			if r.code() != ierrors.Environment || !strings.Contains(r.runErr.Error(), "go.mod: invalid tool ") {
				t.Fatalf("err = %v", r.runErr)
			}

			if readFile(t, filepath.Join(dir, "go.mod")) != bad || r.goes.count("get", "-tool") != 0 || exists(filepath.Join(dir, config.ManifestFile)) {
				t.Fatal("init acted on a go.mod it should have refused")
			}
		})
	}
}

func TestInitDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), rootGoMod)

	r := initIn(t, dir, opts{}, map[string]any{"dry-run": true})
	r.ok(t)

	for _, want := range []string{
		"[dry-run] Would write .gotools.json (strategy=split, dir=tools)",
		"[dry-run] Would install stringer",
		"[dry-run] Would strip tool directives from go.mod and run 'go mod tidy'.",
	} {
		if !strings.Contains(r.out, want) {
			t.Fatalf("missing %q in %q", want, r.out)
		}
	}

	if got := files(t, dir); !slices.Equal(got, []string{"go.mod"}) || readFile(t, filepath.Join(dir, "go.mod")) != rootGoMod {
		t.Fatalf("dry run wrote files: %v", got)
	}

	if exists(filepath.Join(dir, "tools")) {
		t.Fatal("dry run created the tools directory, so it took the lock")
	}

	if r.goes.count("get") != 0 || r.goes.rootCalls(dir, "mod", "tidy") != 0 {
		t.Fatal("dry run ran go")
	}

	if !strings.Contains(r.out, "[dry-run] Would sync (strategy=split") {
		t.Fatalf("the sync did not run as a dry run: %q", r.out)
	}
}

// A plain init on a configured project used to reset it to the defaults.
func TestInitRerunKeepsConfigurationAndTools(t *testing.T) {
	dir := t.TempDir()

	initIn(t, dir, opts{}, map[string]any{"strategy": "module", "dir": ".t", "prefix": "example.com/p"}).ok(t)

	m := readManifest(t, dir)
	m.SetTool(manifest.Tool{Name: "x", Source: "go", Package: "example.com/a/x", Version: "v1.0.0"})

	if err := m.Flush(filepath.Join(dir, config.ManifestFile)); err != nil {
		t.Fatal(err)
	}

	r := initIn(t, dir, opts{}, nil)
	r.ok(t)

	m = readManifest(t, dir)
	if m.Strategy != "module" || m.Dir != ".t" || m.ModulePrefix != "example.com/p" {
		t.Fatalf("configuration reset: %+v", m)
	}

	if tl, ok := m.Tool("x"); !ok || tl.Version != "v1.0.0" {
		t.Fatalf("tool lost: %+v", tl)
	}

	if !strings.Contains(r.out, "Syncing (strategy=module, go=") || !strings.Contains(r.out, "dir=.t)") {
		t.Fatalf("the sync did not run under the resolved layout: %q", r.out)
	}
}

// An explicit flag still wins, and sync then moves the tools.
func TestInitFlagChangesStrategy(t *testing.T) {
	dir := t.TempDir()

	initIn(t, dir, opts{}, map[string]any{"strategy": "split"}).ok(t)

	r := initIn(t, dir, opts{}, map[string]any{"strategy": "unified"})
	r.ok(t)

	if readManifest(t, dir).Strategy != "unified" || !strings.Contains(r.out, "Syncing (strategy=unified") {
		t.Fatalf("manifest %+v, out %q", readManifest(t, dir), r.out)
	}
}

func TestInitRerunResumesAdoption(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), rootGoMod)

	failing := &fakeGo{fail: func(c gocmd.Call) string {
		if len(c.Args) > 0 && c.Args[0] == "get" && strings.Contains(strings.Join(c.Args, " "), "stringer") {
			return "boom"
		}

		return ""
	}}

	if r := initIn(t, dir, opts{goes: failing}, nil); r.runErr == nil {
		t.Fatal("expected failure")
	}

	if names := readManifest(t, dir).ToolNames(); !slices.Equal(names, []string{"addlicense"}) {
		t.Fatalf("manifest tools = %v", names)
	}

	r := initIn(t, dir, opts{}, nil)
	r.ok(t)

	if !strings.Contains(r.out, "addlicense — already managed, skipping") {
		t.Fatalf("out = %q", r.out)
	}

	if names := readManifest(t, dir).ToolNames(); !slices.Equal(names, []string{"addlicense", "stringer"}) {
		t.Fatalf("manifest tools = %v", names)
	}
}

func TestInitRefusesCollidingToolNames(t *testing.T) {
	const twin = `module example.com/test

go 1.24

tool (
	example.com/a/foo
	example.com/b/foo
)

require (
	example.com/a v1.0.0
	example.com/b v1.0.0
)
`

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), twin)

	r := initIn(t, dir, opts{}, nil)
	if r.code() != ierrors.Usage || !strings.Contains(r.runErr.Error(), "'foo'") {
		t.Fatalf("err = %v", r.runErr)
	}

	if readFile(t, filepath.Join(dir, "go.mod")) != twin || exists(filepath.Join(dir, config.ManifestFile)) || r.goes.count("get") != 0 {
		t.Fatal("something was written")
	}

	// The same clash with an already managed tool.
	dir = t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), twin[:strings.Index(twin, "tool (")]+"tool example.com/b/foo\n\nrequire example.com/b v1.0.0\n")

	m := manifest.New()
	m.Strategy, m.ModulePrefix = "split", "tools"
	m.SetTool(manifest.Tool{Name: "foo", Source: "go", Package: "example.com/a/foo", Version: "v1.0.0"})

	if err := m.Flush(filepath.Join(dir, config.ManifestFile)); err != nil {
		t.Fatal(err)
	}

	if r := initIn(t, dir, opts{}, nil); r.code() != ierrors.Usage {
		t.Fatalf("managed clash: %v", r.runErr)
	}
}

func TestInitUsageErrors(t *testing.T) {
	for name, c := range map[string]struct {
		flags map[string]any
		args  []string
	}{
		"strategy":   {flags: map[string]any{"strategy": "flat"}},
		"dir up":     {flags: map[string]any{"dir": "../x"}},
		"dir root":   {flags: map[string]any{"dir": "./"}},
		"dir abs":    {flags: map[string]any{"dir": "/tmp/x"}},
		"go":         {flags: map[string]any{"go": "banana"}},
		"prefix":     {flags: map[string]any{"prefix": "a b"}},
		"positional": {args: []string{"split"}},
	} {
		dir := t.TempDir()

		r := initIn(t, dir, opts{args: c.args}, c.flags)
		if r.code() != ierrors.Usage {
			t.Fatalf("%s: err = %v", name, r.runErr)
		}

		if exists(filepath.Join(dir, config.ManifestFile)) || len(r.goes.calls) != 0 {
			t.Fatalf("%s: acted on a bad request", name)
		}
	}
}

func TestInitPositionalArgumentShowsTheUsage(t *testing.T) {
	r := initIn(t, t.TempDir(), opts{args: []string{"split"}}, nil)

	if want := "Unknown argument: split\n   Usage: "; r.runErr == nil || !strings.HasPrefix(r.runErr.Error(), want) || !strings.HasSuffix(r.runErr.Error(), "init [options]") {
		t.Fatalf("err = %v, want %q...", r.runErr, want)
	}
}

func TestInitRefusesASymlinkedToolsDirectory(t *testing.T) {
	dir := t.TempDir()

	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	r := initIn(t, dir, opts{}, map[string]any{"dir": "linked"})
	if r.code() != ierrors.Environment || !strings.Contains(r.runErr.Error(), "symlink") {
		t.Fatalf("err = %v", r.runErr)
	}

	if exists(filepath.Join(dir, config.ManifestFile)) {
		t.Fatal("manifest written")
	}
}

func TestInitAcceptsAutoPrefixAndInheritGo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.24\n")

	initIn(t, dir, opts{}, map[string]any{"prefix": "custom/p"}).ok(t)

	if readManifest(t, dir).ModulePrefix != "custom/p" {
		t.Fatal("prefix not written")
	}

	initIn(t, dir, opts{}, map[string]any{"prefix": "auto", "go": "inherit"}).ok(t)

	if m := readManifest(t, dir); m.ModulePrefix != "example.com/test" {
		t.Fatalf("prefix = %q", m.ModulePrefix)
	}
}

// The environment steers one run but is never written to the file.
func TestInitDoesNotPersistEnvironment(t *testing.T) {
	dir := t.TempDir()

	r := initIn(t, dir, opts{environ: []string{"GOTOOLS_STRATEGY=module", "GOTOOLS_DIR=envdir"}}, map[string]any{"dir": ".t"})
	r.ok(t)

	m := readManifest(t, dir)
	if m.Strategy != "split" || m.Dir != ".t" {
		t.Fatalf("manifest = %+v", m)
	}

	if !strings.Contains(r.out, "Syncing (strategy=module, go=") || !strings.Contains(r.out, "dir=.t)") {
		t.Fatalf("the environment did not steer the run: %q", r.out)
	}
}

func TestInitOfflineRefusesAdoptionBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), rootGoMod)

	r := initIn(t, dir, opts{environ: []string{"GOTOOLS_OFFLINE=1"}}, nil)
	if r.code() != ierrors.Offline || !strings.Contains(r.runErr.Error(), "cannot adopt 2 tool(s)") {
		t.Fatalf("err = %v", r.runErr)
	}

	if got := files(t, dir); !slices.Equal(got, []string{"go.mod"}) {
		t.Fatalf("files written: %v", got)
	}
}

func TestInitNeedsGo(t *testing.T) {
	g := &fakeGo{fail: func(gocmd.Call) string { return "" }}
	dir := t.TempDir()

	missing := runnerFunc(func(context.Context, gocmd.Call) (gocmd.Result, error) { return gocmd.Result{}, gocmd.ErrNotFound })

	var errb bytes.Buffer

	c := &command.Context{Stdout: &bytes.Buffer{}, Stderr: &errb, Program: "gotools", WorkDir: dir, Go: missing}

	err := run(command.NewInit(), c)
	if ierrors.CodeOf(err) != ierrors.Environment || !strings.Contains(err.Error(), "Go is not installed") {
		t.Fatalf("err = %v", err)
	}

	if exists(filepath.Join(dir, config.ManifestFile)) || len(g.calls) != 0 {
		t.Fatal("wrote without go")
	}
}

type runnerFunc func(context.Context, gocmd.Call) (gocmd.Result, error)

func (f runnerFunc) Run(ctx context.Context, c gocmd.Call) (gocmd.Result, error) { return f(ctx, c) }

// A failing sync fails init, and the manifest is already written, so a rerun
// resumes.
func TestInitSyncFailureIsReported(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "tools", "x.mod"), "module tools/x\n\ngo 1.24\n")

	g := &fakeGo{fail: func(c gocmd.Call) string {
		if len(c.Args) > 1 && c.Args[1] == "tidy" {
			return "dial tcp: i/o timeout"
		}

		return ""
	}}

	r := initIn(t, dir, opts{goes: g}, nil)
	if r.code() != ierrors.Network || !exists(filepath.Join(dir, config.ManifestFile)) {
		t.Fatalf("err = %v", r.runErr)
	}
}

func TestInitWarnsAboutSkippedManifestEntries(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, config.ManifestFile), `{"strategy":"split","tools":{"../evil":{"source":"go","package":"p","version":"v1"}}}`)

	r := initIn(t, dir, opts{}, nil)
	r.ok(t)

	if !strings.Contains(r.err, "Ignoring tool entry with unsafe name: ../evil") {
		t.Fatalf("stderr = %q", r.err)
	}
}

func TestInitRejectsAMalformedManifest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, config.ManifestFile), `{"strategy":`)

	r := initIn(t, dir, opts{}, nil)
	if r.code() != ierrors.Environment || !errors.Is(r.runErr, manifest.ErrMalformed) {
		t.Fatalf("err = %v", r.runErr)
	}
}

func TestInitFindsTheProjectRootFromASubdirectory(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")

	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	initIn(t, root, opts{}, map[string]any{"strategy": "module"}).ok(t)

	r := initIn(t, sub, opts{}, nil)
	r.ok(t)

	if r.ctx.Root != root || readManifest(t, root).Strategy != "module" || exists(filepath.Join(sub, config.ManifestFile)) {
		t.Fatalf("root = %q", r.ctx.Root)
	}
}

// --prefix=auto detects the prefix again, so tools adopted in the same run get
// module paths under the root go.mod's module, not under the old prefix.
func TestInitAutoPrefixIsUsedForAdoptedTools(t *testing.T) {
	dir := t.TempDir()

	m := manifest.New()
	m.Strategy, m.ModulePrefix = "split", "old/prefix"

	if err := m.Flush(filepath.Join(dir, config.ManifestFile)); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(dir, "go.mod"), rootGoMod)

	initIn(t, dir, opts{}, map[string]any{"prefix": "auto"}).ok(t)

	if got := readFile(t, filepath.Join(dir, "tools", "addlicense.mod")); !strings.HasPrefix(got, "module example.com/test/tools/addlicense\n") {
		t.Fatalf("modfile:\n%s", got)
	}

	if readManifest(t, dir).ModulePrefix != "example.com/test" {
		t.Fatalf("manifest prefix %q", readManifest(t, dir).ModulePrefix)
	}
}

// The lock lives in the tools directory, so init's lock excludes every other
// gotools process working on the same tools.
func TestInitTakesTheLockInTheToolsDirectory(t *testing.T) {
	dir := t.TempDir()

	initIn(t, dir, opts{}, map[string]any{"dir": ".t"}).ok(t)

	if !exists(filepath.Join(dir, ".t", ".gotools.flock")) {
		t.Fatalf("no lock file in the tools directory: %v", files(t, dir))
	}
}

func TestInitWaitsForNoOneWhenTheLockIsHeld(t *testing.T) {
	dir := t.TempDir()

	held := make(chan struct{})
	release := make(chan struct{})

	go func() {
		c := &command.Context{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Program: "gotools", WorkDir: dir, Go: &fakeGo{}}
		if err := c.Load(context.Background(), config.WithOverrides(config.Overrides{})); err != nil {
			t.Error(err)

			return
		}

		_ = c.WithLock(context.Background(), func(context.Context) error {
			close(held)
			<-release

			return nil
		})
	}()

	<-held

	defer close(release)

	r := initIn(t, dir, opts{environ: []string{"GOTOOLS_LOCK_TIMEOUT=0"}}, nil)
	if r.code() != ierrors.Lock {
		t.Fatalf("err = %v", r.runErr)
	}
}

// Building the command again, for its help or to ask its name, must not change
// the run of a command that was built earlier.
func TestBuildingTwiceDoesNotShareARunsState(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()

	var outA, outB bytes.Buffer

	mk := func(dir string, out *bytes.Buffer) *command.Context {
		return &command.Context{
			Stdin: strings.NewReader(""), Stdout: out, Stderr: &bytes.Buffer{},
			Program: "gotools", WorkDir: dir, Go: &fakeGo{},
		}
	}

	cmd := command.NewInit()
	built := cmd.Build(mk(dirA, &outA))

	_ = cmd.Build(mk(dirB, &outB)) // built again before the first one runs

	if err := built.Run(context.Background(), []string{"init"}); err != nil {
		t.Fatal(err)
	}

	if !exists(filepath.Join(dirA, config.ManifestFile)) || exists(filepath.Join(dirB, config.ManifestFile)) || outB.Len() != 0 {
		t.Fatalf("the run used the wrong Context: A=%v B=%v out=%q", files(t, dirA), files(t, dirB), outB.String())
	}
}
