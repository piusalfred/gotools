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
	"fmt"
	"os"
	"path/filepath"
	"slices"

	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/fingerprint"
	"github.com/piusalfred/gotools/internal/fsx"
	"github.com/piusalfred/gotools/internal/manifest"
	"github.com/piusalfred/gotools/internal/strategy"
)

// RenameOptions configures Rename.
type RenameOptions struct {
	// DryRun runs every precondition check and reports the rename without
	// touching disk.
	DryRun bool
}

// Rename validates and performs one rename. The caller holds the lock.
func (m *GoManager) Rename(ctx context.Context, oldName, newName string, o RenameOptions) error {
	cfg := m.cfg

	plan, err := m.planRename(oldName, newName)
	if err != nil {
		return err
	}

	if o.DryRun {
		_, _ = fmt.Fprintf(cfg.Out, "🔍 [dry-run] Would rename %s → %s\n", oldName, newName)

		return nil
	}

	_, _ = fmt.Fprintf(cfg.Out, "✏️  Renaming %s → %s...\n", oldName, newName)

	if err := m.apply(ctx, plan); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cfg.Out, "✅ Renamed %s → %s\n", oldName, newName)

	return nil
}

// renamePlan is a validated rename, ready to apply.
type renamePlan struct {
	tool, newTool manifest.Tool
	// from and to are the files or directory to move; empty when the modfile
	// is missing and only the manifest changes.
	from, to []string
	modfile  string // path of the moved modfile (new location)
	warn     string
}

// planRename runs every precondition check without touching disk.
func (m *GoManager) planRename(oldName, newName string) (*renamePlan, error) {
	cfg := m.cfg

	if oldName == newName {
		return nil, ierrors.New(ierrors.Usage, "Rename: the names are identical.")
	}

	mf := m.manifest()

	old, ok := mf.Tool(oldName)
	if !ok {
		return nil, ierrors.New(ierrors.ToolNotFound, "Tool '%s' not found in manifest.", oldName)
	}

	if _, exists := mf.Tool(newName); exists {
		return nil, ierrors.New(ierrors.Usage, "Tool '%s' already exists in manifest. Remove it first.", newName)
	}

	plan := &renamePlan{tool: old, newTool: old}
	plan.newTool.Name = newName
	l := cfg.Layout
	toolsDir := l.ToolsDir(cfg.Root)
	dirRel := filepath.ToSlash(l.Dir)

	switch l.Strategy {
	case strategy.Split:
		modOld, err := l.ModFile(cfg.Root, oldName)
		if err != nil {
			return nil, err
		}

		modNew, err := l.ModFile(cfg.Root, newName)
		if err != nil {
			return nil, err
		}

		sumOld, _ := l.SumFile(cfg.Root, oldName)
		sumNew, _ := l.SumFile(cfg.Root, newName)

		for _, f := range []string{modOld, sumOld} {
			if err := fsx.RejectSymlink(f, "rename "+oldName); err != nil {
				return nil, err
			}
		}

		// Lstat, so a dangling symlink at the target name is seen.
		if fsx.Lexists(modNew) || fsx.Lexists(sumNew) {
			return nil, ierrors.New(ierrors.Environment,
				"Refusing: %s/%s.mod (or .sum) already exists but is not in the manifest.", dirRel, newName)
		}

		if !fsx.Lexists(modOld) {
			plan.warn = fmt.Sprintf("%s/%s.mod not found — renaming in the manifest only (sync will recreate it).", dirRel, oldName)

			return plan, nil
		}

		plan.from, plan.to, plan.modfile = []string{modOld}, []string{modNew}, modNew
		if fsx.Lexists(sumOld) {
			plan.from, plan.to = append(plan.from, sumOld), append(plan.to, sumNew)
		}
	case strategy.Module:
		if err := strategy.ValidateName(oldName); err != nil {
			return nil, err
		}

		if err := strategy.ValidateName(newName); err != nil {
			return nil, err
		}

		dOld, dNew := filepath.Join(toolsDir, oldName), filepath.Join(toolsDir, newName)

		for _, f := range []string{dOld, filepath.Join(dOld, "go.mod")} {
			if err := fsx.RejectSymlink(f, "rename "+oldName); err != nil {
				return nil, err
			}
		}

		if fsx.Lexists(dNew) {
			return nil, ierrors.New(ierrors.Environment, "Refusing: %s/%s already exists but is not in the manifest.", dirRel, newName)
		}

		if !fsx.Lexists(filepath.Join(dOld, "go.mod")) {
			plan.warn = fmt.Sprintf("%s/%s/go.mod not found — renaming in the manifest only (sync will recreate it).", dirRel, oldName)

			return plan, nil
		}

		plan.from, plan.to, plan.modfile = []string{dOld}, []string{dNew}, filepath.Join(dNew, "go.mod")
	case strategy.Unified:
		// Tool names there come from the go.mod directives; a manifest-only
		// rename would desync list, info and exec.
		return nil, ierrors.New(ierrors.Environment,
			"Rename is not supported with the unified strategy.\n"+
				"   Tool names there come from the go.mod tool directives, not the manifest.\n"+
				"   Migrate first:   %[1]s migrate split    (or module)\n"+
				"   Or reinstall:    %[1]s remove %[2]s && %[1]s install %[3]s %[4]s@%[5]s",
			cfg.Program, oldName, newName, old.Package, old.Version)
	default:
		return nil, ierrors.New(ierrors.Environment, "Unknown strategy: %s", l.Strategy)
	}

	return plan, nil
}

