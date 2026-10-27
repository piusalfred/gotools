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

package manifest_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/manifest"
)

func TestRoundTripRealFile(t *testing.T) {
	data, err := os.ReadFile("../../.gotools.json")
	if err != nil {
		t.Skip("no repo manifest")
	}

	res, err := manifest.Parse(data, config.ManifestFile)
	if err != nil || len(res.Warnings) != 0 {
		t.Fatalf("parse: %v %v", err, res.Warnings)
	}

	got, err := res.Manifest.Render()
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != string(data) {
		t.Fatalf("render differs from committed manifest:\n%s\n---\n%s", got, data)
	}
}

func TestParseSingleLine(t *testing.T) {
	in := `{"version":1,"strategy":"module","dir":"t","go_version":"inherit","module_prefix":"x","tools":{"a":{"source":"go","package":"p/a","version":"v1"}}}`

	res, err := manifest.Parse([]byte(in), config.ManifestFile)
	if err != nil {
		t.Fatal(err)
	}

	m := res.Manifest

	if m.Strategy != "module" || m.Dir != "t" || len(m.Tools) != 1 || m.Tools[0].Package != "p/a" {
		t.Fatalf("bad parse: %+v", m)
	}
}

func TestParseFillsSettingsTheFileOmits(t *testing.T) {
	res, err := manifest.Parse([]byte(`{"strategy":"split","settings":{"jobs":4}}`), config.ManifestFile)
	if err != nil {
		t.Fatal(err)
	}

	m := res.Manifest

	want := manifest.New().Settings
	want.Jobs = 4

	if m.Settings != want {
		t.Fatalf("settings = %+v, want %+v", m.Settings, want)
	}
}

func TestTraceSpellings(t *testing.T) {
	cases := map[string]config.TraceMode{
		`"trace": stdout`:   config.TraceStdout,
		`"trace": "stdout"`: config.TraceStdout,
		`"trace": true`:     config.TraceStderr,
		`"trace": "1"`:      config.TraceStderr,
		`"trace": false`:    config.TraceOff,
	}

	for spelling, want := range cases {
		in := `{"strategy":"split","settings":{` + spelling + `}}`

		res, err := manifest.Parse([]byte(in), config.ManifestFile)
		m := res.Manifest
		if err != nil || m.Settings.Trace != want {
			t.Errorf("%s: trace=%q err=%v", spelling, m.Settings.Trace, err)
		}
	}
}

func TestRenderWritesTraceAsAJSONValue(t *testing.T) {
	for mode, want := range map[config.TraceMode]string{
		config.TraceStdout: `"trace": "stdout"`,
		config.TraceStderr: `"trace": true`,
		config.TraceOff:    `"trace": false`,
	} {
		m := manifest.New()
		m.Settings.Trace = mode

		out, err := m.Render()
		if err != nil || !strings.Contains(string(out), want) {
			t.Fatalf("%s: %v\n%s", mode, err, out)
		}

		if _, err = manifest.Parse(out, config.ManifestFile); err != nil {
			t.Fatalf("rewritten manifest does not reparse: %v", err)
		}
	}
}

func TestMalformedAndBadValues(t *testing.T) {
	for _, in := range []string{`{"strategy":"split"`, `{"settings":{"jobs":"many"}}`, `{"settings":{"trace":"loud"}}`, `[]`} {
		_, err := manifest.Parse([]byte(in), "m.json")

		if !errors.Is(err, manifest.ErrMalformed) || ierrors.CodeOf(err) != ierrors.Environment {
			t.Errorf("%s: err=%v code=%d", in, err, ierrors.CodeOf(err))
		}
	}
}

func TestNewerSchema(t *testing.T) {
	_, err := manifest.Parse([]byte(`{"version": 99, "strategy": "split"}`), config.ManifestFile)

	ne, ok := errors.AsType[*manifest.NewerSchemaError](err)
	if !ok || ne.Found != 99 || ne.Supported != manifest.SchemaVersion {
		t.Fatalf("err = %v", err)
	}

	if ierrors.CodeOf(err) != ierrors.Environment {
		t.Fatalf("code = %d", ierrors.CodeOf(err))
	}

	if !strings.Contains(err.Error(), "schema version 99") {
		t.Fatalf("message: %v", err)
	}
}

