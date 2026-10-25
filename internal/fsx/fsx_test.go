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

package fsx_test

import (
	"os"
	"path/filepath"
	"testing"

	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/fsx"
)

func TestWriteFileAtomic(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")

	for _, content := range []string{"one", "two-longer"} {
		if err := fsx.WriteFileAtomic(p, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}

		b, _ := os.ReadFile(p)
		if string(b) != content {
			t.Fatalf("got %q", b)
		}
	}

	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o640 {
		t.Fatalf("perm %v", fi.Mode().Perm())
	}

	if ents, _ := os.ReadDir(filepath.Dir(p)); len(ents) != 1 {
		t.Fatalf("temp file left behind: %v", ents)
	}

	if err := fsx.WriteFileAtomic(filepath.Join(t.TempDir(), "no", "dir", "f"), nil, 0o600); err == nil {
		t.Fatal("missing directory accepted")
	}
}

func TestWriteFileAtomicIgnoresAPlantedSymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	path := filepath.Join(dir, "f")

	_ = os.Symlink(victim, path+".tmp.planted")

	if err := fsx.WriteFileAtomic(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(victim); err == nil {
		t.Fatal("the write followed a planted symlink")
	}
}

func TestRejectSymlink(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	link := filepath.Join(dir, "link")
	dangling := filepath.Join(dir, "dangling")

	_ = os.WriteFile(file, nil, 0o644)
	_ = os.Symlink(file, link)
	_ = os.Symlink(filepath.Join(dir, "nowhere"), dangling)

	for _, ok := range []string{file, filepath.Join(dir, "absent"), dir} {
		if err := fsx.RejectSymlink(ok, "install x"); err != nil {
			t.Errorf("%s rejected: %v", ok, err)
		}
	}

	for _, bad := range []string{link, dangling} {
		err := fsx.RejectSymlink(bad, "install x")
		if ierrors.CodeOf(err) != ierrors.Environment || err.Error() == "" {
			t.Errorf("%s: %v", bad, err)
		}
	}

	if err := fsx.RejectSymlink(link, ""); err == nil || len(err.Error()) == 0 {
		t.Fatalf("no context: %v", err)
	}
}

func TestLexists(t *testing.T) {
	dir := t.TempDir()
	dangling := filepath.Join(dir, "dangling")

	_ = os.Symlink(filepath.Join(dir, "nowhere"), dangling)

	if !fsx.Lexists(dir) || !fsx.Lexists(dangling) || fsx.Lexists(filepath.Join(dir, "absent")) {
		t.Fatal("Lexists must see a dangling symlink and nothing else that is absent")
	}
}

func TestSnapshotRestoresChangedDeletedAndCreatedFiles(t *testing.T) {
	dir := t.TempDir()
	changed, deleted, created := filepath.Join(dir, "changed"), filepath.Join(dir, "deleted"), filepath.Join(dir, "created")

	for f, body := range map[string]string{changed: "before", deleted: "keep me"} {
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	snap := fsx.TakeSnapshot(changed, deleted, created)

	if err := os.WriteFile(changed, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(deleted); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(created, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	snap.Restore()

	if b, _ := os.ReadFile(changed); string(b) != "before" {
		t.Errorf("changed = %q", b)
	}

	if b, _ := os.ReadFile(deleted); string(b) != "keep me" {
		t.Errorf("deleted = %q", b)
	}

	if _, err := os.Stat(created); err == nil {
		t.Error("a file that did not exist was left behind")
	}
}
