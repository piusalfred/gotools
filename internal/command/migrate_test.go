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
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/piusalfred/gotools/internal/command"
	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/gocmd"
)

const (
	pkgX = "example.com/a/x"
	pkgY = "example.com/b/y"
)

// migrateIn runs `migrate args` in dir through urfave/cli.
func migrateIn(t *testing.T, g *fakeGo, dir string, o installOpts, args ...string) installOutcome {
	t.Helper()

	c, out, errb := installContext(dir, g, o)
	err := run(command.NewMigrate(), c, args...)

	return installOutcome{out: out.String(), err: errb.String(), runErr: err, goes: g}
}

// installedProject installs x and y under strategy through the fake go.
func installedProject(t *testing.T, strategy string) string {
	t.Helper()

	dir := newProject(t, strategy)

	for _, pkg := range []string{pkgX + "@v1.0.0", pkgY + "@v1.2.0"} {
		if r := installIn(t, installGo(installOpts{}), dir, installOpts{}, pkg); r.runErr != nil {
			t.Fatalf("setup install %s: err=%v out=%q", pkg, r.runErr, r.out)
		}
	}

	return dir
}

// layoutPaths are the files that hold tool name in strategy, under tools/.
func layoutPaths(strategy, name string) []string {
	switch strategy {
	case "split":
		return []string{"tools/" + name + ".mod"}
	case "module":
		return []string{"tools/" + name + "/go.mod"}
	}

	return []string{"tools/go.mod"}
}

// tree maps every file under dir (relative, slash-separated) to its content,
// except the lock file.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()

	out := map[string]string{}

	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() == ".gotools.flock" {
			return err
		}

		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}

		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = string(b)

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return out
}

func sameTree(t *testing.T, a, b map[string]string) {
	t.Helper()

	if len(a) != len(b) {
		t.Fatalf("trees differ:\nbefore %v\nafter  %v", keys(a), keys(b))
	}

	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			t.Fatalf("file %s changed (present=%v)\nbefore %q\nafter  %q", k, ok, v, w)
		}
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}

	slices.Sort(out)

	return out
}

// failingOn is a go where `go get` of a package containing substr fails.
func failingOn(substr string) *fakeGo {
	g := installGo(installOpts{})
	inner := g.intercept

	g.intercept = func(c gocmd.Call) (gocmd.Result, error, bool) {
		if len(c.Args) > 1 && c.Args[0] == "get" && strings.Contains(c.Args[len(c.Args)-1], substr) {
			return gocmd.Result{Code: 1, Stderr: []byte("boom")}, nil, true
		}

		return inner(c)
	}

	return g
}

// noInstalls fails when g was asked to change anything. The `go env` lookups
// every command makes are not changes.
func noInstalls(t *testing.T, g *fakeGo) {
	t.Helper()

	for _, c := range g.calls {
		if len(c.Args) == 0 || c.Args[0] != "env" {
			t.Fatalf("unexpected go call: %v", c.Args)
		}
	}
}

func manifestStrategy(t *testing.T, dir string) string {
	t.Helper()

	return readManifest(t, dir).Strategy
}

func noMigrateLeftovers(t *testing.T, dir string) {
	t.Helper()

	left, _ := filepath.Glob(filepath.Join(dir, ".gotools-migrate-*"))
	if len(left) > 0 {
		t.Fatalf("leftover backup directories: %v", left)
	}
}

func TestMigrateEveryPair(t *testing.T) {
	all := []string{"split", "module", "unified"}

	for _, from := range all {
		for _, to := range all {
			if from == to {
				continue
			}

			t.Run(from+"_to_"+to, func(t *testing.T) {
				dir := installedProject(t, from)

				r := migrateIn(t, installGo(installOpts{}), dir, installOpts{}, to)
				r.ok(t)

				for _, want := range []string{
					"Migrating from '" + from + "' to '" + to + "'",
					"x " + pkgX + "@v1.0.0",
					"y " + pkgY + "@v1.2.0",
					"Migration from '" + from + "' to '" + to + "' complete",
				} {
					if !strings.Contains(r.out, want) {
						t.Fatalf("missing %q in %q", want, r.out)
					}
				}

				got := tree(t, dir)

				for _, n := range []string{"x", "y"} {
					for _, p := range layoutPaths(to, n) {
						if !strings.Contains(got[p], "tool example.com/") {
							t.Fatalf("%s missing or has no tool directive: %q", p, got[p])
						}
					}

					if to != "unified" && from != "unified" {
						for _, p := range layoutPaths(from, n) {
							if _, ok := got[p]; ok {
								t.Fatalf("old file %s survived", p)
							}
						}
					}
				}

				if _, ok := got["tools/go.mod"]; ok && from == "unified" && to != "unified" {
					t.Fatalf("shared go.mod survived: %v", keys(got))
				}

				if s := manifestStrategy(t, dir); s != to {
					t.Fatalf("manifest strategy = %q, want %q", s, to)
				}

				if tl, _ := readManifest(t, dir).Tool("y"); tl.Version != "v1.2.0" || tl.Package != pkgY {
					t.Fatalf("tool y = %+v", tl)
				}

				noMigrateLeftovers(t, dir)
			})
		}
	}
}

