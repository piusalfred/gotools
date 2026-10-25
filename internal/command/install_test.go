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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/piusalfred/gotools/internal/command"
	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/manifest"
	"github.com/piusalfred/gotools/internal/tools"
)

const imports = "golang.org/x/tools/cmd/goimports"

// installOpts shapes the fake go and the terminal of one install.
type installOpts struct {
	getFail     string // output of a failing `go get`, exit 1
	touchBad    bool   // `go get` writes the modfile, then fails
	toolFail    string // stderr of a failing `go tool -n`
	getErr      error  // `go get` cannot run
	sumdb       string // what `go env GOSUMDB GONOSUMDB GOINSECURE` prints
	environ     []string
	stdin       string
	interactive bool
}

func installGo(o installOpts) *fakeGo {
	return &fakeGo{intercept: func(c gocmd.Call) (gocmd.Result, error, bool) {
		switch {
		case len(c.Args) > 1 && c.Args[0] == "env" && c.Args[1] == "GOSUMDB":
			if o.sumdb != "" {
				return gocmd.Result{Stdout: []byte(o.sumdb)}, nil, true
			}

			return gocmd.Result{Stdout: []byte("sum.golang.org\n\n\n")}, nil, true
		case len(c.Args) > 2 && c.Args[0] == "get" && o.getErr != nil:
			return gocmd.Result{}, o.getErr, true
		case len(c.Args) > 2 && c.Args[0] == "get" && o.getFail != "":
			if o.touchBad {
				_, _ = installing(c)
			}

			return gocmd.Result{Code: 1, Stderr: []byte(o.getFail)}, nil, true
		case len(c.Args) > 1 && c.Args[0] == "tool" && c.Args[1] == "-n" && o.toolFail != "":
			return gocmd.Result{Code: 1, Stderr: []byte(o.toolFail)}, nil, true
		}

		return gocmd.Result{}, nil, false
	}}
}

type installOutcome struct {
	out, err string
	runErr   error
	goes     *fakeGo
}

func (r installOutcome) code() ierrors.Code { return ierrors.CodeOf(r.runErr) }

func (r installOutcome) ok(t *testing.T) {
	t.Helper()

	if r.runErr != nil {
		t.Fatalf("err=%v out=%q stderr=%q", r.runErr, r.out, r.err)
	}
}

func (r installOutcome) ran(args ...string) bool { return r.goes.count(args...) > 0 }

func installContext(dir string, g *fakeGo, o installOpts) (*command.Context, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer

	return &command.Context{
		Stdin: strings.NewReader(o.stdin), Stdout: &out, Stderr: &errb,
		Environ: o.environ, Program: "gotools", WorkDir: dir, Go: g, Interactive: o.interactive,
	}, &out, &errb
}

// installIn runs `install args` in dir through urfave/cli.
func installIn(t *testing.T, g *fakeGo, dir string, o installOpts, args ...string) installOutcome {
	t.Helper()

	c, out, errb := installContext(dir, g, o)
	err := run(command.NewInstall(), c, args...)

	return installOutcome{out: out.String(), err: errb.String(), runErr: err, goes: g}
}

// pipeIn runs install's pipe mode on data.
func pipeIn(t *testing.T, g *fakeGo, dir string, o installOpts, data string) installOutcome {
	t.Helper()

	c, out, errb := installContext(dir, g, o)
	err := command.NewInstall().Pipe(context.Background(), c, []byte(data))

	return installOutcome{out: out.String(), err: errb.String(), runErr: err, goes: g}
}

func newProject(t *testing.T, strategy string) string {
	t.Helper()

	dir := t.TempDir()
	m := manifest.New()
	m.Strategy, m.ModulePrefix = strategy, "tools"

	if err := m.Flush(filepath.Join(dir, config.ManifestFile)); err != nil {
		t.Fatal(err)
	}

	// The split strategy needs a module in the project root.
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.24\n")

	return dir
}

func manifestTools(t *testing.T, dir string) []string { return readManifest(t, dir).ToolNames() }

