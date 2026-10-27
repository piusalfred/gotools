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

// Package modparser reads and edits go.mod files on golang.org/x/mod/modfile.
//
// [Parser] is the seam to the go.mod grammar, with [XParser] as the adapter on
// the Go team's own parser. A parsed [File] reports the facts a tool manager
// needs (the tool directives and the versions that provide them) and makes the
// edits it needs (drop tools, rename the module), leaving everything else in
// the file as it was.
package modparser

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"

	ierrors "github.com/piusalfred/gotools/internal/errors"
)

// Require is one requirement from a go.mod.
type Require struct {
	Path    string
	Version string
}

// File is a parsed go.mod. Edits change the parsed value, which then reports
// the new state; Format renders it. Get one from a Parser.
type File struct {
	Module   string   // module path, "" when there is no module directive
	Go       string   // go directive version, "" when absent
	Tools    []string // tool directive package paths, in file order
	Requires []Require

	file *modfile.File
}

// Parser reads go.mod files. Its errors are Environment errors from package
// errors, with the underlying error reachable through errors.Is and errors.As.
type Parser interface {
	// Parse parses go.mod contents.
	Parse(src []byte) (*File, error)
	// Read reads and parses the go.mod-format file at name. A missing file is
	// an error that satisfies errors.Is(err, fs.ErrNotExist).
	Read(name string) (*File, error)
	// Validate reports every tool directive in f that is not an import path
	// (including one go would read as a flag), joined into one error; nil
	// means it is fine. The grammar is Parse's job: a bad go version or a
	// malformed directive never gets this far.
	Validate(f *File) error
}

// XParser is the Parser on golang.org/x/mod/modfile. It has no state: the
// zero value is ready.
type XParser struct{}

var _ Parser = XParser{}

// Parse implements Parser.
func (XParser) Parse(src []byte) (*File, error) {
	f, err := modfile.Parse("go.mod", src, nil)
	if err != nil {
		return nil, ierrors.Wrap(ierrors.Environment, "", err)
	}

	m := &File{file: f}
	m.refresh()

	return m, nil
}

// Read implements Parser.
func (x XParser) Read(name string) (*File, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, ierrors.Wrap(ierrors.Environment, "", err)
	}

	return x.Parse(b)
}

// Validate implements Parser.
func (XParser) Validate(f *File) error {
	var errs []error

	for _, t := range f.Tools {
		if strings.HasPrefix(t, "-") {
			errs = append(errs, ierrors.New(ierrors.Environment, "invalid tool %q: must not start with '-'", t))

			continue
		}

		if err := module.CheckImportPath(t); err != nil {
			errs = append(errs, fmt.Errorf("invalid tool %q: %w", t, err))
		}
	}

	if err := errors.Join(errs...); err != nil {
		return ierrors.Wrap(ierrors.Environment, "", err)
	}

	return nil
}

// refresh derives the exported fields from the parsed file.
func (m *File) refresh() {
	f := m.file

	m.Module, m.Go, m.Tools, m.Requires = "", "", nil, nil

	if f.Module != nil {
		m.Module = f.Module.Mod.Path
	}

	if f.Go != nil {
		m.Go = f.Go.Version
	}

	// modfile leaves a zero entry behind for a directive it dropped.
	for _, t := range f.Tool {
		if t.Path != "" {
			m.Tools = append(m.Tools, t.Path)
		}
	}

	for _, r := range f.Require {
		if r.Mod.Path != "" {
			m.Requires = append(m.Requires, Require{Path: r.Mod.Path, Version: r.Mod.Version})
		}
	}
}

// ModuleFor returns the require entry providing pkg, trying progressively
// shorter path prefixes (longest wins, as in Go).
func (m *File) ModuleFor(pkg string) (Require, bool) {
	for c := pkg; c != "" && c != "."; c = path.Dir(c) {
		for _, r := range m.Requires {
			if r.Path == c {
				return r, true
			}
		}
	}

	return Require{}, false
}

// VersionFor returns the required version providing pkg, or "".
func (m *File) VersionFor(pkg string) string {
	r, _ := m.ModuleFor(pkg)

	return r.Version
}

// Spec is a tool directive resolved to a binary name and pinned package.
type Spec struct {
	Name    string
	Package string
	Version string // "latest" when the go.mod carries no pin
}

// String renders a Spec as "name pkg@version".
func (s Spec) String() string {
	return fmt.Sprintf("%s %s@%s", s.Name, s.Package, s.Version)
}

// Scan lists the tool directives as name, package and version.
func (m *File) Scan() []Spec {
	out := make([]Spec, 0, len(m.Tools))

	for _, p := range m.Tools {
		v := m.VersionFor(p)
		if v == "" {
			v = "latest"
		}

		out = append(out, Spec{Name: InferBinaryName(p), Package: p, Version: v})
	}

	return out
}

var majorSuffix = regexp.MustCompile(`^v[0-9]+$`)

// InferBinaryName converts a package path (no @version) into the expected
// binary name: drop a trailing /vN element, then use <name> of /cmd/<name>,
// else the last element. A name that merely starts with v and digits, such as
// "v2ray", is kept.
func InferBinaryName(pkg string) string {
	if d, last := path.Split(pkg); majorSuffix.MatchString(last) {
		pkg = strings.TrimSuffix(d, "/")
	}

	return path.Base(pkg)
}

// BinaryNameFor resolves a tool alias to its binary name using the first tool
// directive in m, falling back to toolName.
func (m *File) BinaryNameFor(toolName string) string {
	if len(m.Tools) > 0 {
		return InferBinaryName(m.Tools[0])
	}

	return toolName
}

// DropTools removes the tool directives for pkgs and leaves every other
// directive alone. A package that has no directive is ignored.
func (m *File) DropTools(pkgs ...string) error {
	for _, pkg := range pkgs {
		if err := m.file.DropTool(pkg); err != nil {
			return ierrors.Wrap(ierrors.Generic, "drop tool "+pkg, err)
		}
	}

	m.file.Cleanup()
	m.refresh()

	return nil
}

// StripTools removes every tool directive.
func (m *File) StripTools() error {
	return m.DropTools(slices.Clone(m.Tools)...)
}

// SetModule rewrites the module line and leaves everything else untouched.
func (m *File) SetModule(modPath string) error {
	if err := m.file.AddModuleStmt(modPath); err != nil {
		return ierrors.Wrap(ierrors.Generic, "set module "+modPath, err)
	}

	m.refresh()

	return nil
}

// Format renders the go.mod with the edits made so far.
func (m *File) Format() ([]byte, error) {
	b, err := m.file.Format()
	if err != nil {
		return nil, ierrors.Wrap(ierrors.Generic, "format go.mod", err)
	}

	return b, nil
}

// ModulePath returns the module path of the go.mod in dir, or "" when there is
// none or it declares no module.
func ModulePath(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}

	return modfile.ModulePath(b)
}
