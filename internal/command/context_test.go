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
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/manifest"
	"github.com/piusalfred/gotools/internal/modparser"
)

// adoptionContext is a loaded Context for a project whose root go.mod is gomod
// ("" for none) and whose manifest already manages tools.
func adoptionContext(t *testing.T, gomod string, managed ...manifest.Tool) (*command.Context, *fakeGo, *bytes.Buffer) {
	t.Helper()

	dir := newProject(t, "split")
	if gomod != "" {
		writeFile(t, filepath.Join(dir, "go.mod"), gomod)
	} else if err := os.Remove(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatal(err)
	}

	g := &fakeGo{}

	var out bytes.Buffer

	c := &command.Context{Stdout: &out, Stderr: &bytes.Buffer{}, Program: "gotools", WorkDir: dir, Go: g}
	if err := c.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	for _, tl := range managed {
		c.Manifest.SetTool(tl)
	}

	return c, g, &out
}

func TestRootGoModIsInTheProjectRoot(t *testing.T) {
	c, _, _ := adoptionContext(t, "")

	if got := c.RootGoMod(); got != filepath.Join(c.Root, "go.mod") {
		t.Fatalf("RootGoMod = %q", got)
	}
}

func TestPlanAdoption(t *testing.T) {
	c, _, _ := adoptionContext(t, rootGoMod)

	specs, err := c.PlanAdoption(command.PlanAdoptionParams{})
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, s := range specs {
		names = append(names, s.Name)
	}

	if !slices.Equal(names, []string{"addlicense", "stringer"}) || specs[1].Version != "v0.30.0" {
		t.Fatalf("specs = %+v", specs)
	}

	if specs, err := c.PlanAdoption(command.PlanAdoptionParams{NoMigrate: true}); specs != nil || err != nil {
		t.Fatalf("NoMigrate planned %v, %v", specs, err)
	}

	none, _, _ := adoptionContext(t, "")
	if specs, err := none.PlanAdoption(command.PlanAdoptionParams{}); specs != nil || err != nil {
		t.Fatalf("no go.mod planned %v, %v", specs, err)
	}
}

func TestPlanAdoptionRefusesWhatCannotBeAdopted(t *testing.T) {
	const twin = "module m\n\ngo 1.24\n\ntool (\n\texample.com/a/foo\n\texample.com/b/foo\n)\n"

	c, _, _ := adoptionContext(t, twin)
	if _, err := c.PlanAdoption(
		command.PlanAdoptionParams{},
	); ierrors.CodeOf(err) != ierrors.Usage ||
		!strings.Contains(err.Error(), "'foo'") {
		t.Fatalf("twin: %v", err)
	}

	c, _, _ = adoptionContext(t, "module m\n\ngo 1.24\n\ntool example.com/b/foo\n",
		manifest.Tool{Name: "foo", Source: "go", Package: "example.com/a/foo", Version: "v1.0.0"})
	if _, err := c.PlanAdoption(
		command.PlanAdoptionParams{},
	); ierrors.CodeOf(err) != ierrors.Usage ||
		!strings.Contains(err.Error(), "already managed") {
		t.Fatalf("managed clash: %v", err)
	}

	c, _, _ = adoptionContext(t, "module m\n\ngo 1.24\n\ntool -x\n")
	if _, err := c.PlanAdoption(command.PlanAdoptionParams{}); ierrors.CodeOf(err) != ierrors.Environment {
		t.Fatalf("invalid tool: %v", err)
	}

	c, _, _ = adoptionContext(t, "not a go.mod (((\n")
	if _, err := c.PlanAdoption(command.PlanAdoptionParams{}); ierrors.CodeOf(err) != ierrors.Environment {
		t.Fatalf("unreadable go.mod: %v", err)
	}
}

func TestManagedAsAndUnmanaged(t *testing.T) {
	c, _, _ := adoptionContext(
		t,
		"",
		manifest.Tool{Name: "gi", Source: "go", Package: "example.com/x/goimports", Version: "v1.0.0"},
	)

	byName := modparser.Spec{Name: "gi", Package: "example.com/other"}
	byPackage := modparser.Spec{Name: "goimports", Package: "example.com/x/goimports"}
	unknown := modparser.Spec{Name: "lint", Package: "example.com/y/lint"}

	if as, ok := c.ManagedAs(byName); !ok || as != "gi" {
		t.Errorf("by name: %q %v", as, ok)
	}

	if as, ok := c.ManagedAs(byPackage); !ok || as != "gi" {
		t.Errorf("by package: %q %v", as, ok)
	}

	if _, ok := c.ManagedAs(unknown); ok {
		t.Error("an unknown tool is managed")
	}

	got := c.Unmanaged([]modparser.Spec{byName, byPackage, unknown})
	if len(got) != 1 || got[0] != unknown {
		t.Fatalf("Unmanaged = %+v", got)
	}
}

