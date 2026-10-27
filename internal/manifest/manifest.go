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

// Package manifest reads and writes .gotools.json: the layout, the settings
// and the list of managed tools.
//
// A [Manifest] holds file values only. Values resolved from the environment
// belong to package config, so an environment override is never written back.
// Parse accepts any valid JSON layout and ignores unknown keys; Render writes
// the layout committed files use, with a fixed key order and the tools sorted.
package manifest

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/strategy"
)

// SchemaVersion is the manifest schema this build understands. Bump it only
// when reading would misinterpret a newer format; additive changes do not need
// a bump.
const SchemaVersion = 1

// lockStaleTimeout is written for files that still carry the key; nothing
// reads it.
const lockStaleTimeout = 300

var (
	// ErrMissing matches the error Load returns when there is no manifest.
	ErrMissing = errors.New("manifest not found")
	// ErrMalformed matches the error returned for truncated or malformed JSON.
	ErrMalformed = errors.New("manifest is not parseable: truncated or malformed JSON")
)

// NewerSchemaError reports a manifest written by a newer gotools. It is
// wrapped in an Environment error.
type NewerSchemaError struct {
	File      string
	Found     int // the schema version in the file
	Supported int // the schema version this build understands
}

func (e *NewerSchemaError) Error() string {
	return fmt.Sprintf("This project's %s requires schema version %d, but this build only understands version %d",
		e.File, e.Found, e.Supported)
}

// Tool is one managed tool entry.
type Tool struct {
	Name    string
	Source  string
	Package string
	Version string
}

// Manifest is the in-memory form of .gotools.json. Empty layout fields mean
// the file does not set them. Settings carries the defaults for keys the file
// leaves out, and Render writes them all.
type Manifest struct {
	Schema       int
	Strategy     string
	Dir          string
	GoVersion    string
	ModulePrefix string
	Settings     config.Settings
	Tools        []Tool
}

// FindRoot walks up from start looking for the manifest file. It returns the
// directory that holds it, or start when there is none. It never changes the
// process working directory.
func FindRoot(start string) (string, bool) {
	dir := filepath.Clean(start)

	for {
		if info, err := os.Stat(filepath.Join(dir, config.ManifestFile)); err == nil && info.Mode().IsRegular() {
			return dir, true
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return start, false
		}

		dir = parent
	}
}

// New returns an empty manifest whose settings are the defaults.
func New() *Manifest {
	return &Manifest{Schema: SchemaVersion, Settings: defaultSettingsDoc().settings()}
}

// document is the file's shape. Field order is the key order Render writes.
type document struct {
	Version      jsontext.Value `json:"version"`
	Strategy     string         `json:"strategy"`
	Dir          string         `json:"dir"`
	GoVersion    string         `json:"go_version"`
	ModulePrefix string         `json:"module_prefix"`
	Settings     settingsDoc    `json:"settings"`
	Tools        toolsDoc       `json:"tools"`
}

// settingsDoc lists the settings keys alphabetically, as committed files do.
type settingsDoc struct {
	Jobs             int      `json:"jobs"`
	LockStaleTimeout int      `json:"lock_stale_timeout"`
	LockTimeout      int      `json:"lock_timeout"`
	NoLock           bool     `json:"no_lock"`
	Offline          bool     `json:"offline"`
	OperationTimeout int      `json:"operation_timeout"`
	Trace            traceDoc `json:"trace"`
	Verbose          bool     `json:"verbose"`
}

func defaultSettingsDoc() settingsDoc {
	return docOf(config.Settings{
		Offline:          config.DefaultOffline,
		Jobs:             config.DefaultJobs,
		OperationTimeout: config.DefaultOperationTimeout,
		LockTimeout:      config.DefaultLockTimeout,
		NoLock:           config.DefaultNoLock,
		Trace:            config.DefaultTrace,
		Verbose:          config.DefaultVerbose,
	})
}

func docOf(s config.Settings) settingsDoc {
	return settingsDoc{
		Jobs:             s.Jobs,
		LockStaleTimeout: lockStaleTimeout,
		LockTimeout:      s.LockTimeout,
		NoLock:           s.NoLock,
		Offline:          s.Offline,
		OperationTimeout: s.OperationTimeout,
		Trace:            traceDoc(s.Trace),
		Verbose:          s.Verbose,
	}
}

