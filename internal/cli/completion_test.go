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

package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piusalfred/gotools/internal/cli"
	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/manifest"
)

// runWith runs the app in dir with the environment.
func runWith(t *testing.T, dir string, environ []string, args ...string) result {
	t.Helper()

	var out, errb bytes.Buffer

	app := &cli.App{
		Program: "gotools",
		Version: "v9.9.9",
		Stdout:  &out,
		Stderr:  &errb,
		Environ: environ,
		WorkDir: dir,
		Go:      goStub{},
	}
	err := app.Run(context.Background(), args)

	return result{out: out.String(), err: errb.String(), runErr: err, app: app}
}

// toolProject is a project whose manifest lists tools a and b.
func toolProject(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	m := manifest.New()
	m.Strategy, m.ModulePrefix = "split", "tools"
	m.SetTool(manifest.Tool{Name: "a", Source: "go", Package: "example.com/a", Version: "v1"})
	m.SetTool(manifest.Tool{Name: "b", Source: "go", Package: "example.com/b", Version: "v1"})

	if err := m.Flush(filepath.Join(dir, config.ManifestFile)); err != nil {
		t.Fatal(err)
	}

	return dir
}

func words(out string) []string {
	var w []string

	for l := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		name, _, _ := strings.Cut(l, ":")
		if name != "" {
			w = append(w, name)
		}
	}

	return w
}

func TestCompletionPrintsUrfavesScriptForEachShell(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "pwsh"} {
		r := runWith(t, t.TempDir(), nil, "completion", shell)
		if r.runErr != nil || strings.TrimSpace(r.out) == "" {
			t.Fatalf("%s: err=%v out=%q", shell, r.runErr, r.out)
		}

		if shell != "pwsh" && !strings.Contains(r.out, "generate-shell-completion") {
			t.Errorf("%s script does not ask the program for words", shell)
		}

		if shell == "bash" && !strings.Contains(r.out, "gotools") {
			t.Errorf("bash script lacks the program name")
		}
	}
}

func TestCompletionIsPartOfTheOverview(t *testing.T) {
	r := runWith(t, t.TempDir(), nil, "help")
	if !strings.Contains(r.out, "completion <bash|zsh|fish|pwsh|install>") ||
		!strings.Contains(r.out, "Print or install shell completions.") {
		t.Fatalf("overview:\n%s", r.out)
	}
}

func TestCompletionWithNoShellShowsTheShellsAndAnUnknownOneIsAUsageError(t *testing.T) {
	r := runWith(t, t.TempDir(), nil, "completion")
	if r.runErr != nil || !strings.Contains(r.out, "bash") || !strings.Contains(r.out, "install") {
		t.Fatalf("err=%v out=%q", r.runErr, r.out)
	}

	r = runWith(t, t.TempDir(), nil, "completion", "tcsh")
	if ierrors.CodeOf(r.runErr) != ierrors.Usage || !strings.Contains(r.runErr.Error(), "Unknown shell: tcsh") {
		t.Fatalf("err = %v", r.runErr)
	}
}

