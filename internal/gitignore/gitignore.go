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

// Package gitignore finds the git repository a directory is in and says
// whether a file there is ignored, by reading the ignore files. It never runs
// git, so it is an approximation: it understands literal and basename patterns
// and negation, not every glob, and not the user's global excludes file.
package gitignore

import (
	"bufio"
	"bytes"
	"os"
	"path"
	"path/filepath"
	"strings"

	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/fsx"
)

// File is the ignore file's name.
const File = ".gitignore"

// FindRepo returns the root of the git repository dir is in: the nearest
// directory above it (or itself) that holds .git. A .git file counts, as in a
// worktree or a submodule.
func FindRepo(dir string) (string, bool) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}

	for {
		if fsx.Lexists(filepath.Join(dir, ".git")) {
			return dir, true
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}

		dir = parent
	}
}

// Ignored reports whether the file called name in dir is ignored by the
// repository's ignore files: .git/info/exclude, then the .gitignore of every
// directory from the repository root down to dir. The closest rule that
// matches wins, and a "!" rule un-ignores. dir must be inside repo.
func Ignored(repo, dir, name string) bool {
	repo, dir = filepath.Clean(repo), filepath.Clean(dir)

	sources := []source{{file: filepath.Join(repo, ".git", "info", "exclude"), base: repo}}

	rel, err := filepath.Rel(repo, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return false
	}

	cur := repo
	sources = append(sources, source{file: filepath.Join(cur, File), base: cur})

	if rel != "." {
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			cur = filepath.Join(cur, part)
			sources = append(sources, source{file: filepath.Join(cur, File), base: cur})
		}
	}

	ignored := false

	for _, s := range sources {
		data, err := os.ReadFile(s.file)
		if err != nil {
			continue
		}

		below, err := filepath.Rel(s.base, dir)
		if err != nil {
			continue
		}

		sc := bufio.NewScanner(bytes.NewReader(data))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}

			negate := strings.HasPrefix(line, "!")
			line = strings.TrimPrefix(line, "!")

			if matches(line, filepath.ToSlash(below), name) {
				ignored = !negate
			}
		}
	}

	return ignored
}

type source struct{ file, base string }

// matches reports whether one ignore pattern covers name, which sits in the
// directory below (relative to the ignore file, "." for the same directory).
func matches(pattern, below, name string) bool {
	pattern = strings.TrimSuffix(pattern, "/") // a directory-only pattern never names a file

	if !strings.Contains(pattern, "/") {
		ok, _ := path.Match(pattern, name)

		return ok
	}

	pattern = strings.TrimPrefix(pattern, "**/")
	if !strings.Contains(pattern, "/") {
		ok, _ := path.Match(pattern, name)

		return ok
	}

	target := name
	if below != "." {
		target = below + "/" + name
	}

	ok, _ := path.Match(strings.TrimPrefix(pattern, "/"), target)

	return ok
}

// Entry is a line to keep in an ignore file.
type Entry struct {
	Pattern string // the line itself
	Comment string // why it is there; written above the line when it is added
}

// Has reports whether the file at p already holds the entry's pattern as a
// line of its own. A missing file does not.
func Has(p string, e Entry) (bool, error) {
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return false, nil
	}

	if err != nil {
		return false, ierrors.Wrap(ierrors.Generic, "read "+p, err)
	}

	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) == e.Pattern {
			return true, nil
		}
	}

	return false, nil
}

// Add appends the entry to the file at p, creating it when missing, and
// reports whether it wrote anything. The write is atomic and never follows a
// symlink. An entry the file already holds is left alone.
func Add(p string, e Entry) (bool, error) {
	if err := fsx.RejectSymlink(p, "gitignore"); err != nil {
		return false, err
	}

	have, err := Has(p, e)
	if err != nil || have {
		return false, err
	}

	var (
		data []byte
		perm = os.FileMode(0o644)
	)

	if fi, serr := os.Stat(p); serr == nil {
		perm = fi.Mode().Perm()

		if data, err = os.ReadFile(p); err != nil {
			return false, ierrors.Wrap(ierrors.Generic, "read "+p, err)
		}
	}

	if len(data) > 0 {
		if data[len(data)-1] != '\n' {
			data = append(data, '\n')
		}

		data = append(data, '\n')
	}

	if e.Comment != "" {
		data = append(data, "# "+e.Comment+"\n"...)
	}

	data = append(data, e.Pattern+"\n"...)

	return true, fsx.WriteFileAtomic(p, data, perm)
}
