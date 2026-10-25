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

package lock

import (
	"context"
	"path/filepath"
)

// With runs fn while holding l, releasing it on return or panic. dir names the
// locked directory; it keys re-entrancy.
//
// It is re-entrant through ctx: fn's ctx records dir, and a With on that ctx
// for the same directory runs fn directly. Without that, a nested call would
// open a second file description, which flock treats as another holder, and
// wait on itself. Code that drops the ctx is another holder.
//
//	locker := lock.NewFileLocker(dir, lock.WithTimeout(10*time.Second))
//
//	err := lock.With(ctx, locker, dir, func(ctx context.Context) error {
//		// Work on dir. Pass ctx on: a nested lock.With for dir runs directly.
//		return sync(ctx)
//	})
//	if errors.Is(err, lock.ErrHeld) {
//		// another process is still running
//	}
func With(ctx context.Context, l Locker, dir string, fn func(context.Context) error) error {
	key := keyOf(dir)
	if holds(ctx, key) {
		return fn(ctx)
	}

	h, err := l.Lock(ctx)
	if err != nil {
		return err
	}

	defer h.Unlock()

	return fn(context.WithValue(ctx, heldKey{}, &held{dir: key, outer: heldIn(ctx)}))
}

// held is the chain of directories this call stack holds locked.
type held struct {
	dir   string
	outer *held
}

type heldKey struct{}

func heldIn(ctx context.Context) *held {
	h, _ := ctx.Value(heldKey{}).(*held)

	return h
}

func holds(ctx context.Context, dir string) bool {
	for h := heldIn(ctx); h != nil; h = h.outer {
		if h.dir == dir {
			return true
		}
	}

	return false
}

// keyOf names a directory independently of how its path was spelled.
func keyOf(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}

	return filepath.Clean(dir)
}
