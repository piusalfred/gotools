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

package cli_test

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	urfave "github.com/urfave/cli/v3"

	"github.com/piusalfred/gotools/internal/cli"
	"github.com/piusalfred/gotools/internal/command"
	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/manifest"
)

var update = flag.Bool("update", false, "rewrite the golden help files in testdata/help")

// goStub answers the calls init makes when there is nothing to adopt.
type goStub struct{}

func (goStub) Run(_ context.Context, c gocmd.Call) (gocmd.Result, error) {
	if len(c.Args) > 0 && c.Args[0] == "env" {
		return gocmd.Result{Stdout: []byte("go1.27.0\n")}, nil
	}

	return gocmd.Result{}, nil
}

type result struct {
	out, err string
	runErr   error
	app      *cli.App
}

func run(t *testing.T, dir string, args ...string) result {
	t.Helper()

	var out, errb bytes.Buffer

	app := &cli.App{
		Program: "gotools", Version: "v9.9.9",
		Stdout: &out, Stderr: &errb, Environ: []string{},
		WorkDir: dir, Go: goStub{},
	}

	err := app.Run(context.Background(), args)

	return result{out: out.String(), err: errb.String(), runErr: err, app: app}
}

func golden(t *testing.T, name, got string) {
	t.Helper()

	file := filepath.Join("testdata", "help", name+".txt")

	if *update {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}

		return
	}

	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%v (run `go test ./internal/cli -update` to create it)", err)
	}

	if got != string(want) {
		t.Fatalf("help differs from %s:\n--- got\n%s\n--- want\n%s", file, got, want)
	}
}

// Help is built from the commands' declarations, so it is checked against
// golden files: a change to a flag, a sentence or the layout is a diff to
// review. Run `go test ./internal/cli -update` to accept one.
func TestHelpGolden(t *testing.T) {
	cases := map[string][]string{"root": {"help"}, "completion": {"help", "completion"}}
	for _, c := range command.All() {
		cases[c.Name()] = []string{c.Name(), "--help"}
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			r := run(t, t.TempDir(), args...)
			if r.runErr != nil || r.err != "" {
				t.Fatalf("err=%v stderr=%q", r.runErr, r.err)
			}

			golden(t, name, r.out)
		})
	}
}

func TestHelpIsTheSameWhicheverWayItIsAsked(t *testing.T) {
	dir := t.TempDir()

	a := run(t, dir, "init", "--help").out
	b := run(t, dir, "help", "init").out

	if a == "" || a != b {
		t.Fatalf("init --help and help init differ:\n%s\n---\n%s", a, b)
	}
}

func TestRootHelpListsEveryCommandAndTheExitCodes(t *testing.T) {
	out := run(t, t.TempDir(), "help").out

	for _, c := range command.All() {
		uc := c.Build(&command.Context{})
		if !strings.Contains(out, uc.Name) || !strings.Contains(out, uc.Usage) {
			t.Errorf("overview lacks %s", uc.Name)
		}
	}

	for _, d := range ierrors.Table {
		if !strings.Contains(out, d.Name) {
			t.Errorf("overview lacks exit code %q", d.Name)
		}
	}
}

func TestRunsInitEndToEnd(t *testing.T) {
	dir := t.TempDir()

	r := run(t, dir, "init", "--strategy", "module", "--dir=.tools", "--prefix=example.com/p")
	if r.runErr != nil {
		t.Fatalf("err=%v stderr=%q", r.runErr, r.err)
	}

	if !strings.Contains(r.out, "Initialized .gotools.json (strategy=module, dir=.tools)") {
		t.Fatalf("out = %q", r.out)
	}

	res, err := manifest.Load(filepath.Join(dir, config.ManifestFile))
	if err != nil {
		t.Fatal(err)
	}

	m := res.Manifest
	if m.Strategy != "module" || m.Dir != ".tools" || m.ModulePrefix != "example.com/p" || m.GoVersion != "inherit" {
		t.Fatalf("manifest = %+v", m)
	}

	if !strings.Contains(r.out, "Syncing (strategy=module, go=1.27, dir=.tools)") {
		t.Fatalf("the sync did not run under the flags: %q", r.out)
	}
}

