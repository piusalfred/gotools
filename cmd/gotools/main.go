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

// Command gotools manages Go build tools with `tool` directives, keeping
// their dependencies out of your project's module graph.
package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/piusalfred/gotools"
	"github.com/piusalfred/gotools/internal/cli"
	"github.com/piusalfred/gotools/internal/gocmd"
)

func main() {
	if exitCode := Run(os.Args...); exitCode != 0 {
		os.Exit(exitCode)
	}
}

// Run runs gotools with args (args[0] is the program name) and returns the exit
// code. This is the only place an error becomes output, and main the only place
// the process exits.
func Run(args ...string) int {
	// The context is cancelled by the first SIGINT or SIGTERM, with the signal
	// as its cause: a tool that is running gets that same signal, and the exit
	// code says which one ended the run.
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	defer signal.Stop(signals)

	go func() {
		select {
		case sig := <-signals:
			cancel(gocmd.SignalCause(sig))
		case <-ctx.Done():
		}
	}()

	app := &cli.App{Program: filepath.Base(args[0]), Version: gotools.Version()}

	return signalExit(ctx, app.Report(app.Run(ctx, args[1:])))
}

// signalExitBase is added to a signal's number to make the exit code of a run
// it ended, as shells do.
const signalExitBase = 128

// signalExit is the exit code of a run that a signal cut short: signalExitBase
// plus the signal's number, the shell convention (130 for SIGINT, 143 for
// SIGTERM). A run that succeeded keeps its code even if a signal came after.
func signalExit(ctx context.Context, code int) int {
	if sig, ok := gocmd.SignalOf(ctx); ok && code != 0 {
		if n, ok := sig.(syscall.Signal); ok {
			return signalExitBase + int(n)
		}
	}

	return code
}