func TestCompletionInstallPaths(t *testing.T) {
	home := t.TempDir()

	cases := []struct {
		shell   string
		environ []string
		want    string
	}{
		{"bash", []string{"HOME=" + home}, filepath.Join(home, ".local/share/bash-completion/completions/gotools")},
		{
			"bash",
			[]string{"HOME=" + home, "XDG_DATA_HOME=" + home + "/xdg"},
			home + "/xdg/bash-completion/completions/gotools",
		},
		{
			"bash",
			[]string{"HOME=" + home, "XDG_DATA_HOME=" + home + "/xdg", "BASH_COMPLETION_USER_DIR=" + home + "/bc"},
			home + "/bc/completions/gotools",
		},
		{"zsh", []string{"HOME=" + home}, home + "/.zsh/completions/_gotools"},
		{"fish", []string{"HOME=" + home}, home + "/.config/fish/completions/gotools.fish"},
		{"pwsh", []string{"HOME=" + home}, home + "/.config/powershell/completions/gotools.ps1"},
		{
			"pwsh",
			[]string{"HOME=" + home, "XDG_CONFIG_HOME=" + home + "/cfg"},
			home + "/cfg/powershell/completions/gotools.ps1",
		},
	}

	for _, c := range cases {
		r := runWith(t, t.TempDir(), c.environ, "completion", "install", c.shell)
		if r.runErr != nil || !strings.Contains(r.out, "✅ Installing completion to "+c.want) {
			t.Fatalf("%s: err=%v out=%s", c.shell, r.runErr, r.out)
		}

		b, err := os.ReadFile(c.want)
		if err != nil || !strings.Contains(string(b), "generate-shell-completion") {
			t.Fatalf("%s not written: %v", c.shell, err)
		}

		// What was installed is what `completion <shell>` prints.
		if printed := runWith(t, t.TempDir(), nil, "completion", c.shell).out; string(b) != printed {
			t.Errorf("%s: the installed script differs from the printed one", c.shell)
		}

		// Installing again changes nothing, so there is nothing to back up.
		r = runWith(t, t.TempDir(), c.environ, "completion", "install", c.shell)
		if !strings.Contains(r.out, "Completion at "+c.want+" is already up to date") {
			t.Fatalf("%s second run: %s", c.shell, r.out)
		}

		if _, err := os.Stat(c.want + ".bak"); err == nil {
			t.Fatalf("%s: a backup of an identical script", c.shell)
		}
	}
}

func TestCompletionInstallDetectsTheShell(t *testing.T) {
	home := t.TempDir()

	r := runWith(t, t.TempDir(), []string{"HOME=" + home, "SHELL=/bin/zsh"}, "completion", "install")
	if r.runErr != nil || !strings.Contains(r.out, "Completion installed for zsh") ||
		!strings.Contains(r.out, "fpath=") {
		t.Fatalf("err=%v out=%s", r.runErr, r.out)
	}

	r = runWith(t, t.TempDir(), []string{"HOME=" + home, "SHELL=/bin/tcsh"}, "completion", "install")
	if ierrors.CodeOf(r.runErr) != ierrors.Usage || !strings.Contains(r.runErr.Error(), "Could not detect shell") {
		t.Fatalf("err = %v", r.runErr)
	}

	r = runWith(t, t.TempDir(), []string{"HOME=" + home}, "completion", "install", "ksh")
	if ierrors.CodeOf(r.runErr) != ierrors.Usage || !strings.Contains(r.runErr.Error(), "bash|zsh|fish|pwsh") {
		t.Fatalf("ksh: err=%v", r.runErr)
	}

	r = runWith(t, t.TempDir(), []string{"HOME=" + home, "SHELL=/usr/bin/pwsh"}, "completion", "install")
	if r.runErr != nil || !strings.Contains(r.out, "Completion installed for pwsh") {
		t.Fatalf("pwsh from $SHELL: err=%v out=%q", r.runErr, r.out)
	}
}

func TestCompletionInstallExplainsHowToActivate(t *testing.T) {
	home := t.TempDir()

	for shell, want := range map[string]string{"bash": "~/.bashrc", "fish": "auto-loads completions", "zsh": "compinit", "pwsh": "$PROFILE"} {
		r := runWith(t, t.TempDir(), []string{"HOME=" + home}, "completion", "install", shell)
		if !strings.Contains(r.out, want) {
			t.Errorf("%s: %q lacks %q", shell, r.out, want)
		}
	}
}

