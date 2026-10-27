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
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/piusalfred/gotools/internal/command"
	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/tracer"
)

type tracedProject struct {
	c           *command.Context
	out, errb   *bytes.Buffer
	dir         string
	goes        *fakeGo
	traceRecord func() []map[string]any
}

func traceContext(t *testing.T, environ ...string) tracedProject {
	t.Helper()

	dir := newProject(t, "unified")
	g := &fakeGo{}
	tp := tracedProject{out: &bytes.Buffer{}, errb: &bytes.Buffer{}, dir: dir, goes: g}

	tp.c = &command.Context{
		Stdin:   strings.NewReader(""),
		Stdout:  tp.out,
		Stderr:  tp.errb,
		Environ: environ,
		Program: "gotools",
		WorkDir: dir,
		Go:      g,
	}
	if err := tp.c.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	tp.traceRecord = func() []map[string]any {
		b, err := os.ReadFile(filepath.Join(dir, tracer.FileName))
		if os.IsNotExist(err) {
			return nil
		}

		if err != nil {
			t.Fatal(err)
		}

		var recs []map[string]any

		for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
			var r map[string]any
			if err := json.Unmarshal([]byte(line), &r); err != nil {
				t.Fatalf("%q: %v", line, err)
			}

			recs = append(recs, r)
		}

		return recs
	}

	return tp
}

func TestTracerIsOffByDefaultAndCostsNothing(t *testing.T) {
	tp := traceContext(t)

	tr := tp.c.Tracer(context.Background())
	tr.Log(tracer.Entry{Tool: "x", Binary: "x", Code: new(0)})
	tr.Fatal(tracer.Entry{Tool: "x", Message: "stop"})

	if _, ok := tr.(tracer.Nop); !ok {
		t.Fatalf("tracer = %T", tr)
	}

	if recs := tp.traceRecord(); recs != nil || len(tp.goes.calls) != 0 || tp.errb.Len() != 0 || tp.out.Len() != 0 {
		t.Fatalf("records=%v calls=%v err=%q out=%q", recs, tp.goes.calls, tp.errb.String(), tp.out.String())
	}
}

func TestTracerEchoesToStderrByDefaultWhenOn(t *testing.T) {
	tp := traceContext(t, "GOTOOLS_TRACE=true")

	tp.c.Tracer(context.Background()).
		Log(tracer.Entry{Tool: "fmt", Binary: "gofumpt", Args: []string{"-w", "a b"}, Code: new(0)})

	recs := tp.traceRecord()
	if len(recs) != 1 {
		t.Fatalf("records = %v", recs)
	}

	r := recs[0]
	if r["level"] != "info" || r["tool"] != "fmt" || r["binary"] != "gofumpt" || r["strategy"] != "unified" ||
		r["cmd"] != "gotools exec fmt -w 'a b'" || r["stdin"] != "pipe" || r["exit_code"] != 0.0 {
		t.Fatalf("record = %v", r)
	}

	if tp.out.Len() != 0 || strings.TrimSpace(tp.errb.String()) == "" {
		t.Fatalf("echo: out=%q err=%q", tp.out.String(), tp.errb.String())
	}

	if strings.TrimSpace(tp.errb.String()) != string(mustLine(t, filepath.Join(tp.dir, tracer.FileName))) {
		t.Errorf("the echo differs from the file: %q", tp.errb.String())
	}
}

func mustLine(t *testing.T, path string) []byte {
	t.Helper()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return bytes.TrimSpace(b)
}

func TestTracerCanEchoToStdout(t *testing.T) {
	tp := traceContext(t, "GOTOOLS_TRACE=stdout")

	tp.c.Tracer(context.Background()).Fatal(tracer.Entry{Tool: "nope", Message: "not installed"})

	if tp.errb.Len() != 0 || !strings.Contains(tp.out.String(), `"level":"fatal"`) ||
		!strings.Contains(tp.out.String(), `"error":"not installed"`) {
		t.Fatalf("out=%q err=%q", tp.out.String(), tp.errb.String())
	}

	if recs := tp.traceRecord(); len(recs) != 1 {
		t.Fatalf("records = %v", recs)
	}
}

