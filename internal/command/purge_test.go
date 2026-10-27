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
	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/manifest"
)

func purgeIn(t *testing.T, g *fakeGo, dir, stdin string, args ...string) (string, string, error) {
	t.Helper()

	c, o, e := installContext(dir, g, installOpts{stdin: stdin})
	err := run(command.NewPurge(), c, args...)

	return o.String(), e.String(), err
}

// purgeProject is an installed project with a root module and source file.
func purgeProject(t *testing.T, strategy string) string {
	t.Helper()

	dir := installedProject(t, strategy)
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.24\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n")

	return dir
}

func TestPurgeConfirmedRemovesToolsAndManifestOnly(t *testing.T) {
	for _, st := range []string{"split", "module", "unified"} {
		t.Run(st, func(t *testing.T) {
			dir := purgeProject(t, st)

			out, _, err := purgeIn(t, installGo(installOpts{}), dir, "YES\n")
			if err != nil || !strings.Contains(out, "Purge complete") {
				t.Fatalf("err=%v out=%q", err, out)
			}

			if exists(filepath.Join(dir, "tools")) || exists(filepath.Join(dir, config.ManifestFile)) {
				t.Fatalf("left behind: %v", keys(tree(t, dir)))
			}

			if got := keys(tree(t, dir)); len(got) != 2 || got[0] != "go.mod" || got[1] != "main.go" {
				t.Fatalf("project files = %v", got)
			}
		})
	}
}

func TestPurgeCancelled(t *testing.T) {
	for name, stdin := range map[string]string{"no": "no\n", "lowercase": "yes\n", "empty": "", "eof": "YE"} {
		dir := purgeProject(t, "split")
		before := tree(t, dir)

		out, _, err := purgeIn(t, installGo(installOpts{}), dir, stdin)
		if err != nil || !strings.Contains(out, "Purge cancelled") {
			t.Fatalf("%s: err=%v out=%q", name, err, out)
		}

		sameTree(t, before, tree(t, dir))
	}
}

func TestPurgeDryRun(t *testing.T) {
	dir := purgeProject(t, "split")
	before := tree(t, dir)

	out, _, err := purgeIn(t, installGo(installOpts{}), dir, "", "--dry-run")
	if err != nil || !strings.Contains(out, "Would delete:") ||
		!strings.Contains(out, "  - tools/ (tools directory)") ||
		!strings.Contains(out, "  - .gotools.json (manifest)") ||
		strings.Contains(out, "Are you sure") {
		t.Fatalf("err=%v out=%q", err, out)
	}

	sameTree(t, before, tree(t, dir))

	// With a manifest but no tools directory yet, a dry run creates nothing.
	empty := newProject(t, "split")

	if _, _, err := purgeIn(t, installGo(installOpts{}), empty, "", "--dry-run"); err != nil {
		t.Fatal(err)
	}

	if exists(filepath.Join(empty, "tools")) {
		t.Fatal("dry run created the tools directory")
	}
}

// No manifest means this is not a gotools project, so a tools/ directory in it
// is not ours to delete.
func TestPurgeWithoutManifestTouchesNothing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "tools/keep.txt"), "mine\n")

	out, _, err := purgeIn(t, installGo(installOpts{}), dir, "YES\n")
	if err != nil || !strings.Contains(out, "nothing to purge") || strings.Contains(out, "Are you sure") {
		t.Fatalf("err=%v out=%q", err, out)
	}

	if readFile(t, filepath.Join(dir, "tools/keep.txt")) != "mine\n" {
		t.Fatal("tools/keep.txt was touched")
	}
}

// A typo must not turn a restoring purge into a destructive one.
func TestPurgeRejectsUnknownArguments(t *testing.T) {
	dir := purgeProject(t, "split")
	before := tree(t, dir)

	for _, arg := range []string{"--restor", "--force", "tools"} {
		_, _, err := purgeIn(t, installGo(installOpts{}), dir, "YES\n", arg)
		if ierrors.CodeOf(err) != ierrors.Usage || !strings.Contains(err.Error(), arg) {
			t.Fatalf("%s: err=%v", arg, err)
		}
	}

	sameTree(t, before, tree(t, dir))
}