func (d settingsDoc) settings() config.Settings {
	return config.Settings{
		Offline:          d.Offline,
		Jobs:             d.Jobs,
		OperationTimeout: d.OperationTimeout,
		LockTimeout:      d.LockTimeout,
		NoLock:           d.NoLock,
		Trace:            config.TraceMode(d.Trace),
		Verbose:          d.Verbose,
	}
}

// traceDoc is a trace setting as the file spells it: a boolean, or the string
// "stdout".
type traceDoc config.TraceMode

func (t traceDoc) MarshalJSON() ([]byte, error) {
	switch config.TraceMode(t) {
	case config.TraceStdout:
		return []byte(`"stdout"`), nil
	case config.TraceStderr:
		return []byte("true"), nil
	}

	return []byte("false"), nil
}

func (t *traceDoc) UnmarshalJSON(b []byte) error {
	var s string

	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &s); err != nil {
			return ierrors.Wrap(ierrors.Environment, "trace", err)
		}
	} else {
		s = string(b)
	}

	switch s {
	case "stdout":
		*t = traceDoc(config.TraceStdout)
	case "true", "1":
		*t = traceDoc(config.TraceStderr)
	case "false", "0", "":
		*t = traceDoc(config.TraceOff)
	default:
		return ierrors.New(ierrors.Environment, "trace must be true, false or stdout, not %q", s)
	}

	return nil
}

type toolEntry struct {
	Source  string `json:"source"`
	Package string `json:"package"`
	Version string `json:"version"`
}

// toolsDoc is the tools object. It is written in manifest order (see
// SortedTools), which an ordinary map would not keep.
type toolsDoc []Tool

func (t toolsDoc) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return ierrors.Wrap(ierrors.Generic, "write tools", err)
	}

	for _, tool := range sorted(t) {
		if err := enc.WriteToken(jsontext.String(tool.Name)); err != nil {
			return ierrors.Wrap(ierrors.Generic, "write tool name "+strconv.Quote(tool.Name), err)
		}

		if err := json.MarshalEncode(
			enc,
			toolEntry{Source: tool.Source, Package: tool.Package, Version: tool.Version},
		); err != nil {
			return ierrors.Wrap(ierrors.Generic, "write tool "+strconv.Quote(tool.Name), err)
		}
	}

	if err := enc.WriteToken(jsontext.EndObject); err != nil {
		return ierrors.Wrap(ierrors.Generic, "write tools", err)
	}

	return nil
}

// rawManifest is what Parse reads. Unknown keys are accepted and dropped.
type rawManifest struct {
	Version      jsontext.Value            `json:"version"`
	Strategy     string                    `json:"strategy"`
	Dir          string                    `json:"dir"`
	GoVersion    string                    `json:"go_version"`
	ModulePrefix string                    `json:"module_prefix"`
	Settings     settingsDoc               `json:"settings"`
	Tools        map[string]toolEntry      `json:"tools"`
	Unknown      map[string]jsontext.Value `json:",unknown"`
}

// bareStdout matches an unquoted `"trace": stdout`, which older writers
// produced; it is quoted before parsing so those files still load.
var bareStdout = regexp.MustCompile(`("trace"\s*:\s*)stdout\b`)

// ParseResponse is what Parse and Load return alongside the error.
type ParseResponse struct {
	Manifest *Manifest
	// Warnings are problems that did not stop the parse, such as a skipped tool
	// entry with an unsafe name.
	Warnings []string
}

// Parse decodes manifest bytes. It ignores unknown keys, skips tool entries
// with unsafe names (reported as warnings), and fills settings the file omits
// with their defaults. name only feeds error messages. Every error is an
// Environment error: ErrMalformed for bad JSON or a bad value, and a
// *NewerSchemaError for a newer schema.
func Parse(data []byte, name string) (ParseResponse, error) {
	data = bareStdout.ReplaceAll(data, []byte(`${1}"stdout"`))

	raw := rawManifest{Settings: defaultSettingsDoc()}
	if err := json.Unmarshal(data, &raw, jsontext.AllowDuplicateNames(true)); err != nil {
		return ParseResponse{}, ierrors.Wrap(ierrors.Environment, name, fmt.Errorf("%w: %w", ErrMalformed, err))
	}

	m := &Manifest{
		Schema:       schemaOf(raw.Version),
		Strategy:     raw.Strategy,
		Dir:          raw.Dir,
		GoVersion:    raw.GoVersion,
		ModulePrefix: raw.ModulePrefix,
		Settings:     raw.Settings.settings(),
	}

	if m.Schema > SchemaVersion {
		return ParseResponse{
			Manifest: m,
		}, ierrors.Wrap(
			ierrors.Environment,
			"",
			&NewerSchemaError{File: name, Found: m.Schema, Supported: SchemaVersion},
		)
	}

	var warnings []string

	names := make([]string, 0, len(raw.Tools))
	for n := range raw.Tools {
		names = append(names, n)
	}

	slices.Sort(names)

	for _, n := range names {
		if strategy.ValidateName(n) != nil {
			warnings = append(warnings, "Ignoring tool entry with unsafe name: "+n)

			continue
		}

		t := raw.Tools[n]
		m.Tools = append(m.Tools, Tool{Name: n, Source: t.Source, Package: t.Package, Version: t.Version})
	}

	return ParseResponse{Manifest: m, Warnings: warnings}, nil
}