func TestMigrateDryRunWritesNothing(t *testing.T) {
	for _, args := range [][]string{{"module", "--dry-run"}, {"--dry-run", "module"}} {
		dir := installedProject(t, "split")
		before := tree(t, dir)
		g := installGo(installOpts{})

		r := migrateIn(t, g, dir, installOpts{}, args...)
		r.ok(t)

		if !strings.Contains(r.out, "[dry-run] Would migrate tools/ from 'split' to 'module'") || !strings.Contains(r.out, "x "+pkgX+"@v1.0.0") {
			t.Fatalf("%v: out=%q", args, r.out)
		}

		sameTree(t, before, tree(t, dir))
		noInstalls(t, g)
	}
}

func TestMigrateAlreadyUsing(t *testing.T) {
	dir := installedProject(t, "split")
	before := tree(t, dir)
	g := installGo(installOpts{})

	r := migrateIn(t, g, dir, installOpts{}, "split")
	r.ok(t)

	if !strings.Contains(r.out, "Already using strategy 'split'") {
		t.Fatalf("out=%q", r.out)
	}

	sameTree(t, before, tree(t, dir))
	noInstalls(t, g)
}

// With the disk already in the target layout but the manifest naming another
// strategy, the manifest is brought in line: otherwise sync keeps refusing.
func TestMigrateAdoptsDiskStrategyIntoManifest(t *testing.T) {
	dir := installedProject(t, "unified")

	m := readManifest(t, dir)
	m.Strategy = "split"

	if err := m.Flush(filepath.Join(dir, config.ManifestFile)); err != nil {
		t.Fatal(err)
	}

	disk := tree(t, dir)
	delete(disk, config.ManifestFile)

	g := installGo(installOpts{})

	r := migrateIn(t, g, dir, installOpts{}, "unified")
	r.ok(t)

	if !strings.Contains(r.out, "now says strategy 'unified'") || manifestStrategy(t, dir) != "unified" {
		t.Fatalf("out=%q strategy=%q", r.out, manifestStrategy(t, dir))
	}

	after := tree(t, dir)
	delete(after, config.ManifestFile)
	sameTree(t, disk, after)
	noInstalls(t, g)
}

func TestMigrateAdoptingTheDiskStrategyHonoursDryRun(t *testing.T) {
	dir := installedProject(t, "unified")

	m := readManifest(t, dir)
	m.Strategy = "split"

	if err := m.Flush(filepath.Join(dir, config.ManifestFile)); err != nil {
		t.Fatal(err)
	}

	r := migrateIn(t, installGo(installOpts{}), dir, installOpts{}, "unified", "--dry-run")
	r.ok(t)

	if !strings.Contains(r.out, "[dry-run] Would set strategy to 'unified'") || manifestStrategy(t, dir) != "split" {
		t.Fatalf("out=%q strategy=%q", r.out, manifestStrategy(t, dir))
	}
}

// A hybrid directory whose dominant layout is the target is still cleaned up:
// the warning tells the user to run migrate.
func TestMigrateConsolidatesHybridDirectory(t *testing.T) {
	dir := newProject(t, "unified")

	writeFile(t, filepath.Join(dir, "tools/go.mod"), "module tools\n\ngo 1.24\n\ntool "+pkgY+"\n\nrequire example.com/b v1.2.0\n")
	writeFile(t, filepath.Join(dir, "tools/x.mod"), "module tools/x\n\ngo 1.24\n\ntool "+pkgX+"\n\nrequire example.com/a v1.0.0\n")

	migrateIn(t, installGo(installOpts{}), dir, installOpts{}, "unified").ok(t)

	got := tree(t, dir)
	if _, ok := got["tools/x.mod"]; ok {
		t.Fatalf("split leftover survived: %v", keys(got))
	}

	if mod := got["tools/go.mod"]; !strings.Contains(mod, "tool "+pkgX) || !strings.Contains(mod, "tool "+pkgY) {
		t.Fatalf("shared go.mod:\n%s", mod)
	}

	if names := manifestTools(t, dir); !slices.Equal(names, []string{"x", "y"}) {
		t.Fatalf("manifest tools = %v", names)
	}
}

