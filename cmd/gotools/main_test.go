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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/piusalfred/gotools/internal/gocmd"
)

func build(t *testing.T) string {
	t.Helper()

	bin := filepath.Join(t.TempDir(), "gotools")

	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	return bin
}

// The old wrapper handed the whole embedded script to `bash -c`, which fails on
// Linux once the script exceeds one argv string (128 KiB), and needed bash at
// all. The binary is self-contained: it starts with neither bash nor go on PATH.
func TestBinaryNeedsNothingOnPath(t *testing.T) {
	bin := build(t)
	empty := t.TempDir()

	for _, args := range [][]string{{"version"}, {"--version"}, {"-v"}, {"version", "--format=json"}, {"help"}, {"--help"}, {"-h"}, {"sync", "--help"}, {"help", "install"}} {
		cmd := exec.Command(bin, args...)
		cmd.Env = []string{"PATH=" + empty, "HOME=" + empty}

		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}

		if !strings.Contains(string(out), "gotools") {
			t.Errorf("%v: unexpected output %q", args, out)
		}
	}
}

func TestBinaryReportsAnUnknownCommand(t *testing.T) {
	bin := build(t)

	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}

	defer devnull.Close()

	cmd := exec.Command(bin, "nope")
	cmd.Stdin = devnull

	out, err := cmd.CombinedOutput()
	if _, ok := err.(*exec.ExitError); !ok {
		t.Fatalf("an unknown command must fail, err = %v", err)
	}

	if !strings.Contains(string(out), "❌") || !strings.Contains(string(out), "nope") {
		t.Fatalf("output %q", out)
	}
}

// run runs the binary in dir with the real PATH, since init needs go.
func runIn(t *testing.T, bin, dir string, args ...string) (string, int) {
	t.Helper()

	cmd := exec.Command(bin, args...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), ee.ExitCode()
	}

	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}

	return string(out), 0
}

func project(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/t\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	return dir
}

// init has moved to internal/cli; its sync still runs through lib.
func TestInitWritesTheManifestAndSyncs(t *testing.T) {
	bin, dir := build(t), project(t)

	out, code := runIn(t, bin, dir, "init", "--strategy", "module")
	if code != 0 || !strings.Contains(out, "Initialized .gotools.json (strategy=module, dir=tools)") || !strings.Contains(out, "Sync complete") {
		t.Fatalf("code=%d out=%q", code, out)
	}

	b, err := os.ReadFile(filepath.Join(dir, ".gotools.json"))
	if err != nil || !strings.Contains(string(b), `"strategy": "module"`) || !strings.Contains(string(b), `"module_prefix": "example.com/t"`) {
		t.Fatalf("manifest %q: %v", b, err)
	}
}

func TestInitDryRunWritesNothing(t *testing.T) {
	bin, dir := build(t), project(t)

	out, code := runIn(t, bin, dir, "init", "--dry-run", "--strategy=unified")
	if code != 0 || !strings.Contains(out, "[dry-run] Would write .gotools.json (strategy=unified, dir=tools)") || !strings.Contains(out, "Would sync (strategy=unified") {
		t.Fatalf("code=%d out=%q", code, out)
	}

	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("a dry run wrote %v", entries)
	}
}

func TestInitUsageErrorExitsTwo(t *testing.T) {
	bin, dir := build(t), project(t)

	for _, args := range [][]string{{"init", "--bogus"}, {"init", "--strategy=flat"}, {"init", "extra"}} {
		if out, code := runIn(t, bin, dir, args...); code != 2 || !strings.Contains(out, "❌") {
			t.Errorf("%v: code=%d out=%q", args, code, out)
		}
	}
}

