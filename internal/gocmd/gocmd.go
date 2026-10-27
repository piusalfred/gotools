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

// Package gocmd runs the go command.
//
// [Runner] is the seam: [ExecRunner] runs the real binary, [WithTimeout]
// bounds any runner, and tests substitute a fake. A Runner returns an error
// only when go could not be run at all; a non-zero exit is reported in
// [Result].Code.
package gocmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ErrNotFound reports that the go command is not installed.
var ErrNotFound = errors.New("go not installed")

// killDelay is how long a child that was signalled to stop gets before it is
// killed outright.
const killDelay = 3 * time.Second

// Call is one invocation of the go command. The process working directory is
// never changed; Dir is applied to the child only.
type Call struct {
	Dir  string   // child working directory
	Env  []string // KEY=VALUE pairs overriding the process environment
	Args []string // arguments after "go"
}

// Result is a finished go invocation.
type Result struct {
	Stdout []byte
	Stderr []byte
	Code   int
}

// Runner runs the go command. The error is non-nil only when go could not be
// run (ErrNotFound, a cancelled context, a *TimeoutError).
type Runner interface {
	Run(ctx context.Context, c Call) (Result, error)
}

// ExecRunner runs the real go binary with stdin empty. The child gets its own
// process group, so cancelling stops the compiler and linker go spawns too.
type ExecRunner struct {
	Bin     string    // defaults to "go" from PATH
	Verbose io.Writer // when set, "  ↳ go <args>" is written before each call
	// Env is KEY=VALUE pairs added to the environment of every call; a call's
	// own Env still wins over it.
	Env []string
}

var _ Runner = ExecRunner{}

// Run implements Runner.
func (g ExecRunner) Run(ctx context.Context, c Call) (Result, error) {
	bin := g.Bin
	if bin == "" {
		bin = "go"
	}

	// Resolve up front: a start failure is otherwise indistinguishable from a
	// missing Dir (both ENOENT).
	bin, err := exec.LookPath(bin)
	if err != nil {
		return Result{}, ErrNotFound
	}

	if g.Verbose != nil {
		fmt.Fprintf(g.Verbose, "  ↳ go %s\n", strings.Join(c.Args, " "))
	}

	var stdout, stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, bin, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = append(append(os.Environ(), g.Env...), c.Env...) // later entries win
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	isolate(cmd)

	err = cmd.Run()
	res := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}

	if ctx.Err() != nil {
		return res, fmt.Errorf("run %s: %w", bin, ctx.Err())
	}

	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		res.Code = exitCode(ee)

		return res, nil
	}

	if err != nil {
		return res, fmt.Errorf("run %s: %w", bin, err)
	}

	return res, nil
}

// TimeoutError reports that a bounded run exceeded its limit.
type TimeoutError struct {
	After time.Duration
}

func (e *TimeoutError) Error() string { return fmt.Sprintf("timed out after %s", e.After) }

// WithTimeout returns a Runner whose calls are bounded by d. A d of 0 or less
// disables the bound. A context cancelled by the caller is reported as itself,
// not as a timeout.
func WithTimeout(r Runner, d time.Duration) Runner {
	if d <= 0 {
		return r
	}

	return bounded{inner: r, d: d}
}

type bounded struct {
	inner Runner
	d     time.Duration
}

func (b bounded) Run(ctx context.Context, c Call) (Result, error) {
	tctx, cancel := context.WithTimeout(ctx, b.d)
	defer cancel()

	res, err := b.inner.Run(tctx, c)
	if err != nil && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		return res, &TimeoutError{After: b.d}
	}

	return res, err
}
