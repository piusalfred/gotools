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
	"slices"
	"strings"
	"testing"

	"github.com/piusalfred/gotools/internal/command"
	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/manifest"
)

// syncIn runs `sync args` in dir through urfave/cli.
func syncIn(t *testing.T, g *fakeGo, dir string, o installOpts, args ...string) installOutcome {
	t.Helper()

	c, out, errb := installContext(dir, g, o)
	err := run(command.NewSync(), c, args...)

	return installOutcome{out: out.String(), err: errb.String(), runErr: err, goes: g}
}

// syncProject writes a manifest declaring tools (name, package, version).
func syncProject(t *testing.T, strategy string, tools ...[3]string) string {
	t.Helper()

	dir := t.TempDir()
	m := manifest.New()
	m.Strategy, m.ModulePrefix = strategy, "tools"

	for _, tl := range tools {
		m.SetTool(manifest.Tool{Name: tl[0], Source: "go", Package: tl[1], Version: tl[2]})
	}

	if err := m.Flush(filepath.Join(dir, config.ManifestFile)); err != nil {
		t.Fatal(err)
	}

	// The split strategy needs a module in the project root.
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.24\n")

	return dir
}

func TestSyncRestoresThenFastPath(t *testing.T) {
	for _, st := range []string{"split", "module", "unified"} {
		t.Run(st, func(t *testing.T) {
			dir := syncProject(t, st, [3]string{"x", "example.com/a/x", "v1.0.0"}, [3]string{"y", "example.com/b/y", "v1.2.0"})
			g := installGo(installOpts{})

			r := syncIn(t, g, dir, installOpts{})
			r.ok(t)

			if !strings.Contains(r.out, "Sync restored 2 tool(s)") {
				t.Fatalf("out=%q", r.out)
			}

			if st == "unified" && g.count("mod", "tidy") == 0 {
				t.Fatal("no tidy ran")
			}

			if tl, _ := readManifest(t, dir).Tool("y"); tl.Version != "v1.2.0" {
				t.Fatalf("tool=%+v", tl)
			}

			// The second run has nothing to do: no get, no tidy.
			g2 := installGo(installOpts{})

			r = syncIn(t, g2, dir, installOpts{})
			r.ok(t)

			if !strings.Contains(r.out, "Tools up to date") || g2.count("mod", "tidy") != 0 {
				t.Fatalf("out=%q", r.out)
			}

			// And offline accepts it too.
			r = syncIn(t, installGo(installOpts{}), dir, installOpts{}, "--offline")
			r.ok(t)

			if !strings.Contains(r.out, "fingerprint match") {
				t.Fatalf("offline out=%q", r.out)
			}
		})
	}
}

// The fast path must notice a change to anything the fingerprint covers, not
// only to the modfiles: here the module prefix changes, every modfile still
// matches the manifest, and only the fingerprint can tell.
func TestSyncFastPathNoticesFingerprintedStateChange(t *testing.T) {
	for _, st := range []string{"split", "module", "unified"} {
		t.Run(st, func(t *testing.T) {
			dir := syncProject(t, st, [3]string{"x", "example.com/a/x", "v1.0.0"})

			syncIn(t, installGo(installOpts{}), dir, installOpts{}).ok(t)

			m := readManifest(t, dir)
			m.ModulePrefix = "other"

			if err := m.Flush(filepath.Join(dir, config.ManifestFile)); err != nil {
				t.Fatal(err)
			}

			if r := syncIn(t, installGo(installOpts{}), dir, installOpts{}, "--offline"); r.code() != ierrors.Offline {
				t.Fatalf("offline accepted a stale fingerprint: %v", r.runErr)
			}

			r := syncIn(t, installGo(installOpts{}), dir, installOpts{})
			r.ok(t)

			if strings.Contains(r.out, "Tools up to date") {
				t.Fatalf("online sync took the fast path with a stale fingerprint: %q", r.out)
			}
		})
	}
}

func TestSyncOfflineMismatch(t *testing.T) {
	dir := syncProject(t, "split", [3]string{"x", "example.com/a/x", "v1.0.0"})
	g := installGo(installOpts{})

	r := syncIn(t, g, dir, installOpts{}, "--offline")
	if r.code() != ierrors.Offline || !strings.Contains(r.runErr.Error(), "Network required") || !strings.Contains(r.runErr.Error(), "gotools sync") {
		t.Fatalf("err = %v", r.runErr)
	}

	if g.count("get", "-tool") != 0 {
		t.Fatal("offline sync used the network")
	}

	if r := syncIn(t, installGo(installOpts{}), dir, installOpts{environ: []string{"GOTOOLS_OFFLINE=1"}}); r.code() != ierrors.Offline {
		t.Fatalf("env offline: %v", r.runErr)
	}
}

