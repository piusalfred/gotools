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
	"slices"
	"strings"
	"testing"

	"github.com/piusalfred/gotools/internal/command"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/gocmd"
)

func checkIn(t *testing.T, dir string, g *fakeGo, environ []string, args ...string) (out string, err error) {
	t.Helper()

	var o bytes.Buffer

	c := &command.Context{Stdout: &o, Stderr: &bytes.Buffer{}, Environ: environ, Program: "gotools", WorkDir: dir, Go: g}
	err = run(command.NewCheck(), c, args...)

	return o.String(), err
}

func toolCalls(g *fakeGo) []gocmd.Call {
	var out []gocmd.Call

	for _, c := range g.calls {
		if len(c.Args) > 0 && c.Args[0] == "tool" {
			out = append(out, c)
		}
	}

	return out
}

func TestCheckPassesAndFailsEachStrategy(t *testing.T) {
	for _, st := range []string{"split", "module", "unified"} {
		t.Run(st, func(t *testing.T) {
			dir := infoFixture(t, st)

			out, err := checkIn(t, dir, &fakeGo{}, nil)
			if err != nil || !strings.Contains(out, "  ✅ goimports (golang.org/x/tools/cmd/goimports@v0.30.0)") ||
				!strings.Contains(out, "  1 passed, 0 failed") {
				t.Fatalf("err=%v out=%s", err, out)
			}

			broken := &fakeGo{intercept: func(c gocmd.Call) (gocmd.Result, error, bool) {
				if c.Args[0] == "tool" {
					return gocmd.Result{Code: 2}, nil, true
				}

				return gocmd.Result{}, nil, false
			}}

			out, err = checkIn(t, dir, broken, nil)
			if ierrors.CodeOf(err) != ierrors.ToolNotFound || err.Error() != "" ||
				!strings.Contains(out, "❌ goimports (golang.org/x/tools/cmd/goimports@v0.30.0) — not runnable") ||
				!strings.Contains(out, "0 passed, 1 failed") {
				t.Fatalf("err=%v out=%s", err, out)
			}
		})
	}
}

func TestCheckFallsBackThroughTheProbeArguments(t *testing.T) {
	g := &fakeGo{intercept: func(c gocmd.Call) (gocmd.Result, error, bool) {
		if c.Args[0] != "tool" {
			return gocmd.Result{}, nil, false
		}

		if c.Args[len(c.Args)-1] == "version" {
			return gocmd.Result{}, nil, true
		}

		return gocmd.Result{Code: 1}, nil, true
	}}

	if out, err := checkIn(t, infoFixture(t, "module"), g, nil); err != nil {
		t.Fatalf("err=%v out=%s", err, out)
	}
}

// Offline, a missing module must fail fast instead of reaching for the proxy.
func TestCheckOfflineProbesWithTheProxyOff(t *testing.T) {
	g := &fakeGo{}

	if _, err := checkIn(t, infoFixture(t, "split"), g, []string{"GOTOOLS_OFFLINE=1"}); err != nil {
		t.Fatal(err)
	}

	calls := toolCalls(g)
	if len(calls) == 0 {
		t.Fatal("no tool was probed")
	}

	for _, c := range calls {
		if !slices.Contains(c.Env, "GOPROXY=off") {
			t.Errorf("probe without GOPROXY=off: %+v", c)
		}
	}
}

func TestCheckRequiresGo(t *testing.T) {
	dir := infoFixture(t, "split")

	missing := &fakeGo{intercept: func(gocmd.Call) (gocmd.Result, error, bool) { return gocmd.Result{}, gocmd.ErrNotFound, true }}
	if _, err := checkIn(t, dir, missing, nil); ierrors.CodeOf(err) != ierrors.Environment || !strings.Contains(err.Error(), "Go is not installed") {
		t.Fatalf("missing: %v", err)
	}

	old := &fakeGo{intercept: func(gocmd.Call) (gocmd.Result, error, bool) {
		return gocmd.Result{Stdout: []byte("go1.23.4")}, nil, true
	}}
	if _, err := checkIn(t, dir, old, nil); ierrors.CodeOf(err) != ierrors.Environment || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("old: %v", err)
	}

	broken := &fakeGo{intercept: func(gocmd.Call) (gocmd.Result, error, bool) { return gocmd.Result{}, errors.New("x"), true }}
	if _, err := checkIn(t, dir, broken, nil); ierrors.CodeOf(err) != ierrors.Environment {
		t.Fatalf("broken: %v", err)
	}
}

func TestCheckTakesNoArguments(t *testing.T) {
	g := &fakeGo{}

	out, err := checkIn(t, infoFixture(t, "split"), g, nil, "goimports")
	if ierrors.CodeOf(err) != ierrors.Usage || !strings.Contains(err.Error(), "Unknown argument: goimports") || out != "" || len(g.calls) != 0 {
		t.Fatalf("err=%v out=%q calls=%v", err, out, g.calls)
	}
}

func TestCheckWithNoToolsPasses(t *testing.T) {
	out, err := checkIn(t, newProject(t, "split"), &fakeGo{}, nil)
	if err != nil || !strings.Contains(out, "0 passed, 0 failed") {
		t.Fatalf("err=%v out=%s", err, out)
	}
}

// A tool that rejects every probe flag but builds is a pass; one that does not
// build is a failure.
func TestCheckPassesAToolWithNoProbeFlagThatBuilds(t *testing.T) {
	dir := infoFixture(t, "split")

	exits := func(buildCode int) *fakeGo {
		return &fakeGo{intercept: func(c gocmd.Call) (gocmd.Result, error, bool) {
			if c.Args[0] != "tool" {
				return gocmd.Result{}, nil, false
			}

			if c.Args[1] == "-n" {
				return gocmd.Result{Code: buildCode}, nil, true
			}

			return gocmd.Result{Code: 2, Stderr: []byte("flag provided but not defined")}, nil, true
		}}
	}

	out, err := checkIn(t, dir, exits(0), nil)
	if err != nil || !strings.Contains(out, "✅ goimports") || !strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("builds: err=%v out=%s", err, out)
	}

	out, err = checkIn(t, dir, exits(1), nil)
	if ierrors.CodeOf(err) != ierrors.ToolNotFound || !strings.Contains(out, "not runnable") {
		t.Fatalf("does not build: err=%v out=%s", err, out)
	}
}
