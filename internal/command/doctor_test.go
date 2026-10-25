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
	"encoding/json/v2"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/piusalfred/gotools/internal/command"
	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/gocmd"
)

// doctorProject is an installed project with the sums the doctor looks for.
func doctorProject(t *testing.T, strategy string) string {
	t.Helper()

	dir := installedProject(t, strategy)

	for _, n := range []string{"x", "y"} {
		switch strategy {
		case "split":
			writeFile(t, filepath.Join(dir, "tools", n+".sum"), "sum\n")
		case "module":
			writeFile(t, filepath.Join(dir, "tools", n, "go.sum"), "sum\n")
		}
	}

	if strategy == "unified" {
		writeFile(t, filepath.Join(dir, "tools/go.sum"), "sum\n")
	}

	return dir
}

func doctorIn(t *testing.T, g *fakeGo, dir string, environ []string, args ...string) (out, errs string, err error) {
	t.Helper()

	return runCommand(t, command.NewDoctor(), dir, g, environ, args...)
}

func mustContain(t *testing.T, out string, wants ...string) {
	t.Helper()

	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Fatalf("missing %q in:\n%s", w, out)
		}
	}
}

func TestDoctorHealthyEachStrategy(t *testing.T) {
	for _, st := range []string{"split", "module", "unified"} {
		t.Run(st, func(t *testing.T) {
			out, errs, err := doctorIn(t, &fakeGo{}, doctorProject(t, st), nil)
			if err != nil || errs != "" {
				t.Fatalf("err=%v stderr=%q", err, errs)
			}

			mustContain(t, out,
				"🔍 gotools doctor — checking your environment...",
				"Go installation", "Module proxy", "Configuration", "Lock file", "Module integrity", "Disk usage",
				"Managed tools (2 declared)",
				"Strategy: "+st+" — matches disk",
				"x@v1.0.0 — runnable", "y@v1.2.0 — runnable",
				"tools/ uses",
				"All checks passed. Your environment is healthy.",
			)
		})
	}
}

