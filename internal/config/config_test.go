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

package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
)

// manifest is a manifest as gotools writes it: layout keys, a settings object
// and the tools it manages.
const manifest = `{
  "version": 1,
  "strategy": "module",
  "dir": "dev/tools",
  "go_version": "1.25",
  "module_prefix": "example.com/x",
  "settings": {
    "jobs": 4,
    "lock_timeout": 0,
    "no_lock": true,
    "offline": true,
    "operation_timeout": 30,
    "trace": "stdout",
    "verbose": true
  },
  "tools": {
    "goimports": {
      "source": "go",
      "package": "golang.org/x/tools/cmd/goimports",
      "version": "v0.30.0"
    }
  }
}
`

// project returns a project root holding the manifest, or none when it is "".
func project(t *testing.T, manifest string) string {
	t.Helper()

	root := t.TempDir()

	if manifest != "" {
		if err := os.WriteFile(filepath.Join(root, config.ManifestFile), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

func mustLoad(t *testing.T, root string, environ ...string) config.Config {
	t.Helper()

	c, err := config.Load(root, environ)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	return c
}

func TestDefaults(t *testing.T) {
	t.Parallel()

	want := config.Config{
		Strategy:     config.DefaultStrategy,
		Dir:          config.DefaultDir,
		GoVersion:    config.DefaultGoVersion,
		ModulePrefix: config.DefaultModulePrefix,
		Settings: config.Settings{
			Offline:          config.DefaultOffline,
			Jobs:             config.DefaultJobs,
			OperationTimeout: config.DefaultOperationTimeout,
			LockTimeout:      config.DefaultLockTimeout,
			NoLock:           config.DefaultNoLock,
			Trace:            config.DefaultTrace,
			Verbose:          config.DefaultVerbose,
		},
	}

	if got := mustLoad(t, project(t, "")); got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestManifestOverridesDefaults(t *testing.T) {
	t.Parallel()

	want := config.Config{
		Strategy: config.StrategyModule, Dir: "dev/tools", GoVersion: "1.25", ModulePrefix: "example.com/x",
		Settings: config.Settings{
			Offline: true, Jobs: 4, OperationTimeout: 30, LockTimeout: 0, NoLock: true,
			Trace: config.TraceStdout, Verbose: true,
		},
	}

	if got := mustLoad(t, project(t, manifest)); got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// A manifest names only what it wants to change.
func TestPartialManifestKeepsTheOtherDefaults(t *testing.T) {
	t.Parallel()

	c := mustLoad(t, project(t, `{"settings": {"jobs": 8}}`))

	if c.Settings.Jobs != 8 || c.Strategy != config.DefaultStrategy || c.Settings.LockTimeout != config.DefaultLockTimeout {
		t.Fatalf("got %+v", c)
	}
}

func TestEnvironmentOverridesManifest(t *testing.T) {
	t.Parallel()

	root := project(t, manifest)

	c := mustLoad(t, root, "GOTOOLS_STRATEGY=unified", "GOTOOLS_DIR=b", "GOTOOLS_OFFLINE=0", "GOTOOLS_JOBS=2")

	if c.Strategy != config.StrategyUnified || c.Dir != "b" {
		t.Errorf("layout: %+v", c.Layout)
	}

	// An explicit 0 is a value: it turns the manifest's true off.
	if c.Settings.Offline || c.Settings.Jobs != 2 {
		t.Errorf("settings: %+v", c.Settings)
	}

	// What the environment does not set keeps the manifest's value.
	if c.GoVersion != "1.25" || !c.Settings.NoLock {
		t.Errorf("untouched keys changed: %+v", c)
	}
}

func TestEmptyVariableIsUnset(t *testing.T) {
	t.Parallel()

	c := mustLoad(t, project(t, manifest), "GOTOOLS_JOBS=", "GOTOOLS_DIR=")

	if c.Settings.Jobs != 4 || c.Dir != "dev/tools" {
		t.Fatalf("an empty variable overrode the manifest: %+v", c)
	}
}

// Every variable reaches the field it names.
func TestEveryVariableSetsItsKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		variable, value string
		check           func(config.Config) bool
	}{
		{config.EnvVarStrategy, "unified", func(c config.Config) bool { return c.Strategy == config.StrategyUnified }},
		{config.EnvVarDir, "t", func(c config.Config) bool { return c.Dir == "t" }},
		{config.EnvVarGoVersion, "1.24", func(c config.Config) bool { return c.GoVersion == "1.24" }},
		{config.EnvVarModulePrefix, "p", func(c config.Config) bool { return c.ModulePrefix == "p" }},
		{config.EnvVarOffline, "true", func(c config.Config) bool { return c.Settings.Offline }},
		{config.EnvVarJobs, "3", func(c config.Config) bool { return c.Settings.Jobs == 3 }},
		{config.EnvVarOperationTimeout, "7", func(c config.Config) bool { return c.Settings.OperationTimeout == 7 }},
		{config.EnvVarLockTimeout, "5", func(c config.Config) bool { return c.Settings.LockTimeout == 5 }},
		{config.EnvVarNoLock, "1", func(c config.Config) bool { return c.Settings.NoLock }},
		{config.EnvVarTrace, "stdout", func(c config.Config) bool { return c.Settings.Trace == config.TraceStdout }},
		{config.EnvVarVerbose, "true", func(c config.Config) bool { return c.Settings.Verbose }},
	}

	for _, tc := range cases {
		t.Run(tc.variable, func(t *testing.T) {
			t.Parallel()

			if c := mustLoad(t, project(t, ""), tc.variable+"="+tc.value); !tc.check(c) {
				t.Fatalf("%s=%s: got %+v", tc.variable, tc.value, c)
			}
		})
	}
}

func TestTraceSpellings(t *testing.T) {
	t.Parallel()

	fromEnv := map[string]config.TraceMode{
		"stdout": config.TraceStdout, "true": config.TraceStderr, "1": config.TraceStderr,
		"false": config.TraceOff, "0": config.TraceOff,
	}

	for value, want := range fromEnv {
		if got := mustLoad(t, project(t, ""), config.EnvVarTrace+"="+value).Settings.Trace; got != want {
			t.Errorf("%s=%s: trace %q, want %q", config.EnvVarTrace, value, got, want)
		}
	}

	// In the manifest, trace is a JSON boolean or the string "stdout".
	fromJSON := map[string]config.TraceMode{
		`true`: config.TraceStderr, `false`: config.TraceOff, `"stdout"`: config.TraceStdout,
	}

	for value, want := range fromJSON {
		if got := mustLoad(t, project(t, `{"settings": {"trace": `+value+`}}`)).Settings.Trace; got != want {
			t.Errorf(`"trace": %s: trace %q, want %q`, value, got, want)
		}
	}
}

func TestInvalidValuesAreErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		manifest string
		environ  []string
		want     string // a piece of the error
	}{
		{"jobs that is not a number", "", []string{"GOTOOLS_JOBS=many"}, "settings.jobs"},
		{"jobs of zero", "", []string{"GOTOOLS_JOBS=0"}, "settings.jobs must be at least 1"},
		{"jobs of zero in the manifest", `{"settings": {"jobs": 0}}`, nil, "settings.jobs must be at least 1"},
		{"manifest jobs as text", `{"settings": {"jobs": "many"}}`, nil, "settings.jobs"},
		{"negative timeout", "", []string{"GOTOOLS_LOCK_TIMEOUT=-5"}, "settings.lock_timeout must not be negative"},
		{"negative operation timeout", `{"settings": {"operation_timeout": -1}}`, nil, "settings.operation_timeout must not be negative"},
		{"boolean that is not one", "", []string{"GOTOOLS_OFFLINE=yes"}, "settings.offline"},
		{"unknown trace mode", "", []string{"GOTOOLS_TRACE=loud"}, "settings.trace must be"},
		{"unknown strategy", "", []string{"GOTOOLS_STRATEGY=flat"}, "strategy must be split, module or unified"},
		{"manifest that is not JSON", "{not json", nil, config.ManifestFile},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.Load(project(t, tc.manifest), tc.environ)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// Every problem is reported, so one run is enough to fix them all.
func TestAllProblemsAreReportedTogether(t *testing.T) {
	t.Parallel()

	_, err := config.Load(project(t, ""), []string{"GOTOOLS_STRATEGY=flat", "GOTOOLS_JOBS=0", "GOTOOLS_DIR=../x"})
	if err == nil {
		t.Fatal("accepted")
	}

	for _, want := range []string{"strategy", "settings.jobs", "tools directory"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestUnknownKeysAndVariablesAreIgnored(t *testing.T) {
	t.Parallel()

	c := mustLoad(t, project(t, `{"colour": "blue", "settings": {"sparkle": true}}`), "GOTOOLS_COLOUR=blue", "HOME=/tmp", "GOTOOLS_JOBS=2")

	if c.Settings.Jobs != 2 || c.Strategy != config.DefaultStrategy {
		t.Fatalf("got %+v", c)
	}
}

func TestUnsafeToolsDirectoryIsAnError(t *testing.T) {
	t.Parallel()

	for _, dir := range []string{"..", "../x", "a/../../x", "/abs", "/", ".", "./", ".//"} {
		fromManifest := `{"dir": "` + dir + `"}`

		if _, err := config.Load(project(t, fromManifest), nil); err == nil || !strings.Contains(err.Error(), "tools directory") {
			t.Errorf("manifest dir %q: err = %v", dir, err)
		}

		if _, err := config.Load(project(t, ""), []string{config.EnvVarDir + "=" + dir}); err == nil || !strings.Contains(err.Error(), "tools directory") {
			t.Errorf("%s=%q: err = %v", config.EnvVarDir, dir, err)
		}
	}

	for _, dir := range []string{"tools", ".tools", "dev/tools", "a..b"} {
		if err := config.ValidateDir(dir); err != nil {
			t.Errorf("ValidateDir(%q) = %v", dir, err)
		}
	}
}

func TestErrorCodes(t *testing.T) {
	root := t.TempDir()

	if _, err := config.Load(root, []string{config.EnvVarJobs + "=0"}); ierrors.CodeOf(err) != ierrors.Environment {
		t.Errorf("invalid value: code=%d err=%v", ierrors.CodeOf(err), err)
	}

	if err := os.WriteFile(filepath.Join(root, config.ManifestFile), []byte(`{"strategy":`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := config.Load(root, nil); ierrors.CodeOf(err) != ierrors.Environment {
		t.Errorf("malformed manifest: code=%d err=%v", ierrors.CodeOf(err), err)
	}

	for _, dir := range []string{"", "/abs", "../up", "."} {
		if err := config.ValidateDir(dir); ierrors.CodeOf(err) != ierrors.Usage {
			t.Errorf("ValidateDir(%q): code=%d err=%v", dir, ierrors.CodeOf(err), err)
		}
	}
}

func TestOverridesOutrankEveryLayer(t *testing.T) {
	root := project(t, manifest)

	got, err := config.Load(root, []string{config.EnvVarDir + "=env"}, config.WithOverrides(config.Overrides{
		Strategy: config.StrategyUnified, Dir: "flag", GoVersion: "1.30", ModulePrefix: "example.com/flag",
	}))
	if err != nil {
		t.Fatal(err)
	}

	if got.Strategy != config.StrategyUnified || got.Dir != "flag" || got.GoVersion != "1.30" || got.ModulePrefix != "example.com/flag" {
		t.Fatalf("layout = %+v", got.Layout)
	}
}

func TestEmptyOverridesChangeNothing(t *testing.T) {
	root := project(t, manifest)

	plain, err := config.Load(root, nil)
	if err != nil {
		t.Fatal(err)
	}

	over, err := config.Load(root, nil, config.WithOverrides(config.Overrides{}))
	if err != nil || over != plain {
		t.Fatalf("%+v vs %+v (%v)", over, plain, err)
	}
}

func TestAnInvalidOverrideIsAnError(t *testing.T) {
	_, err := config.Load(t.TempDir(), nil, config.WithOverrides(config.Overrides{Strategy: "flat"}))
	if ierrors.CodeOf(err) != ierrors.Environment {
		t.Fatalf("err = %v", err)
	}
}