// A flag the user did not give is not the flag's default: running init again
// keeps what the manifest says.
func TestAFlagNotGivenDoesNotResetTheManifest(t *testing.T) {
	dir := t.TempDir()

	if r := run(t, dir, "init", "--strategy=unified"); r.runErr != nil {
		t.Fatal(r.runErr)
	}

	if r := run(t, dir, "init"); r.runErr != nil {
		t.Fatal(r.runErr)
	}

	res, _ := manifest.Load(filepath.Join(dir, config.ManifestFile))
	if res.Manifest.Strategy != "unified" {
		t.Fatalf("a plain init reset the strategy to %q", res.Manifest.Strategy)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()

	r := run(t, dir, "init", "--dry-run")
	if r.runErr != nil || !strings.Contains(r.out, "[dry-run] Would write .gotools.json") {
		t.Fatalf("err=%v out=%q", r.runErr, r.out)
	}

	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("dry run wrote %v", entries)
	}

	if !strings.Contains(r.out, "[dry-run] Would sync (strategy=split") {
		t.Fatalf("out = %q", r.out)
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	cases := map[string][]string{
		"unknown flag":   {"init", "--frobnicate"},
		"unknown short":  {"init", "-z"},
		"bad strategy":   {"init", "--strategy=flat"},
		"positional":     {"init", "split"},
		"missing value":  {"init", "--strategy"},
		"bad dir":        {"init", "--dir=../up"},
		"bad go version": {"init", "--go=banana"},
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()

			r := run(t, dir, args...)
			if ierrors.CodeOf(r.runErr) != ierrors.Usage {
				t.Fatalf("err = %v (code %d)", r.runErr, ierrors.CodeOf(r.runErr))
			}

			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Fatalf("a usage error wrote %v", entries)
			}
		})
	}
}

func TestUnknownFlagMessageNamesTheFlagAndTheUsage(t *testing.T) {
	r := run(t, t.TempDir(), "init", "--frobnicate")

	want := "Unknown argument: --frobnicate\n   Usage: gotools init [options]"
	if r.runErr == nil || r.runErr.Error() != want {
		t.Fatalf("err = %v, want %q", r.runErr, want)
	}
}

// urfave/cli would exit 3 for an unknown command, which gotools uses for
// network errors.
func TestUnknownCommandIsAUsageError(t *testing.T) {
	r := run(t, t.TempDir(), "frobnicate")

	if ierrors.CodeOf(r.runErr) != ierrors.Usage || !strings.Contains(r.runErr.Error(), "Unknown command: frobnicate") {
		t.Fatalf("err = %v", r.runErr)
	}
}

func TestReport(t *testing.T) {
	var errb bytes.Buffer

	app := &cli.App{Program: "gotools", Stderr: &errb}

	if code := app.Report(nil); code != 0 || errb.Len() != 0 {
		t.Fatalf("nil: code=%d out=%q", code, errb.String())
	}

	if code := app.Report(ierrors.New(ierrors.Lock, "busy")); code != 4 || errb.String() != "❌ busy\n" {
		t.Fatalf("lock: code=%d out=%q", code, errb.String())
	}

	errb.Reset()

	if code := app.Report(ierrors.Wrap(ierrors.Network, "", nil)); code != 3 || errb.Len() != 0 {
		t.Fatalf("empty message: code=%d out=%q", code, errb.String())
	}

	if code := app.Report(os.ErrNotExist); code != 1 {
		t.Fatalf("plain error: code=%d", code)
	}
}

// fake is a command a test declares: its steps are plain functions of the
// Context.
type fake struct {
	name    string
	aliases []string
	run     func(ctx context.Context, c *command.Context) error
	pipe    func(ctx context.Context, c *command.Context, data []byte) error
}