func TestSyncDetectsDrift(t *testing.T) {
	dir := syncProject(t, "split", [3]string{"x", "example.com/a/x", "v1.0.0"})
	writeFile(t, filepath.Join(dir, "tools/x.mod"), "module tools/x\n\ngo 1.24\n\ntool example.com/a/x\n\nrequire example.com/a v0.9.0\n")

	g := installGo(installOpts{})

	r := syncIn(t, g, dir, installOpts{})
	r.ok(t)

	if !strings.Contains(r.out, "manifest v1.0.0, disk v0.9.0") || g.count("mod", "tidy") != 1 {
		t.Fatalf("out=%q tidy=%d", r.out, g.count("mod", "tidy"))
	}

	if !strings.Contains(readFile(t, filepath.Join(dir, "tools/x.mod")), "example.com/a v1.0.0") {
		t.Fatal("modfile not reinstalled at the manifest version")
	}
}

func TestSyncDiscoversFromDisk(t *testing.T) {
	dir := syncProject(t, "split")
	writeFile(t, filepath.Join(dir, "tools/x.mod"), "module tools/x\n\ngo 1.24\n\ntool example.com/a/x\n\nrequire example.com/a v1.5.0\n")

	r := syncIn(t, installGo(installOpts{}), dir, installOpts{})
	r.ok(t)

	if !strings.Contains(r.out, "discovered from disk") || !strings.Contains(r.out, "Added 1 tool(s)") {
		t.Fatalf("out=%q", r.out)
	}

	if tl, ok := readManifest(t, dir).Tool("x"); !ok || tl.Version != "v1.5.0" {
		t.Fatalf("tool=%+v", tl)
	}
}

// An aliased tool in the unified strategy keeps its manifest name instead of
// being discovered again under the name inferred from its binary.
func TestSyncKeepsTheManifestNameOfAnAliasedUnifiedTool(t *testing.T) {
	dir := syncProject(t, "unified", [3]string{"fmt", "mvdan.cc/gofumpt", "v1.0.0"})
	writeFile(t, filepath.Join(dir, "tools/go.mod"), "module tools\n\ngo 1.24\n\ntool mvdan.cc/gofumpt\n\nrequire mvdan.cc v1.0.0\n")

	syncIn(t, installGo(installOpts{}), dir, installOpts{}).ok(t)

	if got := manifestTools(t, dir); !slices.Equal(got, []string{"fmt"}) {
		t.Fatalf("tools = %v", got)
	}
}

func TestSyncIgnoresAStrayGoModInTheSplitDirectory(t *testing.T) {
	dir := syncProject(t, "split")
	writeFile(t, filepath.Join(dir, "tools/go.mod"), "module tools\n\ngo 1.24\n")
	writeFile(t, filepath.Join(dir, "tools/.hidden.mod"), "module h\n\ngo 1.24\n\ntool example.com/h/h\n")

	g := installGo(installOpts{})

	r := syncIn(t, g, dir, installOpts{})
	r.ok(t)

	if len(manifestTools(t, dir)) != 0 || strings.Contains(r.out, "go.mod") || g.count("mod", "tidy") != 0 {
		t.Fatalf("tools = %v, tidies = %d, out = %q", manifestTools(t, dir), g.count("mod", "tidy"), r.out)
	}
}

func TestSyncDryRunWritesNothing(t *testing.T) {
	dir := syncProject(t, "split", [3]string{"x", "example.com/a/x", "v1.0.0"})
	writeFile(t, filepath.Join(dir, "tools/z.mod"), "module tools/z\n\ngo 1.24\n\ntool example.com/c/z\n\nrequire example.com/c v1.0.0\n")

	before := readFile(t, filepath.Join(dir, config.ManifestFile))
	g := installGo(installOpts{})

	r := syncIn(t, g, dir, installOpts{}, "--dry-run")
	r.ok(t)

	for _, want := range []string{"Would reinstall x", "Would add z", "Would restore 1"} {
		if !strings.Contains(r.out, want) {
			t.Fatalf("out lacks %q:\n%s", want, r.out)
		}
	}

	if readFile(t, filepath.Join(dir, config.ManifestFile)) != before {
		t.Fatal("dry-run changed the manifest")
	}

	if exists(filepath.Join(dir, "tools/x.mod")) || exists(filepath.Join(dir, "tools/.gotools.fingerprint")) {
		t.Fatal("dry-run wrote files")
	}

	if g.count("get", "-tool")+g.count("mod", "tidy") != 0 {
		t.Fatal("dry-run ran go")
	}
}