func TestToolNamesAreOfferedWhereAToolIsExpected(t *testing.T) {
	dir := toolProject(t)

	for _, cmd := range []string{"exec", "info", "rename", "remove"} {
		r := runWith(t, dir, nil, cmd, "--generate-shell-completion")
		if r.runErr != nil || strings.Join(words(r.out), " ") != "a b" {
			t.Errorf("%s: err=%v out=%q", cmd, r.runErr, r.out)
		}
	}

	r := runWith(t, dir, nil, "upgrade", "--generate-shell-completion")
	if strings.Join(words(r.out), " ") != "all a b" {
		t.Errorf("upgrade: %q", r.out)
	}
}

func TestCompletionStopsOfferingNamesOnceTheArgumentsAreComplete(t *testing.T) {
	dir := toolProject(t)

	for _, args := range [][]string{{"info", "a"}, {"exec", "a"}, {"rename", "a"}, {"migrate", "split"}, {"config", "GOTOOLS_JOBS"}} {
		r := runWith(t, dir, nil, append(args, "--generate-shell-completion")...)
		if r.runErr != nil || strings.TrimSpace(r.out) != "" {
			t.Errorf("%v: err=%v out=%q", args, r.runErr, r.out)
		}
	}

	// remove takes any number of names.
	if r := runWith(
		t,
		dir,
		nil,
		"remove",
		"a",
		"--generate-shell-completion",
	); strings.Join(
		words(r.out),
		" ",
	) != "a b" {
		t.Errorf("remove a: %q", r.out)
	}
}

func TestStrategiesAndConfigKeysAreOffered(t *testing.T) {
	r := runWith(t, t.TempDir(), nil, "migrate", "--generate-shell-completion")
	if strings.Join(words(r.out), " ") != "unified split module" {
		t.Errorf("migrate: %q", r.out)
	}

	r = runWith(t, t.TempDir(), nil, "config", "--generate-shell-completion")
	if strings.Join(words(r.out), " ") != strings.Join(config.Keys, " ") {
		t.Errorf("config: %q", r.out)
	}
}

func TestFlagsAndSubcommandsAreOfferedByUrfave(t *testing.T) {
	r := runWith(t, t.TempDir(), nil, "init", "--st", "--generate-shell-completion")
	if !strings.Contains(r.out, "--strategy") {
		t.Errorf("flags: %q", r.out)
	}

	// A command that has subcommands offers them.
	r = runWith(t, t.TempDir(), nil, "completion", "--generate-shell-completion")
	if got := strings.Join(words(r.out), " "); !strings.HasPrefix(got, "bash zsh fish pwsh install") {
		t.Errorf("completion offers %q", got)
	}
}

// Completion only asks for words: nothing is loaded, required or refused, and no
// command runs.
func TestCompletionRunsNothing(t *testing.T) {
	dir := t.TempDir()
	missingGo := &countingGo{}

	var out bytes.Buffer

	app := &cli.App{
		Program: "gotools",
		Stdout:  &out,
		Stderr:  &bytes.Buffer{},
		Environ: []string{},
		WorkDir: dir,
		Go:      missingGo,
	}

	for _, args := range [][]string{{"install", "--generate-shell-completion"}, {"remove", "--generate-shell-completion"}, {"purge", "--generate-shell-completion"}, {"sync", "--generate-shell-completion"}} {
		if err := app.Run(context.Background(), args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}

	if missingGo.calls != 0 {
		t.Fatalf("completion ran go %d times", missingGo.calls)
	}

	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("completion wrote %v", entries)
	}
}

type countingGo struct{ calls int }

func (g *countingGo) Run(context.Context, gocmd.Call) (gocmd.Result, error) {
	g.calls++

	return gocmd.Result{}, nil
}

func TestCompletionOfAProjectThatCannotBeLoadedOffersNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.ManifestFile), []byte(`{broken`), 0o644); err != nil {
		t.Fatal(err)
	}

	r := runWith(t, dir, nil, "exec", "--generate-shell-completion")
	if r.runErr != nil || strings.TrimSpace(r.out) != "" {
		t.Fatalf("err=%v out=%q", r.runErr, r.out)
	}
}

