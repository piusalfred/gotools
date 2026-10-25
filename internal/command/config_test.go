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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piusalfred/gotools/internal/command"
	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/manifest"
)

func configIn(t *testing.T, dir string, environ []string, args ...string) (out string, err error) {
	t.Helper()

	var o bytes.Buffer

	c := &command.Context{Stdout: &o, Stderr: &bytes.Buffer{}, Environ: environ, Program: "gotools", WorkDir: dir, Go: &fakeGo{}}
	err = run(command.NewConfig(), c, args...)

	return o.String(), err
}

func TestConfigCreateSetGet(t *testing.T) {
	dir := t.TempDir()

	if _, err := configIn(t, dir, nil); ierrors.CodeOf(err) != ierrors.Environment {
		t.Fatalf("no manifest: %v", err)
	}

	out, err := configIn(t, dir, nil, "GOTOOLS_JOBS=4")
	if err != nil || !strings.Contains(out, "Created .gotools.json with GOTOOLS_JOBS=4") {
		t.Fatalf("err=%v out=%s", err, out)
	}

	if out, _ := configIn(t, dir, nil, "GOTOOLS_JOBS"); out != "4\n" {
		t.Fatalf("get = %q", out)
	}

	out, err = configIn(t, dir, nil, "GOTOOLS_STRATEGY", "module")
	if err != nil || !strings.Contains(out, "Set GOTOOLS_STRATEGY=module") || !strings.Contains(out, "Strategy changed. Run 'gotools sync'") {
		t.Fatalf("err=%v out=%s", err, out)
	}

	b := readFile(t, filepath.Join(dir, config.ManifestFile))
	if !strings.Contains(b, `"strategy": "module"`) || !strings.Contains(b, `"jobs": 4`) {
		t.Fatalf("manifest:\n%s", b)
	}
}

func TestConfigShowsTheFileAsItIs(t *testing.T) {
	dir := newProject(t, "split")

	out, err := configIn(t, dir, nil)
	if err != nil || out != readFile(t, filepath.Join(dir, config.ManifestFile)) {
		t.Fatalf("err=%v out=%q", err, out)
	}
}

func TestConfigGetsAKeyTheFileLeavesUnset(t *testing.T) {
	dir := newProject(t, "split")
	writeFile(t, filepath.Join(dir, config.ManifestFile), `{"version":1,"strategy":"split"}`)

	out, err := configIn(t, dir, nil, "GOTOOLS_MODULE_PREFIX")
	if err != nil || !strings.Contains(out, "GOTOOLS_MODULE_PREFIX is not set. (auto-detected from root go.mod when empty)") {
		t.Fatalf("err=%v out=%s", err, out)
	}

	if _, err := configIn(t, t.TempDir(), nil, "GOTOOLS_DIR"); ierrors.CodeOf(err) != ierrors.Environment {
		t.Fatalf("no manifest: %v", err)
	}
}

func TestConfigValidation(t *testing.T) {
	cases := [][]string{
		{"GOTOOLS_BOGUS", "x"},
		{"GOTOOLS_BOGUS"},
		{"GOTOOLS_STRATEGY", "nope"},
		{"GOTOOLS_DIR", "../x"},
		{"GOTOOLS_DIR", "./"},
		{"GOTOOLS_VERBOSE", "maybe"},
		{"GOTOOLS_JOBS", "abc"},
		{"GOTOOLS_JOBS", "0"},
		{"GOTOOLS_JOBS", "1", "extra"},
		{"GOTOOLS_JOBS=2", "extra"},
	}

	for _, args := range cases {
		dir := t.TempDir()

		if _, err := configIn(t, dir, nil, args...); ierrors.CodeOf(err) != ierrors.Usage {
			t.Errorf("%v: err=%v", args, err)
		}

		if _, err := os.Stat(filepath.Join(dir, config.ManifestFile)); err == nil {
			t.Errorf("%v wrote a manifest", args)
		}
	}
}

func TestConfigUnknownKeyListsTheValidOnes(t *testing.T) {
	_, err := configIn(t, t.TempDir(), nil, "GOTOOLS_BOGUS")
	if err == nil || !strings.Contains(err.Error(), "Unknown config key: GOTOOLS_BOGUS") || !strings.Contains(err.Error(), strings.Join(config.Keys, ", ")) {
		t.Fatalf("err = %v", err)
	}
}

// The environment steers a run and is never written back to the manifest.
func TestConfigDoesNotPersistTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	environ := []string{"GOTOOLS_DIR=envdir", "GOTOOLS_STRATEGY=unified", "GOTOOLS_JOBS=9"}

	if _, err := configIn(t, dir, environ, "GOTOOLS_VERBOSE", "true"); err != nil {
		t.Fatal(err)
	}

	b := readFile(t, filepath.Join(dir, config.ManifestFile))
	for _, bad := range []string{"envdir", "unified", `"jobs": 9`} {
		if strings.Contains(b, bad) {
			t.Errorf("the environment leaked (%s):\n%s", bad, b)
		}
	}
}

func TestConfigKeepsTheToolsAndTheStrategy(t *testing.T) {
	dir := newProject(t, "module")

	m := readManifest(t, dir)
	m.SetTool(manifest.Tool{Name: "a", Source: "go", Package: "p/a", Version: "v1"})

	if err := m.Flush(filepath.Join(dir, config.ManifestFile)); err != nil {
		t.Fatal(err)
	}

	if _, err := configIn(t, dir, nil, "GOTOOLS_JOBS", "2"); err != nil {
		t.Fatal(err)
	}

	b := readFile(t, filepath.Join(dir, config.ManifestFile))
	if !strings.Contains(b, `"strategy": "module"`) || !strings.Contains(b, "p/a") || !strings.Contains(b, `"jobs": 2`) {
		t.Fatalf("lost data:\n%s", b)
	}
}

func TestConfigRefusesANewerManifest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, config.ManifestFile), `{"version":9,"strategy":"split"}`)

	_, err := configIn(t, dir, nil)
	if ierrors.CodeOf(err) != ierrors.Environment || !strings.Contains(err.Error(), "schema version 9") {
		t.Fatalf("err = %v", err)
	}
}

func TestConfigRefusesASymlinkedToolsDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "tools")); err != nil {
		t.Skip(err)
	}

	if _, err := configIn(t, dir, nil, "GOTOOLS_JOBS", "2"); ierrors.CodeOf(err) != ierrors.Environment {
		t.Fatalf("err = %v", err)
	}
}

func TestConfigNeedsNoGo(t *testing.T) {
	g := &fakeGo{}

	var o bytes.Buffer

	c := &command.Context{Stdout: &o, Stderr: &bytes.Buffer{}, Program: "gotools", WorkDir: t.TempDir(), Go: g}
	if err := run(command.NewConfig(), c, "GOTOOLS_JOBS", "3"); err != nil || len(g.calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, g.calls)
	}
}