// A manifest entry whose files are missing is rebuilt, not dropped.
func TestMigrateKeepsManifestOnlyTools(t *testing.T) {
	dir := syncProject(t, "split", [3]string{"z", "example.com/c/z", "v1.5.0"})

	migrateIn(t, installGo(installOpts{}), dir, installOpts{}, "module").ok(t)

	if mod := readFile(t, filepath.Join(dir, "tools/z/go.mod")); !strings.Contains(mod, "tool example.com/c/z") || !strings.Contains(mod, "v1.5.0") {
		t.Fatalf("tools/z/go.mod:\n%s", mod)
	}

	if tl, ok := readManifest(t, dir).Tool("z"); !ok || tl.Version != "v1.5.0" {
		t.Fatalf("tool z = %+v ok=%v", tl, ok)
	}
}

// Any failure midway restores the old layout, its go.sum files and the
// unrelated files kept in the directory.
func TestMigrateFailureRestoresEverything(t *testing.T) {
	for _, to := range []string{"module", "unified"} {
		t.Run(to, func(t *testing.T) {
			dir := installedProject(t, "split")
			writeFile(t, filepath.Join(dir, "tools/x.sum"), "example.com/a v1.0.0 h1:abc=\n")
			writeFile(t, filepath.Join(dir, "tools/README.md"), "keep me\n")

			before := tree(t, dir)

			r := migrateIn(t, failingOn("example.com/b"), dir, installOpts{}, to)
			if r.runErr == nil || !strings.Contains(r.runErr.Error(), "boom") {
				t.Fatalf("err = %v", r.runErr)
			}

			sameTree(t, before, tree(t, dir))
			noMigrateLeftovers(t, dir)
		})
	}
}

// The old layout's checksum files go with its modfiles.
func TestMigrateMovesTheOldSumFilesAway(t *testing.T) {
	dir := installedProject(t, "split")
	writeFile(t, filepath.Join(dir, "tools/x.sum"), "example.com/a v1.0.0 h1:abc=\n")
	writeFile(t, filepath.Join(dir, "tools/y.sum"), "example.com/b v1.2.0 h1:def=\n")

	migrateIn(t, installGo(installOpts{}), dir, installOpts{}, "module").ok(t)

	got := tree(t, dir)
	for _, old := range []string{"tools/x.sum", "tools/y.sum", "tools/x.mod", "tools/y.mod"} {
		if _, ok := got[old]; ok {
			t.Errorf("old file %s survived: %v", old, keys(got))
		}
	}
}

func TestMigratePreservesUnrelatedFiles(t *testing.T) {
	dir := installedProject(t, "split")
	writeFile(t, filepath.Join(dir, "tools/README.md"), "keep me\n")
	writeFile(t, filepath.Join(dir, "tools/.gitignore"), "*.log\n")
	writeFile(t, filepath.Join(dir, "tools/.hidden.mod"), "module hidden\n")

	migrateIn(t, installGo(installOpts{}), dir, installOpts{}, "unified").ok(t)

	got := tree(t, dir)
	if got["tools/README.md"] != "keep me\n" || got["tools/.gitignore"] != "*.log\n" || got["tools/.hidden.mod"] != "module hidden\n" {
		t.Fatalf("unrelated files lost: %v", keys(got))
	}

	noMigrateLeftovers(t, dir)
}

// Offline mode forbids the reinstall, so it is refused before anything is
// touched.
func TestMigrateOfflineRefusesBeforeTouchingAnything(t *testing.T) {
	dir := installedProject(t, "split")
	before := tree(t, dir)
	o := installOpts{environ: []string{"GOTOOLS_OFFLINE=1"}}

	g := installGo(installOpts{})

	r := migrateIn(t, g, dir, o, "module")
	if r.code() != ierrors.Offline || !strings.Contains(r.runErr.Error(), "Network required") {
		t.Fatalf("err = %v", r.runErr)
	}

	sameTree(t, before, tree(t, dir))
	noMigrateLeftovers(t, dir)

	// A dry run only reads, so it is still allowed offline.
	if r := migrateIn(t, g, dir, o, "module", "--dry-run"); r.runErr != nil || !strings.Contains(r.out, "[dry-run]") {
		t.Fatalf("dry-run: err=%v out=%q", r.runErr, r.out)
	}
}

// Nothing to download means nothing needs the network.
func TestMigrateOfflineWithoutToolsIsFine(t *testing.T) {
	dir := newProject(t, "split")

	migrateIn(t, installGo(installOpts{}), dir, installOpts{environ: []string{"GOTOOLS_OFFLINE=1"}}, "module").ok(t)

	if got := manifestStrategy(t, dir); got != "module" {
		t.Fatalf("manifest strategy = %q", got)
	}
}

