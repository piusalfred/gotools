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

// Package config resolves gotools' configuration.
//
// A value can come from three places. In increasing priority:
//
//  1. the built-in default,
//  2. the manifest file, .gotools.json in the project root,
//  3. the environment, GOTOOLS_*,
//  4. overrides the caller passes, such as command line flags.
//
// Load stacks the three as koanf layers, so a later layer overrides an earlier
// one key by key, and reads the result into a Config. The manifest is read as
// JSON by koanf, so Config mirrors its shape: the layout keys at the top level
// and the settings in a "settings" object. Nothing here reads the process
// environment; the caller passes it in.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	jsonparser "github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/providers/confmap"
	envprovider "github.com/knadh/koanf/providers/env/v2"
	fsprovider "github.com/knadh/koanf/providers/fs"
	"github.com/knadh/koanf/v2"

	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/strategy"
)

// ManifestFile is the manifest's name in the project root.
const ManifestFile = ".gotools.json"

// EnvPrefix starts the name of every environment variable gotools reads.
const EnvPrefix = "GOTOOLS_"

// The environment variables.
const (
	EnvVarStrategy          = "GOTOOLS_STRATEGY"
	EnvVarDir               = "GOTOOLS_DIR"
	EnvVarGoVersion         = "GOTOOLS_GO_VERSION"
	EnvVarModulePrefix      = "GOTOOLS_MODULE_PREFIX"
	EnvVarOffline           = "GOTOOLS_OFFLINE"
	EnvVarJobs              = "GOTOOLS_JOBS"
	EnvVarOperationTimeout  = "GOTOOLS_OPERATION_TIMEOUT"
	EnvVarLockTimeout       = "GOTOOLS_LOCK_TIMEOUT"
	EnvVarNoLock            = "GOTOOLS_NO_LOCK"
	EnvVarTrace             = "GOTOOLS_TRACE"
	EnvVarVerbose           = "GOTOOLS_VERBOSE"
	KeyStrategy             = "strategy"
	KeyDir                  = "dir"
	KeyGoVersion            = "go_version"
	KeyModulePrefix         = "module_prefix"
	KeyOffline              = "settings.offline"
	KeyJobs                 = "settings.jobs"
	KeyOperationTimeout     = "settings.operation_timeout"
	KeyLockTimeout          = "settings.lock_timeout"
	KeyNoLock               = "settings.no_lock"
	KeyTrace                = "settings.trace"
	KeyVerbose              = "settings.verbose"
	DefaultStrategy         = StrategySplit
	DefaultDir              = "tools"
	DefaultGoVersion        = "inherit"
	DefaultModulePrefix     = ""
	DefaultOffline          = false
	DefaultJobs             = 1
	DefaultOperationTimeout = 120
	DefaultLockTimeout      = 10
	DefaultNoLock           = false
	DefaultTrace            = TraceOff
	DefaultVerbose          = false
)

// Strategy is how tools are kept out of the project's own module graph.
type Strategy = strategy.Strategy

const (
	StrategySplit   = strategy.Split
	StrategyModule  = strategy.Module
	StrategyUnified = strategy.Unified
)

// TraceMode says where trace records are echoed. They are always written to
// the trace file as well.
type TraceMode string

const (
	TraceOff    TraceMode = "false"
	TraceStderr TraceMode = "true"
	TraceStdout TraceMode = "stdout"
)

// Config is the resolved configuration, shaped like the manifest.
type Config struct {
	Layout   `koanf:",squash"`
	Settings Settings `koanf:"settings"`
}

// Layout says where the tools live and how they are isolated.
type Layout struct {
	Strategy     Strategy `koanf:"strategy"`
	Dir          string   `koanf:"dir"`
	GoVersion    string   `koanf:"go_version"`
	ModulePrefix string   `koanf:"module_prefix"`
}

// Settings are the runtime knobs. Durations are in seconds.
type Settings struct {
	Offline          bool      `koanf:"offline"`
	Jobs             int       `koanf:"jobs"`
	OperationTimeout int       `koanf:"operation_timeout"`
	LockTimeout      int       `koanf:"lock_timeout"`
	NoLock           bool      `koanf:"no_lock"`
	Trace            TraceMode `koanf:"trace"`
	Verbose          bool      `koanf:"verbose"`
}

// envKeys maps each environment variable to the key it sets.
var envKeys = map[string]string{
	EnvVarStrategy:         KeyStrategy,
	EnvVarDir:              KeyDir,
	EnvVarGoVersion:        KeyGoVersion,
	EnvVarModulePrefix:     KeyModulePrefix,
	EnvVarOffline:          KeyOffline,
	EnvVarJobs:             KeyJobs,
	EnvVarOperationTimeout: KeyOperationTimeout,
	EnvVarLockTimeout:      KeyLockTimeout,
	EnvVarNoLock:           KeyNoLock,
	EnvVarTrace:            KeyTrace,
	EnvVarVerbose:          KeyVerbose,
}

// Overrides are values that outrank every other layer, such as command line
// flags. An empty field is not an override.
type Overrides struct {
	Strategy     Strategy
	Dir          string
	GoVersion    string
	ModulePrefix string
}

func (o Overrides) keys() map[string]any {
	m := map[string]any{}

	if o.Strategy != "" {
		m[KeyStrategy] = o.Strategy
	}

	if o.Dir != "" {
		m[KeyDir] = o.Dir
	}

	if o.GoVersion != "" {
		m[KeyGoVersion] = o.GoVersion
	}

	if o.ModulePrefix != "" {
		m[KeyModulePrefix] = o.ModulePrefix
	}

	return m
}

