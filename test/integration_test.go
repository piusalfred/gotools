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

//go:build integration

// Package integration drives the real gotools binary against the real go
// command and the module proxy. It needs network access and takes minutes:
//
//	go test -tags integration -timeout 30m ./test/...
//
// Unit tests with a fake go live next to the code they test.
package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gotools-integration-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	binary = filepath.Join(dir, "gotools")

	build := exec.Command("go", "build", "-o", binary, "github.com/piusalfred/gotools/cmd/gotools")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr

	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build:", err)
		os.RemoveAll(dir)
		os.Exit(1)
	}

	code := m.Run()

	os.RemoveAll(dir)
	os.Exit(code)
}

// Tools used throughout: small, popular, and built from different modules.
const (
	goimports   = "golang.org/x/tools/cmd/goimports@latest"
	gci         = "github.com/daixiang0/gci@latest"
	staticcheck = "honnef.co/go/tools/cmd/staticcheck@latest"
)

// project is a throw-away module with the gotools binary pointed at it.
type project struct {
	t   *testing.T
	dir string
	env []string
}

func newProject(t *testing.T, env ...string) *project {
	t.Helper()

	dir := t.TempDir()

	for _, f := range []string{"go.mod", "main.go"} {
		b, err := os.ReadFile(filepath.Join("testdata", "project", f))
		if err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(dir, f), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// The developer's own GOTOOLS_* settings must not leak into a test.
	var clean []string

	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GOTOOLS_") {
			clean = append(clean, kv)
		}
	}

	return &project{t: t, dir: dir, env: append(clean, env...)}
}

// run executes gotools with stdin and returns stdout, stderr and the exit code.
func (p *project) run(stdin string, args ...string) (string, string, int) {
	p.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	var out, errb bytes.Buffer

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = p.dir
	cmd.Env = p.env
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout, cmd.Stderr = &out, &errb

	code := 0

	if err := cmd.Run(); err != nil {
		ee, ok := errors.AsType[*exec.ExitError](err)
		if !ok {
			p.t.Fatalf("gotools %v: %v", args, err)
		}

		code = ee.ExitCode()
	}

	return out.String(), errb.String(), code
}

// ok runs gotools and fails the test unless it exits 0.
func (p *project) ok(args ...string) string {
	p.t.Helper()

	out, errs, rc := p.run("", args...)
	if rc != 0 {
		p.t.Fatalf("gotools %s: exit %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), rc, out, errs)
	}

	return out
}

func (p *project) exists(rel string) bool {
	_, err := os.Lstat(filepath.Join(p.dir, rel))

	return err == nil
}

func (p *project) read(rel string) string {
	p.t.Helper()

	b, err := os.ReadFile(filepath.Join(p.dir, rel))
	if err != nil {
		p.t.Fatal(err)
	}

	return string(b)
}

func contains(t *testing.T, what, out string, wants ...string) {
	t.Helper()

	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("%s: missing %q in:\n%s", what, w, out)
		}
	}
}

// listed reports whether name is in the first column of `gotools list`.
func (p *project) listed(name string) bool {
	for _, line := range strings.Split(p.ok("list"), "\n")[2:] {
		if f := strings.Fields(line); len(f) > 0 && f[0] == name {
			return true
		}
	}

	return false
}

// goInfra matches go-command failures, as opposed to the tool's own output.
var goInfra = regexp.MustCompile(`(?i)go:.*(work|vendor|module|cannot find)`)

// execHelp runs `gotools exec <tool> --help`: the tool's exit code is its
// own business, but go must have been able to build and start it.
func (p *project) execHelp(tool string) {
	p.t.Helper()

	out, errs, _ := p.run("", "exec", tool, "--help")
	if goInfra.MatchString(out + errs) {
		p.t.Errorf("exec %s: go infrastructure error:\n%s%s", tool, out, errs)
	}

	if strings.TrimSpace(out+errs) == "" {
		p.t.Errorf("exec %s printed nothing", tool)
	}
}

