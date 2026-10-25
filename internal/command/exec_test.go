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
	"encoding/json/v2"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/piusalfred/gotools/internal/command"
	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/manifest"
	"github.com/piusalfred/gotools/internal/tracer"
)

// fakeTool writes a script standing in for a built tool: it prints its working
// directory and arguments, echoes stdin, and exits with EXIT_CODE.
func fakeTool(t *testing.T) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("shell script fake tool")
	}

	path := filepath.Join(t.TempDir(), "faketool")
	script := "#!/bin/sh\necho \"cwd=$(pwd -P)\"\nfor a in \"$@\"; do echo \"arg=$a\"; done\ncat\nexit ${EXIT_CODE:-0}\n"

	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	return path
}

// buildGo answers `go tool -n` with the fake tool and records the calls.
func buildGo(bin string) *fakeGo {
	return &fakeGo{intercept: func(c gocmd.Call) (gocmd.Result, error, bool) {
		if len(c.Args) > 1 && c.Args[0] == "tool" && c.Args[1] == "-n" {
			return gocmd.Result{Stdout: []byte("build noise\n" + bin + "\n")}, nil, true
		}

		return gocmd.Result{}, nil, false
	}}
}

func execIn(t *testing.T, g *fakeGo, dir string, environ []string, stdin string, args ...string) (out, errs string, err error) {
	t.Helper()

	var o, e bytes.Buffer

	var r io.Reader
	if stdin != "" {
		r = strings.NewReader(stdin)
	}

	c := &command.Context{Stdin: r, Stdout: &o, Stderr: &e, Environ: environ, Program: "gotools", WorkDir: dir, Go: g}
	err = run(command.NewExec(), c, args...)

	return o.String(), e.String(), err
}

func realPath(t *testing.T, p string) string {
	t.Helper()

	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}

	return r
}

func TestExecRunsTheToolInTheInvocationDirectory(t *testing.T) {
	for _, st := range []string{"split", "module", "unified"} {
		t.Run(st, func(t *testing.T) {
			dir := infoFixture(t, st)
			sub := filepath.Join(dir, "pkg", "deep")

			if err := os.MkdirAll(sub, 0o755); err != nil {
				t.Fatal(err)
			}

			g := buildGo(fakeTool(t))

			// Flags, "--" and "--help" after the name belong to the tool.
			out, errs, err := execIn(t, g, sub, nil, "from-stdin\n", "goimports", "-w", "--help", "--", "a b")
			if err != nil || errs != "" {
				t.Fatalf("err=%v stderr=%s", err, errs)
			}

			want := "cwd=" + realPath(t, sub) + "\narg=-w\narg=--help\narg=--\narg=a b\nfrom-stdin\n"
			if out != want {
				t.Fatalf("out=%q want %q", out, want)
			}

			// The build runs in the strategy directory, not where exec was invoked.
			var build gocmd.Call

			for _, c := range g.calls {
				if len(c.Args) > 1 && c.Args[1] == "-n" {
					build = c
				}
			}

			wantDir := map[string]string{"split": dir, "module": filepath.Join(dir, "tools", "goimports"), "unified": filepath.Join(dir, "tools")}[st]
			if build.Dir != wantDir || build.Args[len(build.Args)-1] != "goimports" {
				t.Fatalf("build call = %+v", build)
			}

			if st == "split" && build.Args[2] != "-modfile="+filepath.Join(dir, "tools", "goimports.mod") {
				t.Fatalf("split build args = %v", build.Args)
			}
		})
	}
}

func TestExecPropagatesTheExitCode(t *testing.T) {
	dir := infoFixture(t, "split")
	t.Setenv("EXIT_CODE", "7")

	out, errs, err := execIn(t, buildGo(fakeTool(t)), dir, nil, "", "goimports")
	if ierrors.CodeOf(err) != 7 || err.Error() != "" || errs != "" || !strings.HasPrefix(out, "cwd=") {
		t.Fatalf("err=%v out=%q stderr=%q", err, out, errs)
	}
}

