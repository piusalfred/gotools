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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piusalfred/gotools/internal/command"
)

// inGitRepo makes dir look like the root of a git repository.
func inGitRepo(t *testing.T, dir string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestInitIgnoresTheLockFileInAGitRepo(t *testing.T) {
	dir := t.TempDir()
	inGitRepo(t, dir)

	o := initIn(t, dir, opts{}, nil)
	if o.runErr != nil {
		t.Fatal(o.runErr)
	}

	want := "# The gotools project lock: a pid that changes on every run. Local state, never committed.\n.gotools.flock\n"
	if got := readFile(t, filepath.Join(dir, ".gitignore")); got != want {
		t.Fatalf(".gitignore = %q", got)
	}

	if !strings.Contains(o.out, "📝 Added .gotools.flock to .gitignore") {
		t.Errorf("output lacks the notice:\n%s", o.out)
	}

	// init is the command that fixes it, so it does not nag about it.
	if strings.Contains(o.err, "not ignored by git") {
		t.Errorf("init warned about what it just fixed:\n%s", o.err)
	}

	// Running it again changes nothing and says nothing.
	again := initIn(t, dir, opts{}, nil)
	if again.runErr != nil || strings.Contains(again.out, "Added") ||
		readFile(t, filepath.Join(dir, ".gitignore")) != want {
		t.Fatalf("second init: err=%v out=%s", again.runErr, again.out)
	}
}

func TestInitKeepsAnExistingGitignore(t *testing.T) {
	dir := t.TempDir()
	inGitRepo(t, dir)
	writeFile(t, filepath.Join(dir, ".gitignore"), "bin/\n")

	if o := initIn(t, dir, opts{}, nil); o.runErr != nil {
		t.Fatal(o.runErr)
	}

	got := readFile(t, filepath.Join(dir, ".gitignore"))
	if !strings.HasPrefix(got, "bin/\n\n# The gotools project lock") || !strings.HasSuffix(got, "\n.gotools.flock\n") {
		t.Fatalf(".gitignore = %q", got)
	}
}

func TestInitLeavesAProjectThatAlreadyIgnoresTheLockFile(t *testing.T) {
	for desc, rules := range map[string]string{
		"literal":        ".gotools.flock\n",
		"pattern":        ".gotools.*\n",
		"in tools dir":   "",
		"in a parent":    "",
		"info/exclude":   "",
		"unrelated rule": "",
	} {
		t.Run(desc, func(t *testing.T) {
			repo := t.TempDir()
			inGitRepo(t, repo)

			project := repo

			switch desc {
			case "in tools dir":
				writeFile(t, filepath.Join(repo, "tools", ".gitignore"), ".gotools.flock\n")
			case "in a parent":
				project = filepath.Join(repo, "svc")
				writeFile(t, filepath.Join(repo, ".gitignore"), "**/.gotools.flock\n")
			case "info/exclude":
				writeFile(t, filepath.Join(repo, ".git", "info", "exclude"), ".gotools.flock\n")
			case "unrelated rule":
				writeFile(t, filepath.Join(repo, ".gitignore"), "bin/\n")
			}

			if err := os.MkdirAll(project, 0o755); err != nil {
				t.Fatal(err)
			}

			if rules != "" {
				writeFile(t, filepath.Join(project, ".gitignore"), rules)
			}

			before := ""
			if b, err := os.ReadFile(filepath.Join(project, ".gitignore")); err == nil {
				before = string(b)
			}

			if o := initIn(t, project, opts{}, nil); o.runErr != nil {
				t.Fatal(o.runErr)
			}

			after := ""
			if b, err := os.ReadFile(filepath.Join(project, ".gitignore")); err == nil {
				after = string(b)
			}

			changed := after != before
			if wantChange := desc == "unrelated rule"; changed != wantChange {
				t.Fatalf(".gitignore changed = %v, want %v (%q)", changed, wantChange, after)
			}
		})
	}
}

func TestInitDryRunDoesNotWriteTheGitignore(t *testing.T) {
	dir := t.TempDir()
	inGitRepo(t, dir)

	o := initIn(t, dir, opts{}, map[string]any{command.FlagDryRun: true})
	if o.runErr != nil {
		t.Fatal(o.runErr)
	}

	if !strings.Contains(o.out, "[dry-run] Would add .gotools.flock to .gitignore") {
		t.Errorf("output:\n%s", o.out)
	}

	if exists(filepath.Join(dir, ".gitignore")) {
		t.Fatal("a dry run wrote .gitignore")
	}
}

func TestInitOutsideAGitRepoOrWithoutALockWritesNoGitignore(t *testing.T) {
	plain := t.TempDir()
	if o := initIn(t, plain, opts{}, nil); o.runErr != nil || exists(filepath.Join(plain, ".gitignore")) {
		t.Fatalf("outside a repository: err=%v", o.runErr)
	}

	repo := t.TempDir()
	inGitRepo(t, repo)

	if o := initIn(t, repo, opts{environ: []string{"GOTOOLS_NO_LOCK=1"}}, nil); o.runErr != nil ||
		exists(filepath.Join(repo, ".gitignore")) {
		t.Fatalf("no lock file: err=%v", o.runErr)
	}
}

// A .gitignore that cannot be written is a warning: init has done its job.
func TestInitWarnsWhenTheGitignoreIsASymlink(t *testing.T) {
	dir := t.TempDir()
	inGitRepo(t, dir)

	target := filepath.Join(t.TempDir(), "shared")
	writeFile(t, target, "x\n")

	if err := os.Symlink(target, filepath.Join(dir, ".gitignore")); err != nil {
		t.Fatal(err)
	}

	o := initIn(t, dir, opts{}, nil)
	if o.runErr != nil {
		t.Fatalf("init failed over .gitignore: %v", o.runErr)
	}

	if !strings.Contains(o.err, "Could not add .gotools.flock to .gitignore") || readFile(t, target) != "x\n" {
		t.Fatalf("stderr: %s", o.err)
	}
}

// Every other command warns, so a project set up before this existed, or by
// hand, hears about it.
func TestOtherCommandsWarnAboutAnUnignoredLockFile(t *testing.T) {
	const warning = "tools/.gotools.flock is not ignored by git"

	dir := infoFixture(t, "split")
	inGitRepo(t, dir)

	_, errs, err := runCommand(t, command.NewList(), dir, &fakeGo{}, nil)
	if err != nil || !strings.Contains(errs, warning) || !strings.Contains(errs, "gotools init") {
		t.Fatalf("err=%v stderr=%s", err, errs)
	}

	// Ignored: quiet.
	writeFile(t, filepath.Join(dir, ".gitignore"), ".gotools.flock\n")

	if _, errs, _ = runCommand(t, command.NewList(), dir, &fakeGo{}, nil); strings.Contains(errs, "not ignored") {
		t.Errorf("warned although it is ignored:\n%s", errs)
	}

	// No lock, no file to ignore.
	if err := os.Remove(filepath.Join(dir, ".gitignore")); err != nil {
		t.Fatal(err)
	}

	if _, errs, _ = runCommand(
		t,
		command.NewList(),
		dir,
		&fakeGo{},
		[]string{"GOTOOLS_NO_LOCK=1"},
	); strings.Contains(
		errs,
		"not ignored",
	) {
		t.Errorf("warned about a lock that is never taken:\n%s", errs)
	}
}

func TestNoWarningOutsideAGitRepo(t *testing.T) {
	dir := infoFixture(t, "split")

	if _, errs, err := runCommand(t, command.NewList(), dir, &fakeGo{}, nil); err != nil ||
		strings.Contains(errs, "not ignored") {
		t.Fatalf("err=%v stderr=%s", err, errs)
	}
}

// A project that already has its manifest (set up by hand, or by an older
// gotools) is exactly where Load would warn. init fixes it, so it stays quiet.
func TestInitOnAnExistingProjectFixesWhatOtherCommandsWarnAbout(t *testing.T) {
	dir := newProject(t, "split")
	inGitRepo(t, dir)

	o := initIn(t, dir, opts{}, nil)
	if o.runErr != nil {
		t.Fatal(o.runErr)
	}

	if strings.Contains(o.err, "not ignored by git") {
		t.Errorf("init warned about what it fixes:\n%s", o.err)
	}

	if got := readFile(t, filepath.Join(dir, ".gitignore")); !strings.Contains(got, ".gotools.flock") {
		t.Fatalf(".gitignore = %q", got)
	}

	if _, errs, _ := runCommand(t, command.NewList(), dir, &fakeGo{}, nil); strings.Contains(errs, "not ignored") {
		t.Errorf("still warned after init:\n%s", errs)
	}
}