func TestInstallEachStrategy(t *testing.T) {
	for st, path := range map[string]string{
		"split":   "tools/goimports.mod",
		"module":  "tools/goimports/go.mod",
		"unified": "tools/go.mod",
	} {
		t.Run(st, func(t *testing.T) {
			dir := newProject(t, st)

			r := installIn(t, installGo(installOpts{}), dir, installOpts{}, imports+"@v0.30.0")
			r.ok(t)

			if !strings.Contains(r.out, "✅ Installed goimports") {
				t.Fatalf("out=%q", r.out)
			}

			mod := readFile(t, filepath.Join(dir, path))
			if !strings.Contains(mod, "module tools") || !strings.Contains(mod, "tool "+imports) {
				t.Fatalf("modfile:\n%s", mod)
			}

			tl, ok := readManifest(t, dir).Tool("goimports")
			if !ok || tl.Version != "v0.30.0" || tl.Package != imports {
				t.Fatalf("tool=%+v", tl)
			}
		})
	}
}

func TestInstallAliasAndLatestHint(t *testing.T) {
	dir := newProject(t, "unified")

	r := installIn(t, installGo(installOpts{}), dir, installOpts{}, "gi", imports+"@latest")
	r.ok(t)

	if !strings.Contains(r.out, "Installed at @latest (resolved: v1.2.3)") || !strings.Contains(r.out, "gotools install gi "+imports+"@v1.2.3") {
		t.Fatalf("out=%q", r.out)
	}

	if !slices.Contains(manifestTools(t, dir), "gi") {
		t.Fatal("alias not in manifest")
	}
}

func TestInstallUsageErrors(t *testing.T) {
	dir := newProject(t, "split")

	for name, args := range map[string][]string{
		"none":        {},
		"too many":    {"a", imports, "extra"},
		"flag as pkg": {"--exec=evil"},
		"flag 2nd":    {"x", "-u"},
		"bad path":    {"not a path"},
		"empty path":  {"@v1"},
		"bad name":    {"../x", imports},
		"after --":    {"--", "-x"},
	} {
		r := installIn(t, installGo(installOpts{}), dir, installOpts{}, args...)
		if r.code() != ierrors.Usage {
			t.Errorf("%s: err = %v", name, r.runErr)
		}

		if r.ran("get") {
			t.Errorf("%s: go get ran", name)
		}
	}
}

func TestInstallUsageMessages(t *testing.T) {
	dir := newProject(t, "split")

	r := installIn(t, installGo(installOpts{}), dir, installOpts{})
	if r.code() != ierrors.Usage || !strings.Contains(r.runErr.Error(), "Usage: gotools install [name] <pkg>") {
		t.Fatalf("err = %v", r.runErr)
	}

	r = installIn(t, installGo(installOpts{}), dir, installOpts{}, "a", imports, "extra")
	if !strings.Contains(r.runErr.Error(), "Unknown argument: extra") {
		t.Fatalf("err = %v", r.runErr)
	}
}

func TestInstallOffline(t *testing.T) {
	dir := newProject(t, "split")

	for name, o := range map[string]struct {
		args []string
		env  []string
	}{
		"flag":    {args: []string{"--offline", imports}},
		"setting": {args: []string{imports}, env: []string{"GOTOOLS_OFFLINE=1"}},
	} {
		r := installIn(t, installGo(installOpts{}), dir, installOpts{environ: o.env}, o.args...)
		if r.code() != ierrors.Offline {
			t.Errorf("%s: err = %v", name, r.runErr)
		}

		if r.ran("get") {
			t.Errorf("%s: installed offline", name)
		}
	}
}

func TestInstallDuplicateAndForce(t *testing.T) {
	dir := newProject(t, "split")
	g := installGo(installOpts{})

	installIn(t, g, dir, installOpts{}, imports+"@v0.1.0").ok(t)

	r := installIn(t, g, dir, installOpts{}, imports+"@v0.2.0")
	if r.code() != ierrors.Usage || !strings.Contains(r.runErr.Error(), "--force") {
		t.Fatalf("err = %v", r.runErr)
	}

	installIn(t, g, dir, installOpts{}, "--force", imports+"@v0.2.0").ok(t)

	if tl, _ := readManifest(t, dir).Tool("goimports"); tl.Version != "v0.2.0" {
		t.Fatalf("version=%s", tl.Version)
	}
}

func TestInstallForceDropsStaleDirective(t *testing.T) {
	dir := newProject(t, "split")
	g := installGo(installOpts{})

	installIn(t, g, dir, installOpts{}, "x", "example.com/a/old").ok(t)
	installIn(t, g, dir, installOpts{}, "--force", "x", "example.com/b/new").ok(t)

	mod := readFile(t, filepath.Join(dir, "tools/x.mod"))
	if strings.Contains(mod, "tool example.com/a/old") || !strings.Contains(mod, "tool example.com/b/new") {
		t.Fatalf("modfile:\n%s", mod)
	}
}