// apply performs the rename and undoes every step if a later one fails, so a
// failed rename loses nothing: not the moved files, not the module line, and
// not the manifest.
func (m *GoManager) apply(ctx context.Context, r *renamePlan) (err error) {
	cfg := m.cfg

	var undo []func()

	defer func() {
		if err != nil {
			for _, u := range slices.Backward(undo) {
				u()
			}
		}
	}()

	if r.warn != "" {
		_, _ = fmt.Fprintf(cfg.Out, "⚠️  %s\n", r.warn)
	}

	for i := range r.from {
		from, to := r.from[i], r.to[i]

		if err := os.Rename(from, to); err != nil {
			return ierrors.New(ierrors.Generic, "Could not move %s: %v", from, err)
		}

		undo = append(undo, func() { _ = os.Rename(to, from) })
	}

	if r.modfile != "" {
		orig, err := m.rewriteModule(r)
		if err != nil {
			return err
		}

		undo = append(undo, func() { _ = fsx.WriteFileAtomic(r.modfile, orig, 0o644) })
	}

	mf := m.manifest()

	mf.SetTool(r.newTool)
	mf.RemoveTool(r.tool.Name)

	undo = append(undo, func() {
		mf.SetTool(r.tool)
		mf.RemoveTool(r.newTool.Name)
	})

	if err := m.flush(); err != nil {
		return err
	}

	// Keep the sync fingerprint current: disk now matches the manifest, so the
	// next sync, including an offline one in CI, takes the fast path. It is
	// skipped when the modfile was missing: they disagree there, and a stale
	// fingerprint makes sync reinstall it correctly.
	if r.modfile != "" && cfg.Recorder != nil {
		if err := cfg.Recorder.Record(ctx); err != nil {
			_, _ = fmt.Fprintf(cfg.Err, "  ⚠️  Could not update %s: %v\n", fingerprint.FileName, err)
		}
	}

	return nil
}

// rewriteModule updates the module line, which embeds the tool name, and
// returns the original contents for rollback.
func (m *GoManager) rewriteModule(r *renamePlan) ([]byte, error) {
	cfg := m.cfg

	data, err := os.ReadFile(r.modfile)
	if err != nil {
		return nil, ierrors.New(ierrors.Generic, "Could not read %s: %v", r.modfile, err)
	}

	out, err := m.setModule(data, cfg.Layout.ModulePath(cfg.Prefix, r.newTool.Name))
	if err != nil {
		return nil, ierrors.New(ierrors.Generic, "Could not rewrite the module line in %s: %v", r.modfile, err)
	}

	if err := fsx.WriteFileAtomic(r.modfile, out, 0o644); err != nil {
		return nil, ierrors.New(ierrors.Generic, "Could not write %s: %v", r.modfile, err)
	}

	return data, nil
}

// setModule returns data with its module line set to modPath.
func (m *GoManager) setModule(data []byte, modPath string) ([]byte, error) {
	mod, err := m.cfg.ModFiles.Parse(data)
	if err != nil {
		return nil, err
	}

	if err := mod.SetModule(modPath); err != nil {
		return nil, err
	}

	return mod.Format()
}
