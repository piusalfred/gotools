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

// Package binfo reads what Go binaries say about themselves: the package they
// were built from and the module version, which the Go linker embeds. It reads
// the file and never runs it, so scanning a directory of binaries is safe.
//
// [Scan] lists a directory as [Candidate]s; a candidate that cannot be turned
// back into a "package@version" carries the reason in Skip.
package binfo

import (
	"debug/buildinfo"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/semver"

	ierrors "github.com/piusalfred/gotools/internal/errors"
)

// Info is the build information of one binary.
type Info struct {
	Package string // import path of the main package
	Module  string // module path of the main module
	Version string // module version, "(devel)" when built from a working tree
	// Replaced reports that the main module was replaced when it was built, so
	// Version is not what was compiled.
	Replaced bool
}

// Reader reads the build information of the binary at path. An error means the
// file is not a Go binary, or carries no module information.
type Reader interface {
	Read(path string) (Info, error)
}

// FileReader is the Reader that parses the file with debug/buildinfo.
type FileReader struct{}

var _ Reader = FileReader{}

// ErrNoModule is returned when a binary was built without module information.
var ErrNoModule = errors.New("built without module information")

// Read implements Reader.
func (FileReader) Read(path string) (Info, error) {
	bi, err := buildinfo.ReadFile(path)
	if err != nil {
		return Info{}, fmt.Errorf("read build info: %w", err)
	}

	if bi.Main.Path == "" || bi.Path == "" {
		return Info{}, ErrNoModule
	}

	return Info{
		Package:  bi.Path,
		Module:   bi.Main.Path,
		Version:  bi.Main.Version,
		Replaced: bi.Main.Replace != nil,
	}, nil
}

// Candidate is one file of a scanned directory.
type Candidate struct {
	Name string // the file name, which is the tool's name
	File string // the file's path
	Info
	// Skip says why the binary cannot be imported; empty when it can.
	Skip string
}

// Spec is the "package@version" that rebuilds the binary.
func (c Candidate) Spec() string { return c.Package + "@" + c.Version }

// Scan reads every executable regular file in dir (a symlink to one counts), in
// name order. A directory that does not exist is an Environment error. A file
// that is not a Go binary is returned with Skip set, not dropped, so the caller
// can say it was seen.
func Scan(dir string, r Reader) ([]Candidate, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, ierrors.New(ierrors.Environment, "Could not read %s: %v", dir, err)
	}

	var out []Candidate

	for _, e := range entries {
		file := filepath.Join(dir, e.Name())

		fi, err := os.Stat(file) // follows a symlink to the file it names
		if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
			continue
		}

		c := Candidate{Name: e.Name(), File: file}

		info, err := r.Read(file)

		switch {
		case err != nil && errors.Is(err, ErrNoModule):
			c.Skip = "built without module information"
		case err != nil:
			c.Skip = "not a Go binary"
		default:
			c.Info = info
			c.Skip = importable(info)
		}

		out = append(out, c)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// importable says why info cannot be turned into a package@version go can
// fetch, or "" when it can.
func importable(i Info) string {
	switch {
	case i.Replaced:
		return "built with a replace directive, so the version is not what was compiled"
	case i.Version == "" || i.Version == "(devel)":
		return "built from a working tree, so it has no version"
	case !semver.IsValid(i.Version):
		return fmt.Sprintf("version %q is not a module version", i.Version)
	case strings.Contains(i.Version, "+") && semver.Build(i.Version) != "+incompatible":
		return fmt.Sprintf("version %s was built from a modified tree", i.Version)
	}

	return ""
}