// schemaOf returns the integer schema version; an absent or non-numeric value
// means version 1.
func schemaOf(v jsontext.Value) int {
	n, err := strconv.Atoi(string(v))
	if err != nil || n < 0 {
		return 1
	}

	return n
}

// Load reads and parses the manifest at path. A missing file is an
// Environment error matching ErrMissing.
func Load(path string) (ParseResponse, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ParseResponse{}, ierrors.Wrap(ierrors.Environment, path, ErrMissing)
	}

	if err != nil {
		return ParseResponse{}, ierrors.Wrap(ierrors.Environment, "", err)
	}

	return Parse(data, path)
}

// Status is the result of Check.
type Status int

const (
	Valid       Status = iota // parses, with a strategy
	Missing                   // no manifest file
	NewerSchema               // written by a newer gotools
	Unreadable                // malformed JSON, or no top-level strategy
)

// Check is the non-fatal validation for diagnostics: it classifies the
// manifest at path and returns the error behind any status but Valid.
func Check(path string) (Status, error) {
	res, err := Load(path)

	if _, ok := errors.AsType[*NewerSchemaError](err); ok {
		return NewerSchema, err
	}

	switch {
	case err == nil && res.Manifest.Strategy != "":
		return Valid, nil
	case err == nil:
		return Unreadable, ErrMalformed
	case errors.Is(err, ErrMissing):
		return Missing, err
	}

	return Unreadable, err
}

