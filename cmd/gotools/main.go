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

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/piusalfred/gotools"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := Run(ctx, os.Args); err != nil {
		if ctx.Err() != nil {
			code := 128 + int(syscall.SIGINT)

			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				code = exitErr.ExitCode()
			}

			os.Exit(code)
		}

		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}

		fmt.Fprintf(os.Stderr, "gotools: %v\n", err)
		os.Exit(1)
	}
}

// Run executes the embedded gotools.sh script in a child bash process.
//
// Signal handling: the child is placed in its own process group (Setpgid)
// so kernel-delivered signals don't race with our context cancellation.
// On context cancel the Cancel func sends SIGTERM to the whole process
// group, giving the shell script up to WaitDelay (5 s) to clean up
// before Go escalates to SIGKILL.
func Run(ctx context.Context, args []string) error {
	script := []string{"-c", gotools.SCRIPT, "gotools"}
	if len(args) > 1 {
		script = append(script, args[1:]...)
	}

	cmd := exec.CommandContext(ctx, "bash", script...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 5 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("execution failed: %w", err)
	}

	return nil
}
