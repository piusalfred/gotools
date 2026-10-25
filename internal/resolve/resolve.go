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

// Package resolve finds the tool a name stands for and asks go whether it can
// run.
//
// A [Resolver] maps a tool name, as the user gave it (possibly an alias), to
// the modfile that serves it and the package and binary behind it. A [Prober]
// launches the tool through `go tool` to see whether it runs. Both take their
// seams as fields (the modfile parser, the manifest lookup, the go runner), so
// a test supplies fakes and no go process runs.
package resolve

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/manifest"
	"github.com/piusalfred/gotools/internal/modparser"
	"github.com/piusalfred/gotools/internal/strategy"
)

// Manifest is the part of the manifest a Resolver reads.
type Manifest interface {
	Tool(name string) (manifest.Tool, bool)
}

// Ref is a tool name resolved against the tools directory.
type Ref struct {
	Name       string // name as given (manifest name, possibly an alias)
	Package    string // tool directive serving Name; "" when none is declared
	Binary     string // name passed to `go tool`
	ModFile    string // absolute modfile path
	ModFileRel string // modfile path relative to the project root, slash-separated
}

// MissingError reports that the modfile serving a tool does not exist. The Ref
// returned with it is still filled in, so a caller can say where it looked.
type MissingError struct {
	Path string // relative to the project root
}

func (e *MissingError) Error() string { return "missing " + e.Path }

// Unwrap lets errors.Is(err, fs.ErrNotExist) match.
func (e *MissingError) Unwrap() error { return fs.ErrNotExist }

// Resolver resolves tool names for one project.
type Resolver struct {
	Root     string
	Layout   strategy.Layout
	ModFiles modparser.Parser
	Manifest Manifest // may be nil
}

// Resolve finds the modfile, package and binary serving name.
//
// split and module: the tool's own modfile holds one directive, which serves
// it.
//
// unified: the shared go.mod holds many directives. The manifest entry's
// package is used when it is declared there; without a manifest entry, the
// directive whose inferred binary name equals name. Taking the first directive
// would run the wrong tool whenever the project has two.
func (r *Resolver) Resolve(name string) (Ref, error) {
	rel, err := r.Layout.ModFileRel(name)
	if err != nil {
		return Ref{}, err
	}

	ref := Ref{Name: name, Binary: name, ModFileRel: rel, ModFile: filepath.Join(r.Root, filepath.FromSlash(rel))}

	data, err := os.ReadFile(ref.ModFile)
	if errors.Is(err, fs.ErrNotExist) {
		return ref, &MissingError{Path: rel}
	}

	if err != nil {
		return ref, err
	}

	mod, err := r.ModFiles.Parse(data)
	if err != nil {
		return ref, fmt.Errorf("parse %s: %w", rel, err)
	}

	switch r.Layout.Strategy {
	case strategy.Unified:
		ref.Package = r.unifiedPackage(mod, name)
	default:
		if len(mod.Tools) > 0 {
			ref.Package = mod.Tools[0]
		}
	}

	if ref.Package != "" {
		ref.Binary = modparser.InferBinaryName(ref.Package)
	}

	return ref, nil
}

func (r *Resolver) unifiedPackage(mod *modparser.File, name string) string {
	if r.Manifest != nil {
		if t, ok := r.Manifest.Tool(name); ok {
			if slices.Contains(mod.Tools, t.Package) {
				return t.Package
			}

			return ""
		}
	}

	for _, pkg := range mod.Tools {
		if modparser.InferBinaryName(pkg) == name {
			return pkg
		}
	}

	return ""
}

// Call is the `go tool` invocation for ref, the single definition shared by the
// probes and exec:
//
//	unified  go tool <bin>                    in <dir>
//	split    go tool -modfile=<abs> <bin>     in the project root
//	module   go tool <bin>                    in <dir>/<name>
//
// args are appended after the binary.
func (r *Resolver) Call(ref Ref, args ...string) gocmd.Call {
	c := gocmd.Call{Dir: filepath.Dir(ref.ModFile), Args: []string{"tool"}}

	if r.Layout.Strategy == strategy.Split {
		c.Dir = r.Root
		c.Args = append(c.Args, "-modfile="+ref.ModFile)
	}

	c.Args = append(c.Args, ref.Binary)
	c.Args = append(c.Args, args...)

	return c
}

// probeArgs are tried in order by Runs; one exit 0 is enough.
var probeArgs = []string{"--version", "--help", "version"}

// goLevelFailure matches go-command errors, as opposed to the tool itself
// starting and exiting non-zero (a usage error on empty stdin, say).
var goLevelFailure = regexp.MustCompile(`^go( tool)?:|no such tool|is not a tool|no go\.mod file found`)

// Prober asks go whether tools run. It never runs a tool for `install`: the
// install verify is `go tool -n`, which builds without running.
type Prober struct {
	Resolver *Resolver
	Go       gocmd.Runner
	Offline  bool // GOPROXY=off, so a missing module fails fast
}

// Runs launches the tool with each probe argument until one exits 0. A tool
// that has none of them (many exit 2 on an unknown flag) still counts when go
// can build it, since that is all `go tool -n` asks of it. It is what `check`
// reports.
func (p *Prober) Runs(ctx context.Context, name string) bool {
	ref, err := p.Resolver.Resolve(name)
	if err != nil {
		return false
	}

	for _, arg := range probeArgs {
		call := p.Resolver.Call(ref, arg)
		if p.Offline {
			call.Env = append(call.Env, "GOPROXY=off")
		}

		if res, err := p.Go.Run(ctx, call); err == nil && res.Code == 0 {
			return true
		}
	}

	// No probe argument was accepted: does it at least resolve and build?
	call := p.Resolver.Call(ref)
	call.Args = slices.Insert(call.Args, 1, "-n")

	if p.Offline {
		call.Env = append(call.Env, "GOPROXY=off")
	}

	res, err := p.Go.Run(ctx, call)

	return err == nil && res.Code == 0
}

// Probe says whether `go tool` can launch name under the project's strategy:
// "" when it can, else a one-line reason.
//
// Read-only: stdin is empty, GOTOOLCHAIN=local so a toolchain is never
// downloaded, and GOPROXY=off when offline.
func (p *Prober) Probe(ctx context.Context, name string) string {
	if err := strategy.ValidateName(name); err != nil {
		return "invalid tool name: " + name
	}

	if !p.Resolver.Layout.Strategy.Valid() {
		return "unknown strategy: " + string(p.Resolver.Layout.Strategy)
	}

	ref, err := p.Resolver.Resolve(name)
	if err != nil {
		return err.Error()
	}

	if ref.Package == "" {
		// No directive serves this tool: `go tool` can only fail, so say why
		// without spending a go call.
		return "no tool directive for " + name + " in " + ref.ModFileRel
	}

	call := p.Resolver.Call(ref)
	call.Env = []string{"GOTOOLCHAIN=local"}

	if p.Offline {
		call.Env = append(call.Env, "GOPROXY=off")
	}

	res, err := p.Go.Run(ctx, call)

	switch {
	case errors.Is(err, gocmd.ErrNotFound):
		return "go not installed"
	case err != nil:
		return err.Error()
	case res.Code == 0:
		return ""
	}

	for _, out := range [][]byte{res.Stderr, res.Stdout} {
		for line := range strings.SplitSeq(string(out), "\n") {
			if goLevelFailure.MatchString(line) {
				return line
			}
		}
	}

	return ""
}