func TestLifecycle(t *testing.T) {
	for _, strategy := range []string{"unified", "split", "module"} {
		t.Run(strategy, func(t *testing.T) {
			p := newProject(t)

			p.ok("init", "--strategy="+strategy)

			for name, pkg := range map[string]string{"goimports": goimports, "gci": gci, "staticcheck": staticcheck} {
				p.ok("install", name, pkg)
			}

			for _, name := range []string{"goimports", "gci", "staticcheck"} {
				if !p.listed(name) {
					t.Errorf("%s missing from list", name)
				}

				contains(t, "info "+name, p.ok("info", name), name)
				p.execHelp(name)
			}

			p.ok("sync")
			contains(t, "upgrade", p.ok("upgrade", "gci"), "gci")

			// Reconcile after the upgrade, then both fast paths.
			p.ok("sync")
			contains(t, "sync fast path", p.ok("sync"), "Tools up to date.")
			contains(t, "sync --offline", p.ok("sync", "--offline"), "fingerprint match")

			contains(t, "doctor", p.ok("doctor", "--format=json"), `"healthy":true`)
			contains(t, "list json", p.ok("list", "--format=json"), `"tools":[{`)

			contains(t, "config read", p.ok("config", "GOTOOLS_STRATEGY"), strategy)
			p.ok("config", "GOTOOLS_DIR", "tools")

			// Regression: writing config must not reset the strategy.
			contains(t, "config write keeps the strategy", p.read(".gotools.json"), `"strategy": "`+strategy+`"`)

			p.ok("remove", "goimports")

			if p.listed("goimports") {
				t.Error("goimports still listed after remove")
			}

			p.ok("remove", "gci", "staticcheck")

			if p.listed("gci") || p.listed("staticcheck") {
				t.Error("tools still listed after remove")
			}

			if _, _, rc := p.run("YES\n", "purge"); rc != 0 || p.exists(".gotools.json") {
				t.Errorf("purge: rc=%d, manifest exists=%v", rc, p.exists(".gotools.json"))
			}
		})
	}
}

func TestMigrationRoundTrip(t *testing.T) {
	p := newProject(t)

	p.ok("init", "--strategy=unified")
	p.ok("install", "goimports", goimports)
	p.ok("install", "gci", gci)

	for _, to := range []string{"split", "module", "unified"} {
		contains(t, "migrate "+to, p.ok("migrate", to), "complete")

		if !p.listed("goimports") || !p.listed("gci") {
			t.Fatalf("tools lost after migrating to %s:\n%s", to, p.ok("list"))
		}

		contains(t, "manifest after "+to, p.read(".gotools.json"), `"strategy": "`+to+`"`)
		p.execHelp("gci")
	}

	// After the round trip nothing of the other layouts is left.
	contains(t, "doctor", p.ok("doctor", "--format=json"), `"name":"config.strategy","status":"pass"`)
}

func TestParallelColdRestore(t *testing.T) {
	p := newProject(t)

	p.ok("init", "--strategy=split")
	p.ok("install", "goimports", goimports)
	p.ok("install", "gci", gci)

	for _, f := range []string{"goimports.mod", "goimports.sum", "gci.mod", "gci.sum", ".gotools.fingerprint"} {
		if err := os.Remove(filepath.Join(p.dir, "tools", f)); err != nil {
			t.Fatal(err)
		}
	}

	contains(t, "sync --jobs", p.ok("sync", "--jobs", "4"), "restored 2 tool(s)")

	for _, f := range []string{"tools/goimports.mod", "tools/gci.mod", "tools/.gotools.fingerprint"} {
		if !p.exists(f) {
			t.Errorf("%s not restored", f)
		}
	}

	contains(t, "manifest", p.read(".gotools.json"), `"goimports"`, `"gci"`)
	p.execHelp("gci")
}