func (f fake) Name() string { return f.name }

func (f fake) Build(c *command.Context) *urfave.Command {
	return &urfave.Command{
		Name: f.name, Aliases: f.aliases, Usage: "Fake.",
		Action: func(ctx context.Context, _ *urfave.Command) error {
			if f.run == nil {
				return nil
			}

			return f.run(ctx, c)
		},
	}
}

type fakePiped struct{ fake }

func (f fakePiped) Pipe(ctx context.Context, c *command.Context, data []byte) error {
	return f.pipe(ctx, c, data)
}

func TestHas(t *testing.T) {
	app := &cli.App{Commands: []command.Command{fake{name: "init", aliases: []string{"i"}}}}

	for name, want := range map[string]bool{"init": true, "i": true, "sync": false, "": false} {
		if got := app.Has(name); got != want {
			t.Errorf("Has(%q) = %v", name, got)
		}
	}

	if !(&cli.App{}).Has("init") || !(&cli.App{}).Has("install") {
		t.Fatal("the default command set lacks init or install")
	}
}

func TestACommandSeesTheAppsContext(t *testing.T) {
	var got *command.Context

	app := &cli.App{
		Program: "gotools", WorkDir: "/work", Environ: []string{"A=1"}, Stdin: strings.NewReader(""),
		Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
		Commands: []command.Command{fake{name: "demo", run: func(_ context.Context, c *command.Context) error {
			got = c

			return nil
		}}},
	}

	if err := app.Run(context.Background(), []string{"demo"}); err != nil {
		t.Fatal(err)
	}

	if got.Program != "gotools" || got.WorkDir != "/work" || len(got.Environ) != 1 || got.Stdout == nil ||
		got.Interactive {
		t.Fatalf("context = %+v", got)
	}
}

// Data piped to a bare gotools goes to the command that takes it.
func TestPipedInputGoesToThePipedCommand(t *testing.T) {
	var got string

	piped := fakePiped{fake{name: "install", pipe: func(_ context.Context, _ *command.Context, data []byte) error {
		got = string(data)

		return nil
	}}}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	_, _ = w.WriteString("go install example.com/x@v1\n")
	_ = w.Close()

	app := &cli.App{
		Program:  "gotools",
		Stdin:    r,
		Stdout:   &bytes.Buffer{},
		Stderr:   &bytes.Buffer{},
		Commands: []command.Command{piped},
	}

	if err := app.Run(context.Background(), nil); err != nil || got != "go install example.com/x@v1\n" {
		t.Fatalf("err=%v got=%q", err, got)
	}
}

// With nothing piped, a bare gotools shows the overview and is a usage error.
func TestBareInvocationShowsTheOverview(t *testing.T) {
	var out, errb bytes.Buffer

	app := &cli.App{Program: "gotools", Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb}

	err := app.Run(context.Background(), nil)
	if ierrors.CodeOf(err) != ierrors.Usage || err.Error() != "" {
		t.Fatalf("err = %v", err)
	}

	if !strings.Contains(errb.String(), "Usage: gotools <command>") || out.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", out.String(), errb.String())
	}
}

func TestPipedInputWithNoPipedCommandShowsTheOverview(t *testing.T) {
	app := &cli.App{
		Program:  "gotools",
		Stdin:    strings.NewReader("pkg@v1\n"),
		Stdout:   &bytes.Buffer{},
		Stderr:   &bytes.Buffer{},
		Commands: []command.Command{fake{name: "demo"}},
	}

	if err := app.Run(context.Background(), nil); ierrors.CodeOf(err) != ierrors.Usage {
		t.Fatalf("err = %v", err)
	}
}