func TestAdoptSaysWhenThereIsNothingToAdopt(t *testing.T) {
	c, _, out := adoptionContext(t, "")

	if err := c.Adopt(
		context.Background(),
		command.AdoptParams{},
	); err != nil ||
		!strings.Contains(out.String(), "No go.mod in project root") {
		t.Fatalf("err=%v out=%q", err, out)
	}

	var g *fakeGo

	c, g, out = adoptionContext(t, "module m\n\ngo 1.24\n")

	if err := c.Adopt(
		context.Background(),
		command.AdoptParams{},
	); err != nil || !strings.Contains(out.String(), "No tool directives in go.mod") ||
		len(g.calls) != 0 {
		t.Fatalf("err=%v out=%q", err, out)
	}
}

func TestAdoptDryRunReportsAndChangesNothing(t *testing.T) {
	c, g, out := adoptionContext(t, rootGoMod)

	specs, err := c.PlanAdoption(command.PlanAdoptionParams{})
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Adopt(context.Background(), command.AdoptParams{Specs: specs, DryRun: true}); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"Found 2 tool(s)", "[dry-run] Would install stringer", "[dry-run] Would strip tool directives"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out lacks %q:\n%s", want, out)
		}
	}

	if len(g.calls) != 0 || readFile(t, c.RootGoMod()) != rootGoMod {
		t.Fatal("a dry run changed something")
	}
}

func TestAdoptSkipsWhatIsAlreadyManaged(t *testing.T) {
	c, g, out := adoptionContext(
		t,
		rootGoMod,
		manifest.Tool{Name: "addlicense", Source: "go", Package: "github.com/google/addlicense", Version: "v1.1.1"},
	)

	specs, _ := c.PlanAdoption(command.PlanAdoptionParams{})

	if err := c.Adopt(context.Background(), command.AdoptParams{Specs: specs, DryRun: true}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "addlicense — already managed, skipping") ||
		strings.Contains(out.String(), "Would install addlicense") ||
		len(g.calls) != 0 {
		t.Fatalf("out:\n%s", out)
	}
}

// The pins in the root go.mod are gone once it is stripped, so a pin that
// differs from the manifest's is said out loud.
func TestAdoptWarnsWhenTheRootPinDiffersFromTheManifest(t *testing.T) {
	c, _, _ := adoptionContext(
		t,
		rootGoMod,
		manifest.Tool{Name: "addlicense", Source: "go", Package: "github.com/google/addlicense", Version: "v1.0.0"},
	)

	var errb bytes.Buffer

	c.Stderr = &errb

	specs, _ := c.PlanAdoption(command.PlanAdoptionParams{})
	if err := c.Adopt(context.Background(), command.AdoptParams{Specs: specs, DryRun: true}); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"addlicense: go.mod pins v1.1.1, the manifest keeps v1.0.0", "gotools upgrade addlicense"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, &errb)
		}
	}

	// The same pin is nothing to say.
	c, _, _ = adoptionContext(
		t,
		rootGoMod,
		manifest.Tool{Name: "addlicense", Source: "go", Package: "github.com/google/addlicense", Version: "v1.1.1"},
	)
	errb.Reset()
	c.Stderr = &errb

	if err := c.Adopt(
		context.Background(),
		command.AdoptParams{Specs: specs, DryRun: true},
	); err != nil ||
		strings.Contains(errb.String(), "pins") {
		t.Fatalf("err=%v stderr=%q", err, &errb)
	}
}

// A failed tidy puts the root module back, so the next run finds the
// directives and migrates them instead of finding nothing to do.
func TestAdoptPutsTheRootModuleBackWhenTidyFails(t *testing.T) {
	c, g, _ := adoptionContext(t, rootGoMod)

	tidyFails := true
	g.fail = func(call gocmd.Call) string {
		if tidyFails && len(call.Args) > 1 && call.Args[0] == "mod" && call.Args[1] == "tidy" && call.Dir == c.Root {
			return "go: updates to go.mod needed"
		}

		return ""
	}

	specs, _ := c.PlanAdoption(command.PlanAdoptionParams{})

	err := c.Adopt(context.Background(), command.AdoptParams{Specs: specs})
	if err == nil || !strings.Contains(err.Error(), "go mod tidy failed on the root module") {
		t.Fatalf("err = %v", err)
	}

	if readFile(t, c.RootGoMod()) != rootGoMod {
		t.Fatalf("the root go.mod was left changed:\n%s", readFile(t, c.RootGoMod()))
	}

	// The tools were installed, and a second run still finds the directives.
	tidyFails = false

	again, err := c.PlanAdoption(command.PlanAdoptionParams{})
	if err != nil || len(again) != len(specs) {
		t.Fatalf("plan after the failure: %v, %v", again, err)
	}

	if err := c.Adopt(context.Background(), command.AdoptParams{Specs: again}); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(readFile(t, c.RootGoMod()), "tool ") {
		t.Fatalf("the directives are still there:\n%s", readFile(t, c.RootGoMod()))
	}
}
