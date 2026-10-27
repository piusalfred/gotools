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

// Package fsx holds the file operations gotools needs to keep a project safe:
// writes that are never half done, and a refusal to follow symlinks.
package fsx

import (
	"os"
	"path/filepath"

	ierrors "github.com/piusalfred/gotools/internal/errors"
)

// WriteFileAtomic writes data to a temp file beside path and renames it over
// path, so a crash never leaves a half-written file. Existing contents and
// permissions are replaced, and perm applies to the result. The temp file is
// created exclusively, so a symlink planted at its name cannot redirect the
// write.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp.*")
	if err != nil {
		return ierrors.Wrap(ierrors.Generic, "create temporary file", err)
	}

	name := tmp.Name()

	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(perm)
	}

	if cerr := tmp.Close(); err == nil {
		err = cerr
	}

	if err == nil {
		err = os.Rename(name, path)
	}

	if err != nil {
		_ = os.Remove(name)

		return ierrors.Wrap(ierrors.Generic, "write file atomically", err)
	}

	return nil
}

// RejectSymlink returns an Environment error when path is a symlink. A path
// that does not exist, or cannot be inspected, passes. context names the
// operation in the message and may be empty.
func RejectSymlink(path, context string) error {
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		//nolint:nilerr // a path that is missing or cannot be inspected is not a symlink to refuse
		return nil
	}

	if context != "" {
		context = " (" + context + ")"
	}

	return ierrors.New(
		ierrors.Environment,
		"Refusing to follow symlink: %s%s\n   Remove the symlink and retry.",
		path,
		context,
	)
}

// Lexists reports whether path exists, without following a symlink: a
// dangling symlink exists.
func Lexists(path string) bool {
	_, err := os.Lstat(path)

	return err == nil
}