func TestACommandErrorKeepsItsExitCode(t *testing.T) {
	app := &cli.App{
		Program: "gotools", Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
		Commands: []command.Command{fake{name: "demo", run: func(context.Context, *command.Context) error {
			return ierrors.New(ierrors.Policy, "banned")
		}}},
	}

	if err := app.Run(context.Background(), []string{"demo"}); ierrors.CodeOf(err) != ierrors.Policy {
		t.Fatalf("err = %v", err)
	}
}

func TestRunsInstallEndToEnd(t *testing.T) {
	dir := t.TempDir()

	// The default strategy, split, needs a module in the project root.
	if err := os.WriteFile(
		filepath.Join(dir, "go.mod"),
		[]byte("module example.com/test\n\ngo 1.24\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	if r := run(t, dir, "init"); r.runErr != nil {
		t.Fatal(r.runErr)
	}

	var out, errb bytes.Buffer

	app := &cli.App{
		Program: "gotools", Stdout: &out, Stderr: &errb, Environ: []string{}, WorkDir: dir,
		Go: installingGo{},
	}

	if err := app.Run(context.Background(), []string{"install", "mylint", "example.com/a/lint@v1.0.0"}); err != nil {
		t.Fatalf("err=%v stderr=%q", err, errb.String())
	}

	if !strings.Contains(out.String(), "✅ Installed mylint") {
		t.Fatalf("out = %q", out.String())
	}

	res, _ := manifest.Load(filepath.Join(dir, config.ManifestFile))
	if tl, ok := res.Manifest.Tool("mylint"); !ok || tl.Package != "example.com/a/lint" || tl.Version != "v1.0.0" {
		t.Fatalf("manifest tools: %+v", res.Manifest.Tools)
	}
}

// installingGo plays go's part in an install.
type installingGo struct{}

func (installingGo) Run(_ context.Context, c gocmd.Call) (gocmd.Result, error) {
	if len(c.Args) < 3 || c.Args[0] != "get" {
		if len(c.Args) > 0 && c.Args[0] == "env" {
			return gocmd.Result{Stdout: []byte("go1.27.0\n")}, nil
		}

		return gocmd.Result{}, nil
	}

	modfile := filepath.Join(c.Dir, "go.mod")

	for _, a := range c.Args {
		if name, ok := strings.CutPrefix(a, "-modfile="); ok {
			modfile = filepath.Join(c.Dir, name)
		}
	}

	base, ver, _ := strings.Cut(c.Args[len(c.Args)-1], "@")

	f, err := os.OpenFile(modfile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return gocmd.Result{Code: 1, Stderr: []byte(err.Error())}, nil
	}

	defer f.Close()

	_, _ = f.WriteString("\ntool " + base + "\n\nrequire " + path.Dir(base) + " " + ver + "\n")

	return gocmd.Result{}, nil
}

// --version and -v do what the version command does.
func TestVersionFlags(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"-v"}, {"version"}} {
		r := run(t, t.TempDir(), args...)
		if r.runErr != nil || r.out != "gotools v9.9.9\n" || r.err != "" {
			t.Errorf("%v: err=%v out=%q stderr=%q", args, r.runErr, r.out, r.err)
		}
	}
}

func TestVersionAsJSONThroughTheApp(t *testing.T) {
	r := run(t, t.TempDir(), "version", "--json")
	if r.runErr != nil || r.out != `{"schema_version":1,"name":"gotools","version":"v9.9.9"}`+"\n" {
		t.Fatalf("err=%v out=%q", r.runErr, r.out)
	}
}

func TestVersionUsageErrorQuotesTheHelpLine(t *testing.T) {
	help := run(t, t.TempDir(), "version", "--help").out
	usage, _, _ := strings.Cut(help, "\n")

	r := run(t, t.TempDir(), "version", "--bogus")
	if want := strings.TrimPrefix(
		usage,
		"Usage: ",
	); r.runErr == nil ||
		!strings.Contains(r.runErr.Error(), "Usage: "+want) {
		t.Fatalf("err = %v, want the line %q", r.runErr, want)
	}
}
