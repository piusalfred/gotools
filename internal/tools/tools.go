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

// Package tools changes which tools a project manages.
//
// A [Manager] owns each change as one transaction over the tools directory and
// the manifest, for every strategy: a failure puts the files back and leaves
// the manifest as it was. [GoManager] is the Manager that drives the go
// command. Everything it needs from outside, the go runner, the manifest
// store, the go.mod parser, the fingerprint and the output streams, is
// injected through [Config], so tests drive it with fakes and no go.
package tools

import (
	"context"
	"errors"
	"io"
	"regexp"
	"strings"

	"golang.org/x/mod/module"

	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/manifest"
	"github.com/piusalfred/gotools/internal/modparser"
	"github.com/piusalfred/gotools/internal/strategy"
)

// Manager changes which tools a project manages. The caller holds the project
// lock and has refused offline mode where an operation needs the network.
type Manager interface {
	// Add installs one tool and records it in the manifest (unless
	// o.NoFlush). It is all-or-nothing.
	Add(ctx context.Context, o AddOptions) (manifest.Tool, error)
	// Remove deletes one tool's files and manifest entry.
	Remove(ctx context.Context, name string) (RemoveResult, error)
	// Rename renames a tool's files and manifest entry, undoing every step if
	// a later one fails.
	Rename(ctx context.Context, oldName, newName string, o RenameOptions) error
	// Upgrade moves one tool to @latest and records it.
	Upgrade(ctx context.Context, from manifest.Tool) (manifest.Tool, error)
	// Restore reinstalls tools at the versions the manifest pins,
	// concurrently where the strategy allows.
	Restore(ctx context.Context, tools []manifest.Tool, o RestoreOptions) error
}

// Store is the project's manifest and the way to persist it.
type Store interface {
	// Manifest is the in-memory manifest. Operations change it and call Flush.
	Manifest() *manifest.Manifest
	// Flush writes the manifest to disk.
	Flush() error
}

// VersionResolver resolves the Go version a new tool's modfile declares.
type VersionResolver interface {
	Resolve(ctx context.Context) (string, error)
}

// Recorder records the sync fingerprint once disk and manifest agree.
type Recorder interface {
	Record(ctx context.Context) error
}

// Advisor reports Go settings that weaken module verification for a module.
// Its warnings are advisory: they never fail an install.
type Advisor interface {
	Warnings(ctx context.Context, dir, modulePath string) []string
}

// Config is what a GoManager works on and with.
type Config struct {
	Root     string          // the project root
	Layout   strategy.Layout // the strategy and tools directory
	Prefix   string          // the module path prefix for tool modules
	Store    Store
	Versions VersionResolver
	ModFiles modparser.Parser // modparser.XParser when nil
	Go       gocmd.Runner
	Network  gocmd.Runner // for network-bound calls; Go when nil
	Recorder Recorder     // no fingerprint is recorded when nil
	Advisor  Advisor      // no advice when nil
	Out, Err io.Writer    // discarded when nil
	Program  string       // the command name used in hints
}

// GoManager is the Manager that drives the go command: modfiles are edited
// through the parser and `go get -tool`, and what it prints goes to Config.Out
// and Config.Err.
type GoManager struct {
	cfg Config
}

var _ Manager = (*GoManager)(nil)

// New returns the GoManager for cfg. Build it per command: the runners and
// the streams belong to the command.
func New(cfg Config) *GoManager {
	if cfg.ModFiles == nil {
		cfg.ModFiles = modparser.XParser{}
	}

	if cfg.Network == nil {
		cfg.Network = cfg.Go
	}

	if cfg.Out == nil {
		cfg.Out = io.Discard
	}

	if cfg.Err == nil {
		cfg.Err = io.Discard
	}

	return &GoManager{cfg: cfg}
}

func (m *GoManager) manifest() *manifest.Manifest { return m.cfg.Store.Manifest() }

// flush writes the manifest, as an error that names what failed.
func (m *GoManager) flush() error {
	if err := m.cfg.Store.Flush(); err != nil {
		return ierrors.Wrap(ierrors.Generic, "Could not write the manifest", err)
	}

	return nil
}

var (
	networkFailure = regexp.MustCompile(
		`(?i)dial tcp|i/o timeout|connection timed out|no such host|connection refused|network is unreachable|could not resolve host|fetch failed|timeout exceeded`,
	)
	usageFailure = regexp.MustCompile(
		`(?i)invalid module version syntax|malformed module path|invalid package path|can.t request version`,
	)
	notATool = regexp.MustCompile(`(?im)^(go: )?(no such tool|unknown command|tool not found)`)
)

// FailureCode classifies go's output: network problems are Network errors so
// CI can retry with backoff, bad package paths or versions are Usage errors,
// anything else is Generic.
func FailureCode(output string) ierrors.Code {
	switch {
	case networkFailure.MatchString(output):
		return ierrors.Network
	case usageFailure.MatchString(output):
		return ierrors.Usage
	}

	return ierrors.Generic
}

// CheckPackageSpec rejects a `pkg[@version]` that is not an import path, or
// that `go get` would read as one of its own flags. It guards values from the
// command line and from the committed manifest alike.
func CheckPackageSpec(spec string) error {
	base, _, _ := strings.Cut(spec, "@")

	if strings.HasPrefix(spec, "-") {
		return ierrors.New(ierrors.Environment, "Invalid package %q: must not start with '-'", spec)
	}

	if err := module.CheckImportPath(base); err != nil {
		return ierrors.New(ierrors.Environment, "Invalid package %q: %v", base, err)
	}

	return nil
}

// timeoutToExit maps a gocmd.TimeoutError to a Network error naming the
// operation; other errors pass through unchanged.
func timeoutToExit(err error, what string) error {
	if te, ok := errors.AsType[*gocmd.TimeoutError](err); ok {
		return ierrors.New(
			ierrors.Network,
			"%s timed out after %s. Check your network or raise %s.",
			what,
			te.After,
			config.EnvVarOperationTimeout,
		)
	}

	return err
}

// goFailure turns a failed go call into the matching error: a timeout is a
// Network error, anything else a Generic one carrying go's stderr.
func goFailure(what string, res gocmd.Result, err error) error {
	if err != nil {
		return timeoutToExit(err, what)
	}

	return ierrors.New(ierrors.Generic, "%s failed (exit %d):\n%s", what, res.Code, res.Stderr)
}
