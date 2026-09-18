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
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runCaptured calls Run with os.Stdout and os.Stderr redirected to pipes and
// returns what the child bash process wrote to them.
func runCaptured(t *testing.T, args ...string) (string, error) {
	t.Helper()

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}

	origStdout, origStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, outW

	done := make(chan string, 1)

	go func() {
		data, _ := io.ReadAll(outR)
		done <- string(data)
	}()

	runErr := Run(context.Background(), append([]string{"gotools"}, args...))

	os.Stdout, os.Stderr = origStdout, origStderr
	_ = outW.Close()
	output := <-done
	_ = outR.Close()

	return output, runErr
}

// TestRunVersion is the regression test for the wrapper failing with
// "fork/exec /usr/bin/bash: argument list too long". Linux caps a single argv
// string at MAX_ARG_STRLEN (128 KiB) and gotools.sh is larger than that, so
// the script must not be passed to bash as an argument.
func TestRunVersion(t *testing.T) {
	output, err := runCaptured(t, "version")
	if err != nil {
		t.Fatalf("Run(version) failed: %v (output: %q)", err, output)
	}

	if !strings.Contains(output, "gotools.sh v") {
		t.Errorf("Run(version) printed %q, want it to contain the version", output)
	}
}

// TestRunPassesExecutableAsArgvZero checks that the script still sees the
// gotools executable as $0. self-update, uninstall, and check all depend on
// it: they rewrite $0 or re-invoke it.
func TestRunPassesExecutableAsArgvZero(t *testing.T) {
	execPath, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	// usage() ends in `exit $E_USAGE`, so `help` exits non-zero. Only the
	// text matters here.
	output, _ := runCaptured(t, "help")

	want := "Usage: " + filepath.Base(execPath)
	if !strings.Contains(output, want) {
		t.Errorf("Run(help) printed %q, want it to contain %q", output, want)
	}
}

// TestRunRemovesExtractedScript checks that the temporary copy of the script
// does not outlive the child process.
func TestRunRemovesExtractedScript(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)

	if _, err := runCaptured(t, "version"); err != nil {
		t.Fatalf("Run(version) failed: %v", err)
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("os.ReadDir: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("TMPDIR holds %d leftover entries, want 0", len(entries))
	}
}

// TestRunPropagatesExitCode checks that the script's exit code survives the
// wrapper: an unknown command exits E_USAGE (2).
func TestRunPropagatesExitCode(t *testing.T) {
	_, err := runCaptured(t, "no-such-command")
	if err == nil {
		t.Fatal("Run(no-such-command) succeeded, want a usage error")
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("Run(no-such-command) returned %v, want an *exec.ExitError", err)
	}

	if exitErr.ExitCode() != 2 {
		t.Errorf("exit code %d, want 2 (E_USAGE)", exitErr.ExitCode())
	}
}
