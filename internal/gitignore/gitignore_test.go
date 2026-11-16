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

package gitignore_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piusalfred/gotools/internal/gitignore"
)

func write(t *testing.T, p, s string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindRepo(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")

	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, ok := gitignore.FindRepo(sub); ok {
		t.Fatal("found a repository where there is none")
	}

	// A .git file (worktree, submodule) counts as much as a directory.
	write(t, filepath.Join(root, ".git"), "gitdir: elsewhere\n")

	if got, ok := gitignore.FindRepo(sub); !ok || got != root {
		t.Fatalf("FindRepo = %q, %v; want %q", got, ok, root)
	}
}

func TestIgnored(t *testing.T) {
	const name = ".gotools.flock"

	for _, tc := range []struct {
		desc  string
		files map[string]string // relative to the repository root
		dir   string            // relative to the repository root
		want  bool
	}{
		{"nothing", nil, "tools", false},
		{"basename pattern", map[string]string{".gitignore": name + "\n"}, "tools", true},
		{"anchored at the root", map[string]string{".gitignore": "/" + name + "\n"}, "tools", false},
		{"anchored at its directory", map[string]string{".gitignore": "/tools/" + name + "\n"}, "tools", true},
		{"any depth", map[string]string{".gitignore": "**/" + name + "\n"}, "a/tools", true},
		{"glob", map[string]string{".gitignore": ".gotools.*\n"}, "tools", true},
		{"comment is not a rule", map[string]string{".gitignore": "# " + name + "\n"}, "tools", false},
		{"negated later", map[string]string{".gitignore": name + "\n!" + name + "\n"}, "tools", false},
		{"nested file wins", map[string]string{".gitignore": name + "\n", "tools/.gitignore": "!" + name + "\n"}, "tools", false},
		{"info/exclude", map[string]string{".git/info/exclude": name + "\n"}, "tools", true},
		{"unrelated name", map[string]string{".gitignore": "other\n"}, "tools", false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			repo := t.TempDir()

			for f, body := range tc.files {
				write(t, filepath.Join(repo, f), body)
			}

			if got := gitignore.Ignored(repo, filepath.Join(repo, tc.dir), name); got != tc.want {
				t.Fatalf("Ignored = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAdd(t *testing.T) {
	e := gitignore.Entry{Pattern: ".gotools.flock", Comment: "local lock"}
	p := filepath.Join(t.TempDir(), ".gitignore")

	added, err := gitignore.Add(p, e)
	if err != nil || !added {
		t.Fatalf("Add on a missing file = %v, %v", added, err)
	}

	if got := read(t, p); got != "# local lock\n.gotools.flock\n" {
		t.Fatalf("file = %q", got)
	}

	// Twice is once.
	if added, err = gitignore.Add(p, e); err != nil || added {
		t.Fatalf("second Add = %v, %v", added, err)
	}

	// A file with no final newline keeps its last line intact.
	write(t, p, "bin/")

	if _, err = gitignore.Add(p, e); err != nil {
		t.Fatal(err)
	}

	if got := read(t, p); got != "bin/\n\n# local lock\n.gotools.flock\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestAddKeepsPermissionsAndRefusesSymlinks(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".gitignore")
	write(t, p, "x\n")

	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := gitignore.Add(p, gitignore.Entry{Pattern: "y"}); err != nil {
		t.Fatal(err)
	}

	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}

	link := filepath.Join(dir, "link", ".gitignore")
	target := filepath.Join(dir, "target")
	write(t, target, "")

	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if _, err := gitignore.Add(link, gitignore.Entry{Pattern: "y"}); err == nil ||
		!strings.Contains(err.Error(), "symlink") {
		t.Fatalf("err = %v", err)
	}

	if read(t, target) != "" {
		t.Fatal("wrote through the symlink")
	}
}

func read(t *testing.T, p string) string {
	t.Helper()

	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}
