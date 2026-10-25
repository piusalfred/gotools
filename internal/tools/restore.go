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
	"io"
	"sync"

	"github.com/piusalfred/gotools/internal/manifest"
	"github.com/piusalfred/gotools/internal/strategy"
)

// RestoreOptions configures Restore.
type RestoreOptions struct {
	// Jobs is how many tools install at once (split and module only). Values
	// below 2 install one after the other.
	Jobs int
}

// Upgrade reinstalls from at @latest and records the result, quietly: the
// caller reports in its own words. The returned tool carries the resolved
// version, which equals from.Version when it was already the latest.
func (m *GoManager) Upgrade(ctx context.Context, from manifest.Tool) (manifest.Tool, error) {
	return m.Add(ctx, AddOptions{Name: from.Name, Pkg: from.Package + "@latest", Quiet: true})
}

func pkgAt(t manifest.Tool) string { return t.Package + "@" + t.Version }

// Restore reinstalls work serially, or concurrently for split and module,
// where each tool has its own modfile. Unified shares one go.mod, so it stays
// serial.
//
// A parallel run is all-or-nothing for the manifest: it is merged and written
// once, only if every job succeeded. Each job rolls back its own files on
// failure; jobs that finished before another failed keep their modfiles, and
// the next sync sees them as drift and reinstalls.
func (m *GoManager) Restore(ctx context.Context, work []manifest.Tool, o RestoreOptions) error {
	cfg, jobs := m.cfg, o.Jobs

	if len(work) == 0 {
		return nil
	}

	if cfg.Layout.Strategy == strategy.Unified || jobs <= 1 || len(work) == 1 {
		if cfg.Layout.Strategy == strategy.Unified && jobs > 1 {
			_, _ = fmt.Fprintln(cfg.Out, "  ⚠ unified strategy: installs are serial (shared go.mod).")
		}

		for _, t := range work {
			if _, err := m.Add(ctx, AddOptions{Name: t.Name, Pkg: pkgAt(t)}); err != nil {
				return err
			}
		}

		return nil
	}

	return m.restoreParallel(ctx, work, jobs)
}

func (m *GoManager) restoreParallel(ctx context.Context, work []manifest.Tool, jobs int) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Each job writes through serialized streams.
	cfg := m.cfg
	cfg.Out = &lockedWriter{w: cfg.Out}
	cfg.Err = &lockedWriter{w: cfg.Err}
	worker := &GoManager{cfg: cfg}

	var (
		wg      sync.WaitGroup
		sem     = make(chan struct{}, jobs)
		results = make([]manifest.Tool, len(work))
		errs    = make([]error, len(work))
	)

	for i, t := range work {
		sem <- struct{}{}

		wg.Go(func() {
			defer func() { <-sem }()

			if ctx.Err() != nil {
				errs[i] = ctx.Err()

				return
			}

			results[i], errs[i] = worker.Add(ctx, AddOptions{Name: t.Name, Pkg: pkgAt(t), NoFlush: true})
			if errs[i] != nil {
				cancel()
			}
		})
	}

	wg.Wait()

	// Report the first real failure, not the cancellations it caused.
	for _, err := range errs {
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}

	for _, err := range errs {
		if err != nil {
			return err
		}
	}

	mf := m.manifest()
	for _, t := range results {
		mf.SetTool(t)
	}

	return m.flush()
}

// lockedWriter serializes writes from concurrent install jobs.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.w.Write(b)
}