func TestInstallSamePackageChoices(t *testing.T) {
	prep := func(t *testing.T) (string, *fakeGo) {
		t.Helper()

		dir := newProject(t, "split")
		g := installGo(installOpts{})

		installIn(t, g, dir, installOpts{}, "goimports", imports+"@v0.1.0").ok(t)

		return dir, g
	}

	t.Run("non-interactive installs another and hints", func(t *testing.T) {
		dir, g := prep(t)

		r := installIn(t, g, dir, installOpts{}, "gi", imports+"@v0.2.0")
		r.ok(t)

		if !strings.Contains(r.out, "already installed as 'goimports'") || !strings.Contains(r.out, "gotools rename goimports gi") {
			t.Fatalf("out=%q", r.out)
		}

		if got := manifestTools(t, dir); !slices.Equal(got, []string{"gi", "goimports"}) {
			t.Fatalf("tools=%v", got)
		}
	})

	for choice, want := range map[string][]string{
		"\n":  {"gi", "goimports"}, // default: install
		"1\n": {"gi", "goimports"},
		"2\n": {"goimports"}, // upgrade the managed entry
		"3\n": {"gi"},        // rename
		"4\n": {"goimports"}, // skip
	} {
		t.Run("choice "+strings.TrimSpace(choice), func(t *testing.T) {
			dir, g := prep(t)

			r := installIn(t, g, dir, installOpts{stdin: choice, interactive: true}, "gi", imports+"@v0.2.0")
			r.ok(t)

			if got := manifestTools(t, dir); !slices.Equal(got, want) {
				t.Fatalf("tools=%v want %v", got, want)
			}

			if choice == "2\n" {
				if tl, _ := readManifest(t, dir).Tool("goimports"); tl.Version != "v0.2.0" {
					t.Fatalf("not upgraded: %+v", tl)
				}
			}
		})
	}
}

// A rename the user chooses keeps the sync fingerprint current, so the next
// sync takes its fast path.
func TestInstallRenameChoiceRecordsTheFingerprint(t *testing.T) {
	dir := newProject(t, "split")
	g := installGo(installOpts{})

	installIn(t, g, dir, installOpts{}, "goimports", imports+"@v0.1.0").ok(t)
	installIn(t, g, dir, installOpts{stdin: "3\n", interactive: true}, "gi", imports+"@v0.2.0").ok(t)

	if !exists(filepath.Join(dir, "tools", ".gotools.fingerprint")) {
		t.Fatalf("no fingerprint after a rename: %v", files(t, dir))
	}
}

// A second name for a package already managed at the same version is refused,
// and the rename that would settle it is suggested.
func TestInstallRefusesASecondNameAtTheSameVersion(t *testing.T) {
	dir := newProject(t, "split")
	g := installGo(installOpts{})

	installIn(t, g, dir, installOpts{}, "goimports", imports+"@v0.1.0").ok(t)

	r := installIn(t, g, dir, installOpts{}, "gi", imports+"@v0.1.0")
	if r.code() != ierrors.Usage || !strings.Contains(r.runErr.Error(), "gotools rename goimports gi") {
		t.Fatalf("err = %v", r.runErr)
	}

	if got := manifestTools(t, dir); !slices.Equal(got, []string{"goimports"}) || exists(filepath.Join(dir, "tools/gi.mod")) {
		t.Fatalf("the refused twin left something behind: %v", got)
	}
}

func TestInstallFailureClassAndRollback(t *testing.T) {
	for name, tc := range map[string]struct {
		fail string
		want ierrors.Code
	}{
		"network": {"dial tcp: lookup proxy: no such host", ierrors.Network},
		"version": {"invalid module version syntax", ierrors.Usage},
		"generic": {"something else broke", ierrors.Generic},
	} {
		t.Run(name, func(t *testing.T) {
			dir := newProject(t, "module")

			r := installIn(t, installGo(installOpts{getFail: tc.fail, touchBad: true}), dir, installOpts{}, imports)
			if r.code() != tc.want || !strings.Contains(r.runErr.Error(), tc.fail) {
				t.Fatalf("err = %v", r.runErr)
			}

			if exists(filepath.Join(dir, "tools/goimports")) {
				t.Fatal("module directory left behind")
			}

			if len(manifestTools(t, dir)) != 0 {
				t.Fatal("manifest changed")
			}
		})
	}
}