type loadOptions struct {
	overrides  Overrides
	noManifest bool
}

// Option adjusts how Load resolves the configuration.
type Option func(*loadOptions)

// WithOverrides adds a layer above the environment: default < manifest <
// environment < overrides.
func WithOverrides(o Overrides) Option {
	return func(l *loadOptions) { l.overrides = o }
}

// WithoutManifest leaves the manifest out: the configuration is the defaults,
// the environment and the overrides. A diagnostic uses it to still resolve a
// layout when the manifest cannot be read.
func WithoutManifest() Option {
	return func(l *loadOptions) { l.noManifest = true }
}

// Load resolves the configuration of the project at root. environ lists the
// environment as NAME=value pairs, like os.Environ; nil means no variables.
//
// A project without a manifest is configured by the defaults and the
// environment alone. A manifest that is not valid JSON, a value of the wrong
// type, or a value that fails Validate is an error.
func Load(root string, environ []string, opts ...Option) (Config, error) {
	var o loadOptions
	for _, opt := range opts {
		opt(&o)
	}

	k := koanf.New(".")

	defaults := map[string]any{
		KeyStrategy:         DefaultStrategy,
		KeyDir:              DefaultDir,
		KeyGoVersion:        DefaultGoVersion,
		KeyModulePrefix:     DefaultModulePrefix,
		KeyOffline:          DefaultOffline,
		KeyJobs:             DefaultJobs,
		KeyOperationTimeout: DefaultOperationTimeout,
		KeyLockTimeout:      DefaultLockTimeout,
		KeyNoLock:           DefaultNoLock,
		KeyTrace:            DefaultTrace,
		KeyVerbose:          DefaultVerbose,
	}

	if err := k.Load(confmap.Provider(defaults, "."), nil); err != nil {
		return Config{}, ierrors.Wrap(ierrors.Environment, "config: defaults", err)
	}

	if !o.noManifest {
		manifest := fsprovider.Provider(os.DirFS(root), ManifestFile)
		if err := k.Load(manifest, jsonparser.Parser()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Config{}, ierrors.Wrap(ierrors.Environment, ManifestFile, err)
		}
	}

	environment := envprovider.Provider(".", envprovider.Opt{
		Prefix:        EnvPrefix,
		EnvironFunc:   func() []string { return environ },
		TransformFunc: envKey,
	})
	if err := k.Load(environment, nil); err != nil {
		return Config{}, ierrors.Wrap(ierrors.Environment, "config: environment", err)
	}

	if err := k.Load(confmap.Provider(o.overrides.keys(), "."), nil); err != nil {
		return Config{}, ierrors.Wrap(ierrors.Environment, "config: overrides", err)
	}

	var c Config
	if err := k.Unmarshal("", &c); err != nil {
		return Config{}, ierrors.Wrap(ierrors.Environment, "config", err)
	}

	c.Settings.Trace = traceMode(c.Settings.Trace)

	if err := c.Validate(); err != nil {
		return Config{}, ierrors.Wrap(ierrors.Environment, "", err)
	}

	return c, nil
}

// envKey tells the environment layer which key a variable sets. A variable
// that is empty, or that gotools does not know, maps to no key and is skipped.
func envKey(name, value string) (string, any) {
	if value == "" {
		return "", nil
	}

	return envKeys[name], value
}

// traceMode folds the other spellings of on and off into the TraceMode names:
// a JSON true or false decodes as "1" or "0", and "1" and "0" work in the
// environment.
func traceMode(m TraceMode) TraceMode {
	switch m {
	case "1":
		return TraceStderr
	case "0":
		return TraceOff
	}

	return m
}

// Validate reports every value that is out of range.
func (c Config) Validate() error {
	var errs []error

	if !c.Strategy.Valid() {
		errs = append(errs, fmt.Errorf("%s must be split, module or unified, not %q", KeyStrategy, c.Strategy))
	}

	if err := ValidateDir(c.Dir); err != nil {
		errs = append(errs, err)
	}

	if c.Settings.Jobs < 1 {
		errs = append(errs, fmt.Errorf("%s must be at least 1, not %d", KeyJobs, c.Settings.Jobs))
	}

	if c.Settings.OperationTimeout < 0 {
		errs = append(errs, fmt.Errorf("%s must not be negative, not %d", KeyOperationTimeout, c.Settings.OperationTimeout))
	}

	if c.Settings.LockTimeout < 0 {
		errs = append(errs, fmt.Errorf("%s must not be negative, not %d", KeyLockTimeout, c.Settings.LockTimeout))
	}

	switch c.Settings.Trace {
	case TraceOff, TraceStderr, TraceStdout:
	default:
		errs = append(errs, fmt.Errorf("%s must be false, true or stdout, not %q", KeyTrace, c.Settings.Trace))
	}

	return errors.Join(errs...)
}

// ValidateDir reports whether dir may serve as the tools directory. It must be
// a relative path that stays inside the project and is not the project root,
// since everything in it is created, rewritten and removed.
func ValidateDir(dir string) error {
	switch {
	case dir == "":
		return ierrors.New(ierrors.Usage, "tools directory must not be empty")
	case filepath.IsAbs(dir) || strings.HasPrefix(dir, "/"):
		return ierrors.New(ierrors.Usage, "tools directory must be relative to the project: %s", dir)
	}

	for part := range strings.SplitSeq(dir, "/") {
		if part == ".." {
			return ierrors.New(ierrors.Usage, "tools directory must stay inside the project: %s", dir)
		}
	}

	if filepath.Clean(dir) == "." {
		return ierrors.New(ierrors.Usage, "tools directory must not be the project root")
	}

	return nil
}
