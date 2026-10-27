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

func TestKeysAreTheEnvironmentVariablesInHelpOrder(t *testing.T) {
	want := []string{
		"GOTOOLS_STRATEGY", "GOTOOLS_DIR", "GOTOOLS_GO_VERSION", "GOTOOLS_MODULE_PREFIX",
		"GOTOOLS_OFFLINE", "GOTOOLS_JOBS", "GOTOOLS_OPERATION_TIMEOUT", "GOTOOLS_LOCK_TIMEOUT",
		"GOTOOLS_LOCK_STALE_TIMEOUT", "GOTOOLS_NO_LOCK", "GOTOOLS_TRACE", "GOTOOLS_VERBOSE",
	}

	if strings.Join(config.Keys, " ") != strings.Join(want, " ") {
		t.Fatalf("Keys = %v", config.Keys)
	}

	for _, k := range want {
		if !config.IsKey(k) {
			t.Errorf("%s is not a key", k)
		}
	}

	if config.IsKey("GOTOOLS_NOPE") || config.IsKey("strategy") || config.IsKey("") {
		t.Error("an unknown name is a key")
	}
}

func TestIsSettingSeparatesTheLayoutKeys(t *testing.T) {
	for _, k := range []string{config.EnvVarStrategy, config.EnvVarDir, config.EnvVarGoVersion, config.EnvVarModulePrefix, "GOTOOLS_NOPE"} {
		if config.IsSetting(k) {
			t.Errorf("%s is a setting", k)
		}
	}

	for _, k := range []string{config.EnvVarOffline, config.EnvVarJobs, config.EnvVarTrace, config.EnvVarLockStaleTimeout} {
		if !config.IsSetting(k) {
			t.Errorf("%s is not a setting", k)
		}
	}
}

func TestParseSettingTypesTheValue(t *testing.T) {
	cases := []struct {
		key, value string
		check      func(config.Settings) bool
	}{
		{config.EnvVarOffline, "true", func(s config.Settings) bool { return s.Offline }},
		{config.EnvVarOffline, "1", func(s config.Settings) bool { return s.Offline }},
		{config.EnvVarNoLock, "true", func(s config.Settings) bool { return s.NoLock }},
		{config.EnvVarVerbose, "1", func(s config.Settings) bool { return s.Verbose }},
		{config.EnvVarTrace, "stdout", func(s config.Settings) bool { return s.Trace == config.TraceStdout }},
		{config.EnvVarTrace, "true", func(s config.Settings) bool { return s.Trace == config.TraceStderr }},
		{config.EnvVarTrace, "1", func(s config.Settings) bool { return s.Trace == config.TraceStderr }},
		{config.EnvVarTrace, "0", func(s config.Settings) bool { return s.Trace == config.TraceOff }},
		{config.EnvVarJobs, "8", func(s config.Settings) bool { return s.Jobs == 8 }},
		{config.EnvVarOperationTimeout, "0", func(s config.Settings) bool { return s.OperationTimeout == 0 }},
		{config.EnvVarOperationTimeout, "45", func(s config.Settings) bool { return s.OperationTimeout == 45 }},
		{config.EnvVarLockTimeout, "7", func(s config.Settings) bool { return s.LockTimeout == 7 }},
	}

	for _, tc := range cases {
		s := config.Settings{Jobs: 1, OperationTimeout: 120, LockTimeout: 10, Trace: config.TraceStdout}
		if tc.key == config.EnvVarTrace && tc.value == "stdout" {
			s.Trace = config.TraceOff
		}

		if err := config.ParseSetting(tc.key, tc.value, &s); err != nil || !tc.check(s) {
			t.Errorf("%s=%s: err=%v settings=%+v", tc.key, tc.value, err, s)
		}
	}
}