func TestSyncUsageErrors(t *testing.T) {
	dir := syncProject(t, "split")

	for _, args := range [][]string{{"--bogus"}, {"--jobs"}, {"--jobs=0"}, {"--jobs=x"}, {"--jobs", "-1"}, {"extra"}} {
		g := installGo(installOpts{})

		if r := syncIn(t, g, dir, installOpts{}, args...); r.code() != ierrors.Usage {
			t.Errorf("%v: err = %v", args, r.runErr)
		}

		if len(g.calls) != 0 {
			t.Errorf("%v: acted on a bad request", args)
		}
	}
}

func TestSyncStrategyMismatch(t *testing.T) {
	dir := syncProject(t, "split")
	writeFile(t, filepath.Join(dir, "tools/go.mod"), "module tools\n\ngo 1.24\n\ntool example.com/a/x\n")

	// A dry run reports and changes nothing.
	r := syncIn(t, installGo(installOpts{}), dir, installOpts{}, "--dry-run")
	r.ok(t)

	if !strings.Contains(r.out, "Would auto-migrate from 'unified' to 'split'") || !exists(filepath.Join(dir, "tools/go.mod")) {
		t.Fatalf("out=%q", r.out)
	}

	// A real run moves the disk to the strategy the manifest asks for.
	r = syncIn(t, installGo(installOpts{}), dir, installOpts{})
	r.ok(t)

	if !strings.Contains(r.out, "Auto-migrating to 'split'") {
		t.Fatalf("out=%q", r.out)
	}

	if !exists(filepath.Join(dir, "tools/x.mod")) || exists(filepath.Join(dir, "tools/go.mod")) {
		t.Fatalf("layout not migrated: %v", files(t, dir))
	}
}

// A failed automatic migration puts the old layout back and fails the sync.
func TestSyncMigrationFailureRestoresTheLayout(t *testing.T) {
	dir := syncProject(t, "split")
	writeFile(t, filepath.Join(dir, "tools/go.mod"), "module tools\n\ngo 1.24\n\ntool example.com/a/x\n")

	g := &fakeGo{intercept: func(c gocmd.Call) (gocmd.Result, error, bool) {
		if len(c.Args) > 1 && c.Args[0] == "get" {
			return gocmd.Result{Code: 1, Stderr: []byte("dial tcp: i/o timeout")}, nil, true
		}

		return gocmd.Result{}, nil, false
	}}

	r := syncIn(t, g, dir, installOpts{})
	if r.code() != ierrors.Network {
		t.Fatalf("err = %v", r.runErr)
	}

	if !exists(filepath.Join(dir, "tools/go.mod")) || exists(filepath.Join(dir, "tools/x.mod")) {
		t.Fatalf("the old layout was not restored: %v", files(t, dir))
	}
}

// An env override of the strategy is not the user's persisted choice, so an
// automatic migration must not write it to the manifest.
func TestSyncAutoMigrationDoesNotPersistTheEnvironmentStrategy(t *testing.T) {
	dir := installedProject(t, "split")

	r := syncIn(t, installGo(installOpts{}), dir, installOpts{environ: []string{"GOTOOLS_STRATEGY=module"}})
	r.ok(t)

	if !strings.Contains(r.out, "Auto-migrating to 'module'") {
		t.Fatalf("out=%q", r.out)
	}

	if got := manifestStrategy(t, dir); got != "split" {
		t.Fatalf("the environment's strategy leaked into the manifest: %q", got)
	}

	if !exists(filepath.Join(dir, "tools/x/go.mod")) || exists(filepath.Join(dir, "tools/x.mod")) {
		t.Fatalf("layout not migrated: %v", files(t, dir))
	}
}

func TestSyncParallel(t *testing.T) {
	dir := syncProject(t, "split",
		[3]string{"a", "example.com/a/a", "v1.0.0"}, [3]string{"b", "example.com/b/b", "v1.0.0"},
		[3]string{"c", "example.com/c/c", "v1.0.0"}, [3]string{"d", "example.com/d/d", "v1.0.0"})

	r := syncIn(t, installGo(installOpts{}), dir, installOpts{}, "--jobs", "3")
	r.ok(t)

	if !strings.Contains(r.out, "Sync restored 4 tool(s)") {
		t.Fatalf("out=%q", r.out)
	}

	if got := manifestTools(t, dir); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("tools=%v", got)
	}

	if r := syncIn(t, installGo(installOpts{}), dir, installOpts{}, "--offline"); r.runErr != nil {
		t.Fatalf("fingerprint not written after a parallel sync: %v", r.runErr)
	}
}

