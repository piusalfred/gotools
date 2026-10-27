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

package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/fsx"
	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/modparser"
	"github.com/piusalfred/gotools/internal/strategy"
)

// RemoveResult says what Remove found for a name.
type RemoveResult struct {
	OnDisk     bool // files on disk served the tool, and were removed
	InManifest bool // the manifest listed the tool, and no longer does
}

// Remove deletes the tool's files for the project's strategy and drops it from
// the manifest, writing the manifest before it returns. A name that is neither
// on disk nor in the manifest is not an error here: the result says so and the
// caller decides. The manifest is written per tool, so a failure on tool N
// leaves tools 1..N-1 removed from both the disk and the manifest.
func (m *GoManager) Remove(ctx context.Context, name string) (RemoveResult, error) {
	onDisk, err := m.removeFromDisk(ctx, name)
	if err != nil {
		return RemoveResult{OnDisk: onDisk}, err
	}

	res := RemoveResult{OnDisk: onDisk, InManifest: m.manifest().RemoveTool(name)}

	if res.InManifest {
		if err := m.flush(); err != nil {
			return res, err
		}
	}

	return res, nil
}

// removeFromDisk deletes the tool's files per strategy and reports whether
// anything on disk served the tool.
func (m *GoManager) removeFromDisk(ctx context.Context, name string) (bool, error) {
	l := m.cfg.Layout

	switch l.Strategy {
	case strategy.Unified:
		return m.removeUnified(ctx, name, l.ToolsDir(m.cfg.Root))
	case strategy.Split:
		return m.removeSplit(name)
	case strategy.Module:
		return m.removeModule(name)
	}

	return false, ierrors.New(ierrors.Environment, "Unknown strategy: %s", l.Strategy)
}

// removeSplit deletes the tool's modfile and sum.
func (m *GoManager) removeSplit(name string) (bool, error) {
	cfg := m.cfg

	mod, err := cfg.Layout.ModFile(cfg.Root, name)
	if err != nil {
		return false, err
	}

	sum, err := cfg.Layout.SumFile(cfg.Root, name)
	if err != nil {
		return false, err
	}

	if !fsx.Lexists(mod) {
		_, _ = fmt.Fprintf(cfg.Out, "  ⚠️  %s.mod not found.\n", name)

		return false, nil
	}

	files := []string{mod, sum}

	for _, f := range files {
		if err = fsx.RejectSymlink(f, "remove "+name); err != nil {
			return false, err
		}
	}

	for _, f := range files {
		if err = os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, ierrors.New(ierrors.Generic, "Could not remove %s: %v", f, err)
		}
	}

	_, _ = fmt.Fprintf(cfg.Out, "  ✅ Removed %s.mod / %s.sum\n", name, name)

	return true, nil
}

// removeModule deletes the tool's module directory.
func (m *GoManager) removeModule(name string) (bool, error) {
	cfg := m.cfg

	if err := strategy.ValidateName(name); err != nil {
		return false, err
	}

	d := filepath.Join(cfg.Layout.ToolsDir(cfg.Root), name)
	rel := filepath.ToSlash(filepath.Join(cfg.Layout.Dir, name))

	if !fsx.Lexists(d) {
		_, _ = fmt.Fprintf(cfg.Out, "  ⚠️  %s/ not found.\n", rel)

		return false, nil
	}

	if err := fsx.RejectSymlink(d, "remove "+name); err != nil {
		return false, err
	}

	if err := os.RemoveAll(d); err != nil {
		return false, ierrors.New(ierrors.Generic, "Could not remove %s: %v", d, err)
	}

	_, _ = fmt.Fprintf(cfg.Out, "  ✅ Removed %s/\n", rel)

	return true, nil
}

// removeUnified drops the tool's directive from the shared go.mod and tidies
// it.
func (m *GoManager) removeUnified(ctx context.Context, name, toolsDir string) (bool, error) {
	cfg := m.cfg

	rel, err := cfg.Layout.ModFileRel(name)
	if err != nil {
		return false, err
	}

	modfile := filepath.Join(cfg.Root, filepath.FromSlash(rel))

	mod, err := cfg.ModFiles.Read(modfile)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	}

	if err = fsx.RejectSymlink(modfile, "remove "+name); err != nil {
		return false, err
	}

	pkg := m.unifiedPackage(name, mod.Tools)
	if pkg == "" {
		_, _ = fmt.Fprintf(cfg.Out, "  ⚠️  Tool %s not found in %s\n", name, rel)

		return false, nil
	}

	res, err := cfg.Go.Run(ctx, gocmd.Call{Dir: toolsDir, Args: []string{"mod", "edit", "-droptool=" + pkg}})
	if err != nil || res.Code != 0 {
		return false, goFailure("go mod edit -droptool", res, err)
	}

	res, err = cfg.Network.Run(ctx, gocmd.Call{Dir: toolsDir, Args: []string{"mod", "tidy"}})
	if err != nil || res.Code != 0 {
		return false, goFailure("go mod tidy in "+filepath.ToSlash(cfg.Layout.Dir), res, err)
	}

	_, _ = fmt.Fprintf(cfg.Out, "  ✅ Dropped %s from %s\n", name, rel)

	return true, nil
}

// unifiedPackage is the directive in the shared go.mod that serves name: the
// manifest entry's package when the go.mod declares it, and without a manifest
// entry the directive whose inferred binary name is name.
func (m *GoManager) unifiedPackage(name string, directives []string) string {
	if t, ok := m.manifest().Tool(name); ok {
		if slices.Contains(directives, t.Package) {
			return t.Package
		}

		return ""
	}

	for _, pkg := range directives {
		if modparser.InferBinaryName(pkg) == name {
			return pkg
		}
	}

	return ""
}
