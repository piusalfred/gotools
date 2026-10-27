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

package command

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/manifest"
	"github.com/piusalfred/gotools/internal/tools"
)

// fakeManager is a tools.Manager that records what the commands ask of it and
// answers from tables, so a command loop is tested without a go runner or a
// tools directory.
type fakeManager struct {
	removed   []string
	upgraded  []string
	removeRes map[string]tools.RemoveResult
	upgradeTo map[string]string // name -> version after the upgrade
	failOn    string            // the call for this name returns errBoom
}

var errBoom = errors.New("boom")

func (f *fakeManager) Add(context.Context, tools.AddOptions) (manifest.Tool, error) {
	return manifest.Tool{}, nil
}

func (f *fakeManager) Remove(_ context.Context, name string) (tools.RemoveResult, error) {
	f.removed = append(f.removed, name)

	if name == f.failOn {
		return tools.RemoveResult{}, errBoom
	}

	return f.removeRes[name], nil
}

func (f *fakeManager) Rename(context.Context, string, string, tools.RenameOptions) error { return nil }

func (f *fakeManager) Upgrade(_ context.Context, from manifest.Tool) (manifest.Tool, error) {
	f.upgraded = append(f.upgraded, from.Name)

	if from.Name == f.failOn {
		return manifest.Tool{}, errBoom
	}

	to := from
	if v, ok := f.upgradeTo[from.Name]; ok {
		to.Version = v
	}

	return to, nil
}

func (f *fakeManager) Restore(context.Context, []manifest.Tool, tools.RestoreOptions) error {
	return nil
}

var _ tools.Manager = (*fakeManager)(nil)

func TestRemoveToolsReportsUnknownNamesAfterTheRest(t *testing.T) {
	var out bytes.Buffer

	f := &fakeManager{removeRes: map[string]tools.RemoveResult{
		"a": {OnDisk: true, InManifest: true},
		"c": {InManifest: true},
		// b is neither on disk nor in the manifest
	}}

	err := removeTools(context.Background(), &out, f, []string{"a", "b", "c"}, false)
	if ierrors.CodeOf(err) != ierrors.ToolNotFound || !strings.Contains(err.Error(), "Tool not found: b") {
		t.Fatalf("err = %v", err)
	}

	// The typo is reported once the other names were processed.
	if strings.Join(f.removed, ",") != "a,b,c" {
		t.Fatalf("calls = %v", f.removed)
	}

	for _, want := range []string{"Removing a...", "Removing b...", "Removing c..."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, &out)
		}
	}
}

func TestRemoveToolsStopsAtTheFirstFailure(t *testing.T) {
	f := &fakeManager{failOn: "b", removeRes: map[string]tools.RemoveResult{"a": {OnDisk: true, InManifest: true}}}

	err := removeTools(context.Background(), &bytes.Buffer{}, f, []string{"a", "b", "c"}, false)
	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v", err)
	}

	if strings.Join(f.removed, ",") != "a,b" {
		t.Fatalf("calls = %v, want c never attempted", f.removed)
	}
}

func TestRemoveToolsDryRunAsksForNothing(t *testing.T) {
	var out bytes.Buffer

	f := &fakeManager{}
	if err := removeTools(context.Background(), &out, f, []string{"a", "b"}, true); err != nil {
		t.Fatal(err)
	}

	if len(f.removed) != 0 {
		t.Fatalf("a dry run removed %v", f.removed)
	}

	if !strings.Contains(out.String(), "[dry-run] Would remove a") ||
		!strings.Contains(out.String(), "[dry-run] Would remove b") {
		t.Fatalf("output:\n%s", &out)
	}
}

// upgradeManifest lists x@v1.0.0 and y@v1.2.0.
func upgradeManifest() *manifest.Manifest {
	m := manifest.New()
	m.SetTool(manifest.Tool{Name: "x", Source: "go", Package: "example.com/a/x", Version: "v1.0.0"})
	m.SetTool(manifest.Tool{Name: "y", Source: "go", Package: "example.com/b/y", Version: "v1.2.0"})

	return m
}

func TestUpgradeToolsNarratesEachOutcome(t *testing.T) {
	var out bytes.Buffer

	f := &fakeManager{upgradeTo: map[string]string{"x": "v1.1.0"}}

	if err := upgradeTools(context.Background(), &out, upgradeManifest(), f, []string{"x", "y"}, false); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"Upgrading x (example.com/a/x) from v1.0.0",
		"x: v1.0.0 → v1.1.0",
		"y: already at latest (v1.2.0)",
		"Upgrade complete.",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, &out)
		}
	}
}

func TestUpgradeToolsSkipsUnknownNamesAndFailsAtTheEnd(t *testing.T) {
	var out bytes.Buffer

	f := &fakeManager{}

	err := upgradeTools(context.Background(), &out, upgradeManifest(), f, []string{"typo", "x"}, false)
	if ierrors.CodeOf(err) != ierrors.ToolNotFound || !strings.Contains(err.Error(), "Tool not found: typo") {
		t.Fatalf("err = %v", err)
	}

	// The known tool was still upgraded; the unknown one never reached the manager.
	if strings.Join(f.upgraded, ",") != "x" {
		t.Fatalf("calls = %v", f.upgraded)
	}

	if !strings.Contains(out.String(), "Tool typo not found in manifest, skipping.") {
		t.Fatalf("output:\n%s", &out)
	}
}

func TestUpgradeToolsStopsAtTheFirstFailureAndDryRunAsksForNothing(t *testing.T) {
	f := &fakeManager{failOn: "x"}

	if err := upgradeTools(
		context.Background(),
		&bytes.Buffer{},
		upgradeManifest(),
		f,
		[]string{"x", "y"},
		false,
	); !errors.Is(
		err,
		errBoom,
	) {
		t.Fatalf("err = %v", err)
	}

	if strings.Join(f.upgraded, ",") != "x" {
		t.Fatalf("calls = %v, want y never attempted", f.upgraded)
	}

	var out bytes.Buffer

	f = &fakeManager{}
	if err := upgradeTools(context.Background(), &out, upgradeManifest(), f, []string{"x", "y"}, true); err != nil {
		t.Fatal(err)
	}

	if len(f.upgraded) != 0 || !strings.Contains(out.String(), "[dry-run] Would upgrade 2 tool(s).") {
		t.Fatalf("calls=%v output:\n%s", f.upgraded, &out)
	}
}
