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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piusalfred/gotools/internal/command"
	ierrors "github.com/piusalfred/gotools/internal/errors"
)

func uninstallIn(t *testing.T, exe, stdin string, args ...string) (out string, err error) {
	t.Helper()

	var o bytes.Buffer

	c := &command.Context{
		Stdin: strings.NewReader(stdin), Stdout: &o, Stderr: &bytes.Buffer{}, Program: "gotools", WorkDir: t.TempDir(),
		Exe: func() (string, error) { return exe, nil },
	}
	err = run(command.NewUninstall(), c, args...)

	return o.String(), err
}

func fakeExe(t *testing.T) string {
	t.Helper()

	exe := filepath.Join(t.TempDir(), "gotools")
	writeFile(t, exe, "#!/bin/sh\n")

	return exe
}

func TestUninstallRemovesTheRunningBinary(t *testing.T) {
	for _, answer := range []string{"y\n", "Y\n", "yes\n", " YES \n", "y"} {
		exe := fakeExe(t)

		out, err := uninstallIn(t, exe, answer)
		if err != nil || !strings.Contains(out, "gotools has been uninstalled") || exists(exe) {
			t.Fatalf("%q: err=%v out=%q exists=%v", answer, err, out, exists(exe))
		}
	}
}

func TestUninstallCancelled(t *testing.T) {
	for _, answer := range []string{"n\n", "\n", "", "maybe\n", "yy\n"} {
		exe := fakeExe(t)

		out, err := uninstallIn(t, exe, answer)
		if err != nil || !strings.Contains(out, "Uninstall cancelled") || !exists(exe) {
			t.Fatalf("%q: err=%v out=%q exists=%v", answer, err, out, exists(exe))
		}
	}
}

func TestUninstallWithNoStdinIsACancel(t *testing.T) {
	exe := fakeExe(t)

	c := &command.Context{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Program: "gotools", Exe: func() (string, error) { return exe, nil }}
	if err := run(command.NewUninstall(), c); err != nil || !exists(exe) {
		t.Fatalf("err=%v exists=%v", err, exists(exe))
	}
}

func TestUninstallDryRunAndUsage(t *testing.T) {
	exe := fakeExe(t)

	// Temp dirs may sit behind a symlink (macOS /var): the real path is shown.
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}

	out, err := uninstallIn(t, exe, "y\n", "--dry-run")
	if err != nil || !strings.Contains(out, "[dry-run] Would remove "+resolved) || strings.Contains(out, "(y/N)") || !exists(exe) {
		t.Fatalf("err=%v out=%q", err, out)
	}

	for _, args := range [][]string{{"--force"}, {"extra"}} {
		if _, err := uninstallIn(t, exe, "y\n", args...); ierrors.CodeOf(err) != ierrors.Usage || !exists(exe) {
			t.Fatalf("%v: err=%v", args, err)
		}
	}
}

func TestUninstallNamesWhatItWillDeleteAndWhatItKeeps(t *testing.T) {
	exe := fakeExe(t)

	out, _ := uninstallIn(t, exe, "n\n")
	for _, want := range []string{"WARNING: This will delete the gotools executable (", "Projects keep their .gotools.json and tools directory", "(y/N): "} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

// Started through a symlink, both the link and the binary go.
func TestUninstallRemovesTheSymlinkAndItsTarget(t *testing.T) {
	target := fakeExe(t)
	link := filepath.Join(t.TempDir(), "gotools")

	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	out, err := uninstallIn(t, link, "y\n")
	if err != nil {
		t.Fatalf("err=%v out=%q", err, out)
	}

	if _, err := os.Lstat(link); err == nil {
		t.Fatal("symlink left behind")
	}

	if exists(target) {
		t.Fatal("binary left behind")
	}
}

func TestUninstallReportsARemovalFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can remove anything")
	}

	exe := fakeExe(t)
	dir := filepath.Dir(exe)

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}

	defer func() { _ = os.Chmod(dir, 0o755) }()

	_, err := uninstallIn(t, exe, "y\n")
	if ierrors.CodeOf(err) != ierrors.Environment || !strings.Contains(err.Error(), "Could not remove") {
		t.Fatalf("err = %v", err)
	}
}

func TestUninstallWhenTheBinaryIsAlreadyGone(t *testing.T) {
	exe := fakeExe(t)
	_ = os.Remove(exe)

	if out, err := uninstallIn(t, exe, "y\n"); err != nil || !strings.Contains(out, "has been uninstalled") {
		t.Fatalf("err=%v out=%q", err, out)
	}
}

func TestUninstallCannotLocateItself(t *testing.T) {
	c := &command.Context{Stdin: strings.NewReader("y\n"), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Program: "gotools", Exe: func() (string, error) { return "", os.ErrNotExist }}

	if err := run(command.NewUninstall(), c); ierrors.CodeOf(err) != ierrors.Environment {
		t.Fatalf("err = %v", err)
	}
}

func TestUninstallDryRunNamesTheLinkAndItsTarget(t *testing.T) {
	target := fakeExe(t)
	link := filepath.Join(t.TempDir(), "gotools")

	if err := os.Symlink(target, link); err != nil {
		t.Skip(err)
	}

	resolved, _ := filepath.EvalSymlinks(target)

	out, err := uninstallIn(t, link, "", "--dry-run")
	if err != nil || !strings.Contains(out, "Would remove "+link+" and "+resolved) {
		t.Fatalf("err=%v out=%q", err, out)
	}
}
