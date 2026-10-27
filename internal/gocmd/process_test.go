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

//go:build unix

package gocmd_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/piusalfred/gotools/internal/gocmd"
)

func sh(script string, p gocmd.Process) gocmd.Process {
	p.Bin, p.Args = "sh", []string{"-c", script, "sh"}

	return p
}

// sleepsUntilStopped must exec: the runner signals only the direct child, and a
// shell that forks sleep instead (dash does, bash and macOS sh do not) defers a
// SIGINT until sleep ends, so the child would be killed after WaitDelay (137)
// and not stopped by the signal under test.
const sleepsUntilStopped = "exec sleep 30"

func TestProcessPassesTheStreamsThrough(t *testing.T) {
	var out, errb bytes.Buffer

	code, err := gocmd.ExecProcessRunner{}.Run(
		context.Background(),
		sh(`read x; echo "out:$x $1"; echo "err:$x" >&2`, gocmd.Process{
			Stdin: strings.NewReader("hello\n"), Stdout: &out, Stderr: &errb, Args: nil,
		}),
	)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}

	if out.String() != "out:hello \n" || errb.String() != "err:hello\n" {
		t.Fatalf("out=%q err=%q", out.String(), errb.String())
	}
}

func TestProcessGetsItsArgumentsVerbatim(t *testing.T) {
	var out bytes.Buffer

	p := gocmd.Process{
		Bin:    "sh",
		Args:   []string{"-c", `printf '%s|' "$@"`, "sh", "a b", "--flag", "", "$HOME"},
		Stdout: &out,
	}
	if _, err := (gocmd.ExecProcessRunner{}).Run(context.Background(), p); err != nil {
		t.Fatal(err)
	}

	if out.String() != "a b|--flag||$HOME|" {
		t.Fatalf("out=%q", out.String())
	}
}

func TestProcessRunsInItsDirectory(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	if _, err := (gocmd.ExecProcessRunner{}).Run(
		context.Background(),
		sh("pwd", gocmd.Process{Dir: dir, Stdout: &out}),
	); err != nil {
		t.Fatal(err)
	}

	if got := strings.TrimSpace(out.String()); got != dir {
		t.Fatalf("pwd = %q, want %q", got, dir)
	}

	// The caller's own directory is untouched.
	if wd, _ := os.Getwd(); wd == dir {
		t.Fatal("the process changed the working directory")
	}
}

// A tool's exit status is its own, not an error of the runner.
func TestProcessReportsTheExitStatus(t *testing.T) {
	for script, want := range map[string]int{"exit 0": 0, "exit 3": 3, "exit 42": 42, `kill -TERM $$`: 128 + 15, `kill -9 $$`: 128 + 9} {
		code, err := gocmd.ExecProcessRunner{}.Run(context.Background(), sh(script, gocmd.Process{}))
		if err != nil || code != want {
			t.Errorf("%q: code=%d err=%v, want %d", script, code, err, want)
		}
	}
}

func TestProcessThatCannotStartIsAnError(t *testing.T) {
	code, err := gocmd.ExecProcessRunner{}.Run(
		context.Background(),
		gocmd.Process{Bin: filepath.Join(t.TempDir(), "nope")},
	)
	if err == nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

// The tool is told to stop (SIGTERM), and its status says so.
func TestProcessStopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()

	code, err := gocmd.ExecProcessRunner{}.Run(ctx, sh(sleepsUntilStopped, gocmd.Process{}))
	if err != nil || code != 128+15 || ctx.Err() == nil {
		t.Fatalf("code=%d err=%v ctx=%v", code, err, ctx.Err())
	}

	if time.Since(start) > 10*time.Second {
		t.Fatalf("took %s to stop", time.Since(start))
	}
}

// A context cancelled by a signal passes that signal on, so the child ends with
// the status that signal gives and not as if it had been told to terminate.
func TestProcessGetsTheSignalThatCancelledTheContext(t *testing.T) {
	for sig, want := range map[syscall.Signal]int{syscall.SIGINT: 128 + 2, syscall.SIGHUP: 128 + 1, syscall.SIGTERM: 128 + 15} {
		ctx, cancel := context.WithCancelCause(context.Background())
		time.AfterFunc(100*time.Millisecond, func() { cancel(gocmd.SignalCause(sig)) })

		code, err := gocmd.ExecProcessRunner{}.Run(ctx, sh(sleepsUntilStopped, gocmd.Process{}))
		if err != nil || code != want {
			t.Errorf("%v: code=%d err=%v, want %d", sig, code, err, want)
		}
	}

	// Any other cause is a plain SIGTERM.
	ctx, cancel := context.WithCancelCause(context.Background())
	time.AfterFunc(100*time.Millisecond, func() { cancel(errors.New("done")) })

	if code, err := (gocmd.ExecProcessRunner{}).Run(
		ctx,
		sh(sleepsUntilStopped, gocmd.Process{}),
	); err != nil ||
		code != 128+15 {
		t.Errorf("code=%d err=%v", code, err)
	}
}

func TestSignalOf(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())

	if _, ok := gocmd.SignalOf(ctx); ok {
		t.Fatal("a live context has a signal")
	}

	cancel(gocmd.SignalCause(syscall.SIGINT))

	if sig, ok := gocmd.SignalOf(ctx); !ok || sig != syscall.SIGINT {
		t.Fatalf("SignalOf = %v, %v", sig, ok)
	}

	if _, ok := gocmd.SignalOf(context.Background()); ok {
		t.Fatal("the background context has a signal")
	}

	other, stop := context.WithCancelCause(context.Background())
	stop(errors.New("x"))

	if _, ok := gocmd.SignalOf(other); ok {
		t.Fatal("another cause counts as a signal")
	}
}
