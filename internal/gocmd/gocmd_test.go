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

package gocmd_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/piusalfred/gotools/internal/gocmd"
)

type slow struct{ d time.Duration }

func (s slow) Run(ctx context.Context, _ gocmd.Call) (gocmd.Result, error) {
	select {
	case <-time.After(s.d):
		return gocmd.Result{}, nil
	case <-ctx.Done():
		return gocmd.Result{}, ctx.Err()
	}
}

func TestWithTimeoutBoundsTheCall(t *testing.T) {
	_, err := gocmd.WithTimeout(slow{time.Minute}, 20*time.Millisecond).Run(context.Background(), gocmd.Call{})

	te, ok := errors.AsType[*gocmd.TimeoutError](err)
	if !ok || te.After != 20*time.Millisecond || !strings.Contains(err.Error(), "timed out after 20ms") {
		t.Fatalf("err = %v", err)
	}
}

func TestWithTimeoutReportsACallerCancellationAsItself(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)

	_, err := gocmd.WithTimeout(slow{time.Minute}, time.Minute).Run(ctx, gocmd.Call{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}

	if _, ok := errors.AsType[*gocmd.TimeoutError](err); ok {
		t.Fatal("a cancellation was reported as a timeout")
	}
}

func TestWithTimeoutZeroDisablesTheBound(t *testing.T) {
	r := slow{10 * time.Millisecond}

	if got := gocmd.WithTimeout(r, 0); got != gocmd.Runner(r) {
		t.Fatalf("a zero bound wrapped the runner: %T", got)
	}

	if _, err := gocmd.WithTimeout(r, -1).Run(context.Background(), gocmd.Call{}); err != nil {
		t.Fatal(err)
	}
}

func TestExecRunnerMissingBinary(t *testing.T) {
	_, err := gocmd.ExecRunner{Bin: "definitely-not-a-go-binary"}.Run(context.Background(), gocmd.Call{})
	if !errors.Is(err, gocmd.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestExecRunnerCapturesOutputAndExitCode(t *testing.T) {
	dir := t.TempDir()

	var verbose strings.Builder

	r := gocmd.ExecRunner{Bin: "sh", Verbose: &verbose}

	res, err := r.Run(context.Background(), gocmd.Call{Dir: dir, Args: []string{"-c", "pwd; echo out; echo err >&2; echo $GOCMD_TEST; exit 3"}, Env: []string{"GOCMD_TEST=yes"}})
	if err != nil || res.Code != 3 {
		t.Fatalf("res=%+v err=%v", res, err)
	}

	want, _ := filepath.EvalSymlinks(dir)

	lines := strings.Split(strings.TrimSpace(string(res.Stdout)), "\n")
	if got, _ := filepath.EvalSymlinks(lines[0]); got != want || lines[1] != "out" || lines[2] != "yes" {
		t.Fatalf("stdout %q (dir %q)", res.Stdout, want)
	}

	if strings.TrimSpace(string(res.Stderr)) != "err" {
		t.Fatalf("stderr %q", res.Stderr)
	}

	if !strings.HasPrefix(verbose.String(), "  ↳ go -c ") {
		t.Fatalf("verbose %q", verbose.String())
	}
}

func TestExecRunnerReportsSignalDeathsAsShellStyleCodes(t *testing.T) {
	res, err := gocmd.ExecRunner{Bin: "sh"}.Run(context.Background(), gocmd.Call{Args: []string{"-c", "kill -TERM $$"}})
	if err != nil || res.Code != 128+int(syscall.SIGTERM) {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestExecRunnerStopsTheWholeProcessGroupOnCancel(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "grandchild-survived")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()

	_, err := gocmd.ExecRunner{Bin: "sh"}.Run(ctx, gocmd.Call{Args: []string{"-c", "(sleep 1; touch " + marker + ") & wait"}})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Fatalf("err=%v after %s", err, time.Since(start))
	}

	time.Sleep(1200 * time.Millisecond)

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the grandchild outlived the cancelled call")
	}
}

// A timeout bounds any runner, so the same code runs with the real go or a
// fake.
func ExampleWithTimeout() {
	var r gocmd.Runner = slow{time.Minute}

	_, err := gocmd.WithTimeout(r, 10*time.Millisecond).Run(context.Background(), gocmd.Call{Args: []string{"get", "-tool", "example.com/x"}})

	fmt.Println(err)

	// Output:
	// timed out after 10ms
}

// A deadline the caller set is the caller's, not this runner's bound.
func TestWithTimeoutReportsACallerDeadlineAsItself(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := gocmd.WithTimeout(slow{time.Minute}, time.Minute).Run(ctx, gocmd.Call{})
	if _, ok := errors.AsType[*gocmd.TimeoutError](err); ok || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestExecRunnerEnvIsAddedToEveryCallAndACallsOwnEnvWins(t *testing.T) {
	r := gocmd.ExecRunner{Env: []string{"GOPROXY=runner", "GOFLAGS=-mod=mod"}}

	res, err := r.Run(context.Background(), gocmd.Call{Dir: t.TempDir(), Env: []string{"GOFLAGS=-mod=readonly"}, Args: []string{"env", "GOPROXY", "GOFLAGS"}})
	if err != nil {
		t.Skipf("go is not available: %v", err)
	}

	if got := string(res.Stdout); got != "runner\n-mod=readonly\n" {
		t.Fatalf("env = %q", got)
	}
}