func TestInitHelpComesFromTheMovedCommand(t *testing.T) {
	bin := build(t)

	out, code := runIn(t, bin, t.TempDir(), "init", "--help")
	if code != 0 || !strings.Contains(out, "Running init again keeps what the manifest already says") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

// sync and migrate have moved: a layout that differs from the manifest is
// migrated by the same binary, with no tools to download.
func TestSyncAutoMigratesAndMigrateRuns(t *testing.T) {
	bin, dir := build(t), project(t)

	if out, code := runIn(t, bin, dir, "init", "--strategy", "module"); code != 0 {
		t.Fatalf("init: code=%d out=%q", code, out)
	}

	if err := os.WriteFile(filepath.Join(dir, "tools", "x.mod"), []byte("module tools/x\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := runIn(t, bin, dir, "sync")
	if code != 0 || !strings.Contains(out, "Auto-migrating to 'module'") || !strings.Contains(out, "Migration from 'split' to 'module' complete") {
		t.Fatalf("sync: code=%d out=%q", code, out)
	}

	out, code = runIn(t, bin, dir, "migrate", "split", "--dry-run")
	if code != 0 || !strings.Contains(out, "[dry-run] Would migrate tools/ from 'module' to 'split'") {
		t.Fatalf("migrate --dry-run: code=%d out=%q", code, out)
	}

	if out, code := runIn(t, bin, dir, "migrate", "flat"); code != 2 || !strings.Contains(out, "Invalid strategy: flat") {
		t.Fatalf("migrate flat: code=%d out=%q", code, out)
	}
}

// version has moved, and --version and -v are served with it.
func TestVersionAndItsFlags(t *testing.T) {
	bin := build(t)

	for _, args := range [][]string{{"version"}, {"--version"}, {"-v"}} {
		out, code := runIn(t, bin, t.TempDir(), args...)
		if code != 0 || !strings.HasPrefix(out, "gotools v") || strings.Count(out, "\n") != 1 {
			t.Errorf("%v: code=%d out=%q", args, code, out)
		}
	}

	out, code := runIn(t, bin, t.TempDir(), "version", "--json")
	if code != 0 || !strings.HasPrefix(out, `{"schema_version":1,"name":"gotools","version":"v`) {
		t.Fatalf("code=%d out=%q", code, out)
	}

	if out, code := runIn(t, bin, t.TempDir(), "version", "--json", "--text"); code != 2 || !strings.Contains(out, "Conflicting output formats") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

// info has moved to internal/cli: it resolves the tool from the modfile on disk.
func TestInfoShowsAToolAndItsExitCode(t *testing.T) {
	bin, dir := build(t), project(t)

	if err := os.MkdirAll(filepath.Join(dir, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}

	mod := "module tools/goimports\n\ngo 1.24\n\ntool golang.org/x/tools/cmd/goimports\n\nrequire golang.org/x/tools v0.30.0\n"
	if err := os.WriteFile(filepath.Join(dir, "tools", "goimports.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, ".gotools.json"), []byte(`{"strategy":"split"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := runIn(t, bin, dir, "info", "goimports")
	if code != 0 || !strings.Contains(out, "  Version:    v0.30.0\n") || !strings.Contains(out, "  Strategy:   split\n") {
		t.Fatalf("code=%d out=%q", code, out)
	}

	if out, code := runIn(t, bin, dir, "info", "nope", "--json"); code != 5 || !strings.Contains(out, `"error":"tool not found: nope"`) {
		t.Fatalf("code=%d out=%q", code, out)
	}

	if _, code := runIn(t, bin, dir, "info"); code != 2 {
		t.Fatalf("code=%d", code)
	}
}

// check has moved to internal/cli: a tool that is not runnable exits 5.
func TestCheckExitsFiveForAToolThatCannotRun(t *testing.T) {
	bin, dir := build(t), project(t)

	if err := os.MkdirAll(filepath.Join(dir, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "tools", "ghost.mod"), []byte("module tools/ghost\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest := `{"strategy":"split","tools":{"ghost":{"source":"go","package":"example.com/ghost","version":"v1.0.0"}}}`
	if err := os.WriteFile(filepath.Join(dir, ".gotools.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := runIn(t, bin, dir, "check")
	if code != 5 || !strings.Contains(out, "❌ ghost (example.com/ghost@v1.0.0) — not runnable") || !strings.Contains(out, "0 passed, 1 failed") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

// config has moved to internal/cli: it creates, reads and sets the manifest.
func TestConfigCreatesReadsAndSets(t *testing.T) {
	bin, dir := build(t), project(t)

	if out, code := runIn(t, bin, dir, "config", "GOTOOLS_JOBS=4"); code != 0 || !strings.Contains(out, "Created .gotools.json with GOTOOLS_JOBS=4") {
		t.Fatalf("code=%d out=%q", code, out)
	}

	if out, code := runIn(t, bin, dir, "config", "GOTOOLS_JOBS"); code != 0 || out != "4\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}

	if _, code := runIn(t, bin, dir, "config", "GOTOOLS_JOBS", "0"); code != 2 {
		t.Fatalf("code=%d", code)
	}
}

// remove, rename and upgrade have moved to internal/cli.
func TestRemoveRenameAndUpgradeUsageExitTwo(t *testing.T) {
	bin, dir := build(t), project(t)

	for _, args := range [][]string{{"remove"}, {"rename", "a"}, {"upgrade"}, {"upgrade", "all", "x"}} {
		if out, code := runIn(t, bin, dir, args...); code != 2 {
			t.Errorf("%v: code=%d out=%q", args, code, out)
		}
	}

	if out, code := runIn(t, bin, dir, "remove", "ghost"); code != 5 || !strings.Contains(out, "Tool not found: ghost") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

// list has moved to internal/cli.
func TestListShowsTheManifestAndRejectsABadFormat(t *testing.T) {
	bin, dir := build(t), project(t)

	if err := os.WriteFile(filepath.Join(dir, ".gotools.json"), []byte(`{"strategy":"split","tools":{"a":{"source":"go","package":"p/a","version":"v1"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, code := runIn(t, bin, dir, "list", "--json"); code != 0 || !strings.HasPrefix(out, `{"schema_version":1,"strategy":"split"`) || !strings.Contains(out, `"name":"a"`) {
		t.Fatalf("code=%d out=%q", code, out)
	}

	if _, code := runIn(t, bin, dir, "list", "--format=xml"); code != 2 {
		t.Fatalf("code=%d", code)
	}
}

// exec has moved to internal/cli. Everything after the tool's name is the
// tool's, so even gotools' own flags are not read.
func TestExecPassesFlagsToTheToolAndReportsAMissingOne(t *testing.T) {
	bin, dir := build(t), project(t)

	if err := os.WriteFile(filepath.Join(dir, ".gotools.json"), []byte(`{"strategy":"split"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := runIn(t, bin, dir, "exec", "ghost", "--version", "-v", "--json")
	if code != 5 || !strings.Contains(out, "Tool 'ghost' not found (tools/ghost.mod missing). Run 'install' first.") {
		t.Fatalf("code=%d out=%q", code, out)
	}

	if out, code := runIn(t, bin, dir, "exec"); code != 2 {
		t.Fatalf("code=%d out=%q", code, out)
	}

	if out, code := runIn(t, bin, dir, "exec", "--help"); code != 0 || !strings.Contains(out, "Run a managed tool with the given arguments") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

// uninstall has moved to internal/cli. Its dry run names the binary and keeps it.
func TestUninstallDryRunKeepsTheBinary(t *testing.T) {
	bin := build(t)

	out, code := runIn(t, bin, t.TempDir(), "uninstall", "--dry-run")
	if code != 0 || !strings.Contains(out, "[dry-run] Would remove") {
		t.Fatalf("code=%d out=%q", code, out)
	}

	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("binary gone: %v", err)
	}
}

// purge has moved to internal/cli: without a manifest there is nothing to wipe,
// and a typo is refused before anything is deleted.
func TestPurgeRefusesATypoAndLeavesAForeignToolsDirectory(t *testing.T) {
	bin, dir := build(t), project(t)

	if err := os.MkdirAll(filepath.Join(dir, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}

	if out, code := runIn(t, bin, dir, "purge"); code != 0 || !strings.Contains(out, "nothing to purge") {
		t.Fatalf("code=%d out=%q", code, out)
	}

	if _, code := runIn(t, bin, dir, "purge", "--restor"); code != 2 {
		t.Fatalf("code=%d", code)
	}

	if _, err := os.Stat(filepath.Join(dir, "tools")); err != nil {
		t.Fatal("tools directory removed")
	}
}

// doctor has moved to internal/cli. It always exits 0, even for a broken
// manifest, and a bad flag is the only way to a non-zero code.
func TestDoctorReportsAndNeverFailsTheShell(t *testing.T) {
	bin, dir := build(t), project(t)

	if err := os.WriteFile(filepath.Join(dir, ".gotools.json"), []byte(`{"strategy": "spl`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := runIn(t, bin, dir, "doctor", "--offline")
	if code != 0 || !strings.Contains(out, "❌ .gotools.json:") || !strings.Contains(out, "Go installation") {
		t.Fatalf("code=%d out=%q", code, out)
	}

	if out, code := runIn(t, bin, dir, "doctor", "--json", "--offline"); code != 0 || !strings.Contains(out, `"healthy":false`) {
		t.Fatalf("code=%d out=%q", code, out)
	}

	if _, code := runIn(t, bin, dir, "doctor", "--fix"); code != 2 {
		t.Fatalf("code=%d", code)
	}

	if _, err := os.Stat(filepath.Join(dir, "tools")); err == nil {
		t.Fatal("doctor created the tools directory")
	}
}

// self-update has moved to internal/cli. Offline mode and a bad argument are
// refused before anything is fetched.
func TestSelfUpdateRefusesOfflineAndBadArguments(t *testing.T) {
	bin := build(t)

	if out, code := runIn(t, bin, t.TempDir(), "self-update", "--offline"); code != 6 || !strings.Contains(out, "Offline mode: cannot check for updates without network.") {
		t.Fatalf("code=%d out=%q", code, out)
	}

	if _, code := runIn(t, bin, t.TempDir(), "self-upgrade", "--now"); code != 2 {
		t.Fatalf("code=%d", code)
	}
}

// completion has moved to internal/cli and is urfave/cli's: the program is asked
// for words, and answers for a project's tools.
func TestCompletionAsksTheProgramForWords(t *testing.T) {
	bin, dir := build(t), project(t)

	if err := os.WriteFile(filepath.Join(dir, ".gotools.json"), []byte(`{"strategy":"split","tools":{"aa":{"source":"go","package":"p/a","version":"v1"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, code := runIn(t, bin, dir, "exec", "--generate-shell-completion"); code != 0 || out != "aa\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}

	if out, code := runIn(t, bin, dir, "--generate-shell-completion"); code != 0 || !strings.Contains(out, "completion:") || !strings.Contains(out, "doctor:") {
		t.Fatalf("code=%d out=%q", code, out)
	}

	if out, code := runIn(t, bin, dir, "completion", "bash"); code != 0 || !strings.Contains(out, "--generate-shell-completion") {
		t.Fatalf("code=%d out=%q", code, out)
	}

	if _, code := runIn(t, bin, dir, "completion", "tcsh"); code != 2 {
		t.Fatalf("code=%d", code)
	}
}

func TestBinaryExitCodes(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "gotools")

	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}

	defer devnull.Close()

	for args, want := range map[string]int{"nope": 2, "": 2, "version --format=yaml": 2, "version": 0} {
		cmd := exec.Command(bin, strings.Fields(args)...)
		cmd.Stdin = devnull

		code := 0

		if err := cmd.Run(); err != nil {
			ee, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(err)
			}

			code = ee.ExitCode()
		}

		if code != want {
			t.Errorf("gotools %q: exit %d, want %d", args, code, want)
		}
	}
}

// self-update asks before installing; offline still stops it first, even with --yes.
func TestSelfUpdateYesStillRefusesOffline(t *testing.T) {
	bin := build(t)

	if out, code := runIn(t, bin, t.TempDir(), "self-update", "--yes", "--offline"); code != 6 || strings.Contains(out, "(y/N)") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestSignalExit(t *testing.T) {
	cancelled := func(cause error) context.Context {
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(cause)

		return ctx
	}

	cases := []struct {
		name string
		ctx  context.Context
		code int
		want int
	}{
		{"no signal", context.Background(), 5, 5},
		{"cancelled for another reason", cancelled(errors.New("x")), 5, 5},
		{"sigint", cancelled(gocmd.SignalCause(syscall.SIGINT)), 1, 130},
		{"sigint over a tool's own code", cancelled(gocmd.SignalCause(syscall.SIGINT)), 130, 130},
		{"sigterm", cancelled(gocmd.SignalCause(syscall.SIGTERM)), 143, 143},
		{"sigterm over a generic failure", cancelled(gocmd.SignalCause(syscall.SIGTERM)), 1, 143},
		{"a run that succeeded keeps 0", cancelled(gocmd.SignalCause(syscall.SIGINT)), 0, 0},
	}

	for _, c := range cases {
		if got := signalExit(c.ctx, c.code); got != c.want {
			t.Errorf("%s: signalExit = %d, want %d", c.name, got, c.want)
		}
	}
}