func TestParseSettingRefusesAValueThatDoesNotFit(t *testing.T) {
	cases := []struct{ key, value, want string }{
		{config.EnvVarOffline, "yes", "true or false"},
		{config.EnvVarNoLock, "", "true or false"},
		{config.EnvVarVerbose, "stdout", "true or false"},
		{config.EnvVarTrace, "yes", "true, false, or stdout"},
		{config.EnvVarJobs, "0", "a positive number"},
		{config.EnvVarJobs, "-1", "a positive number"},
		{config.EnvVarJobs, "+2", "a positive number"},
		{config.EnvVarJobs, "x", "a positive number"},
		{config.EnvVarOperationTimeout, "-5", "a non-negative number"},
		{config.EnvVarLockTimeout, "1.5", "a non-negative number"},
		{config.EnvVarLockStaleTimeout, "soon", "a non-negative number"},
	}

	for _, tc := range cases {
		before := config.Settings{Jobs: 3, Trace: config.TraceStdout, LockTimeout: 4}
		s := before

		err := config.ParseSetting(tc.key, tc.value, &s)
		if ierrors.CodeOf(err) != ierrors.Usage ||
			!strings.Contains(err.Error(), "Invalid value: "+tc.value+" (must be "+tc.want+")") {
			t.Errorf("%s=%q: err=%v", tc.key, tc.value, err)
		}

		if s != before {
			t.Errorf("%s=%q changed the settings: %+v", tc.key, tc.value, s)
		}
	}

	if err := config.ParseSetting("GOTOOLS_NOPE", "1", &config.Settings{}); ierrors.CodeOf(err) != ierrors.Usage {
		t.Errorf("unknown key: %v", err)
	}

	if err := config.ParseSetting(
		config.EnvVarStrategy,
		"split",
		&config.Settings{},
	); ierrors.CodeOf(
		err,
	) != ierrors.Usage {
		t.Errorf("a layout key is not a setting: %v", err)
	}
}

func TestStaleTimeoutIsCheckedAndNeverStored(t *testing.T) {
	s := config.Settings{LockTimeout: 10, OperationTimeout: 120}
	before := s

	if err := config.ParseSetting(config.EnvVarLockStaleTimeout, "99", &s); err != nil || s != before {
		t.Fatalf("err=%v settings=%+v", err, s)
	}
}

func TestFormatSettingIsWhatParseSettingAccepts(t *testing.T) {
	s := config.Settings{
		Offline:          true,
		Jobs:             6,
		OperationTimeout: 30,
		LockTimeout:      0,
		Trace:            config.TraceStdout,
		Verbose:          true,
	}

	want := map[string]string{
		config.EnvVarOffline: "true", config.EnvVarNoLock: "false", config.EnvVarVerbose: "true",
		config.EnvVarTrace: "stdout", config.EnvVarJobs: "6", config.EnvVarOperationTimeout: "30",
		config.EnvVarLockTimeout: "0", config.EnvVarLockStaleTimeout: "300",
	}

	for key, w := range want {
		got, ok := config.FormatSetting(key, s)
		if !ok || got != w {
			t.Errorf("%s = %q (%v), want %q", key, got, ok, w)
		}

		var back config.Settings
		if err := config.ParseSetting(key, got, &back); err != nil {
			t.Errorf("%s: %q is not accepted: %v", key, got, err)
		}
	}

	for _, k := range []string{config.EnvVarStrategy, "GOTOOLS_NOPE"} {
		if _, ok := config.FormatSetting(k, s); ok {
			t.Errorf("%s is formatted as a setting", k)
		}
	}

	if got, _ := config.FormatSetting(config.EnvVarTrace, config.Settings{Trace: config.TraceStderr}); got != "true" {
		t.Errorf("trace on = %q", got)
	}
}

func TestWithoutManifestIgnoresTheFileAndKeepsTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, config.ManifestFile),
		[]byte(`{"strategy":"module","dir":"dev/tools"}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(dir, []string{"GOTOOLS_JOBS=5"}, config.WithoutManifest())
	if err != nil || cfg.Strategy != config.DefaultStrategy || cfg.Dir != config.DefaultDir || cfg.Settings.Jobs != 5 {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}

	// An unreadable manifest is not even looked at.
	if err := os.WriteFile(filepath.Join(dir, config.ManifestFile), []byte(`{broken`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := config.Load(dir, nil, config.WithoutManifest()); err != nil {
		t.Fatalf("err = %v", err)
	}
}
