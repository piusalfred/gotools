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

package gocmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
)

// Process is a child run with the caller's own streams, as exec runs a tool.
type Process struct {
	Bin    string   // the program to run
	Args   []string // its arguments
	Dir    string   // child working directory; the process's own when empty
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// ProcessRunner runs a [Process]. The code is the child's exit status, 128+n
// when signal n killed it. The error is non-nil only when the child could not
// be run at all: a child stopped by the context reports its signal as the code.
type ProcessRunner interface {
	Run(ctx context.Context, p Process) (code int, err error)
}

// ExecProcessRunner runs the real program. The child stays in gotools' process
// group, so the terminal delivers Ctrl-C to it directly and it may read the
// terminal; cancelling the context sends it SIGTERM, and it is killed if it
// does not stop. If the context was cancelled with SignalCause, the child gets
// that signal, else SIGTERM.
type ExecProcessRunner struct{}

var _ ProcessRunner = ExecProcessRunner{}

// Run implements ProcessRunner.
func (ExecProcessRunner) Run(ctx context.Context, p Process) (int, error) {
	cmd := exec.CommandContext(ctx, p.Bin, p.Args...) //nolint:gosec // G204: exec runs the tool the manifest names
	cmd.Dir = p.Dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = p.Stdin, p.Stdout, p.Stderr
	terminateOnCancel(ctx, cmd)

	err := cmd.Run()

	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitCode(ee), nil
	}

	if ctx.Err() != nil {
		return 0, fmt.Errorf("run %s: %w", p.Bin, ctx.Err())
	}

	if err != nil {
		return 0, fmt.Errorf("run %s: %w", p.Bin, err)
	}

	return 0, nil
}