func TestDoctorJSON(t *testing.T) {
	dir := doctorProject(t, "split")

	out, _, err := doctorIn(t, &fakeGo{}, dir, nil, "--format=json")
	if err != nil || strings.Count(out, "\n") != 1 {
		t.Fatalf("err=%v out=%q", err, out)
	}

	mustContain(t, out, `{"schema_version":1,"healthy":true,"issues":0,"checks":[{`, `"name":"tools.x","status":"pass"`)

	var rep struct {
		Schema  int  `json:"schema_version"`
		Healthy bool `json:"healthy"`
		Checks  []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"checks"`
	}

	if err := json.Unmarshal([]byte(out), &rep); err != nil || rep.Schema != 1 || !rep.Healthy || len(rep.Checks) != 9 {
		t.Fatalf("err=%v rep=%+v", err, rep)
	}

	if alias, _, _ := doctorIn(t, &fakeGo{}, dir, nil, "--json"); alias != out {
		t.Fatalf("--json differs from --format=json:\n%s\n%s", alias, out)
	}

	broken := &fakeGo{intercept: func(c gocmd.Call) (gocmd.Result, error, bool) {
		if c.Args[0] == "tool" {
			return gocmd.Result{Code: 1, Stderr: []byte(`go: no such tool "x"`)}, nil, true
		}

		return gocmd.Result{}, nil, false
	}}

	bad, _, err := doctorIn(t, broken, dir, nil, "--format=json")
	if err != nil {
		t.Fatalf("a finding is not a failure: %v", err)
	}

	mustContain(t, bad, `"healthy":false`, `"issues":2`, `"status":"warn"`)
}

func TestDoctorUsageErrorsPrintNothing(t *testing.T) {
	dir := doctorProject(t, "split")

	for name, args := range map[string][]string{"format": {"--format=yaml"}, "stray": {"nope"}, "flag": {"--fix"}, "both": {"--json", "--text"}} {
		out, _, err := doctorIn(t, &fakeGo{}, dir, nil, args...)
		if ierrors.CodeOf(err) != ierrors.Usage || out != "" {
			t.Fatalf("%s: err=%v out=%q", name, err, out)
		}
	}
}

// Whatever it finds, the doctor exits 0.
func TestDoctorNeverFailsTheShell(t *testing.T) {
	for name, g := range map[string]*fakeGo{
		"go missing": {intercept: func(gocmd.Call) (gocmd.Result, error, bool) { return gocmd.Result{}, gocmd.ErrNotFound, true }},
		"go too old": {intercept: func(c gocmd.Call) (gocmd.Result, error, bool) {
			return gocmd.Result{Stdout: []byte("go1.20.0\n")}, nil, c.Args[0] == "env"
		}},
	} {
		out, _, err := doctorIn(t, g, doctorProject(t, "split"), nil)
		if err != nil || !strings.Contains(out, "issue") {
			t.Fatalf("%s: err=%v out=%q", name, err, out)
		}
	}
}

func TestDoctorOfflineFromTheFlagOrTheEnvironment(t *testing.T) {
	dir := doctorProject(t, "split")

	for name, call := range map[string]func(g *fakeGo) (string, error){
		"flag": func(g *fakeGo) (string, error) { out, _, err := doctorIn(t, g, dir, nil, "--offline"); return out, err },
		"env": func(g *fakeGo) (string, error) {
			out, _, err := doctorIn(t, g, dir, []string{"GOTOOLS_OFFLINE=1"})

			return out, err
		},
	} {
		g := &fakeGo{}

		out, err := call(g)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		mustContain(t, out, "offline mode — reachability not checked")

		for _, c := range g.calls {
			if len(c.Args) > 0 && c.Args[0] == "list" {
				t.Fatalf("%s: the proxy was probed", name)
			}

			if len(c.Args) > 0 && (c.Args[0] == "mod" || c.Args[0] == "tool") && !slices.Contains(c.Env, "GOPROXY=off") {
				t.Fatalf("%s: %v ran without GOPROXY=off", name, c.Args)
			}
		}
	}
}

func TestDoctorReportsAProjectItCannotLoad(t *testing.T) {
	// No manifest.
	out, _, err := doctorIn(t, &fakeGo{}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}

	mustContain(t, out, "no .gotools.json — run 'gotools init'", "cannot compare strategy", "Go installation")

	// A manifest from a newer gotools.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, config.ManifestFile), `{"version": 2, "strategy": "split"}`)

	out, _, err = doctorIn(t, &fakeGo{}, dir, nil)
	if err != nil {
		t.Fatal(err)
	}

	mustContain(t, out, "requires schema version 2", "Go installation", "Disk usage")

	// Broken JSON, and JSON without a strategy.
	for name, body := range map[string]string{"truncated": `{"strategy": "spl`, "no strategy": `{"dir": "tools"}`} {
		dir = t.TempDir()
		writeFile(t, filepath.Join(dir, config.ManifestFile), body)

		out, _, err = doctorIn(t, &fakeGo{}, dir, nil)
		if err != nil || !strings.Contains(out, "❌ .gotools.json:") || !strings.Contains(out, "Go installation") {
			t.Fatalf("%s: err=%v out=%q", name, err, out)
		}
	}

	// An invalid tools directory in the manifest is reported, not fatal.
	dir = t.TempDir()
	writeFile(t, filepath.Join(dir, config.ManifestFile), `{"strategy": "split", "dir": "../escape"}`)

	out, _, err = doctorIn(t, &fakeGo{}, dir, nil)
	if err != nil || !strings.Contains(out, "tools directory must stay inside the project") {
		t.Fatalf("err=%v out=%q", err, out)
	}
}

// The doctor writes nothing, not even the clean-up of a crashed write.
func TestDoctorIsReadOnly(t *testing.T) {
	dir := newProject(t, "split")
	writeFile(t, filepath.Join(dir, config.ManifestFile+".tmp.99999"), "{}")

	before := tree(t, dir)

	for _, args := range [][]string{nil, {"--json"}, {"--offline"}} {
		if _, _, err := doctorIn(t, &fakeGo{}, dir, nil, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}

	sameTree(t, before, tree(t, dir))

	if exists(filepath.Join(dir, "tools")) {
		t.Fatal("the doctor created the tools directory")
	}
}

func TestDoctorLeavesNoScratchDirectoryBehind(t *testing.T) {
	g := &fakeGo{}

	if _, _, err := doctorIn(t, g, doctorProject(t, "split"), nil); err != nil {
		t.Fatal(err)
	}

	for _, c := range g.calls {
		if len(c.Args) > 0 && c.Args[0] == "env" && exists(c.Dir) {
			t.Fatalf("scratch directory %s left behind", c.Dir)
		}
	}
}
