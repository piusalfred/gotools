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

// Package lock excludes other processes from a directory with an flock(2)
// advisory lock.
//
// The kernel drops the lock when its holder exits or crashes, so there is no
// stale-lock detection. [Locker] is the seam: [FileLocker] is the flock
// implementation, and tests substitute their own. [With] runs a function under
// a Locker and is re-entrant through its context.
package lock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	ierrors "github.com/piusalfred/gotools/internal/errors"
)

// FileName is the lock file inside the locked directory. It is never deleted:
// removing it races with a waiting process.
const FileName = ".gotools.flock"

const poll = 100 * time.Millisecond

// ErrHeld matches the error Lock returns when another holder is still there at
// the deadline. The error is a Lock error from package errors.
var ErrHeld = errors.New("lock held by another process")

// ErrUnavailable matches the error Lock returns when the lock cannot be set
// up: the directory or file cannot be created, or locking fails. The error is
// an Environment error from package errors.
var ErrUnavailable = errors.New("lock unavailable")

// HeldError is the error behind ErrHeld.
type HeldError struct {
	Path string // the lock file
	PID  int    // the recorded holder; 0 when unknown
}

func (e *HeldError) Error() string {
	return "Another gotools process is running" + Holder{PID: e.PID}.String() + ". Try again later."
}

func (e *HeldError) Is(target error) bool { return target == ErrHeld }

// UnavailableError is the error behind ErrUnavailable.
type UnavailableError struct {
	Path string // the directory or file that failed
	Op   string // what was attempted
	Err  error
}

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("Could not %s %s: %v", e.Op, e.Path, e.Err)
}

func (e *UnavailableError) Unwrap() error { return e.Err }

func (e *UnavailableError) Is(target error) bool { return target == ErrUnavailable }

func unavailable(e *UnavailableError) error {
	return ierrors.Wrap(ierrors.Environment, "", e)
}

// Locker is the lock on one directory. It excludes other processes, not other
// goroutines of this one.
type Locker interface {
	// Lock takes the lock, waiting for another holder as the implementation
	// is configured to. It returns an error matching ErrHeld when a holder
	// was still there at the deadline, ErrUnavailable when the lock could not
	// be set up, and ctx.Err() when ctx is done.
	Lock(ctx context.Context) (*Lock, error)
	// Holder reports whether another process holds the lock, without taking
	// it or creating anything.
	Holder() (Holder, error)
}

// Holder describes who holds a directory's lock.
type Holder struct {
	Held bool // another process holds the lock right now
	PID  int  // the recorded holder; 0 when nobody holds it or it is unknown
}

// String formats the holder for messages, e.g. " (pid 123)", or "" when the
// pid is unknown.
func (h Holder) String() string {
	if h.PID == 0 {
		return ""
	}

	return " (pid " + strconv.Itoa(h.PID) + ")"
}

// Lock is a held lock. The zero value and a nil *Lock unlock as no-ops.
type Lock struct {
	release func()
}

// New returns a Lock that calls release once, on the first Unlock. It is for
// Locker implementations; release must drop whatever Lock took.
func New(release func()) *Lock {
	return &Lock{release: release}
}

// Unlock drops the lock. It is safe to call more than once.
func (l *Lock) Unlock() {
	if l == nil || l.release == nil {
		return
	}

	release := l.release
	l.release = nil

	release()
}

// FileLocker is the Locker that holds an flock(2) on a file in a directory.
// Build it with NewFileLocker; the fields are private so that settings can be
// added as options without breaking callers.
type FileLocker struct {
	dir      string
	timeout  time.Duration
	disabled bool
}

var _ Locker = FileLocker{}

// Option configures a FileLocker.
type Option func(*FileLocker)

// WithTimeout sets how long Lock waits for another holder. The default, 0,
// tries exactly once.
func WithTimeout(d time.Duration) Option {
	return func(l *FileLocker) { l.timeout = d }
}

// WithDisabled makes Lock succeed without locking.
func WithDisabled(disabled bool) Option {
	return func(l *FileLocker) { l.disabled = disabled }
}

// NewFileLocker returns the lock for the directory dir. It touches nothing on
// disk until Lock.
func NewFileLocker(dir string, opts ...Option) FileLocker {
	l := FileLocker{dir: dir}

	for _, opt := range opts {
		opt(&l)
	}

	return l
}

// Lock implements Locker. It waits up to the configured timeout, in real time,
// for another holder. The holder's pid is written into the file for messages
// only; correctness never depends on it.
func (l FileLocker) Lock(ctx context.Context) (*Lock, error) {
	if l.disabled {
		return &Lock{}, nil
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return nil, unavailable(&UnavailableError{Path: l.dir, Op: "create", Err: err})
	}

	path := filepath.Join(l.dir, FileName)

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, unavailable(&UnavailableError{Path: path, Op: "open lock", Err: err})
	}

	deadline := time.Now().Add(l.timeout)

	for {
		ok, err := tryLock(f)
		if err != nil {
			_ = f.Close()

			return nil, unavailable(&UnavailableError{Path: path, Op: "take lock", Err: err})
		}

		if ok {
			break
		}

		wait := time.Until(deadline)
		if wait <= 0 {
			_ = f.Close()

			return nil, ierrors.Wrap(ierrors.Lock, "", &HeldError{Path: path, PID: readHolder(path)})
		}

		select {
		case <-ctx.Done():
			_ = f.Close()

			return nil, ctx.Err()
		case <-time.After(min(wait, poll)):
		}
	}

	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)

	return New(func() {
		unlock(f)
		_ = f.Close()
	}), nil
}

// Holder implements Locker. A missing lock file means nobody holds the lock,
// and nothing is created. The check takes the lock for an instant and gives it
// back. It ignores the disabled setting: it describes the directory, not this
// process's settings.
func (l FileLocker) Holder() (Holder, error) {
	path := filepath.Join(l.dir, FileName)

	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Holder{}, nil
	}

	if err != nil {
		return Holder{}, fmt.Errorf("inspect lock: %w", err)
	}

	defer f.Close()

	ok, err := tryLock(f)
	if err != nil {
		return Holder{}, err
	}

	if ok {
		unlock(f)

		return Holder{}, nil
	}

	return Holder{Held: true, PID: readHolder(path)}, nil
}

func readHolder(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid < 0 {
		return 0
	}

	return pid
}