func TestPreVersionManifestIsV1(t *testing.T) {
	res, err := manifest.Parse([]byte(`{"strategy":"split"}`), config.ManifestFile)
	m := res.Manifest
	if err != nil || m.Schema != 1 {
		t.Fatalf("%v %+v", err, m)
	}
}

func TestNonNumericSchemaVersionIsV1(t *testing.T) {
	for _, body := range []string{
		`{"version": "abc", "strategy": "split"}`,
		`{"version": null, "strategy": "split"}`,
	} {
		res, err := manifest.Parse([]byte(body), config.ManifestFile)
		m := res.Manifest
		if err != nil || m.Schema != 1 {
			t.Fatalf("%s: schema=%v err=%v", body, m, err)
		}
	}
}

func TestUnsafeToolNamesSkipped(t *testing.T) {
	in := `{"strategy":"split","tools":{"ok":{"source":"go","package":"p","version":"v1"},
	"../evil":{"source":"go","package":"p","version":"v1"},"-x":{}}}`

	res, err := manifest.Parse([]byte(in), config.ManifestFile)
	if err != nil {
		t.Fatal(err)
	}

	if m := res.Manifest; len(m.Tools) != 1 || m.Tools[0].Name != "ok" || len(res.Warnings) != 2 {
		t.Fatalf("tools=%+v warns=%v", m.Tools, res.Warnings)
	}
}

func TestUnknownKeysDropped(t *testing.T) {
	in := `{"strategy":"split","extra":{"a":1},"settings":{"bogus":1,"jobs":4},"tools":{}}`

	res, err := manifest.Parse([]byte(in), config.ManifestFile)
	if err != nil {
		t.Fatal(err)
	}

	m := res.Manifest

	out, _ := m.Render()
	if s := string(
		out,
	); strings.Contains(s, "extra") || strings.Contains(s, "bogus") ||
		!strings.Contains(s, `"jobs": 4`) {
		t.Fatalf("unexpected output:\n%s", s)
	}
}

func TestRenderDefaults(t *testing.T) {
	out, err := (&manifest.Manifest{}).Render()
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{`"strategy": "split"`, `"dir": "tools"`, `"go_version": "inherit"`, `"module_prefix": "tools"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %s:\n%s", want, out)
		}
	}
}

func TestRenderEscapesStrings(t *testing.T) {
	m := manifest.New()
	m.Dir = `we"ird`

	out, _ := m.Render()
	if got, err := manifest.Parse(out, config.ManifestFile); err != nil || got.Manifest.Dir != m.Dir {
		t.Fatalf("not valid JSON: %v", err)
	}
}

func TestToolStore(t *testing.T) {
	m := manifest.New()
	m.SetTool(manifest.Tool{Name: "a", Package: "p/a"})
	m.SetTool(manifest.Tool{Name: "a", Package: "p/a2"})

	if tl, ok := m.Tool("a"); !ok || tl.Package != "p/a2" || len(m.Tools) != 1 {
		t.Fatalf("%+v", m.Tools)
	}

	if n, ok := m.FindByPackage("p/a2"); !ok || n != "a" {
		t.Fatal("find by package")
	}

	if got := m.ToolNames(); len(got) != 1 || got[0] != "a" {
		t.Fatalf("ToolNames = %v", got)
	}

	if !m.RemoveTool("a") || m.RemoveTool("a") {
		t.Fatal("remove")
	}
}

func TestTwins(t *testing.T) {
	m := manifest.New()
	m.SetTool(manifest.Tool{Name: "a", Package: "p/x", Version: "v1"})
	m.SetTool(manifest.Tool{Name: "b", Package: "p/x", Version: "v1"})
	m.SetTool(manifest.Tool{Name: "c", Package: "p/x", Version: "v2"})
	m.SetTool(manifest.Tool{Name: "d", Package: "p/y", Version: "v1"})

	if twin, ok := m.Twin("a", "p/x", "v1", false); !ok || twin.Name != "b" {
		t.Fatalf("Twin = %+v %v", twin, ok)
	}

	if _, ok := m.Twin("a", "p/x", "v9", false); ok {
		t.Fatal("a different version is not a twin outside unified")
	}

	if twin, ok := m.Twin("c", "p/x", "v2", true); !ok || twin.Package != "p/x" {
		t.Fatalf("unified Twin = %+v %v", twin, ok)
	}

	if g := m.Twins(false); len(g) != 1 || len(g[0]) != 2 {
		t.Fatalf("Twins(false) = %v", g)
	}

	if g := m.Twins(true); len(g) != 1 || len(g[0]) != 3 {
		t.Fatalf("Twins(true) = %v", g)
	}
}

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	write := func(s string) string {
		p := filepath.Join(dir, "m.json")
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}

		return p
	}

	if st, err := manifest.Check(
		filepath.Join(dir, "none"),
	); st != manifest.Missing ||
		!errors.Is(err, manifest.ErrMissing) {
		t.Errorf("missing: %v %v", st, err)
	}

	cases := map[string]manifest.Status{
		`{"version":1,"strategy":"split"}`: manifest.Valid,
		`{"version":2,"strategy":"split"}`: manifest.NewerSchema,
		`{"version":1,"strategy":"split"`:  manifest.Unreadable,
		`{"version":1}`:                    manifest.Unreadable,
	}
	for in, want := range cases {
		if st, _ := manifest.Check(write(in)); st != want {
			t.Errorf("%s: status=%d want %d", in, st, want)
		}
	}
}