func TestGoModAdoptionAndPurgeRestore(t *testing.T) {
	p := newProject(t)

	// A root module that already declares a tool the go 1.24 way.
	cmd := exec.Command("go", "get", "-tool", "golang.org/x/tools/cmd/stringer@v0.30.0")
	cmd.Dir, cmd.Env = p.dir, p.env

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go get -tool: %v\n%s", err, out)
	}

	if !strings.Contains(p.read("go.mod"), "tool golang.org/x/tools/cmd/stringer") {
		t.Fatalf("setup: no tool directive:\n%s", p.read("go.mod"))
	}

	contains(t, "init", p.ok("init"), "Migrated 1 tool(s) from go.mod into gotools")

	if strings.Contains(p.read("go.mod"), "stringer") && strings.Contains(p.read("go.mod"), "tool ") {
		t.Errorf("directive not stripped:\n%s", p.read("go.mod"))
	}

	if !p.listed("stringer") {
		t.Fatal("stringer not adopted")
	}

	p.execHelp("stringer")

	// And back again.
	if _, errs, rc := p.run("YES\n", "purge", "--restore"); rc != 0 {
		t.Fatalf("purge --restore: rc=%d\n%s", rc, errs)
	}

	if !strings.Contains(p.read("go.mod"), "tool golang.org/x/tools/cmd/stringer") || p.exists(".gotools.json") {
		t.Errorf("restore failed:\n%s", p.read("go.mod"))
	}
}

func TestRenameKeepsToolRunnable(t *testing.T) {
	for _, strategy := range []string{"split", "module"} {
		t.Run(strategy, func(t *testing.T) {
			p := newProject(t)

			p.ok("init", "--strategy="+strategy)
			p.ok("install", "gci", gci)
			p.ok("rename", "gci", "imports-order")

			if !p.listed("imports-order") || p.listed("gci") {
				t.Fatalf("rename not reflected:\n%s", p.ok("list"))
			}

			// go itself must accept the rewritten module line.
			p.execHelp("imports-order")
			contains(t, "doctor", p.ok("doctor", "--format=json"), `"healthy":true`)
		})
	}
}

func TestOneToolOneName(t *testing.T) {
	p := newProject(t)

	p.ok("init", "--strategy=split")
	p.ok("install", "gci", gci)

	_, errs, rc := p.run("", "install", "sorter", gci)
	if rc != 2 || !strings.Contains(errs, "second name for the same tool") {
		t.Fatalf("rc=%d err=%q", rc, errs)
	}

	if p.exists("tools/sorter.mod") {
		t.Error("the refused install left a modfile")
	}
}

// A proxy that accepts the connection and never answers must end in
// E_NETWORK within the operation timeout, with nothing left behind.
func TestStalledProxyTimesOut(t *testing.T) {
	release := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)

	p := newProject(t,
		"GOPROXY="+srv.URL, "GOSUMDB=off", "GOFLAGS=-mod=mod", "GOTOOLS_OPERATION_TIMEOUT=2",
	)

	p.ok("init", "--strategy=split", "--no-migrate")

	start := time.Now()

	_, errs, rc := p.run("", "install", "stalled", "example.com/never/tool@v1.0.0")
	if rc != 3 || !strings.Contains(errs, "timed out") {
		t.Fatalf("rc=%d err=%q", rc, errs)
	}

	if time.Since(start) > time.Minute {
		t.Errorf("took %s to give up", time.Since(start))
	}

	if p.exists("tools/stalled.mod") || p.exists("tools/stalled.sum") {
		t.Error("the stalled install left files behind")
	}
}

// In the real world: doctor on a healthy project talks to the proxy and
// to go, and reports every group.
func TestDoctorTextReport(t *testing.T) {
	p := newProject(t)

	p.ok("init", "--strategy=split")
	p.ok("install", "gci", gci)

	contains(t, "doctor", p.ok("doctor"),
		"Go installation", "Module proxy", "Configuration", "Managed tools (1 declared)",
		"Lock file", "Module integrity", "Disk usage", "All checks passed.")
}

// Stdin pipe mode with the real thing.
func TestPipeInstall(t *testing.T) {
	p := newProject(t)

	p.ok("init", "--strategy=split")

	if _, errs, rc := p.run("# tools\ngo install " + gci + "\n"); rc != 0 {
		t.Fatalf("pipe install: rc=%d\n%s", rc, errs)
	}

	if !p.listed("gci") {
		t.Fatal("gci not installed from stdin")
	}
}

