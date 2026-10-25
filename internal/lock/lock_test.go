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
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/lock"
)

func TestLockAcquireRelease(t *testing.T) {
	dir := t.TempDir()

	l, err := lock.NewFileLocker(dir).Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, lock.FileName)

	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("lock file: %v %v", fi, err)
	}

	if b, _ := os.ReadFile(path); strings.TrimSpace(string(b)) == "" {
		t.Fatal("holder pid not recorded")
	}

	l.Unlock()
	l.Unlock()

	var nilLock *lock.Lock
	nilLock.Unlock()

	l2, err := lock.NewFileLocker(dir).Lock(context.Background())
	if err != nil {
		t.Fatalf("reacquire: %v", err)
	}

	l2.Unlock()
}

func TestLockExcludesSecondHolder(t *testing.T) {
	dir := t.TempDir()

	l, err := lock.NewFileLocker(dir).Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	defer l.Unlock()

	start := time.Now()
	_, err = lock.NewFileLocker(dir, lock.WithTimeout(30*time.Millisecond)).Lock(context.Background())

	var held *lock.HeldError
	if !errors.Is(err, lock.ErrHeld) || !errors.As(err, &held) || held.PID != os.Getpid() {
		t.Fatalf("err = %v", err)
	}

	if !strings.Contains(err.Error(), " (pid "+strconv.Itoa(os.Getpid())+")") {
		t.Fatalf("message lacks the holder: %v", err)
	}

	if d := time.Since(start); d < 30*time.Millisecond || d > 5*time.Second {
		t.Fatalf("waited %s", d)
	}

	if _, err := lock.NewFileLocker(dir).Lock(context.Background()); !errors.Is(err, lock.ErrHeld) {
		t.Fatalf("zero timeout err = %v", err)
	}
}

func TestLockWaiterGetsLockOnRelease(t *testing.T) {
	dir := t.TempDir()

	l, err := lock.NewFileLocker(dir).Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	time.AfterFunc(30*time.Millisecond, l.Unlock)

	l2, err := lock.NewFileLocker(dir, lock.WithTimeout(5*time.Second)).Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	l2.Unlock()
}

func TestLockCancelAndDisabled(t *testing.T) {
	dir := t.TempDir()

	l, _ := lock.NewFileLocker(dir).Lock(context.Background())
	defer l.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)

	if _, err := lock.NewFileLocker(dir, lock.WithTimeout(time.Minute)).Lock(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}

	d, err := lock.NewFileLocker(dir, lock.WithDisabled(true)).Lock(context.Background())
	if err != nil {
		t.Fatalf("disabled lock must not block: %v", err)
	}

	d.Unlock()
}

func TestLockRefusesCancelledContext(t *testing.T) {
	dir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := lock.NewFileLocker(dir).Lock(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, lock.FileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled Lock created the lock file: %v", err)
	}
}

func TestLockUnavailable(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := lock.NewFileLocker(filepath.Join(file, "sub")).Lock(context.Background())

	var unavailable *lock.UnavailableError
	if !errors.Is(err, lock.ErrUnavailable) || !errors.As(err, &unavailable) {
		t.Fatalf("err = %v", err)
	}

	if errors.Is(err, lock.ErrHeld) {
		t.Fatal("an unavailable lock must not match ErrHeld")
	}
}

func TestHolder(t *testing.T) {
	dir := t.TempDir()
	lk := lock.NewFileLocker(dir)

	if h, err := lk.Holder(); err != nil || h != (lock.Holder{}) {
		t.Fatalf("missing file: %+v err=%v", h, err)
	}

	if _, err := os.Stat(filepath.Join(dir, lock.FileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Holder created the lock file: %v", err)
	}

	l, err := lk.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if h, err := lk.Holder(); err != nil || !h.Held || h.PID != os.Getpid() {
		t.Fatalf("held: %+v err=%v", h, err)
	} else if want := " (pid " + strconv.Itoa(os.Getpid()) + ")"; h.String() != want {
		t.Fatalf("String() = %q, want %q", h.String(), want)
	}

	l.Unlock()

	if h, err := lk.Holder(); err != nil || h != (lock.Holder{}) || h.String() != "" {
		t.Fatalf("released: %+v err=%v", h, err)
	}

	l, _ = lk.Lock(context.Background())
	defer l.Unlock()

	if h, _ := lock.NewFileLocker(dir, lock.WithDisabled(true)).Holder(); !h.Held {
		t.Fatal("Disabled hid a held lock")
	}
}

// A holder that dies without releasing frees the lock, with no stale-lock
// code involved.
func TestLockFreedWhenHolderProcessDies(t *testing.T) {
	if dir := os.Getenv("LOCK_TEST_HOLDER"); dir != "" {
		if _, err := lock.NewFileLocker(dir).Lock(context.Background()); err != nil {
			os.Exit(3)
		}

		_ = os.WriteFile(filepath.Join(dir, "ready"), nil, 0o600)

		time.Sleep(time.Minute)
		os.Exit(0)
	}

	dir := t.TempDir()

	cmd := exec.Command(os.Args[0], "-test.run=^TestLockFreedWhenHolderProcessDies$")
	cmd.Env = append(os.Environ(), "LOCK_TEST_HOLDER="+dir)

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	for i := 0; ; i++ {
		if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
			break
		}

		if i > 500 {
			t.Fatal("holder never took the lock")
		}

		time.Sleep(10 * time.Millisecond)
	}

	if _, err := lock.NewFileLocker(dir).Lock(context.Background()); !errors.Is(err, lock.ErrHeld) {
		t.Fatalf("live holder must block, err = %v", err)
	}

	if h, _ := lock.NewFileLocker(dir).Holder(); !h.Held || h.PID != cmd.Process.Pid {
		t.Fatalf("Holder: %+v, want pid %d", h, cmd.Process.Pid)
	}

	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	l, err := lock.NewFileLocker(dir, lock.WithTimeout(5*time.Second)).Lock(context.Background())
	if err != nil {
		t.Fatalf("lock not freed after holder died: %v", err)
	}

	l.Unlock()
}