func TestInstallFailedForceRestoresModfile(t *testing.T) {
	dir := newProject(t, "split")

	installIn(t, installGo(installOpts{}), dir, installOpts{}, imports+"@v0.1.0").ok(t)

	before := readFile(t, filepath.Join(dir, "tools/goimports.mod"))

	r := installIn(t, installGo(installOpts{getFail: "boom", touchBad: true}), dir, installOpts{}, "--force", imports+"@v0.9.0")
	if r.code() != ierrors.Generic {
		t.Fatalf("err = %v", r.runErr)
	}

	if after := readFile(t, filepath.Join(dir, "tools/goimports.mod")); after != before {
		t.Fatalf("modfile changed:\n%s\n---\n%s", before, after)
	}
}

func TestInstallTimeout(t *testing.T) {
	dir := newProject(t, "split")

	r := installIn(t, installGo(installOpts{getErr: context.DeadlineExceeded}), dir, installOpts{}, imports)
	if r.code() != ierrors.Network || !strings.Contains(r.runErr.Error(), "GOTOOLS_OPERATION_TIMEOUT") {
		t.Fatalf("err = %v", r.runErr)
	}

	if exists(filepath.Join(dir, "tools/goimports.mod")) {
		t.Fatal("modfile left behind")
	}
}

func TestInstallVerifyFailureRollsBack(t *testing.T) {
	dir := newProject(t, "split")

	r := installIn(t, installGo(installOpts{toolFail: "build failed"}), dir, installOpts{}, imports)
	if r.code() != ierrors.Environment || !strings.Contains(r.runErr.Error(), "failed to build") {
		t.Fatalf("err = %v", r.runErr)
	}

	if exists(filepath.Join(dir, "tools/goimports.mod")) || len(manifestTools(t, dir)) != 0 {
		t.Fatal("install not rolled back")
	}

	r = installIn(t, installGo(installOpts{toolFail: "go: no such tool \"x\""}), dir, installOpts{}, imports)
	if r.code() != ierrors.Environment || !strings.Contains(r.runErr.Error(), "not runnable") {
		t.Fatalf("err = %v", r.runErr)
	}
}

// install builds the tool to check it; it never runs what it just downloaded.
func TestInstallNeverRunsTheTool(t *testing.T) {
	dir := newProject(t, "split")
	g := installGo(installOpts{})

	installIn(t, g, dir, installOpts{}, imports).ok(t)

	for _, c := range g.calls {
		if len(c.Args) > 1 && c.Args[0] == "tool" && c.Args[1] != "-n" {
			t.Fatalf("go tool ran the binary: %v", c.Args)
		}
	}
}

