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

package gocmd

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// isolate puts the child in its own process group and makes cancellation
// signal the whole group. After WaitDelay the leader is killed outright.
func isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 3 * time.Second
}

// terminateOnCancel signals only the direct child (it shares our process group, so
// the terminal already reaches it) and kills it after WaitDelay. The child gets
// the signal that ended ctx when there was one (see cancelSignal), so a SIGINT
// stops it with the status a SIGINT does, 130, and not as if it were told to
// terminate.
func terminateOnCancel(ctx context.Context, cmd *exec.Cmd) {
	cmd.Cancel = func() error { return cmd.Process.Signal(cancelSignal(ctx)) }
	cmd.WaitDelay = 3 * time.Second
}

// cancelSignal is the signal that cancelled ctx (see SignalCause), else SIGTERM.
func cancelSignal(ctx context.Context) os.Signal {
	if sig, ok := SignalOf(ctx); ok {
		return sig
	}

	return syscall.SIGTERM
}

// exitCode maps a finished child to the shell convention: its exit code, or
// 128+signal when a signal killed it (ExitCode reports -1 then).
func exitCode(ee *exec.ExitError) int {
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}

	return ee.ExitCode()
}
