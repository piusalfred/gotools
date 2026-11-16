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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piusalfred/gotools/internal/binfo"
	"github.com/piusalfred/gotools/internal/command"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/gocmd"
)

// fakeBins stands in for the build information of the files in a bin
// directory, by file name. A file it does not know is not a Go binary.
type fakeBins map[string]binfo.Info

func (f fakeBins) Read(path string) (binfo.Info, error) {
	i, ok := f[filepath.Base(path)]
	if !ok {
		return binfo.Info{}, errors.New("not a go binary")
	}

	return i, nil
}

func bin(pkg, module, version string) binfo.Info {
	return binfo.Info{Package: pkg, Module: module, Version: version}
}

// binDir writes an executable file for every name, as 'go install' would.
func binDir(t *testing.T, names ...string) string {
	t.Helper()

	dir := t.TempDir()

	for _, n := range names {
		writeFile(t, filepath.Join(dir, n), "binary")

		if err := os.Chmod(filepath.Join(dir, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

func importIn(
	t *testing.T,
	project string,
	bins fakeBins,
	g *fakeGo,
	environ []string,
	args ...string,
) (string, string, error) {
	t.Helper()

	var o, e bytes.Buffer

	c := &command.Context{
		Stdout: &o, Stderr: &e, Environ: environ, Program: "gotools", WorkDir: project, Go: g, Binaries: bins,
	}
	err := run(command.NewImportGobin(), c, args...)

	return o.String(), e.String(), err
}

var gobinTools = fakeBins{
	"goimports":   bin(imports, "golang.org/x/tools", "v0.30.0"),
	"staticcheck": bin("honnef.co/go/tools/cmd/staticcheck", "honnef.co/go/tools", "v0.6.0"),
	"mytool":      bin("example.com/me/mytool", "example.com/me/mytool", "(devel)"),
}

func TestImportGobinEachStrategy(t *testing.T) {
	for _, st := range []string{"split", "module", "unified"} {
		t.Run(st, func(t *testing.T) {
			project := newProject(t, st)
			dir := binDir(t, "goimports", "staticcheck", "mytool", "notgo")

			out, _, err := importIn(t, project, gobinTools, &fakeGo{}, nil, "--all", "--from", dir)
			if err != nil {
				t.Fatalf("err=%v out=%s", err, out)
			}

			m := readManifest(t, project)
			for name, ver := range map[string]string{"goimports": "v0.30.0", "staticcheck": "v0.6.0"} {
				if tool, ok := m.Tool(name); !ok || tool.Version != ver {
					t.Errorf("%s = %+v, %v; want %s", name, tool, ok, ver)
				}
			}

			// A binary built from a working tree has no version: skipped, not fatal.
			if _, ok := m.Tool("mytool"); ok {
				t.Error("imported a binary with no version")
			}

			for _, want := range []string{"✅ Imported 2 tool(s).", "Skipped 2", "mytool", "built from a working tree", "notgo", "not a Go binary"} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
		})
	}
}

// With nothing chosen the command only shows the directory.
func TestImportGobinListsByDefault(t *testing.T) {
	project := newProject(t, "split")
	dir := binDir(t, "goimports", "mytool")
	g := &fakeGo{}

	// Offline is fine: nothing is fetched.
	out, _, err := importIn(t, project, gobinTools, g, []string{"GOTOOLS_OFFLINE=1"}, "--from", dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"goimports", imports + "@v0.30.0", "mytool", "skip:", "1 can be imported", "gotools import-gobin --all"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	if n := g.count("get"); n != 0 {
		t.Errorf("listing ran go get %d times", n)
	}

	if got := manifestTools(t, project); len(got) != 0 {
		t.Errorf("listing changed the manifest: %v", got)
	}
}

func TestImportGobinNamedBinariesOnly(t *testing.T) {
	project := newProject(t, "split")
	dir := binDir(t, "goimports", "staticcheck")

	if _, _, err := importIn(t, project, gobinTools, &fakeGo{}, nil, "--from", dir, "staticcheck"); err != nil {
		t.Fatal(err)
	}

	if got := manifestTools(t, project); len(got) != 1 || got[0] != "staticcheck" {
		t.Fatalf("manifest = %v", got)
	}
}

// Some tools cannot be installed this way. That is a skip, and the rest go on.
func TestImportGobinSkipsWhatCannotBeInstalled(t *testing.T) {
	project := newProject(t, "split")
	dir := binDir(t, "goimports", "staticcheck")
	g := &fakeGo{fail: func(c gocmd.Call) string {
		if len(c.Args) > 0 && c.Args[0] == "get" && strings.Contains(strings.Join(c.Args, " "), "staticcheck") {
			return "go: honnef.co/go/tools@v0.6.0: reading go.mod: 404 Not Found\nsecond line"
		}

		return ""
	}}

	out, _, err := importIn(t, project, gobinTools, g, nil, "--all", "--from", dir)
	if err != nil {
		t.Fatalf("a failed tool must not fail the run: %v", err)
	}

	m := readManifest(t, project)
	if _, ok := m.Tool("goimports"); !ok {
		t.Error("goimports was not imported")
	}

	if _, ok := m.Tool("staticcheck"); ok {
		t.Error("the failed tool is in the manifest")
	}

	for _, want := range []string{"✅ Imported 1 tool(s).", "Skipped 1", "staticcheck", "could not be installed", "404 Not Found"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	if strings.Contains(out, "second line") {
		t.Error("the reason is more than the first line of go's message")
	}

	// The failed install left nothing behind.
	if exists(filepath.Join(project, "tools", "staticcheck.mod")) {
		t.Error("the failed tool left its modfile")
	}
}

func TestImportGobinDryRun(t *testing.T) {
	project := newProject(t, "split")
	dir := binDir(t, "goimports", "mytool")
	g := &fakeGo{}

	out, _, err := importIn(t, project, gobinTools, g, nil, "--all", "--dry-run", "--from", dir)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "Would import goimports ("+imports+"@v0.30.0)") || !strings.Contains(out, "mytool") {
		t.Errorf("output:\n%s", out)
	}

	if g.count("get") != 0 || len(manifestTools(t, project)) != 0 || exists(filepath.Join(project, "tools")) {
		t.Error("a dry run changed something")
	}
}

func TestImportGobinUnknownNameIsToolNotFoundAfterTheRest(t *testing.T) {
	project := newProject(t, "split")
	dir := binDir(t, "goimports")

	_, _, err := importIn(t, project, gobinTools, &fakeGo{}, nil, "--from", dir, "nope", "goimports")
	if ierrors.CodeOf(err) != ierrors.ToolNotFound || !strings.Contains(err.Error(), "'nope'") {
		t.Fatalf("err = %v", err)
	}

	if got := manifestTools(t, project); len(got) != 1 {
		t.Fatalf("the binary that exists was not imported: %v", got)
	}
}

func TestImportGobinSkipsWhatIsManaged(t *testing.T) {
	project := infoFixture(t, "split") // manages goimports
	writeFile(t, filepath.Join(project, "go.mod"), "module example.com/test\n\ngo 1.24\n")
	dir := binDir(t, "goimports", "imports")
	bins := fakeBins{
		"goimports": bin(imports, "golang.org/x/tools", "v0.31.0"),
		// The same package under another file name: the twin rule.
		"imports": bin(imports, "golang.org/x/tools", "v0.30.0"),
	}

	out, _, err := importIn(t, project, bins, &fakeGo{}, nil, "--all", "--from", dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"already managed (version v0.30.0)", "already managed as 'goimports'", "Nothing imported."} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// A module has one version in the unified strategy.
func TestImportGobinUnifiedKeepsOneVersionPerModule(t *testing.T) {
	project := newProject(t, "unified")
	dir := binDir(t, "a", "b")
	bins := fakeBins{
		"a": bin("example.com/m/cmd/a", "example.com/m", "v1.0.0"),
		"b": bin("example.com/m/cmd/b", "example.com/m", "v1.1.0"),
	}

	out, _, err := importIn(t, project, bins, &fakeGo{}, nil, "--all", "--from", dir)
	if err != nil {
		t.Fatal(err)
	}

	if got := manifestTools(t, project); len(got) != 1 || got[0] != "a" {
		t.Fatalf("manifest = %v", got)
	}

	if !strings.Contains(out, "one version per module") {
		t.Errorf("output:\n%s", out)
	}
}

func TestImportGobinRefusesOfflineBeforeChangingAnything(t *testing.T) {
	project := newProject(t, "split")
	dir := binDir(t, "goimports")
	g := &fakeGo{}

	for _, args := range [][]string{{"--all"}, {"goimports"}} {
		_, _, err := importIn(t, project, gobinTools, g, []string{"GOTOOLS_OFFLINE=1"}, append(args, "--from", dir)...)
		if ierrors.CodeOf(err) != ierrors.Offline {
			t.Errorf("%v: err = %v", args, err)
		}
	}

	if _, _, err := importIn(
		t,
		project,
		gobinTools,
		g,
		nil,
		"--all",
		"--offline",
		"--from",
		dir,
	); ierrors.CodeOf(
		err,
	) != ierrors.Offline {
		t.Errorf("--offline: err = %v", err)
	}

	if g.count("get") != 0 || len(manifestTools(t, project)) != 0 {
		t.Error("an offline run changed something")
	}
}

func TestImportGobinRejectsNamesWithAll(t *testing.T) {
	_, _, err := importIn(t, newProject(t, "split"), gobinTools, &fakeGo{}, nil, "--all", "goimports")
	if ierrors.CodeOf(err) != ierrors.Usage {
		t.Fatalf("err = %v", err)
	}
}

// Without --from the directory is what go says: GOBIN, else GOPATH/bin.
func TestImportGobinFindsTheDirectoryThroughGo(t *testing.T) {
	gobin := binDir(t, "goimports")
	gopath := t.TempDir()
	gopathBin := filepath.Join(gopath, "bin")

	if err := os.MkdirAll(gopathBin, 0o755); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(gopathBin, "staticcheck"), "binary")

	if err := os.Chmod(filepath.Join(gopathBin, "staticcheck"), 0o755); err != nil {
		t.Fatal(err)
	}

	answer := func(stdout string) *fakeGo {
		return &fakeGo{intercept: func(c gocmd.Call) (gocmd.Result, error, bool) {
			if len(c.Args) == 3 && c.Args[0] == "env" && c.Args[1] == "GOBIN" {
				return gocmd.Result{Stdout: []byte(stdout)}, nil, true
			}

			return gocmd.Result{}, nil, false
		}}
	}

	out, _, err := importIn(t, newProject(t, "split"), gobinTools, answer(gobin+"\n"+gopath+"\n"), nil)
	if err != nil || !strings.Contains(out, gobin) || !strings.Contains(out, "goimports") {
		t.Fatalf("GOBIN: err=%v out=%s", err, out)
	}

	// GOBIN is empty: the first GOPATH entry's bin.
	out, _, err = importIn(
		t,
		newProject(t, "split"),
		gobinTools,
		answer("\n"+gopath+string(os.PathListSeparator)+"/x\n"),
		nil,
	)
	if err != nil || !strings.Contains(out, gopathBin) || !strings.Contains(out, "staticcheck") {
		t.Fatalf("GOPATH: err=%v out=%s", err, out)
	}

	// Neither: say how to name it.
	_, _, err = importIn(t, newProject(t, "split"), gobinTools, answer("\n\n"), nil)
	if ierrors.CodeOf(err) != ierrors.Environment || !strings.Contains(err.Error(), "--from") {
		t.Fatalf("empty: err = %v", err)
	}
}

func TestImportGobinMissingDirectory(t *testing.T) {
	_, _, err := importIn(
		t,
		newProject(t, "split"),
		gobinTools,
		&fakeGo{},
		nil,
		"--from",
		filepath.Join(t.TempDir(), "none"),
	)
	if ierrors.CodeOf(err) != ierrors.Environment {
		t.Fatalf("err = %v", err)
	}
}