func TestFlushAtomicAndCleanup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, config.ManifestFile)

	m := manifest.New()
	m.Strategy = "split"

	if err := m.Flush(path); err != nil {
		t.Fatal(err)
	}

	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("stat: %v %v", st, err)
	}

	if left, _ := filepath.Glob(path + ".tmp.*"); len(left) != 0 {
		t.Fatalf("temp left: %v", left)
	}

	stale := path + ".tmp.abc"
	_ = os.WriteFile(stale, nil, 0o600)
	manifest.CleanupTemps(path)

	if _, err := os.Stat(stale); err == nil {
		t.Fatal("stale temp not removed")
	}

	victim := filepath.Join(dir, "victim")
	_ = os.Symlink(victim, path+".tmp.planted")

	if err := m.Flush(path); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(victim); err == nil {
		t.Fatal("write followed planted symlink")
	}
}

func TestRenderOrdersBySourcePackageVersionName(t *testing.T) {
	m := manifest.New()
	m.Strategy = "split"

	for _, tl := range []manifest.Tool{
		{Name: "gofumpt", Source: "go", Package: "mvdan.cc/gofumpt", Version: "v0.10.0"},
		{Name: "staticcheck", Source: "go", Package: "honnef.co/go/tools/cmd/staticcheck", Version: "v0.7.0"},
		{Name: "zzz-early", Source: "go", Package: "github.com/aaa/zzz", Version: "v1.0.0"},
		{Name: "aaa-late", Source: "go", Package: "github.com/aaa/zzz", Version: "v1.0.0"},
		{Name: "mylint", Source: "go", Package: "github.com/aaa/zzz", Version: "v1.0.0"},
		{Name: "addlicense", Source: "go", Package: "github.com/google/addlicense", Version: "v1.2.0"},
		{Name: "older", Source: "go", Package: "github.com/aaa/zzz", Version: "v0.9.0"},
	} {
		m.SetTool(tl)
	}

	out, _ := m.Render()
	last := -1

	for _, name := range []string{"older", "aaa-late", "mylint", "zzz-early", "addlicense", "staticcheck", "gofumpt"} {
		i := strings.Index(string(out), `"`+name+`": {`)
		if i < 0 {
			t.Fatalf("%s missing:\n%s", name, out)
		}

		if i < last {
			t.Fatalf("%s written out of order:\n%s", name, out)
		}

		last = i
	}
}

func TestFlushFailureLeavesNoTempFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, config.ManifestFile)

	m := manifest.New()
	if err := m.Flush(path); err != nil {
		t.Fatal(err)
	}

	before, _ := os.ReadFile(path)

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}

	m.Dir = "changed"

	err := m.Flush(path)
	if err == nil || ierrors.CodeOf(err) != ierrors.Environment {
		t.Fatalf("flush into a read-only directory: %v", err)
	}

	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("a failed flush changed the manifest")
	}

	if left, _ := filepath.Glob(path + ".tmp.*"); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}

	if err := m.Flush(path); err != nil {
		t.Fatalf("flush after the failure: %v", err)
	}
}

