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

// Package strategy is the tool-isolation strategy: which files hold a tool's
// go.mod and go.sum, and which strategies a tools directory already uses.
//
// A [Strategy] says how tools are kept apart. A [Layout] pairs a strategy with
// a tools directory and answers where each tool's files live. [Detect] reads a
// directory on disk and reports which layouts it holds.
package strategy

import (
	"bufio"
	"bytes"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/modparser"
)

// Strategy is a tool-isolation strategy.
type Strategy string

const (
	// Split keeps one <name>.mod and <name>.sum per tool in a flat directory.
	Split Strategy = "split"
	// Module keeps one <name>/go.mod per tool.
	Module Strategy = "module"
	// Unified keeps every tool directive in one go.mod.
	Unified Strategy = "unified"
)

// Valid reports whether s is one of the three known strategies.
func (s Strategy) Valid() bool {
	switch s {
	case Split, Module, Unified:
		return true
	}

	return false
}

// Parse returns the strategy named s, or an Environment error when it is not
// one of the three.
func Parse(s string) (Strategy, error) {
	if st := Strategy(s); st.Valid() {
		return st, nil
	}

	return "", ierrors.New(ierrors.Environment, "Invalid strategy %q (must be unified, split, or module)", s)
}

// ValidateName rejects tool names that would escape the tools directory or
// break file handling: empty, "." and "..", a leading "-", a path separator,
// whitespace and control characters.
func ValidateName(name string) error {
	switch {
	case name == "":
		return ierrors.New(ierrors.Usage, "tool name must not be empty")
	case name == "." || name == "..":
		return ierrors.New(ierrors.Usage, "invalid tool name: %s", name)
	case strings.HasPrefix(name, "-"):
		return ierrors.New(ierrors.Usage, "invalid tool name (must not start with '-'): %s", name)
	case strings.ContainsAny(name, `/\`):
		return ierrors.New(ierrors.Usage, "invalid tool name (must not contain a path separator): %s", name)
	}

	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return ierrors.New(ierrors.Usage, "invalid tool name (must not contain whitespace or control characters): %s", name)
		}
	}

	return nil
}

// Layout is a strategy applied to a tools directory.
type Layout struct {
	Strategy Strategy
	Dir      string // the tools directory, relative to the project root
}

// dirSlash is the tools directory in clean, slash-separated form.
func (l Layout) dirSlash() string { return path.Clean(filepath.ToSlash(l.Dir)) }

// ToolsDir is the tools directory under root.
func (l Layout) ToolsDir(root string) string {
	return filepath.Join(root, filepath.FromSlash(l.dirSlash()))
}

// ModFileRel is the modfile holding tool name, relative to the project root
// and slash-separated:
//
//	unified  <dir>/go.mod
//	split    <dir>/<name>.mod
//	module   <dir>/<name>/go.mod
//
// A name that is joined into the path is validated.
func (l Layout) ModFileRel(name string) (string, error) {
	return l.toolFileRel(name, "go.mod", ".mod")
}

// SumFileRel is the checksum file matching ModFileRel.
func (l Layout) SumFileRel(name string) (string, error) {
	return l.toolFileRel(name, "go.sum", ".sum")
}

func (l Layout) toolFileRel(name, goFile, splitExt string) (string, error) {
	dir := l.dirSlash()

	switch l.Strategy {
	case Unified:
		return path.Join(dir, goFile), nil
	case Split, Module:
		if err := ValidateName(name); err != nil {
			return "", err
		}

		if l.Strategy == Split {
			return path.Join(dir, name+splitExt), nil
		}

		return path.Join(dir, name, goFile), nil
	}

	return "", ierrors.New(ierrors.Environment, "Unknown strategy: %s", l.Strategy)
}

// ModFile is ModFileRel as a path under root.
func (l Layout) ModFile(root, name string) (string, error) {
	rel, err := l.ModFileRel(name)
	if err != nil {
		return "", err
	}

	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

// SumFile is SumFileRel as a path under root.
func (l Layout) SumFile(root, name string) (string, error) {
	rel, err := l.SumFileRel(name)
	if err != nil {
		return "", err
	}

	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

// ModulePath is the module path for a tool's own go.mod (name != "") or for
// the tools directory itself (name == ""), under prefix. The directory is
// cleaned, so "./tools" and "tools/" give the same path.
func (l Layout) ModulePath(prefix, name string) string {
	return path.Join(prefix, l.dirSlash(), name)
}

// Detection is what Detect found in a tools directory.
type Detection struct {
	// Strategy is the dominant layout ("" when the directory is absent or
	// holds no recognizable layout). Priority: unified, module, split.
	Strategy Strategy
	// Found lists every layout present, in priority order.
	Found []Strategy
}

// Hybrid reports whether more than one layout is present.
func (d Detection) Hybrid() bool { return len(d.Found) > 1 }

// Warning returns the hybrid-directory warning, without a trailing newline, or
// "" when the directory is not hybrid. dir is the display path and program the
// command name the warning points at.
func (d Detection) Warning(dir, program string) string {
	if !d.Hybrid() {
		return ""
	}

	desc := map[Strategy]string{
		Unified: "   - unified  (go.mod with tool directives)",
		Module:  "   - module   (subdirectories with go.mod)",
		Split:   "   - split    (flat .mod files)",
	}

	lines := []string{"⚠️  Hybrid tools directory detected in " + strings.TrimSuffix(dir, "/") + "/:"}
	for _, s := range d.Found {
		lines = append(lines, desc[s])
	}

	lines = append(lines, "   Run '"+program+" migrate <strategy>' to clean up.")

	return strings.Join(lines, "\n")
}

// Detect inspects dir, an on-disk path, and reports which layouts it holds.
// Dot-entries are ignored and symlinks are followed: detection is read-only.
func Detect(dir string) Detection {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Detection{}
	}

	unified := hasToolDirectives(filepath.Join(dir, "go.mod"))
	module, split := false, false

	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, ".") {
			continue
		}

		fi, err := os.Stat(filepath.Join(dir, n))
		if err != nil {
			continue
		}

		switch {
		case fi.IsDir():
			if isRegular(filepath.Join(dir, n, "go.mod")) {
				module = true
			}
		case fi.Mode().IsRegular() && n != "go.mod" && strings.HasSuffix(n, ".mod"):
			split = true
		}
	}

	var d Detection

	for _, c := range []struct {
		on bool
		s  Strategy
	}{{unified, Unified}, {module, Module}, {split, Split}} {
		if c.on {
			d.Found = append(d.Found, c.s)
		}
	}

	if len(d.Found) > 0 {
		d.Strategy = d.Found[0]
	}

	return d
}

// hasToolDirectives reports whether the go.mod at file declares any tool. An
// unparseable file falls back to a line scan, so a damaged unified go.mod is
// still recognized as unified.
func hasToolDirectives(file string) bool {
	data, err := os.ReadFile(file)
	if err != nil {
		return false
	}

	if m, err := (modparser.XParser{}).Parse(data); err == nil {
		return len(m.Tools) > 0
	}

	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(l, "tool ") || strings.HasPrefix(l, "tool\t") || strings.HasPrefix(l, "tool(") {
			return true
		}
	}

	return false
}

func isRegular(p string) bool {
	fi, err := os.Stat(p)

	return err == nil && fi.Mode().IsRegular()
}
