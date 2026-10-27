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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piusalfred/gotools/internal/command"
	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/manifest"
)

func TestListTextAndJSONEachStrategy(t *testing.T) {
	for _, st := range []string{"split", "module", "unified"} {
		t.Run(st, func(t *testing.T) {
			dir := infoFixture(t, st)
			g := &fakeGo{}

			out, _, err := runCommand(t, command.NewList(), dir, g, nil)
			if err != nil || !strings.Contains(out, "goimports") ||
				!strings.Contains(out, "golang.org/x/tools/cmd/goimports@v0.30.0") || !strings.Contains(out, st) {
				t.Fatalf("err=%v out=\n%s", err, out)
			}

			out, _, err = runCommand(t, command.NewList(), dir, g, nil, "--format=json")
			want := `{"schema_version":1,"strategy":"` + st + `","dir":"tools","go_version":"inherit","tools":[` +
				`{"name":"goimports","source":"go","package":"golang.org/x/tools/cmd/goimports","version":"v0.30.0","go":"1.24"}]}` + "\n"

			if err != nil || out != want {
				t.Fatalf("json:\n%s\nwant:\n%s", out, want)
			}

			if len(g.calls) != 0 {
				t.Errorf("list ran go: %v", g.calls)
			}
		})
	}
}

func TestListTextTable(t *testing.T) {
	out, _, err := runCommand(t, command.NewList(), infoFixture(t, "split"), &fakeGo{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	want := "  TOOL               SOURCE STRATEGY   GO       MODFILE                        PACKAGE@VERSION\n" +
		"  ----               ------ --------   --       -------                        ---------------\n" +
		"  goimports          go     split      1.24     tools/goimports.mod            golang.org/x/tools/cmd/goimports@v0.30.0\n"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestListShowsTheManifestWhenNothingIsOnDisk(t *testing.T) {
	dir := newProject(t, "split")

	m := readManifest(t, dir)
	m.SetTool(manifest.Tool{Name: "a", Source: "go", Package: "p/a", Version: "v1"})

	if err := m.Flush(filepath.Join(dir, config.ManifestFile)); err != nil {
		t.Fatal(err)
	}

	out, _, _ := runCommand(t, command.NewList(), dir, &fakeGo{}, nil)
	if !strings.Contains(out, "(manifest)") || !strings.Contains(out, "p/a@v1") {
		t.Fatalf("out:\n%s", out)
	}

	out, _, _ = runCommand(t, command.NewList(), dir, &fakeGo{}, nil, "--json")
	if !strings.Contains(out, `"go":"?"`) {
		t.Fatalf("json:\n%s", out)
	}
}

func TestListWithNoToolsIsAnEmptyArray(t *testing.T) {
	out, _, err := runCommand(t, command.NewList(), newProject(t, "split"), &fakeGo{}, nil, "--json")
	if err != nil || !strings.Contains(out, `"tools":[]`) {
		t.Fatalf("err=%v out=%s", err, out)
	}
}

func TestListUsageErrors(t *testing.T) {
	dir := t.TempDir()

	for _, args := range [][]string{{"--bogus"}, {"--format=xml"}, {"--json", "--text"}, {"extra"}} {
		if _, _, err := runCommand(
			t,
			command.NewList(),
			dir,
			&fakeGo{},
			nil,
			args...); ierrors.CodeOf(
			err,
		) != ierrors.Usage {
			t.Errorf("%v: err=%v", args, err)
		}
	}
}

// A stray tools/go.mod must not be listed as a tool named "go".
func TestListIgnoresAStrayGoMod(t *testing.T) {
	dir := infoFixture(t, "split")
	writeFile(t, filepath.Join(dir, "tools", "go.mod"), "module x\n\ngo 1.24\n\ntool a/b\n")

	out, _, _ := runCommand(t, command.NewList(), dir, &fakeGo{}, nil)
	if strings.Contains(out, "\n  go ") {
		t.Fatalf("stray go.mod listed:\n%s", out)
	}
}

// A tool that is on disk but not in the manifest is listed, with the source go.
func TestListShowsAToolThatIsOnlyOnDisk(t *testing.T) {
	dir := newProject(t, "split")
	writeFile(
		t,
		filepath.Join(dir, "tools", "lint.mod"),
		"module tools/lint\n\ngo 1.25\n\ntool example.com/lint/cmd/lint\n\nrequire example.com/lint v1.0.0\n",
	)

	out, _, err := runCommand(t, command.NewList(), dir, &fakeGo{}, nil)
	if err != nil || !strings.Contains(out, "lint") || !strings.Contains(out, "go     split      1.25") ||
		!strings.Contains(out, "example.com/lint/cmd/lint@v1.0.0") {
		t.Fatalf("err=%v out=\n%s", err, out)
	}
}

// An unreadable modfile is a warning, and the others are still listed.
func TestListWarnsAboutAnUnreadableModfile(t *testing.T) {
	dir := infoFixture(t, "split")
	writeFile(t, filepath.Join(dir, "tools", "broken.mod"), "this is not a modfile\n")

	out, errs, err := runCommand(t, command.NewList(), dir, &fakeGo{}, nil)
	if err != nil || !strings.Contains(out, "goimports") || !strings.Contains(errs, "⚠️") {
		t.Fatalf("err=%v out=%q err=%q", err, out, errs)
	}
}

func TestListRefusesASymlinkedToolsDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "tools")); err != nil {
		t.Skip(err)
	}

	if _, _, err := runCommand(t, command.NewList(), dir, &fakeGo{}, nil); ierrors.CodeOf(err) != ierrors.Environment {
		t.Fatalf("err = %v", err)
	}
}

// A directive with no pinned requirement is shown as unknown, and a modfile
// with no go directive has an unknown Go version.
func TestListShowsWhatTheModfileDoesNotPin(t *testing.T) {
	dir := newProject(t, "split")
	writeFile(t, filepath.Join(dir, "tools", "bare.mod"), "module tools/bare\n\ntool example.com/bare/cmd/bare\n")

	out, _, err := runCommand(t, command.NewList(), dir, &fakeGo{}, nil)
	if err != nil || !strings.Contains(out, "example.com/bare/cmd/bare@unknown") ||
		!strings.Contains(out, "bare               go     split      ?") {
		t.Fatalf("err=%v out=\n%s", err, out)
	}
}

// JSON lists the tools in the manifest's own order: by source, package, then
// version, never the order they were added in.
func TestListJSONIsSorted(t *testing.T) {
	dir := newProject(t, "split")

	m := readManifest(t, dir)
	m.SetTool(manifest.Tool{Name: "z", Source: "go", Package: "p/a", Version: "v1"})
	m.SetTool(manifest.Tool{Name: "a", Source: "go", Package: "p/z", Version: "v1"})

	if err := m.Flush(filepath.Join(dir, config.ManifestFile)); err != nil {
		t.Fatal(err)
	}

	out, _, err := runCommand(t, command.NewList(), dir, &fakeGo{}, nil, "--json")
	if err != nil || strings.Index(out, `"name":"z"`) > strings.Index(out, `"name":"a"`) {
		t.Fatalf("err=%v out=%s", err, out)
	}
}
