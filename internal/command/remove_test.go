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
	"time"

	"github.com/piusalfred/gotools/internal/command"
	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/manifest"
)

// runCommand runs cmd in dir with g and the environment, returning stdout,
// stderr and the error.
func runCommand(
	t *testing.T,
	cmd command.Command,
	dir string,
	g *fakeGo,
	environ []string,
	args ...string,
) (string, string, error) {
	t.Helper()

	var o, e bytes.Buffer

	c := &command.Context{
		Stdin:   strings.NewReader(""),
		Stdout:  &o,
		Stderr:  &e,
		Environ: environ,
		Program: "gotools",
		WorkDir: dir,
		Go:      g,
	}
	err := run(cmd, c, args...)

	return o.String(), e.String(), err
}

func TestRemoveSplitAndModule(t *testing.T) {
	for st, path := range map[string]string{"split": "tools/goimports.mod", "module": "tools/goimports"} {
		t.Run(st, func(t *testing.T) {
			dir := infoFixture(t, st)
			writeFile(t, filepath.Join(dir, "tools", "goimports.sum"), "x\n")

			out, _, err := runCommand(t, command.NewRemove(), dir, &fakeGo{}, nil, "goimports")
			if err != nil || !strings.Contains(out, "🗑️  Removing goimports...") ||
				!strings.Contains(out, "✅ Removed") {
				t.Fatalf("err=%v out=%s", err, out)
			}

			if exists(filepath.Join(dir, path)) ||
				(st == "split" && exists(filepath.Join(dir, "tools", "goimports.sum"))) {
				t.Fatal("files remain")
			}

			if got := manifestTools(t, dir); len(got) != 0 {
				t.Fatalf("manifest still lists %v", got)
			}
		})
	}
}

func TestRemoveUnifiedDropsTheDirectiveAndTidies(t *testing.T) {
	dir := infoFixture(t, "unified")
	g := &fakeGo{}

	out, _, err := runCommand(t, command.NewRemove(), dir, g, nil, "goimports")
	if err != nil || !strings.Contains(out, "✅ Dropped goimports from tools/go.mod") {
		t.Fatalf("err=%v out=%s", err, out)
	}

	var seen []string

	for _, c := range g.calls {
		if c.Args[0] != "env" {
			seen = append(seen, strings.Join(c.Args, " "))

			if filepath.Base(c.Dir) != "tools" {
				t.Errorf("ran in %s", c.Dir)
			}
		}
	}

	if strings.Join(seen, "|") != "mod edit -droptool=golang.org/x/tools/cmd/goimports|mod tidy" {
		t.Fatalf("calls = %v", seen)
	}

	if len(manifestTools(t, dir)) != 0 {
		t.Fatal("manifest not updated")
	}
}

func TestRemoveDryRunChangesNothing(t *testing.T) {
	dir := infoFixture(t, "split")
	before := tree(t, dir)

	out, _, err := runCommand(t, command.NewRemove(), dir, &fakeGo{}, nil, "goimports", "--dry-run")
	if err != nil || !strings.Contains(out, "🔍 [dry-run] Would remove goimports") {
		t.Fatalf("err=%v out=%s", err, out)
	}

	sameTree(t, before, tree(t, dir))
}

// A bad name must not leave the names before it half-removed.
func TestRemoveValidatesEveryNameFirst(t *testing.T) {
	dir := infoFixture(t, "split")

	_, _, err := runCommand(t, command.NewRemove(), dir, &fakeGo{}, nil, "goimports", "../evil")
	if ierrors.CodeOf(err) != ierrors.Usage || !strings.Contains(err.Error(), "path separator") {
		t.Fatalf("err = %v", err)
	}

	if !exists(filepath.Join(dir, "tools", "goimports.mod")) {
		t.Fatal("goimports was removed before validation finished")
	}
}

// An unknown name fails, but the known ones are still removed.
func TestRemoveUnknownNameFailsButRemovesTheRest(t *testing.T) {
	dir := infoFixture(t, "split")

	_, _, err := runCommand(t, command.NewRemove(), dir, &fakeGo{}, nil, "typo", "goimports")
	if ierrors.CodeOf(err) != ierrors.ToolNotFound || !strings.Contains(err.Error(), "Tool not found: typo") {
		t.Fatalf("err = %v", err)
	}

	if exists(filepath.Join(dir, "tools", "goimports.mod")) {
		t.Fatal("the known tool was not removed")
	}
}