func TestNewReleasesOnce(t *testing.T) {
	n := 0
	l := lock.New(func() { n++ })

	l.Unlock()
	l.Unlock()

	if n != 1 {
		t.Fatalf("release ran %d times", n)
	}
}

type fakeLocker struct {
	err     error
	locks   int
	unlocks int
}

func (f *fakeLocker) Lock(context.Context) (*lock.Lock, error) {
	f.locks++

	if f.err != nil {
		return nil, f.err
	}

	return lock.New(func() { f.unlocks++ }), nil
}

func (f *fakeLocker) Holder() (lock.Holder, error) { return lock.Holder{}, nil }

func TestWithIsReentrant(t *testing.T) {
	dir := t.TempDir()
	lk := lock.NewFileLocker(dir)
	ran := false

	err := lock.With(context.Background(), lk, dir, func(ctx context.Context) error {
		// The same directory spelled differently, through another locker.
		other := lock.NewFileLocker(filepath.Join(dir, "..", filepath.Base(dir)))

		if err := lock.With(ctx, other, filepath.Join(dir, "."), func(context.Context) error { ran = true; return nil }); err != nil {
			return err
		}

		if h, _ := lk.Holder(); !h.Held {
			t.Error("the nested With released the outer lock")
		}

		return nil
	})
	if err != nil {
		t.Fatalf("nested With: %v", err)
	}

	if !ran {
		t.Fatal("nested fn did not run")
	}

	if h, _ := lk.Holder(); h.Held {
		t.Fatal("lock still held after With returned")
	}
}

func TestWithExcludesAnUnrelatedContext(t *testing.T) {
	dir := t.TempDir()
	lk := lock.NewFileLocker(dir)

	err := lock.With(context.Background(), lk, dir, func(context.Context) error {
		return lock.With(context.Background(), lk, dir, func(context.Context) error { return nil })
	})
	if !errors.Is(err, lock.ErrHeld) {
		t.Fatalf("err = %v", err)
	}
}

func TestWithTakesADifferentDirectoryAgain(t *testing.T) {
	a, b := &fakeLocker{}, &fakeLocker{}

	err := lock.With(context.Background(), a, "a", func(ctx context.Context) error {
		return lock.With(ctx, b, "b", func(context.Context) error { return nil })
	})
	if err != nil {
		t.Fatal(err)
	}

	if a.locks != 1 || b.locks != 1 {
		t.Fatalf("locks a=%d b=%d", a.locks, b.locks)
	}
}

func TestWithUsesTheInjectedLocker(t *testing.T) {
	fake := &fakeLocker{}

	err := lock.With(context.Background(), fake, "d", func(ctx context.Context) error {
		if fake.locks != 1 || fake.unlocks != 0 {
			t.Errorf("inside: locks=%d unlocks=%d", fake.locks, fake.unlocks)
		}

		return lock.With(ctx, fake, "d", func(context.Context) error { return nil })
	})
	if err != nil {
		t.Fatal(err)
	}

	if fake.locks != 1 || fake.unlocks != 1 {
		t.Fatalf("after: locks=%d unlocks=%d", fake.locks, fake.unlocks)
	}
}

func TestWithReleasesOnPanic(t *testing.T) {
	fake := &fakeLocker{}

	func() {
		defer func() { _ = recover() }()

		_ = lock.With(context.Background(), fake, "d", func(context.Context) error { panic("boom") })
	}()

	if fake.unlocks != 1 {
		t.Fatalf("unlocks = %d", fake.unlocks)
	}
}

func TestWithStopsWhenTheLockerFails(t *testing.T) {
	busy := errors.New("busy")
	ran := false

	err := lock.With(context.Background(), &fakeLocker{err: busy}, "d", func(context.Context) error { ran = true; return nil })
	if !errors.Is(err, busy) || ran {
		t.Fatalf("err=%v ran=%v", err, ran)
	}
}

func TestLockErrorsCarryExitCodes(t *testing.T) {
	dir := t.TempDir()

	l, _ := lock.NewFileLocker(dir).Lock(context.Background())
	defer l.Unlock()

	_, err := lock.NewFileLocker(dir).Lock(context.Background())
	if ierrors.CodeOf(err) != ierrors.Lock || !strings.Contains(err.Error(), "Another gotools process is running (pid ") {
		t.Fatalf("held: code=%d err=%v", ierrors.CodeOf(err), err)
	}

	file := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(file, nil, 0o600)

	_, err = lock.NewFileLocker(filepath.Join(file, "sub")).Lock(context.Background())
	if ierrors.CodeOf(err) != ierrors.Environment {
		t.Fatalf("unavailable: code=%d err=%v", ierrors.CodeOf(err), err)
	}
}