func TestFindRoot(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")

	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	if got, found := manifest.FindRoot(sub); found || got != sub {
		t.Fatalf("no manifest: %q %v", got, found)
	}

	if err := os.WriteFile(filepath.Join(root, config.ManifestFile), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got, found := manifest.FindRoot(sub); !found || got != root {
		t.Fatalf("from a subdirectory: %q %v", got, found)
	}

	if got, found := manifest.FindRoot(root); !found || got != root {
		t.Fatalf("from the root: %q %v", got, found)
	}

	// A directory named like the manifest is not a manifest.
	other := t.TempDir()
	_ = os.Mkdir(filepath.Join(other, config.ManifestFile), 0o755)

	if _, found := manifest.FindRoot(other); found {
		t.Fatal("a directory counted as a manifest")
	}
}

func TestGetAndSetEachKey(t *testing.T) {
	m := manifest.New()

	set := map[string]string{
		config.EnvVarStrategy: "module", config.EnvVarDir: ".tools", config.EnvVarGoVersion: "1.25",
		config.EnvVarModulePrefix: "example.com/x", config.EnvVarOffline: "1", config.EnvVarJobs: "4",
		config.EnvVarOperationTimeout: "30", config.EnvVarLockTimeout: "2", config.EnvVarLockStaleTimeout: "9",
		config.EnvVarNoLock: "true", config.EnvVarTrace: "stdout", config.EnvVarVerbose: "true",
	}

	for _, k := range config.Keys {
		if err := m.Set(k, set[k]); err != nil {
			t.Fatalf("Set %s: %v", k, err)
		}
	}

	want := map[string]string{
		config.EnvVarStrategy: "module", config.EnvVarDir: ".tools", config.EnvVarGoVersion: "1.25",
		config.EnvVarModulePrefix: "example.com/x", config.EnvVarOffline: "true", config.EnvVarJobs: "4",
		config.EnvVarOperationTimeout: "30", config.EnvVarLockTimeout: "2", config.EnvVarLockStaleTimeout: "300",
		config.EnvVarNoLock: "true", config.EnvVarTrace: "stdout", config.EnvVarVerbose: "true",
	}

	for _, k := range config.Keys {
		if v, ok := m.Get(k); !ok || v != want[k] {
			t.Errorf("Get %s = %q (%v), want %q", k, v, ok, want[k])
		}
	}

	// What was set survives the file.
	path := filepath.Join(t.TempDir(), config.ManifestFile)
	if err := m.Flush(path); err != nil {
		t.Fatal(err)
	}

	res, err := manifest.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	for _, k := range config.Keys {
		if v, _ := res.Manifest.Get(k); v != want[k] {
			t.Errorf("after reload %s = %q, want %q", k, v, want[k])
		}
	}
}

func TestGetSaysWhetherALayoutKeyIsSet(t *testing.T) {
	m := manifest.New()

	for _, k := range []string{config.EnvVarStrategy, config.EnvVarDir, config.EnvVarGoVersion, config.EnvVarModulePrefix} {
		if v, ok := m.Get(k); ok || v != "" {
			t.Errorf("%s = %q (%v) on an empty manifest", k, v, ok)
		}
	}

	// A setting always has a value: the default when the file is silent.
	if v, ok := m.Get(config.EnvVarJobs); !ok || v != "1" {
		t.Errorf("jobs = %q (%v)", v, ok)
	}

	if _, ok := m.Get("GOTOOLS_NOPE"); ok {
		t.Error("an unknown key is set")
	}
}

func TestSetRefusesABadValueAndChangesNothing(t *testing.T) {
	m := manifest.New()
	m.Strategy, m.Dir = "split", "tools"

	cases := map[string]string{
		config.EnvVarStrategy:         "everything",
		config.EnvVarDir:              "../out",
		config.EnvVarJobs:             "0",
		config.EnvVarOffline:          "maybe",
		config.EnvVarTrace:            "loud",
		config.EnvVarOperationTimeout: "-1",
		"GOTOOLS_NOPE":                "x",
	}

	for k, v := range cases {
		if err := m.Set(k, v); ierrors.CodeOf(err) != ierrors.Usage {
			t.Errorf("Set %s=%s: %v", k, v, err)
		}
	}

	if m.Strategy != "split" || m.Dir != "tools" || m.Settings.Jobs != 1 || m.Settings.Offline {
		t.Errorf("manifest changed: %+v", m)
	}
}
