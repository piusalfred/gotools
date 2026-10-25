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

package lock_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/piusalfred/gotools/internal/lock"
)

// With holds the lock for the duration of fn. The ctx it passes to fn records
// the held directory, so a function fn calls can take the same lock again
// without waiting on itself.
func ExampleWith() {
	dir, _ := os.MkdirTemp("", "lock-example")
	defer os.RemoveAll(dir)

	locker := lock.NewFileLocker(dir, lock.WithTimeout(time.Second))

	err := lock.With(context.Background(), locker, dir, func(ctx context.Context) error {
		fmt.Println("holding the lock")

		// Pass ctx down: this nested call runs directly instead of waiting.
		return lock.With(ctx, locker, dir, func(context.Context) error {
			fmt.Println("nested call runs without waiting")

			return nil
		})
	})
	fmt.Println("error:", err)

	held, _ := locker.Holder()
	fmt.Println("held after return:", held.Held)

	// Output:
	// holding the lock
	// nested call runs without waiting
	// error: <nil>
	// held after return: false
}

// A caller that starts from a fresh context is another holder: while the lock
// is held, it gets an error matching ErrHeld once its timeout passes.
func ExampleWith_busy() {
	dir, _ := os.MkdirTemp("", "lock-example")
	defer os.RemoveAll(dir)

	locker := lock.NewFileLocker(dir)

	_ = lock.With(context.Background(), locker, dir, func(context.Context) error {
		err := lock.With(context.Background(), locker, dir, func(context.Context) error {
			return nil
		})
		fmt.Println("busy:", errors.Is(err, lock.ErrHeld))

		return nil
	})

	// Output:
	// busy: true
}