func TestMigrateUsageErrors(t *testing.T) {
	dir := installedProject(t, "split")
	before := tree(t, dir)

	for name, args := range map[string][]string{
		"none":     {},
		"dry only": {"--dry-run"},
		"invalid":  {"flat"},
		"flag":     {"--force"},
		"extra":    {"module", "unified"},
	} {
		g := installGo(installOpts{})

		if r := migrateIn(t, g, dir, installOpts{}, args...); r.code() != ierrors.Usage {
			t.Errorf("%s: err = %v", name, r.runErr)
		}

		if len(g.calls) != 0 {
			t.Errorf("%s: acted on a bad request", name)
		}
	}

	sameTree(t, before, tree(t, dir))
}

func TestMigrateUsageMessages(t *testing.T) {
	dir := newProject(t, "split")

	r := migrateIn(t, installGo(installOpts{}), dir, installOpts{})
	if !strings.Contains(r.runErr.Error(), "Usage: gotools migrate <unified|split|module> [--dry-run]") {
		t.Fatalf("err = %v", r.runErr)
	}

	r = migrateIn(t, installGo(installOpts{}), dir, installOpts{}, "flat")
	if !strings.Contains(r.runErr.Error(), "Invalid strategy: flat (must be unified, split, or module)") {
		t.Fatalf("err = %v", r.runErr)
	}
}

// A symlinked modfile could point anywhere: refuse before moving a thing.
func TestMigrateRefusesSymlinkedEntries(t *testing.T) {
	dir := installedProject(t, "split")
	outside := filepath.Join(t.TempDir(), "evil.mod")
	writeFile(t, outside, "module evil\n")

	if err := os.Remove(filepath.Join(dir, "tools/x.mod")); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(dir, "tools/x.mod")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	g := installGo(installOpts{})

	r := migrateIn(t, g, dir, installOpts{}, "module")
	if r.code() != ierrors.Environment || !strings.Contains(r.runErr.Error(), "symlink") {
		t.Fatalf("err = %v", r.runErr)
	}

	if fi, err := os.Lstat(filepath.Join(dir, "tools/x.mod")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink was disturbed: %v", err)
	}

	noInstalls(t, g)
}

// A modfile gotools cannot read stops the migration instead of being lost.
func TestMigrateRefusesUnreadableModfile(t *testing.T) {
	dir := installedProject(t, "split")
	writeFile(t, filepath.Join(dir, "tools/broken.mod"), "this is not a go.mod\n@@@\n")

	before := tree(t, dir)
	g := installGo(installOpts{})

	r := migrateIn(t, g, dir, installOpts{}, "module")
	if r.code() != ierrors.Environment || !strings.Contains(r.runErr.Error(), "broken") {
		t.Fatalf("err = %v", r.runErr)
	}

	sameTree(t, before, tree(t, dir))
	noInstalls(t, g)
}

// With --dry-run an unreadable modfile is reported, and nothing else changes.
func TestMigrateDryRunReportsAnUnreadableModfile(t *testing.T) {
	dir := installedProject(t, "split")
	writeFile(t, filepath.Join(dir, "tools/broken.mod"), "this is not a go.mod\n@@@\n")

	before := tree(t, dir)

	r := migrateIn(t, installGo(installOpts{}), dir, installOpts{}, "module", "--dry-run")
	r.ok(t)

	if !strings.Contains(r.err, "broken") || !strings.Contains(r.out, "Would migrate") {
		t.Fatalf("out=%q stderr=%q", r.out, r.err)
	}

	sameTree(t, before, tree(t, dir))
}

func TestMigrateLockHeld(t *testing.T) {
	dir := installedProject(t, "split")
	before := tree(t, dir)
	o := installOpts{environ: []string{"GOTOOLS_LOCK_TIMEOUT=0"}}

	holder := &command.Context{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Program: "gotools", WorkDir: dir, Go: installGo(installOpts{})}
	if err := holder.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	err := holder.WithLock(context.Background(), func(context.Context) error {
		if r := migrateIn(t, installGo(installOpts{}), dir, o, "module"); r.code() != ierrors.Lock {
			t.Errorf("err = %v", r.runErr)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	sameTree(t, before, tree(t, dir))
}

// Context.Migrate is the method sync calls; the caller holds the lock.
func TestContextMigrateTakesNoLock(t *testing.T) {
	dir := installedProject(t, "split")

	c, _, _ := installContext(dir, installGo(installOpts{}), installOpts{})
	if err := c.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	held := false

	err := c.WithLock(context.Background(), func(ctx context.Context) error {
		held = true

		return c.Migrate(ctx, command.MigrateParams{To: "module"})
	})
	if err != nil || !held {
		t.Fatalf("a migration under the caller's lock waited on itself: %v", err)
	}

	if !exists(filepath.Join(dir, "tools/x/go.mod")) {
		t.Fatalf("not migrated: %v", files(t, dir))
	}

	// Without PersistStrategy the manifest keeps its own strategy.
	if got := manifestStrategy(t, dir); got != "split" {
		t.Fatalf("manifest strategy = %q", got)
	}
}