// A go.work file above the tool modules must not break them: go refuses
// -modfile in workspace mode, so gotools runs its own go calls without it.
func TestToolsWorkInsideAGoWorkspace(t *testing.T) {
	for _, strategy := range []string{"split", "module", "unified"} {
		t.Run(strategy, func(t *testing.T) {
			p := newProject(t)

			if err := os.WriteFile(filepath.Join(p.dir, "go.work"), []byte("go 1.24\n\nuse .\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			p.ok("init", "--strategy="+strategy)
			p.ok("install", "goimports", goimports)
			contains(t, "doctor finds the tool runnable", p.ok("doctor"), "goimports@")
			contains(t, "doctor finds the tool runnable", p.ok("doctor"), "runnable")
			p.execHelp("goimports")
		})
	}
}

// A signal sent to gotools alone, not to the terminal's whole group, must reach
// the tool it is running, and the exit code says which signal it was.
func TestSignalToGotoolsReachesTheTool(t *testing.T) {
	for sig, want := range map[syscall.Signal]int{syscall.SIGINT: 130, syscall.SIGTERM: 143} {
		t.Run(sig.String(), func(t *testing.T) {
			p := newProject(t)
			p.ok("init")
			p.ok("install", "goimports", goimports)

			// goimports with no arguments filters stdin, so it waits for as
			// long as the pipe stays open.
			cmd := exec.Command(binary, "exec", "goimports")
			cmd.Dir, cmd.Env = p.dir, p.env

			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}

			defer stdin.Close()

			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}

			// Give go time to build the tool and start it.
			time.Sleep(3 * time.Second)

			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}

			done := make(chan error, 1)

			go func() { done <- cmd.Wait() }()

			select {
			case err := <-done:
				ee, ok := errors.AsType[*exec.ExitError](err)
				if !ok || ee.ExitCode() != want {
					t.Fatalf("exit = %v, want %d", err, want)
				}
			case <-time.After(30 * time.Second):
				_ = cmd.Process.Kill()

				t.Fatal("gotools did not stop after the signal: the tool kept it waiting")
			}
		})
	}
}

// exec runs the tool where it was started, in every strategy, as `go tool`
// does: from a package directory, `exec goimports -l .` sees that package and
// nothing else. A go:generate directive is the case that matters, since go
// runs each one in its own package's directory. (Reported in pull request #50
// against the Bash version.)
func TestExecRunsInTheInvocationDirectory(t *testing.T) {
	for _, strategy := range []string{"split", "unified", "module"} {
		t.Run(strategy, func(t *testing.T) {
			p := newProject(t)
			p.ok("init", "--strategy="+strategy)
			p.ok("install", "goimports", goimports)

			// Badly formatted files at the project root, in the package and
			// in the tools directory: only the package's is in scope.
			for rel, pkg := range map[string]string{"root_probe.go": "main", "sub/sub_probe.go": "sub", "tools/tool_probe.go": "tools"} {
				path := filepath.Join(p.dir, rel)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}

				if err := os.WriteFile(path, []byte("package   "+pkg+"\nfunc F(){}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			sub := filepath.Join(p.dir, "sub")

			run := func(dir, name string, args ...string) string {
				t.Helper()

				cmd := exec.Command(name, args...)
				cmd.Dir, cmd.Env = dir, p.env

				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%s %v: %v\n%s", name, args, err, out)
				}

				return strings.TrimSpace(string(out))
			}

			if got := run(sub, binary, "exec", "goimports", "-l", "."); got != "sub_probe.go" {
				t.Fatalf("exec from the package scanned %q, want only sub_probe.go", got)
			}

			if err := os.WriteFile(
				filepath.Join(sub, "gen.go"),
				[]byte("//go:generate "+binary+" exec goimports -l .\npackage sub\n"),
				0o600,
			); err != nil {
				t.Fatal(err)
			}

			if got := run(p.dir, "go", "generate", "./sub/"); got != "sub_probe.go" {
				t.Fatalf("go generate scanned %q, want only sub_probe.go", got)
			}
		})
	}
}