func TestHasKnowsTheCompletionCommand(t *testing.T) {
	if !(&cli.App{}).Has("completion") {
		t.Fatal("completion is not served by the app")
	}
}

// A command that offers names still offers its flags while one is being typed.
func TestFlagsAreStillOfferedWhereNamesAre(t *testing.T) {
	dir := toolProject(t)

	r := runWith(t, dir, nil, "remove", "--dr", "--generate-shell-completion")
	if !strings.Contains(r.out, "--dry-run") || strings.Contains(r.out, "\na\n") {
		t.Fatalf("remove: %q", r.out)
	}

	r = runWith(t, dir, nil, "upgrade", "--off", "--generate-shell-completion")
	if !strings.Contains(r.out, "--offline") {
		t.Fatalf("upgrade: %q", r.out)
	}
}

// Completion only looks: not even the leftovers of a crashed write are cleaned.
func TestCompletionLeavesStaleTempFilesAlone(t *testing.T) {
	dir := toolProject(t)
	stale := filepath.Join(dir, config.ManifestFile+".tmp.1")

	if err := os.WriteFile(stale, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if r := runWith(t, dir, nil, "exec", "--generate-shell-completion"); strings.Join(words(r.out), " ") != "a b" {
		t.Fatalf("out = %q", r.out)
	}

	if _, err := os.Stat(stale); err != nil {
		t.Fatal("completion removed a temp file")
	}
}

// The last of two definitions of a variable wins, as in a real environment.
func TestCompletionInstallReadsTheEnvironmentLastDefinitionFirst(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()

	r := runWith(t, t.TempDir(), []string{"HOME=" + first, "HOME=" + second}, "completion", "install", "zsh")
	if r.runErr != nil || !strings.Contains(r.out, second) || strings.Contains(r.out, first) {
		t.Fatalf("err=%v out=%q", r.runErr, r.out)
	}
}

// A script that differs from the one being installed, say one edited by hand,
// is kept as .bak before it is replaced.
func TestCompletionInstallKeepsWhatItReplaces(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".zsh/completions/_gotools")

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("# my own tweaks\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := runWith(t, t.TempDir(), []string{"HOME=" + home}, "completion", "install", "zsh")
	if r.runErr != nil ||
		!strings.Contains(
			r.out,
			"Updating existing completion at "+path+" (the previous one is kept as _gotools.bak)",
		) {
		t.Fatalf("err=%v out=%q", r.runErr, r.out)
	}

	if b, err := os.ReadFile(path + ".bak"); err != nil || string(b) != "# my own tweaks\n" {
		t.Fatalf("backup = %q, %v", b, err)
	}

	if b, _ := os.ReadFile(
		path,
	); strings.Contains(string(b), "my own tweaks") ||
		!strings.Contains(string(b), "generate-shell-completion") {
		t.Fatalf("the script was not replaced: %q", b)
	}

	// An older backup is replaced by the newer one, never stacked.
	if err := os.WriteFile(path, []byte("# edited again\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runWith(t, t.TempDir(), []string{"HOME=" + home}, "completion", "install", "zsh")

	if b, _ := os.ReadFile(path + ".bak"); string(b) != "# edited again\n" {
		t.Fatalf("backup = %q", b)
	}
}

// `completion --help` reads like `help completion`, though urfave/cli prints a
// command that has subcommands through a template of its own.
func TestCompletionHelpFlagMatchesHelpCommand(t *testing.T) {
	want := runWith(t, t.TempDir(), nil, "help", "completion").out

	for _, flag := range []string{"--help", "-h"} {
		got := runWith(t, t.TempDir(), nil, "completion", flag).out
		if got == "" || got != want {
			t.Fatalf("completion %s:\n%s\n--- help completion:\n%s", flag, got, want)
		}
	}

	if got := runWith(t, t.TempDir(), nil, "completion").out; got != want {
		t.Fatalf("bare completion differs from help completion:\n%s", got)
	}
}