// With several tools in the shared go.mod, exec runs the one it was asked for.
func TestExecUnifiedPicksTheRequestedTool(t *testing.T) {
	dir := infoFixture(t, "unified")

	m := readManifest(t, dir)
	m.SetTool(manifestTool("lint", "example.com/x/cmd/staticcheck"))

	if err := m.Flush(filepath.Join(dir, config.ManifestFile)); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(dir, "tools", "go.mod"),
		"module tools/tools\n\ngo 1.24\n\ntool golang.org/x/tools/cmd/goimports\n\ntool example.com/x/cmd/staticcheck\n")

	g := buildGo(fakeTool(t))

	if _, _, err := execIn(t, g, dir, nil, "", "lint"); err != nil {
		t.Fatal(err)
	}

	built := 0

	for _, c := range g.calls {
		if len(c.Args) > 1 && c.Args[1] == "-n" {
			built++

			if c.Args[len(c.Args)-1] != "staticcheck" {
				t.Fatalf("built %v, want the staticcheck directive", c.Args)
			}
		}
	}

	if built != 1 {
		t.Fatalf("built %d times", built)
	}
}

func TestExecRefusals(t *testing.T) {
	bin := fakeTool(t)

	t.Run("usage and names", func(t *testing.T) {
		dir := infoFixture(t, "split")

		for _, c := range []struct {
			args []string
			msg  string
		}{
			{nil, "Missing argument"},
			{[]string{"../x"}, "path separator"},
			{[]string{"-x"}, "must not start with '-'"},
		} {
			_, _, err := execIn(t, buildGo(bin), dir, nil, "", c.args...)
			if ierrors.CodeOf(err) != ierrors.Usage || !strings.Contains(err.Error(), c.msg) {
				t.Errorf("%v: err=%v", c.args, err)
			}
		}
	})

	t.Run("not installed", func(t *testing.T) {
		for st, msg := range map[string]string{
			"split":  "Tool 'ghost' not found (tools/ghost.mod missing). Run 'install' first.",
			"module": "Tool 'ghost' not found (tools/ghost/go.mod missing). Run 'install' first.",
		} {
			_, _, err := execIn(t, buildGo(bin), infoFixture(t, st), nil, "", "ghost")
			if ierrors.CodeOf(err) != ierrors.ToolNotFound || !strings.Contains(err.Error(), msg) {
				t.Errorf("%s: err=%v", st, err)
			}
		}
	})

	t.Run("unified without go.mod", func(t *testing.T) {
		dir := infoFixture(t, "unified")
		_ = os.Remove(filepath.Join(dir, "tools", "go.mod"))

		_, _, err := execIn(t, buildGo(bin), dir, nil, "", "goimports")
		if ierrors.CodeOf(err) != ierrors.ToolNotFound || !strings.Contains(err.Error(), "No tools/go.mod found. Run 'init' first.") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("no directive for the tool", func(t *testing.T) {
		_, _, err := execIn(t, buildGo(bin), infoFixture(t, "unified"), nil, "", "stranger")
		if ierrors.CodeOf(err) != ierrors.ToolNotFound || !strings.Contains(err.Error(), "has no tool directive") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("symlinked modfile", func(t *testing.T) {
		dir := infoFixture(t, "split")
		mod := filepath.Join(dir, "tools", "goimports.mod")
		real := filepath.Join(dir, "real.mod")

		if err := os.Rename(mod, real); err != nil {
			t.Fatal(err)
		}

		if err := os.Symlink(real, mod); err != nil {
			t.Fatal(err)
		}

		_, _, err := execIn(t, buildGo(bin), dir, nil, "", "goimports")
		if ierrors.CodeOf(err) != ierrors.Environment || !strings.Contains(err.Error(), "Refusing to follow symlink") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("symlinked module directory", func(t *testing.T) {
		dir := infoFixture(t, "module")
		d := filepath.Join(dir, "tools", "goimports")
		real := filepath.Join(dir, "realdir")

		if err := os.Rename(d, real); err != nil {
			t.Fatal(err)
		}

		if err := os.Symlink(real, d); err != nil {
			t.Fatal(err)
		}

		_, _, err := execIn(t, buildGo(bin), dir, nil, "", "goimports")
		if ierrors.CodeOf(err) != ierrors.Environment || !strings.Contains(err.Error(), "Refusing to follow symlink") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestExecBuildFailure(t *testing.T) {
	g := &fakeGo{intercept: func(c gocmd.Call) (gocmd.Result, error, bool) {
		if c.Args[0] == "tool" {
			return gocmd.Result{Code: 1, Stderr: []byte("go: no such tool\n")}, nil, true
		}

		return gocmd.Result{}, nil, false
	}}

	_, errs, err := execIn(t, g, infoFixture(t, "split"), nil, "", "goimports")
	if ierrors.CodeOf(err) != ierrors.Generic || !strings.Contains(errs, "go: no such tool") || !strings.Contains(err.Error(), "Could not build tool 'goimports'") {
		t.Fatalf("err=%v stderr=%s", err, errs)
	}
}

func TestExecBuildOutcomes(t *testing.T) {
	dir := infoFixture(t, "split")

	cases := map[string]struct {
		result gocmd.Result
		err    error
		code   ierrors.Code
		msg    string
	}{
		"go missing": {err: gocmd.ErrNotFound, code: ierrors.Environment, msg: "go is not installed"},
		"go broken":  {err: os.ErrPermission, code: ierrors.Generic, msg: "go tool -n goimports:"},
		"no path":    {result: gocmd.Result{Stdout: []byte("\n")}, code: ierrors.Generic, msg: "printed no binary path"},
		"gone":       {result: gocmd.Result{Stdout: []byte(filepath.Join(dir, "gone"))}, code: ierrors.Generic, msg: "cannot be run"},
	}

	for name, tc := range cases {
		g := &fakeGo{intercept: func(c gocmd.Call) (gocmd.Result, error, bool) {
			if c.Args[0] == "tool" {
				return tc.result, tc.err, true
			}

			return gocmd.Result{}, nil, false
		}}

		_, _, err := execIn(t, g, dir, nil, "", "goimports")
		if ierrors.CodeOf(err) != tc.code || !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("%s: err=%v", name, err)
		}
	}
}

func TestExecToolThatCannotStart(t *testing.T) {
	dir := infoFixture(t, "split")

	bad := filepath.Join(dir, "notexec")
	writeFile(t, bad, "not a program")

	_, _, err := execIn(t, buildGo(bad), dir, nil, "", "goimports")
	if ierrors.CodeOf(err) != ierrors.Generic || !strings.Contains(err.Error(), "Could not run goimports") {
		t.Fatalf("err = %v", err)
	}
}

func TestExecHelpIsExecsOnlyWhenItComesFirst(t *testing.T) {
	g := buildGo(fakeTool(t))

	var o bytes.Buffer

	c := &command.Context{Stdout: &o, Stderr: &bytes.Buffer{}, Program: "gotools", WorkDir: infoFixture(t, "split"), Go: g}

	for _, flag := range []string{"--help", "-h"} {
		o.Reset()

		if err := run(command.NewExec(), c, flag); err != nil || !strings.Contains(o.String(), "Run a managed tool with the given arguments") {
			t.Fatalf("%s: err=%v out=%q", flag, err, o.String())
		}
	}

	if len(g.calls) != 0 {
		t.Fatalf("help ran go: %v", g.calls)
	}
}

func readTrace(t *testing.T, dir string) []tracer.Record {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, tracer.FileName))
	if err != nil {
		t.Fatal(err)
	}

	var recs []tracer.Record

	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var r tracer.Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("bad trace line %q: %v", line, err)
		}

		recs = append(recs, r)
	}

	return recs
}

func TestExecTrace(t *testing.T) {
	dir := infoFixture(t, "split")

	_, errs, err := execIn(t, buildGo(fakeTool(t)), dir, []string{"GOTOOLS_TRACE=1"}, "", "goimports", `say "hi"`, "it's")
	if err != nil {
		t.Fatal(err)
	}

	recs := readTrace(t, dir)
	if len(recs) != 1 {
		t.Fatalf("records = %d", len(recs))
	}

	r := recs[0]
	if r.Level != "info" || r.Tool != "goimports" || r.Strategy != "split" || r.Stdin != "pipe" ||
		r.ExitCode == nil || *r.ExitCode != 0 || len(r.Args) != 2 || r.Args[0] != `say "hi"` {
		t.Fatalf("record = %+v", r)
	}

	if want := `gotools exec goimports 'say "hi"' 'it'\''s'`; r.Cmd != want {
		t.Fatalf("cmd = %q, want %q", r.Cmd, want)
	}

	if r.Env.Dir != "tools" || r.Env.Trace != "true" || r.Env.ModulePrefix != "tools" || r.Env.GoVersion == "" {
		t.Fatalf("env = %+v", r.Env)
	}

	// The live copy goes to stderr by default and holds the same line.
	if !strings.Contains(errs, `"tool":"goimports"`) {
		t.Fatalf("no stderr record: %q", errs)
	}
}

func TestExecTraceModesAndLevels(t *testing.T) {
	dir := infoFixture(t, "split")
	t.Setenv("EXIT_CODE", "3")

	out, errs, err := execIn(t, buildGo(fakeTool(t)), dir, []string{"GOTOOLS_TRACE=stdout"}, "", "goimports")
	if ierrors.CodeOf(err) != 3 || !strings.Contains(out, `"level":"error"`) || strings.Contains(errs, `"level"`) {
		t.Fatalf("err=%v out=%q stderr=%q", err, out, errs)
	}

	// A tool that never ran is a fatal record without exit_code or args.
	_, _, err = execIn(t, buildGo(fakeTool(t)), dir, []string{"GOTOOLS_TRACE=1"}, "", "ghost")
	if ierrors.CodeOf(err) != ierrors.ToolNotFound {
		t.Fatalf("err=%v", err)
	}

	recs := readTrace(t, dir)
	last := recs[len(recs)-1]

	if last.Level != "fatal" || last.Tool != "ghost" || last.ExitCode != nil || last.Args != nil || !strings.Contains(last.Error, "not found") {
		t.Fatalf("fatal = %+v", last)
	}
}

func TestExecTraceOffWritesNothing(t *testing.T) {
	dir := infoFixture(t, "split")

	if _, _, err := execIn(t, buildGo(fakeTool(t)), dir, nil, "", "goimports"); err != nil {
		t.Fatal(err)
	}

	if exists(filepath.Join(dir, tracer.FileName)) {
		t.Fatal("trace file written with trace off")
	}
}

// The process runner is injectable, so a fake stands in for the tool.
func TestExecUsesTheContextsProcessRunner(t *testing.T) {
	dir := infoFixture(t, "split")
	f := &fakeProcesses{}

	var o bytes.Buffer

	c := &command.Context{Stdin: strings.NewReader("in"), Stdout: &o, Stderr: &bytes.Buffer{}, Program: "gotools", WorkDir: dir, Go: buildGo("/bin/sh"), Process: f}

	err := run(command.NewExec(), c, "goimports", "-w", ".")
	if ierrors.CodeOf(err) != 7 {
		t.Fatalf("err = %v", err)
	}

	if f.got.Bin != "/bin/sh" || strings.Join(f.got.Args, " ") != "-w ." || f.got.Dir != dir || f.got.Stdout != io.Writer(&o) {
		t.Fatalf("process = %+v", f.got)
	}
}

func manifestTool(name, pkg string) manifest.Tool {
	return manifest.Tool{Name: name, Source: "go", Package: pkg, Version: "v1"}
}