// Render produces the manifest bytes in the layout committed files use: fixed
// key order, 2-space indent, settings alphabetical, tools sorted by source,
// package, version and name. Layout fields the manifest leaves empty are
// written as their defaults, and the module prefix defaults to the directory.
func (m *Manifest) Render() ([]byte, error) {
	dir := orDefault(m.Dir, config.DefaultDir)

	doc := document{
		Version:      jsontext.Value("1"),
		Strategy:     orDefault(m.Strategy, string(config.DefaultStrategy)),
		Dir:          dir,
		GoVersion:    orDefault(m.GoVersion, config.DefaultGoVersion),
		ModulePrefix: orDefault(m.ModulePrefix, dir),
		Settings:     docOf(m.Settings),
		Tools:        m.Tools,
	}

	b, err := json.Marshal(
		doc,
		jsontext.WithIndent("  "),
		jsontext.EscapeForHTML(false),
		jsontext.AllowInvalidUTF8(true),
	)
	if err != nil {
		return nil, ierrors.Wrap(ierrors.Generic, "render manifest", err)
	}

	return append(b, '\n'), nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

// Flush writes the manifest atomically: a temp file created exclusively next to
// the target, so a pre-planted symlink cannot redirect the write, then renamed
// over it.
func (m *Manifest) Flush(path string) error {
	data, err := m.Render()
	if err != nil {
		return ierrors.Wrap(ierrors.Generic, "cannot render "+path, err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp.*")
	if err != nil {
		return ierrors.Wrap(ierrors.Environment, "cannot create temp file for "+path, err)
	}

	name := tmp.Name()

	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}

	if err == nil {
		err = os.Rename(name, path)
	}

	if err != nil {
		_ = os.Remove(name)

		return ierrors.Wrap(ierrors.Environment, "cannot write "+path, err)
	}

	return nil
}

// CleanupTemps removes the temp files a crashed Flush left next to path.
func CleanupTemps(path string) {
	matches, _ := filepath.Glob(path + ".tmp.*")
	for _, f := range matches {
		_ = os.Remove(f)
	}
}

// Tool returns the entry for name.
func (m *Manifest) Tool(name string) (Tool, bool) {
	for _, t := range m.Tools {
		if t.Name == name {
			return t, true
		}
	}

	return Tool{}, false
}

// SetTool adds or replaces a tool entry.
func (m *Manifest) SetTool(t Tool) {
	for i := range m.Tools {
		if m.Tools[i].Name == t.Name {
			m.Tools[i] = t

			return
		}
	}

	m.Tools = append(m.Tools, t)
}

// RemoveTool deletes a tool entry, reporting whether it existed.
func (m *Manifest) RemoveTool(name string) bool {
	for i := range m.Tools {
		if m.Tools[i].Name == name {
			m.Tools = slices.Delete(m.Tools, i, i+1)

			return true
		}
	}

	return false
}

// ToolNames lists tool names in store order.
func (m *Manifest) ToolNames() []string {
	out := make([]string, 0, len(m.Tools))
	for _, t := range m.Tools {
		out = append(out, t.Name)
	}

	return out
}

// FindByPackage returns the name of the first tool whose package is pkg.
func (m *Manifest) FindByPackage(pkg string) (string, bool) {
	for _, t := range m.Tools {
		if t.Package == pkg {
			return t.Name, true
		}
	}

	return "", false
}

// SortedTools returns the tools in manifest order: source, package, version,
// then name.
func (m *Manifest) SortedTools() []Tool { return sorted(m.Tools) }

func sorted(tools []Tool) []Tool {
	out := slices.Clone(tools)
	slices.SortFunc(out, compare)

	return out
}

func compare(a, b Tool) int {
	for _, d := range []int{
		strings.Compare(a.Source, b.Source),
		strings.Compare(a.Package, b.Package),
		strings.Compare(a.Version, b.Version),
		strings.Compare(a.Name, b.Name),
	} {
		if d != 0 {
			return d
		}
	}

	return 0
}

// Twin reports another tool that is the same tool as (name, pkg, version): the
// same package, and either the same version or, when unified is true, any
// version. The unified strategy shares one go.mod, which holds a single version
// of a module, so a second name for a package can only be the same tool.
func (m *Manifest) Twin(name, pkg, version string, unified bool) (Tool, bool) {
	for _, t := range m.Tools {
		if t.Name != name && t.Package == pkg && (unified || t.Version == version) {
			return t, true
		}
	}

	return Tool{}, false
}

// Twins groups the tools that are one tool under several names (see Twin), in
// manifest order. Each group has at least two entries.
func (m *Manifest) Twins(unified bool) [][]Tool {
	var groups [][]Tool

	seen := map[string]bool{}
	all := m.SortedTools()

	for _, t := range all {
		key := t.Package
		if !unified {
			key += "@" + t.Version
		}

		if seen[key] {
			continue
		}

		seen[key] = true

		var group []Tool

		for _, u := range all {
			if u.Package == t.Package && (unified || u.Version == t.Version) {
				group = append(group, u)
			}
		}

		if len(group) > 1 {
			groups = append(groups, group)
		}
	}

	return groups
}

// Get returns the file's value for a GOTOOLS_* key (config.Keys) and whether it
// is set. A layout key the file leaves out is not set; a settings key always
// has a value, the default when the file says nothing.
func (m *Manifest) Get(key string) (string, bool) {
	var v string

	switch key {
	case config.EnvVarStrategy:
		v = m.Strategy
	case config.EnvVarDir:
		v = m.Dir
	case config.EnvVarGoVersion:
		v = m.GoVersion
	case config.EnvVarModulePrefix:
		v = m.ModulePrefix
	default:
		return config.FormatSetting(key, m.Settings)
	}

	return v, v != ""
}

// Set applies value to a GOTOOLS_* key in memory, after checking it against
// what the key holds. A value that does not fit is a usage error and leaves the
// manifest unchanged; Flush writes it.
func (m *Manifest) Set(key, value string) error {
	switch key {
	case config.EnvVarStrategy:
		if !strategy.Strategy(value).Valid() {
			return ierrors.New(ierrors.Usage, "Invalid strategy: %s (must be unified, split, or module)", value)
		}

		m.Strategy = value
	case config.EnvVarDir:
		if err := config.ValidateDir(value); err != nil {
			return err
		}

		m.Dir = value
	case config.EnvVarGoVersion:
		m.GoVersion = value
	case config.EnvVarModulePrefix:
		m.ModulePrefix = value
	default:
		return config.ParseSetting(key, value, &m.Settings)
	}

	return nil
}