func TestInstallRejectsASymlinkedModfile(t *testing.T) {
	dir := newProject(t, "split")
	outside := filepath.Join(t.TempDir(), "target.mod")

	writeFile(t, outside, "module x\n")

	if err := os.MkdirAll(filepath.Join(dir, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(dir, "tools/goimports.mod")); err != nil {
		t.Skip(err)
	}

	if r := installIn(t, installGo(installOpts{}), dir, installOpts{}, imports); r.runErr == nil {
		t.Fatal("symlinked modfile accepted")
	}

	if readFile(t, outside) != "module x\n" {
		t.Fatal("symlink target modified")
	}
}

func TestInstallLockHeld(t *testing.T) {
	dir := newProject(t, "split")

	holder := &command.Context{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Program: "gotools", WorkDir: dir, Go: installGo(installOpts{})}
	if err := holder.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	err := holder.WithLock(context.Background(), func(context.Context) error {
		r := installIn(t, installGo(installOpts{}), dir, installOpts{environ: []string{"GOTOOLS_LOCK_TIMEOUT=0"}}, imports)
		if r.code() != ierrors.Lock {
			t.Errorf("err = %v", r.runErr)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// What weakens Go's checksum verification is reported on stderr and never
// stops an install.
func TestInstallWarnsWhenVerificationIsWeakened(t *testing.T) {
	dir := newProject(t, "split")

	r := installIn(t, installGo(installOpts{sumdb: "off\n\n\n"}), dir, installOpts{}, imports)
	r.ok(t)

	if !strings.Contains(r.err, "GOSUMDB=off") {
		t.Fatalf("stderr = %q", r.err)
	}
}

func TestInstallLoadsTheManifestWarnings(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.24\n")
	writeFile(t, filepath.Join(dir, config.ManifestFile), `{"strategy":"split","tools":{"../evil":{"source":"go","package":"p","version":"v1"}}}`)

	r := installIn(t, installGo(installOpts{}), dir, installOpts{}, imports)
	r.ok(t)

	if !strings.Contains(r.err, "Ignoring tool entry with unsafe name: ../evil") {
		t.Fatalf("stderr = %q", r.err)
	}
}

func TestInstallRejectsAMalformedManifest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, config.ManifestFile), `{"strategy":`)

	r := installIn(t, installGo(installOpts{}), dir, installOpts{}, imports)
	if r.code() != ierrors.Environment || !errors.Is(r.runErr, manifest.ErrMalformed) {
		t.Fatalf("err = %v", r.runErr)
	}
}

func TestInstallNeedsGo(t *testing.T) {
	dir := newProject(t, "split")

	missing := runnerFunc(func(context.Context, gocmd.Call) (gocmd.Result, error) { return gocmd.Result{}, gocmd.ErrNotFound })

	c := &command.Context{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Program: "gotools", WorkDir: dir, Go: missing}

	err := run(command.NewInstall(), c, imports)
	if ierrors.CodeOf(err) != ierrors.Environment || !strings.Contains(err.Error(), "Go is not installed") {
		t.Fatalf("err = %v", err)
	}
}

func TestInstallTakesTheLockInTheToolsDirectory(t *testing.T) {
	dir := newProject(t, "split")

	installIn(t, installGo(installOpts{}), dir, installOpts{}, imports).ok(t)

	if !exists(filepath.Join(dir, "tools", ".gotools.flock")) {
		t.Fatalf("no lock file in the tools directory: %v", files(t, dir))
	}
}

func TestInstallTimeoutSettingIsHonoured(t *testing.T) {
	dir := newProject(t, "split")

	slow := &fakeGo{intercept: func(c gocmd.Call) (gocmd.Result, error, bool) {
		if len(c.Args) > 0 && c.Args[0] == "get" {
			time.Sleep(200 * time.Millisecond)

			return gocmd.Result{}, context.DeadlineExceeded, true
		}

		return gocmd.Result{}, nil, false
	}}

	r := installIn(t, slow, dir, installOpts{environ: []string{"GOTOOLS_OPERATION_TIMEOUT=0"}}, imports)
	if r.code() == ierrors.Network {
		t.Fatalf("a disabled bound still reported a timeout: %v", r.runErr)
	}
}

func TestPipeMode(t *testing.T) {
	dir := newProject(t, "split")

	data := "# tools\n\ngo install " + imports + "@v0.30.0\nmylint example.com/a/lint@v1.0.0\n  example.com/b/other  \n" + imports + "@v0.30.0\n"

	pipeIn(t, installGo(installOpts{}), dir, installOpts{}, data).ok(t)

	if got := manifestTools(t, dir); !slices.Equal(got, []string{"goimports", "mylint", "other"}) {
		t.Fatalf("tools = %v", got)
	}
}

// One bad line stops the run before anything is installed.
func TestPipeModeChecksEveryLineFirst(t *testing.T) {
	dir := newProject(t, "split")

	for name, data := range map[string]string{
		"flag":      imports + "\n-x\n",
		"bad path":  imports + "\nnot a path\n",
		"too many":  imports + "\na b c\n",
		"conflict":  "x example.com/a/one\nx example.com/b/two\n",
		"empty":     "\n# nothing\n",
		"bad name":  "../x " + imports + "\n",
		"go prefix": "go install\n",
	} {
		g := installGo(installOpts{})

		r := pipeIn(t, g, dir, installOpts{}, data)
		if r.code() != ierrors.Usage {
			t.Errorf("%s: err = %v", name, r.runErr)
		}

		if r.ran("get") || len(manifestTools(t, dir)) != 0 {
			t.Errorf("%s: something was installed", name)
		}
	}
}

func TestPipeModeNamesTheBadLine(t *testing.T) {
	dir := newProject(t, "split")

	r := pipeIn(t, installGo(installOpts{}), dir, installOpts{}, imports+"\nnot a path\n")
	if !strings.Contains(r.runErr.Error(), `stdin line 2 ("not a path")`) {
		t.Fatalf("err = %v", r.runErr)
	}
}

func TestPipeModeHonoursOffline(t *testing.T) {
	dir := newProject(t, "split")

	r := pipeIn(t, installGo(installOpts{}), dir, installOpts{environ: []string{"GOTOOLS_OFFLINE=1"}}, imports+"\n")
	if r.code() != ierrors.Offline {
		t.Fatalf("err = %v", r.runErr)
	}
}

func TestInstallIsThePipedCommand(t *testing.T) {
	var piped command.Piped = command.NewInstall()

	if piped.Name() != command.NameInstall {
		t.Fatalf("name = %q", piped.Name())
	}

	n := 0

	for _, cmd := range command.All() {
		if _, ok := cmd.(command.Piped); ok {
			n++
		}
	}

	if n != 1 {
		t.Fatalf("%d commands take piped input, want exactly one", n)
	}
}

// Install is the Context's own method, so another command can install without
// going through the install command.
func TestContextInstall(t *testing.T) {
	dir := newProject(t, "split")
	g := installGo(installOpts{})

	c, out, _ := installContext(dir, g, installOpts{})
	if err := c.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	err := c.Install(context.Background(), command.InstallParams{Specs: []tools.AddOptions{
		{Name: "a", Pkg: "example.com/a/a@v1.0.0", RejectTwin: true},
		{Name: "b", Pkg: "example.com/b/b@v1.0.0", RejectTwin: true},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if got := manifestTools(t, dir); !slices.Equal(got, []string{"a", "b"}) || !strings.Contains(out.String(), "Installed b") {
		t.Fatalf("tools = %v, out = %q", got, out)
	}

	// Without Force a tool that is managed already is refused; with it, it is
	// installed over.
	again := command.InstallParams{Specs: []tools.AddOptions{{Name: "a", Pkg: "example.com/a/a@v1.1.0", RejectTwin: true}}}

	if err := c.Install(context.Background(), again); ierrors.CodeOf(err) != ierrors.Usage {
		t.Fatalf("err = %v", err)
	}

	again.Force = true

	if err := c.Install(context.Background(), again); err != nil {
		t.Fatal(err)
	}

	if tl, _ := c.Manifest.Tool("a"); tl.Version != "v1.1.0" {
		t.Fatalf("a = %+v", tl)
	}
}

// go cannot use -modfile outside a module, so the split strategy, which runs go
// from the project root, needs a go.mod there. Say so before go does.
func TestSplitNeedsARootGoMod(t *testing.T) {
	for _, st := range []string{"module", "unified"} {
		dir := newProject(t, st)
		if err := os.Remove(filepath.Join(dir, "go.mod")); err != nil {
			t.Fatal(err)
		}

		if r := installIn(t, installGo(installOpts{}), dir, installOpts{}, imports); r.runErr != nil {
			t.Errorf("%s without a root go.mod: %v", st, r.runErr)
		}
	}

	dir := newProject(t, "split")
	if err := os.Remove(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatal(err)
	}

	g := installGo(installOpts{})

	r := installIn(t, g, dir, installOpts{}, imports)
	if r.code() != ierrors.Environment || !strings.Contains(r.runErr.Error(), "The split strategy needs a go.mod in the project root") ||
		!strings.Contains(r.runErr.Error(), "gotools migrate module") {
		t.Fatalf("err = %v", r.runErr)
	}

	if r.ran("get") || exists(filepath.Join(dir, "tools")) {
		t.Fatal("install started before refusing")
	}

	// A project that has no tools has nothing to sync, so it is not refused.
	if r := syncIn(t, installGo(installOpts{}), dir, installOpts{}); r.runErr != nil {
		t.Fatalf("sync of an empty split project: %v", r.runErr)
	}

	// With tools to restore it is.
	m := readManifest(t, dir)
	m.SetTool(manifest.Tool{Name: "x", Source: "go", Package: "example.com/a/x", Version: "v1.0.0"})

	if err := m.Flush(filepath.Join(dir, config.ManifestFile)); err != nil {
		t.Fatal(err)
	}

	if r := syncIn(t, installGo(installOpts{}), dir, installOpts{}); r.code() != ierrors.Environment || !strings.Contains(r.runErr.Error(), "split strategy needs a go.mod") {
		t.Fatalf("sync with tools: %v", r.runErr)
	}
}
