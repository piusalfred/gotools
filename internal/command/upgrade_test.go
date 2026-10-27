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
	"path/filepath"
	"strings"
	"testing"

	"github.com/piusalfred/gotools/internal/command"
	ierrors "github.com/piusalfred/gotools/internal/errors"
)

func toolVersion(t *testing.T, dir, name string) string {
	t.Helper()

	tl, _ := readManifest(t, dir).Tool(name)

	return tl.Version
}

func upgradeIn(t *testing.T, g *fakeGo, dir string, environ []string, args ...string) (string, string, error) {
	t.Helper()

	return runCommand(t, command.NewUpgrade(), dir, g, environ, args...)
}

func TestUpgradeOneToolEachStrategy(t *testing.T) {
	for _, st := range []string{"split", "module", "unified"} {
		t.Run(st, func(t *testing.T) {
			dir := installedProject(t, st)

			out, _, err := upgradeIn(t, installGo(installOpts{}), dir, nil, "x")
			if err != nil {
				t.Fatalf("err=%v out=%q", err, out)
			}

			for _, want := range []string{"Upgrading x (" + pkgX + ") from v1.0.0", "x: v1.0.0 → v1.2.3", "Upgrade complete"} {
				if !strings.Contains(out, want) {
					t.Fatalf("missing %q in %q", want, out)
				}
			}

			// The install chatter and the pin-your-version hint do not apply.
			if strings.Contains(out, "Installing") || strings.Contains(out, "Pin this version") {
				t.Fatalf("install output leaked: %q", out)
			}

			if toolVersion(t, dir, "x") != "v1.2.3" || toolVersion(t, dir, "y") != "v1.2.0" {
				t.Fatalf("versions x=%s y=%s", toolVersion(t, dir, "x"), toolVersion(t, dir, "y"))
			}

			if mod := readFile(t, filepath.Join(dir, layoutPaths(st, "x")[0])); !strings.Contains(mod, "v1.2.3") {
				t.Fatalf("modfile not upgraded:\n%s", mod)
			}
		})
	}
}

func TestUpgradeAllAndTheUpdateAlias(t *testing.T) {
	dir := installedProject(t, "split")

	var out, errb bytes.Buffer

	c := &command.Context{Stdout: &out, Stderr: &errb, Program: "gotools", WorkDir: dir, Go: installGo(installOpts{})}

	uc := command.NewUpgrade().Build(c)
	if err := uc.Run(context.Background(), []string{"update", "all"}); err != nil {
		t.Fatalf("err=%v out=%q err=%q", err, out.String(), errb.String())
	}

	if !uc.HasName("update") {
		t.Fatal("update is not an alias")
	}

	if toolVersion(t, dir, "x") != "v1.2.3" || toolVersion(t, dir, "y") != "v1.2.3" {
		t.Fatalf("versions x=%s y=%s", toolVersion(t, dir, "x"), toolVersion(t, dir, "y"))
	}

	// A second run finds nothing newer.
	out2, _, err := upgradeIn(t, installGo(installOpts{}), dir, nil, "x")
	if err != nil || !strings.Contains(out2, "x: already at latest (v1.2.3)") {
		t.Fatalf("err=%v out=%q", err, out2)
	}
}

func TestUpgradeAllWithNoTools(t *testing.T) {
	out, _, err := upgradeIn(t, installGo(installOpts{}), newProject(t, "split"), nil, "all")
	if err != nil || !strings.Contains(out, "No tools found to upgrade") {
		t.Fatalf("err=%v out=%q", err, out)
	}
}

func TestUpgradeDryRunWritesNothing(t *testing.T) {
	dir := installedProject(t, "unified")
	before := tree(t, dir)
	g := installGo(installOpts{})

	out, _, err := upgradeIn(t, g, dir, nil, "all", "--dry-run")
	if err != nil || !strings.Contains(out, "[dry-run] Would upgrade x ("+pkgX+") to @latest") ||
		!strings.Contains(out, "Would upgrade 2 tool(s)") {
		t.Fatalf("err=%v out=%q", err, out)
	}

	sameTree(t, before, tree(t, dir))
	noInstalls(t, g)
}

// A name the manifest does not know is an error, but only after the others were
// upgraded.
func TestUpgradeUnknownNameFailsButUpgradesTheRest(t *testing.T) {
	dir := installedProject(t, "split")

	out, _, err := upgradeIn(t, installGo(installOpts{}), dir, nil, "nope", "x")
	if ierrors.CodeOf(err) != ierrors.ToolNotFound || !strings.Contains(err.Error(), "Tool not found: nope") {
		t.Fatalf("err = %v", err)
	}

	if !strings.Contains(out, "Tool nope not found in manifest, skipping") || toolVersion(t, dir, "x") != "v1.2.3" {
		t.Fatalf("out=%q x=%s", out, toolVersion(t, dir, "x"))
	}
}

func TestUpgradeUsageErrors(t *testing.T) {
	dir := installedProject(t, "split")
	before := tree(t, dir)

	for name, args := range map[string][]string{
		"none":       {},
		"flags only": {"--dry-run"},
		"bad name":   {"x", "../evil"},
		"flag name":  {"--force"},
		"all+name":   {"all", "x"},
	} {
		g := installGo(installOpts{})

		if _, _, err := upgradeIn(t, g, dir, nil, args...); ierrors.CodeOf(err) != ierrors.Usage {
			t.Fatalf("%s: err=%v", name, err)
		}

		noInstalls(t, g)
	}

	sameTree(t, before, tree(t, dir))
}

func TestUpgradeOffline(t *testing.T) {
	dir := installedProject(t, "split")
	before := tree(t, dir)

	for name, call := range map[string]func() error{
		"env": func() error {
			_, _, err := upgradeIn(t, installGo(installOpts{}), dir, []string{"GOTOOLS_OFFLINE=1"}, "all")

			return err
		},
		"flag": func() error {
			_, _, err := upgradeIn(t, installGo(installOpts{}), dir, nil, "all", "--offline")

			return err
		},
	} {
		if err := call(); ierrors.CodeOf(err) != ierrors.Offline || !strings.Contains(err.Error(), "cannot upgrade") {
			t.Fatalf("%s: err=%v", name, err)
		}
	}

	sameTree(t, before, tree(t, dir))
}

// A failed upgrade leaves that tool exactly as it was; tools upgraded before it
// stay upgraded and recorded.
func TestUpgradeFailureKeepsTheFailedToolIntact(t *testing.T) {
	dir := installedProject(t, "split")
	yMod := readFile(t, filepath.Join(dir, "tools/y.mod"))

	_, _, err := upgradeIn(t, failingOn("example.com/b"), dir, nil, "all")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}

	if readFile(t, filepath.Join(dir, "tools/y.mod")) != yMod || toolVersion(t, dir, "y") != "v1.2.0" {
		t.Fatal("failed tool changed")
	}

	if toolVersion(t, dir, "x") != "v1.2.3" {
		t.Fatalf("earlier upgrade lost: x=%s", toolVersion(t, dir, "x"))
	}
}
