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
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piusalfred/gotools/internal/command"
	ierrors "github.com/piusalfred/gotools/internal/errors"
)

// A command that promises to write nothing must not even clean up after a
// crashed write.
func TestLoadReadOnlyLeavesStaleTempFiles(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		dir := newProject(t, "split")
		stale := filepath.Join(dir, ".gotools.json.tmp.123")
		writeFile(t, stale, "half a manifest")

		c := &command.Context{
			Stdout:   &bytes.Buffer{},
			Stderr:   &bytes.Buffer{},
			WorkDir:  dir,
			Go:       &fakeGo{},
			ReadOnly: readOnly,
		}
		if err := c.Load(context.Background()); err != nil {
			t.Fatal(err)
		}

		_, err := os.Stat(stale)
		if kept := err == nil; kept != readOnly {
			t.Errorf("ReadOnly=%v: temp file kept=%v", readOnly, kept)
		}
	}
}

func TestExecutable(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "gotools")
	writeFile(t, target, "bin")

	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	c := &command.Context{Exe: func() (string, error) { return link, nil }}

	if got, err := c.Executable(); err != nil || got != target {
		t.Fatalf("Executable = %q, %v; want %q", got, err, target)
	}

	// A path that does not resolve is returned as it is.
	gone := filepath.Join(dir, "gone")

	if got, err := (&command.Context{Exe: func() (string, error) { return gone, nil }}).Executable(); err != nil ||
		got != gone {
		t.Fatalf("Executable = %q, %v", got, err)
	}

	if got, err := (&command.Context{}).Executable(); err != nil || got == "" {
		t.Fatalf("default Executable = %q, %v", got, err)
	}
}

func TestExecutableFailureIsAnEnvironmentError(t *testing.T) {
	c := &command.Context{Exe: func() (string, error) { return "", os.ErrNotExist }}

	_, err := c.Executable()
	if ierrors.CodeOf(err) != ierrors.Environment ||
		!strings.Contains(err.Error(), "Could not locate the gotools executable") {
		t.Fatalf("err = %v", err)
	}
}

func TestExecutablePathsKeepTheLinkItWasStartedThrough(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "gotools")
	writeFile(t, target, "bin")

	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skip(err)
	}

	started, resolved, err := (&command.Context{Exe: func() (string, error) { return link, nil }}).ExecutablePaths()
	if err != nil || started != link || resolved != target {
		t.Fatalf("started=%q resolved=%q err=%v", started, resolved, err)
	}
}