// The record carries the configuration it ran under, including the Go version
// the tools resolve to.
func TestTracerStampsTheConfiguration(t *testing.T) {
	tp := traceContext(t, "GOTOOLS_TRACE=true", "GOTOOLS_VERBOSE=1", "GOTOOLS_GO_VERSION=1.25")

	tp.c.Tracer(context.Background()).Error(tracer.Entry{Tool: "t", Binary: "t", Code: new(2)})

	env, _ := tp.traceRecord()[0]["env"].(map[string]any)

	want := map[string]any{
		"GOTOOLS_STRATEGY": "unified", "GOTOOLS_DIR": "tools", "GOTOOLS_GO_VERSION": "1.25",
		"GOTOOLS_MODULE_PREFIX": "tools", "GOTOOLS_VERBOSE": "1", "GOTOOLS_TRACE": "true",
	}

	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %v, want %v", k, env[k], v)
		}
	}

	if tp.traceRecord()[0]["level"] != "error" {
		t.Errorf("record = %v", tp.traceRecord()[0])
	}
}

func TestTracerResolvesAnInheritedGoVersionFromGo(t *testing.T) {
	tp := traceContext(t, "GOTOOLS_TRACE=true", "GOTOOLS_GO_VERSION=inherit")
	tp.goes.intercept = func(c gocmd.Call) (gocmd.Result, error, bool) {
		if len(c.Args) > 0 && c.Args[0] == "env" {
			return gocmd.Result{Stdout: []byte("go1.27.0\n")}, nil, true
		}

		return gocmd.Result{}, nil, false
	}

	tp.c.Tracer(context.Background()).Log(tracer.Entry{Tool: "t", Code: new(0)})

	env, _ := tp.traceRecord()[0]["env"].(map[string]any)
	if v, _ := env["GOTOOLS_GO_VERSION"].(string); v == "" || v == "inherit" {
		t.Fatalf("env = %v", env)
	}
}

// A log that cannot be written never fails the caller.
func TestTracerReportsAnUnwritableLogAndCarriesOn(t *testing.T) {
	tp := traceContext(t, "GOTOOLS_TRACE=true")

	if err := os.Mkdir(filepath.Join(tp.dir, tracer.FileName), 0o755); err != nil {
		t.Fatal(err)
	}

	tp.c.Tracer(context.Background()).Log(tracer.Entry{Tool: "t", Code: new(0)})

	if !strings.Contains(tp.errb.String(), "trace: could not write") ||
		!strings.Contains(tp.errb.String(), `"tool":"t"`) {
		t.Fatalf("err = %q", tp.errb.String())
	}
}

type fakeProcesses struct{ got gocmd.Process }

func (f *fakeProcesses) Run(_ context.Context, p gocmd.Process) (int, error) {
	f.got = p

	return 7, nil
}

func TestProcessesIsInjectableAndDefaultsToTheRealProgram(t *testing.T) {
	f := &fakeProcesses{}
	c := &command.Context{Process: f}

	code, err := c.Processes().Run(context.Background(), gocmd.Process{Bin: "x"})
	if err != nil || code != 7 || f.got.Bin != "x" {
		t.Fatalf("code=%d err=%v got=%+v", code, err, f.got)
	}

	if _, ok := (&command.Context{}).Processes().(gocmd.ExecProcessRunner); !ok {
		t.Fatalf("default = %T", (&command.Context{}).Processes())
	}
}

// The default go runner keeps the tool modules out of any workspace.
func TestDefaultRunnerTurnsTheWorkspaceOff(t *testing.T) {
	r, ok := (&command.Context{}).Runner().(gocmd.ExecRunner)
	if !ok || !slices.Contains(r.Env, "GOWORK=off") {
		t.Fatalf("runner = %#v", (&command.Context{}).Runner())
	}

	// An injected runner is left as it is.
	f := &fakeGo{}
	if (&command.Context{Go: f}).Runner() != gocmd.Runner(f) {
		t.Fatal("the injected runner was replaced")
	}
}