func TestPurgeRestoreAddsToolsBackToGoMod(t *testing.T) {
	dir := purgeProject(t, "split")
	g := installGo(installOpts{})

	out, _, err := purgeIn(t, g, dir, "YES\n", "--restore")
	if err != nil {
		t.Fatalf("err=%v out=%q", err, out)
	}

	for _, want := range []string{
		"It will also add the managed tools back to your go.mod.",
		"⬆ x (" + pkgX + "@v1.0.0) → go.mod",
		"Restored 2 tool(s) to go.mod.",
		"💡 To come back: gotools init --strategy=split --prefix=tools",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}

	mod := readFile(t, filepath.Join(dir, "go.mod"))
	if !strings.Contains(mod, "tool "+pkgX) || !strings.Contains(mod, "tool "+pkgY) ||
		!strings.Contains(mod, "v1.2.0") {
		t.Fatalf("go.mod:\n%s", mod)
	}

	if g.rootCalls(dir, "get", "-tool", pkgX+"@v1.0.0") != 1 {
		t.Fatalf("calls: %v", g.calls)
	}

	if exists(filepath.Join(dir, "tools")) || exists(filepath.Join(dir, config.ManifestFile)) {
		t.Fatal("not wiped after restore")
	}
}

func TestPurgeRestoreDryRun(t *testing.T) {
	dir := purgeProject(t, "split")
	before := tree(t, dir)
	g := installGo(installOpts{})

	out, _, err := purgeIn(t, g, dir, "", "--restore", "--dry-run")
	if err != nil {
		t.Fatalf("err=%v out=%q", err, out)
	}

	for _, want := range []string{
		"[dry-run] Would restore x (" + pkgX + "@v1.0.0) to go.mod",
		"[dry-run] Would restore 2 tool(s) to go.mod.",
		"[dry-run] To come back: gotools init --strategy=split --prefix=tools",
		"Would delete:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}

	sameTree(t, before, tree(t, dir))
	noInstalls(t, g)
}

func TestPurgeRestoreHintCarriesTheConfiguration(t *testing.T) {
	dir := t.TempDir()

	m := manifest.New()
	m.Strategy, m.Dir, m.GoVersion, m.ModulePrefix = "module", ".tools dir", "1.24", "example.com/test"

	if err := m.Flush(filepath.Join(dir, config.ManifestFile)); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.24\n")

	out, _, err := purgeIn(t, installGo(installOpts{}), dir, "", "--restore", "--dry-run")
	if err != nil ||
		!strings.Contains(
			out,
			"To come back: gotools init --strategy=module '--dir=.tools dir' --go=1.24 --prefix=example.com/test",
		) {
		t.Fatalf("err=%v out=%q", err, out)
	}
}

// A failed restore keeps gotools intact and puts the root go.mod back exactly
// as it was.
func TestPurgeRestoreFailureChangesNothing(t *testing.T) {
	dir := purgeProject(t, "split")
	before := tree(t, dir)

	// x is added to go.mod first, then y fails.
	_, _, err := purgeIn(t, failingOn("example.com/b"), dir, "YES\n", "--restore")
	if err == nil || !strings.Contains(err.Error(), "Failed to restore y") {
		t.Fatalf("err = %v", err)
	}

	sameTree(t, before, tree(t, dir))

	// A network failure keeps its exit code.
	_, _, err = purgeIn(
		t,
		installGo(installOpts{getFail: "dial tcp: lookup proxy.golang.org: no such host"}),
		dir,
		"YES\n",
		"--restore",
	)
	if ierrors.CodeOf(err) != ierrors.Network {
		t.Fatalf("network: err=%v", err)
	}

	sameTree(t, before, tree(t, dir))
}

func TestPurgeRestoreNeedsARootGoMod(t *testing.T) {
	dir := installedProject(t, "split")

	if err := os.Remove(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatal(err)
	}

	before := tree(t, dir)

	_, _, err := purgeIn(t, installGo(installOpts{}), dir, "YES\n", "--restore")
	if ierrors.CodeOf(err) != ierrors.Environment || !strings.Contains(err.Error(), "no go.mod in project root") {
		t.Fatalf("err = %v", err)
	}

	sameTree(t, before, tree(t, dir))
}

func TestPurgeRestoreRefusesASymlinkedRootGoMod(t *testing.T) {
	dir := installedProject(t, "split")
	target := filepath.Join(dir, "real.mod")
	writeFile(t, target, "module example.com/test\n\ngo 1.24\n")

	if err := os.Symlink(target, filepath.Join(dir, "go.mod")); err != nil {
		t.Skip(err)
	}

	_, _, err := purgeIn(t, installGo(installOpts{}), dir, "YES\n", "--restore")
	if ierrors.CodeOf(err) != ierrors.Environment || !strings.Contains(err.Error(), "Refusing to follow symlink") {
		t.Fatalf("err = %v", err)
	}
}

func TestPurgeRestoreSkipsToolsThatAreNotGoTools(t *testing.T) {
	dir := newProject(t, "split")
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.24\n")

	m := readManifest(t, dir)
	m.SetTool(manifest.Tool{Name: "bin", Source: "binary", Package: "example.com/bin", Version: "v1"})

	if err := m.Flush(filepath.Join(dir, config.ManifestFile)); err != nil {
		t.Fatal(err)
	}

	g := installGo(installOpts{})

	out, _, err := purgeIn(t, g, dir, "YES\n", "--restore")
	if err != nil || !strings.Contains(out, "bin: source 'binary' — skipping") ||
		!strings.Contains(out, "No tools to restore.") ||
		g.rootCalls(dir, "get") != 0 {
		t.Fatalf("err=%v out=%q", err, out)
	}
}

// Package paths from the committed manifest reach `go get` as arguments, so one
// that looks like a flag must never get there.
func TestFlagLikePackagesFromTheManifestAreRefused(t *testing.T) {
	dir := syncProject(t, "split", [3]string{"evil", "-toolexec=/tmp/x", "v1.0.0"})
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.24\n")

	g := installGo(installOpts{})

	if r := syncIn(
		t,
		g,
		dir,
		installOpts{},
	); r.code() != ierrors.Environment ||
		!strings.Contains(r.runErr.Error(), "Invalid package") {
		t.Fatalf("sync: err=%v", r.runErr)
	}

	if _, _, err := purgeIn(
		t,
		g,
		dir,
		"YES\n",
		"--restore",
	); ierrors.CodeOf(err) != ierrors.Environment ||
		!strings.Contains(err.Error(), "Invalid package") {
		t.Fatalf("purge: err=%v", err)
	}

	for _, c := range g.calls {
		for _, a := range c.Args {
			if strings.HasPrefix(a, "-toolexec") {
				t.Fatalf("go was called with %v", c.Args)
			}
		}
	}

	if _, err := os.Stat(filepath.Join(dir, config.ManifestFile)); err != nil {
		t.Fatal("manifest removed")
	}
}

// Only a restoring purge needs Go; a plain purge works without it.
func TestPurgeNeedsGoOnlyToRestore(t *testing.T) {
	missing := func() *fakeGo {
		return &fakeGo{
			intercept: func(gocmd.Call) (gocmd.Result, error, bool) { return gocmd.Result{}, gocmd.ErrNotFound, true },
		}
	}

	dir := purgeProject(t, "split")
	before := tree(t, dir)

	if _, _, err := purgeIn(t, missing(), dir, "YES\n", "--restore"); ierrors.CodeOf(err) != ierrors.Environment {
		t.Fatalf("restore: err=%v", err)
	}

	sameTree(t, before, tree(t, dir))

	if out, _, err := purgeIn(t, missing(), dir, "YES\n"); err != nil || !strings.Contains(out, "Purge complete") {
		t.Fatalf("plain: err=%v out=%q", err, out)
	}
}

// The prompt comes before the lock, so a purge that is declined never waits on
// another process.
func TestPurgeAsksBeforeTakingTheLock(t *testing.T) {
	dir := purgeProject(t, "split")

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
		c, o, _ := installContext(
			dir,
			installGo(installOpts{}),
			installOpts{stdin: "no\n", environ: []string{"GOTOOLS_LOCK_TIMEOUT=0"}},
		)
		if err := run(command.NewPurge(), c); err != nil || !strings.Contains(o.String(), "Purge cancelled") {
			t.Errorf("declined: err=%v out=%q", err, o.String())
		}

		c, _, _ = installContext(
			dir,
			installGo(installOpts{}),
			installOpts{stdin: "YES\n", environ: []string{"GOTOOLS_LOCK_TIMEOUT=0"}},
		)
		if err := run(command.NewPurge(), c); ierrors.CodeOf(err) != ierrors.Lock {
			t.Errorf("confirmed: err=%v", err)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A bad package after good ones leaves go.mod as it was: the good ones were
// already added.
func TestPurgeRestoreUndoesEarlierToolsWhenALaterPackageIsInvalid(t *testing.T) {
	dir := syncProject(t, "split", [3]string{"x", pkgX, "v1.0.0"}, [3]string{"bad", "example.com/z y", "v1.0.0"})
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.24\n")

	before := tree(t, dir)

	_, _, err := purgeIn(t, installGo(installOpts{}), dir, "YES\n", "--restore")
	if ierrors.CodeOf(err) != ierrors.Environment || !strings.Contains(err.Error(), "Invalid package") {
		t.Fatalf("err = %v", err)
	}

	sameTree(t, before, tree(t, dir))
}