// The jobs setting applies when the flag is not given.
func TestSyncJobsSettingApplies(t *testing.T) {
	dir := syncProject(t, "unified", [3]string{"a", "example.com/a/a", "v1.0.0"}, [3]string{"b", "example.com/b/b", "v1.0.0"})

	r := syncIn(t, installGo(installOpts{}), dir, installOpts{environ: []string{"GOTOOLS_JOBS=4"}})
	r.ok(t)

	if !strings.Contains(r.out, "unified strategy: installs are serial") {
		t.Fatalf("the jobs setting was ignored: %q", r.out)
	}
}

func TestSyncParallelFailureLeavesManifest(t *testing.T) {
	dir := syncProject(t, "split", [3]string{"a", "example.com/a/a", "latest"}, [3]string{"b", "example.com/b/b", "latest"})
	before := readFile(t, filepath.Join(dir, config.ManifestFile))

	g := &fakeGo{intercept: func(c gocmd.Call) (gocmd.Result, error, bool) {
		if len(c.Args) > 1 && c.Args[0] == "get" && strings.HasSuffix(c.Args[len(c.Args)-1], "b/b@latest") {
			return gocmd.Result{Code: 1, Stderr: []byte("boom")}, nil, true
		}

		return gocmd.Result{}, nil, false
	}}

	r := syncIn(t, g, dir, installOpts{}, "--jobs=2")
	if r.code() != ierrors.Generic || !strings.Contains(r.runErr.Error(), "boom") {
		t.Fatalf("err = %v", r.runErr)
	}

	if readFile(t, filepath.Join(dir, config.ManifestFile)) != before {
		t.Fatal("manifest changed after a failed parallel sync")
	}

	if exists(filepath.Join(dir, "tools/b.mod")) || exists(filepath.Join(dir, "tools/.gotools.fingerprint")) {
		t.Fatal("failed job left files, or the fingerprint was written")
	}
}

func TestSyncUnifiedJobsWarns(t *testing.T) {
	dir := syncProject(t, "unified", [3]string{"a", "example.com/a/a", "v1.0.0"}, [3]string{"b", "example.com/b/b", "v1.0.0"})

	r := syncIn(t, installGo(installOpts{}), dir, installOpts{}, "--jobs", "4")
	r.ok(t)

	if !strings.Contains(r.out, "unified strategy: installs are serial") {
		t.Fatalf("out=%q", r.out)
	}
}

func TestSyncTidyNetworkFailure(t *testing.T) {
	dir := syncProject(t, "module")
	writeFile(t, filepath.Join(dir, "tools/x/go.mod"), "module tools/x\n\ngo 1.24\n")

	g := &fakeGo{intercept: func(c gocmd.Call) (gocmd.Result, error, bool) {
		if len(c.Args) > 1 && c.Args[1] == "tidy" {
			return gocmd.Result{Code: 1, Stderr: []byte("dial tcp: i/o timeout")}, nil, true
		}

		return gocmd.Result{}, nil, false
	}}

	if r := syncIn(t, g, dir, installOpts{}); r.code() != ierrors.Network {
		t.Fatalf("err = %v", r.runErr)
	}
}

func TestSyncTidyGenericFailureIsNotAUsageError(t *testing.T) {
	dir := syncProject(t, "module")
	writeFile(t, filepath.Join(dir, "tools/x/go.mod"), "module tools/x\n\ngo 1.24\n")

	g := &fakeGo{intercept: func(c gocmd.Call) (gocmd.Result, error, bool) {
		if len(c.Args) > 1 && c.Args[1] == "tidy" {
			return gocmd.Result{Code: 1, Stderr: []byte("invalid module version syntax")}, nil, true
		}

		return gocmd.Result{}, nil, false
	}}

	if r := syncIn(t, g, dir, installOpts{}); r.code() != ierrors.Generic {
		t.Fatalf("a bad version in go's output is not the user's typo here: %v", r.runErr)
	}
}