func TestRemoveACleansUpAManifestOnlyEntry(t *testing.T) {
	dir := infoFixture(t, "split")
	_ = os.Remove(filepath.Join(dir, "tools", "goimports.mod"))

	out, _, err := runCommand(t, command.NewRemove(), dir, &fakeGo{}, nil, "goimports")
	if err != nil || !strings.Contains(out, "goimports.mod not found") || len(manifestTools(t, dir)) != 0 {
		t.Fatalf("err=%v out=%s tools=%v", err, out, manifestTools(t, dir))
	}
}

// A failure on a later tool still persists the earlier removals.
func TestRemoveFailureStillFlushesTheManifest(t *testing.T) {
	dir := infoFixture(t, "split")

	m := readManifest(t, dir)
	m.SetTool(manifest.Tool{Name: "other", Source: "go", Package: "example.com/other", Version: "v1.0.0"})

	if err := m.Flush(filepath.Join(dir, config.ManifestFile)); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "elsewhere.mod")
	writeFile(t, target, "module x\n")

	if err := os.Symlink(target, filepath.Join(dir, "tools", "other.mod")); err != nil {
		t.Fatal(err)
	}

	_, _, err := runCommand(t, command.NewRemove(), dir, &fakeGo{}, nil, "goimports", "other")
	if ierrors.CodeOf(err) != ierrors.Environment || !strings.Contains(err.Error(), "Refusing to follow symlink") {
		t.Fatalf("err = %v", err)
	}

	if got := manifestTools(t, dir); len(got) != 1 || got[0] != "other" {
		t.Fatalf("manifest = %v", got)
	}

	if !exists(target) {
		t.Fatal("the symlink target was touched")
	}
}

func TestRemoveNeedsANameAndTheLock(t *testing.T) {
	dir := infoFixture(t, "split")

	if _, _, err := runCommand(t, command.NewRemove(), dir, &fakeGo{}, nil); ierrors.CodeOf(err) != ierrors.Usage {
		t.Fatalf("no args: %v", err)
	}

	holder := &command.Context{
		Stdout:  &bytes.Buffer{},
		Stderr:  &bytes.Buffer{},
		Program: "gotools",
		WorkDir: dir,
		Go:      &fakeGo{},
	}
	if err := holder.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	err := holder.WithLock(context.Background(), func(context.Context) error {
		start := time.Now()

		_, _, err := runCommand(t, command.NewRemove(), dir, &fakeGo{}, []string{"GOTOOLS_LOCK_TIMEOUT=0"}, "goimports")
		if ierrors.CodeOf(err) != ierrors.Lock ||
			!strings.Contains(err.Error(), "Another gotools process is running") ||
			time.Since(start) > 5*time.Second {
			t.Errorf("err = %v", err)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if !exists(filepath.Join(dir, "tools", "goimports.mod")) {
		t.Fatal("removed while another process held the lock")
	}

	if _, _, err := runCommand(
		t,
		command.NewRemove(),
		dir,
		&fakeGo{},
		[]string{"GOTOOLS_NO_LOCK=1"},
		"goimports",
	); err != nil {
		t.Fatalf("GOTOOLS_NO_LOCK: %v", err)
	}
}

func TestRemoveRepeatedNamesRemoveOnce(t *testing.T) {
	dir := infoFixture(t, "split")

	out, _, err := runCommand(t, command.NewRemove(), dir, &fakeGo{}, nil, "goimports", "goimports")
	if err != nil || strings.Count(out, "Removing goimports") != 1 {
		t.Fatalf("err=%v out=%s", err, out)
	}
}

// A request with no name stops before Go is looked at.
func TestRemoveWithNoNameSpendsNoGoCall(t *testing.T) {
	g := &fakeGo{}

	_, _, err := runCommand(t, command.NewRemove(), infoFixture(t, "split"), g, nil)
	if ierrors.CodeOf(err) != ierrors.Usage || !strings.Contains(err.Error(), "Missing argument") || len(g.calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, g.calls)
	}
}
