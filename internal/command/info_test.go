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
	"path/filepath"
	"strings"
	"testing"

	"github.com/piusalfred/gotools/internal/command"
	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/manifest"
)

const goimportsMod = "module tools/goimports\n\ngo 1.24\n\ntool golang.org/x/tools/cmd/goimports\n\nrequire golang.org/x/tools v0.30.0\n"

// infoFixture builds a project for strategy with one tool, "goimports".
func infoFixture(t *testing.T, st string) string {
	t.Helper()

	dir := t.TempDir()
	m := manifest.New()
	m.Strategy, m.ModulePrefix = st, "tools"
	m.SetTool(manifest.Tool{Name: "goimports", Source: "go", Package: imports, Version: "v0.30.0"})

	if err := m.Flush(filepath.Join(dir, config.ManifestFile)); err != nil {
		t.Fatal(err)
	}

	switch st {
	case "split":
		writeFile(t, filepath.Join(dir, "tools", "goimports.mod"), goimportsMod)
	case "module":
		writeFile(t, filepath.Join(dir, "tools", "goimports", "go.mod"), goimportsMod)
	case "unified":
		writeFile(t, filepath.Join(dir, "tools", "go.mod"), goimportsMod)
	}

	return dir
}

func infoIn(t *testing.T, dir string, g *fakeGo, args ...string) (out, errs string, err error) {
	t.Helper()

	var o, e bytes.Buffer

	c := &command.Context{Stdout: &o, Stderr: &e, Program: "gotools", WorkDir: dir, Go: g}
	err = run(command.NewInfo(), c, args...)

	return o.String(), e.String(), err
}

func TestInfoTextEachStrategy(t *testing.T) {
	for _, st := range []string{"split", "module", "unified"} {
		t.Run(st, func(t *testing.T) {
			g := &fakeGo{}

			out, _, err := infoIn(t, infoFixture(t, st), g, "goimports")
			if err != nil {
				t.Fatal(err)
			}

			for _, want := range []string{
				"  Tool:       goimports\n",
				"  Source:     go\n",
				"  Package:    golang.org/x/tools/cmd/goimports\n",
				"  Version:    v0.30.0\n",
				"  Go:         1.24\n",
				"  Strategy:   " + st + "\n",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q in\n%s", want, out)
				}
			}

			if len(g.calls) != 0 {
				t.Errorf("text info ran go: %v", g.calls)
			}
		})
	}
}

func TestInfoJSONSaysWhetherTheToolRuns(t *testing.T) {
	dir := infoFixture(t, "split")

	out, _, err := infoIn(t, dir, &fakeGo{}, "goimports", "--json")
	want := `{"schema_version":1,"name":"goimports","source":"go","strategy":"split","go":"1.24",` +
		`"package":"golang.org/x/tools/cmd/goimports","version":"v0.30.0","runnable":true}` + "\n"

	if err != nil || out != want {
		t.Fatalf("err=%v out=%q", err, out)
	}

	broken := &fakeGo{fail: func(gocmd.Call) string { return "go: boom" }}

	out, _, _ = infoIn(t, dir, broken, "goimports", "--json")
	if !strings.Contains(out, `"runnable":false`) {
		t.Fatalf("out=%s", out)
	}
}

func TestInfoUnknownTool(t *testing.T) {
	dir := infoFixture(t, "split")

	out, _, err := infoIn(t, dir, &fakeGo{}, "nope", "--json")
	if ierrors.CodeOf(err) != ierrors.ToolNotFound || err.Error() != "" ||
		out != `{"schema_version":1,"error":"tool not found: nope"}`+"\n" {
		t.Fatalf("json: err=%v out=%q", err, out)
	}

	_, _, err = infoIn(t, dir, &fakeGo{}, "nope")
	if ierrors.CodeOf(err) != ierrors.ToolNotFound || !strings.Contains(err.Error(), "Tool 'nope' not found (") {
		t.Fatalf("text: %v", err)
	}

	_, _, err = infoIn(t, infoFixture(t, "unified"), &fakeGo{}, "nope")
	if ierrors.CodeOf(err) != ierrors.ToolNotFound || !strings.Contains(err.Error(), "not found in tools/go.mod") {
		t.Fatalf("unified: %v", err)
	}
}

// The manifest fills in what the modfile lacks, and a tool in the manifest
// without a modfile is still shown as JSON, not runnable.
func TestInfoFallsBackToTheManifest(t *testing.T) {
	dir := infoFixture(t, "split")
	writeFile(t, filepath.Join(dir, "tools", "goimports.mod"), "module tools/goimports\n")

	g := &fakeGo{}

	out, _, err := infoIn(t, dir, g, "goimports", "--json")
	if err != nil || !strings.Contains(out, `"version":"v0.30.0"`) || !strings.Contains(out, `"runnable":false`) {
		t.Fatalf("err=%v out=%s", err, out)
	}

	if len(g.calls) != 0 {
		t.Errorf("a tool without a directive spent a go call: %v", g.calls)
	}
}

func TestInfoUsageErrors(t *testing.T) {
	dir := infoFixture(t, "split")

	for _, args := range [][]string{nil, {"a", "b"}, {"goimports", "--json", "--text"}, {"--bogus", "goimports"}} {
		if _, _, err := infoIn(t, dir, &fakeGo{}, args...); ierrors.CodeOf(err) != ierrors.Usage {
			t.Errorf("%v: err=%v", args, err)
		}
	}
}

func TestInfoWarnsAboutManifestProblems(t *testing.T) {
	dir := infoFixture(t, "split")
	writeFile(t, filepath.Join(dir, config.ManifestFile), `{"schema_version":1,"strategy":"split","dir":"tools","tools":{"goimports":{"source":"go","package":"`+imports+`","version":"v0.30.0"}},"extra":1}`)

	if _, _, err := infoIn(t, dir, &fakeGo{}, "goimports"); err != nil {
		t.Fatal(err)
	}
}

func TestInfoShowsTheManifestsSource(t *testing.T) {
	dir := infoFixture(t, "split")

	m := readManifest(t, dir)
	m.SetTool(manifest.Tool{Name: "goimports", Source: "internal", Package: imports, Version: "v0.30.0"})

	if err := m.Flush(filepath.Join(dir, config.ManifestFile)); err != nil {
		t.Fatal(err)
	}

	out, _, err := infoIn(t, dir, &fakeGo{}, "goimports")
	if err != nil || !strings.Contains(out, "  Source:     internal\n") {
		t.Fatalf("err=%v out=%s", err, out)
	}
}