func TestSyncTakesTheLock(t *testing.T) {
	dir := syncProject(t, "split")

	holder := &command.Context{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Program: "gotools", WorkDir: dir, Go: installGo(installOpts{})}
	if err := holder.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	err := holder.WithLock(context.Background(), func(context.Context) error {
		r := syncIn(t, installGo(installOpts{}), dir, installOpts{environ: []string{"GOTOOLS_LOCK_TIMEOUT=0"}})
		if r.code() != ierrors.Lock {
			t.Errorf("err = %v", r.runErr)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Context.Sync is the method init calls: with Unlocked it takes no lock, so a
// dry run does not create the tools directory.
func TestContextSyncUnlockedTakesNoLock(t *testing.T) {
	dir := syncProject(t, "split")

	c, _, _ := installContext(dir, installGo(installOpts{}), installOpts{})
	if err := c.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := c.Sync(context.Background(), command.SyncParams{DryRun: true, Unlocked: true}); err != nil {
		t.Fatal(err)
	}

	if exists(filepath.Join(dir, "tools")) {
		t.Fatal("an unlocked dry run created the tools directory")
	}

	if err := c.Sync(context.Background(), command.SyncParams{DryRun: true}); err != nil {
		t.Fatal(err)
	}

	if !exists(filepath.Join(dir, "tools", ".gotools.flock")) {
		t.Fatal("a locked sync took no lock")
	}
}

func TestSyncNeedsGo(t *testing.T) {
	dir := syncProject(t, "split")

	missing := runnerFunc(func(context.Context, gocmd.Call) (gocmd.Result, error) { return gocmd.Result{}, gocmd.ErrNotFound })

	c := &command.Context{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Program: "gotools", WorkDir: dir, Go: missing}

	err := run(command.NewSync(), c)
	if ierrors.CodeOf(err) != ierrors.Environment || !strings.Contains(err.Error(), "Go is not installed") {
		t.Fatalf("err = %v", err)
	}
}

// A hand edit to a tool's modfile, a deleted sum or a file nobody declared
// changes nothing in the manifest, so only the fingerprint's view of the files
// can send sync past its fast path.
func TestSyncFastPathNoticesHandEditsToTheToolFiles(t *testing.T) {
	edits := map[string]func(t *testing.T, dir, st string){
		"an edited modfile": func(t *testing.T, dir, st string) {
			p := filepath.Join(dir, layoutPaths(st, "x")[0])
			writeFile(t, p, readFile(t, p)+"\n// edited by hand\n")
		},
		"a deleted sum": func(t *testing.T, dir, st string) {
			sums := map[string]string{"split": "tools/x.sum", "module": "tools/x/go.sum", "unified": "tools/go.sum"}
			if err := os.Remove(filepath.Join(dir, sums[st])); err != nil {
				t.Fatal(err)
			}
		},
		"an orphan modfile": func(t *testing.T, dir, st string) {
			writeFile(t, filepath.Join(dir, "tools", "orphan.mod"), "module orphan\n\ngo 1.24\n")
		},
	}

	for name, edit := range edits {
		for _, st := range []string{"split", "module", "unified"} {
			t.Run(name+"/"+st, func(t *testing.T) {
				dir := syncProject(t, st, [3]string{"x", "example.com/a/x", "v1.0.0"})

				// The fake go writes no sums; a real one does.
				sums := map[string]string{"split": "tools/x.sum", "module": "tools/x/go.sum", "unified": "tools/go.sum"}
				writeFile(t, filepath.Join(dir, sums[st]), "sum\n")

				syncIn(t, installGo(installOpts{}), dir, installOpts{}).ok(t)

				// Untouched, the second run is the fast path.
				if r := syncIn(t, installGo(installOpts{}), dir, installOpts{}); !strings.Contains(r.out, "Tools up to date") {
					t.Fatalf("an untouched project missed the fast path: %q", r.out)
				}

				edit(t, dir, st)

				if r := syncIn(t, installGo(installOpts{}), dir, installOpts{}, "--offline"); r.code() != ierrors.Offline {
					t.Fatalf("offline accepted a project whose files changed: %v", r.runErr)
				}

				r := syncIn(t, installGo(installOpts{}), dir, installOpts{})
				r.ok(t)

				if strings.Contains(r.out, "Tools up to date") {
					t.Fatalf("sync took the fast path although %s: %q", name, r.out)
				}
			})
		}
	}
}

// The fingerprint file, the lock and other dot-files are not tool files.
func TestSyncFastPathIgnoresDotFiles(t *testing.T) {
	dir := syncProject(t, "split", [3]string{"x", "example.com/a/x", "v1.0.0"})

	syncIn(t, installGo(installOpts{}), dir, installOpts{}).ok(t)
	writeFile(t, filepath.Join(dir, "tools", ".scratch"), "noise")
	writeFile(t, filepath.Join(dir, "tools", ".cache", "x"), "noise")

	if r := syncIn(t, installGo(installOpts{}), dir, installOpts{}); !strings.Contains(r.out, "Tools up to date") {
		t.Fatalf("a dot-file broke the fast path: %q", r.out)
	}
}
