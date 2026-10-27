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
	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/fingerprint"
	"github.com/piusalfred/gotools/internal/manifest"
)

func TestRenameSplit(t *testing.T) {
	dir := infoFixture(t, "split")
	writeFile(t, filepath.Join(dir, "tools", "goimports.sum"), "sumdata\n")

	out, _, err := runCommand(t, command.NewRename(), dir, &fakeGo{}, nil, "goimports", "gi")
	if err != nil || !strings.Contains(out, "✅ Renamed goimports → gi") {
		t.Fatalf("err=%v out=%s", err, out)
	}

	tools := filepath.Join(dir, "tools")
	if exists(filepath.Join(tools, "goimports.mod")) || exists(filepath.Join(tools, "goimports.sum")) {
		t.Fatal("old files remain")
	}

	if mod := readFile(t, filepath.Join(tools, "gi.mod")); !strings.Contains(mod, "module tools/tools/gi\n") ||
		!strings.Contains(mod, "tool golang.org/x/tools/cmd/goimports") {
		t.Fatalf("mod = %s", mod)
	}

	if readFile(t, filepath.Join(tools, "gi.sum")) != "sumdata\n" {
		t.Fatal("sum not moved intact")
	}

	if got := manifestTools(t, dir); len(got) != 1 || got[0] != "gi" {
		t.Fatalf("manifest = %v", got)
	}

	if fp := readFile(t, filepath.Join(tools, fingerprint.FileName)); !strings.Contains(fp, "|split|1.27|tools") {
		t.Fatalf("fingerprint = %q", fp)
	}
}

func TestRenameModule(t *testing.T) {
	dir := infoFixture(t, "module")

	if _, _, err := runCommand(t, command.NewRename(), dir, &fakeGo{}, nil, "goimports", "gi"); err != nil {
		t.Fatal(err)
	}

	if exists(filepath.Join(dir, "tools", "goimports")) {
		t.Fatal("old dir remains")
	}

	if mod := readFile(
		t,
		filepath.Join(dir, "tools", "gi", "go.mod"),
	); !strings.Contains(
		mod,
		"module tools/tools/gi\n",
	) {
		t.Fatalf("mod = %s", mod)
	}
}

func TestRenameUnifiedIsRefused(t *testing.T) {
	dir := infoFixture(t, "unified")

	_, _, err := runCommand(t, command.NewRename(), dir, &fakeGo{}, nil, "goimports", "gi")
	if ierrors.CodeOf(err) != ierrors.Environment ||
		!strings.Contains(err.Error(), "not supported with the unified strategy") ||
		!strings.Contains(err.Error(), "gotools install gi golang.org/x/tools/cmd/goimports@v0.30.0") {
		t.Fatalf("err = %v", err)
	}

	if got := manifestTools(t, dir); got[0] != "goimports" {
		t.Fatalf("manifest changed: %v", got)
	}
}

func TestRenamePreconditions(t *testing.T) {
	dir := infoFixture(t, "split")

	m := readManifest(t, dir)
	m.SetTool(manifest.Tool{Name: "taken", Source: "go", Package: "example.com/taken", Version: "v1"})

	if err := m.Flush(filepath.Join(dir, config.ManifestFile)); err != nil {
		t.Fatal(err)
	}

	// A dangling symlink at the target name must block the rename.
	if err := os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, "tools", "dangling.mod")); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		args []string
		code ierrors.Code
		msg  string
	}{
		{[]string{"goimports"}, ierrors.Usage, "Missing argument"},
		{[]string{"a", "b", "c"}, ierrors.Usage, "Unknown argument: c"},
		{[]string{"goimports", "goimports"}, ierrors.Usage, "names are identical"},
		{[]string{"goimports", "../x"}, ierrors.Usage, "path separator"},
		{[]string{"ghost", "x"}, ierrors.ToolNotFound, "'ghost' not found in manifest"},
		{[]string{"goimports", "taken"}, ierrors.Usage, "'taken' already exists"},
		{[]string{"goimports", "dangling"}, ierrors.Environment, "already exists but is not in the manifest"},
	}

	for _, c := range cases {
		for _, dry := range []bool{false, true} {
			args := append([]string{}, c.args...)
			if dry {
				args = append(args, "--dry-run")
			}

			_, _, err := runCommand(t, command.NewRename(), dir, &fakeGo{}, nil, args...)
			if ierrors.CodeOf(err) != c.code || err == nil || !strings.Contains(err.Error(), c.msg) {
				t.Errorf("%v: err=%v", args, err)
			}
		}
	}

	if !exists(filepath.Join(dir, "tools", "goimports.mod")) {
		t.Fatal("a refused rename moved files")
	}
}

func TestRenameDryRunChangesNothing(t *testing.T) {
	dir := infoFixture(t, "split")
	before := tree(t, dir)

	out, _, err := runCommand(t, command.NewRename(), dir, &fakeGo{}, nil, "goimports", "gi", "--dry-run")
	if err != nil || !strings.Contains(out, "🔍 [dry-run] Would rename goimports → gi") {
		t.Fatalf("err=%v out=%s", err, out)
	}

	sameTree(t, before, tree(t, dir))
}

func TestRenameRefusesASymlinkedSource(t *testing.T) {
	dir := infoFixture(t, "split")
	mod := filepath.Join(dir, "tools", "goimports.mod")
	target := filepath.Join(dir, "real.mod")

	if err := os.Rename(mod, target); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(target, mod); err != nil {
		t.Fatal(err)
	}

	_, _, err := runCommand(t, command.NewRename(), dir, &fakeGo{}, nil, "goimports", "gi")
	if ierrors.CodeOf(err) != ierrors.Environment || !strings.Contains(err.Error(), "Refusing to follow symlink") {
		t.Fatalf("err = %v", err)
	}
}

func TestRenameManifestOnlyWhenTheModfileIsMissing(t *testing.T) {
	dir := infoFixture(t, "split")
	_ = os.Remove(filepath.Join(dir, "tools", "goimports.mod"))

	out, _, err := runCommand(t, command.NewRename(), dir, &fakeGo{}, nil, "goimports", "gi")
	if err != nil || !strings.Contains(out, "renaming in the manifest only") || manifestTools(t, dir)[0] != "gi" {
		t.Fatalf("err=%v out=%s", err, out)
	}

	if exists(filepath.Join(dir, "tools", fingerprint.FileName)) {
		t.Fatal("fingerprint written although disk and manifest disagree")
	}
}

// If the manifest cannot be written, the files go back.
func TestRenameRollsBackWhenTheManifestWriteFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}

	dir := infoFixture(t, "split")
	orig := readFile(t, filepath.Join(dir, "tools", "goimports.mod"))

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}

	defer func() { _ = os.Chmod(dir, 0o700) }()

	_, _, err := runCommand(t, command.NewRename(), dir, &fakeGo{}, nil, "goimports", "gi")
	if ierrors.CodeOf(err) != ierrors.Generic || !strings.Contains(err.Error(), "manifest") {
		t.Fatalf("err = %v", err)
	}

	if exists(filepath.Join(dir, "tools", "gi.mod")) ||
		readFile(t, filepath.Join(dir, "tools", "goimports.mod")) != orig {
		t.Fatal("rename was not rolled back")
	}
}
