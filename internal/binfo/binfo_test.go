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

package binfo_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/piusalfred/gotools/internal/binfo"
	ierrors "github.com/piusalfred/gotools/internal/errors"
)

type fakeReader map[string]binfo.Info // by file name

func (f fakeReader) Read(path string) (binfo.Info, error) {
	i, ok := f[filepath.Base(path)]
	if !ok {
		return binfo.Info{}, errors.New("not a go binary")
	}

	if i.Module == "" {
		return binfo.Info{}, binfo.ErrNoModule
	}

	return i, nil
}

func TestScan(t *testing.T) {
	dir := t.TempDir()

	for name, mode := range map[string]os.FileMode{
		"goimports": 0o755, "devel": 0o755, "dirty": 0o755, "replaced": 0o755, "nomod": 0o755,
		"notgo": 0o755, "garbage": 0o755, "incompat": 0o755, "plain.txt": 0o644, "gobad": 0o755,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), mode); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A symlink to a binary is a binary.
	if err := os.Symlink(filepath.Join(dir, "goimports"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	good := binfo.Info{Package: "golang.org/x/tools/cmd/goimports", Module: "golang.org/x/tools", Version: "v0.30.0"}
	r := fakeReader{
		"goimports": good,
		"link":      good,
		"devel":     {Package: "example.com/m/cmd/devel", Module: "example.com/m", Version: "(devel)"},
		"dirty": {
			Package: "example.com/m/cmd/dirty",
			Module:  "example.com/m",
			Version: "v1.2.3-0.20250101000000-abcdef123456+dirty",
		},
		"replaced": {
			Package:  "example.com/m/cmd/replaced",
			Module:   "example.com/m",
			Version:  "v1.0.0",
			Replaced: true,
		},
		"nomod":    {},
		"garbage":  {Package: "example.com/m/cmd/garbage", Module: "example.com/m", Version: "latest"},
		"incompat": {Package: "example.com/m/cmd/incompat", Module: "example.com/m", Version: "v2.0.0+incompatible"},
	}

	got, err := binfo.Scan(dir, r)
	if err != nil {
		t.Fatal(err)
	}

	skip := map[string]string{}
	names := []string{}

	for _, c := range got {
		names = append(names, c.Name)
		skip[c.Name] = c.Skip
	}

	want := []string{
		"devel",
		"dirty",
		"garbage",
		"gobad",
		"goimports",
		"incompat",
		"link",
		"nomod",
		"notgo",
		"replaced",
	}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}

	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v", names, want)
		}
	}

	for name, importable := range map[string]bool{
		"goimports": true, "link": true, "incompat": true,
		"devel": false, "dirty": false, "replaced": false, "nomod": false, "notgo": false, "garbage": false, "gobad": false,
	} {
		if (skip[name] == "") != importable {
			t.Errorf("%s: Skip = %q, importable should be %v", name, skip[name], importable)
		}
	}

	for _, c := range got {
		if c.Name == "goimports" && c.Spec() != "golang.org/x/tools/cmd/goimports@v0.30.0" {
			t.Errorf("Spec = %s", c.Spec())
		}
	}
}

func TestScanMissingDirectory(t *testing.T) {
	_, err := binfo.Scan(filepath.Join(t.TempDir(), "nope"), fakeReader{})
	if ierrors.CodeOf(err) != ierrors.Environment {
		t.Fatalf("err = %v", err)
	}
}

// The real reader reads a real Go binary and refuses a file that is not one.
func TestFileReader(t *testing.T) {
	exe, err := os.Executable() // the test binary itself
	if err != nil {
		t.Fatal(err)
	}

	info, err := binfo.FileReader{}.Read(exe)
	if err != nil {
		t.Fatal(err)
	}

	if info.Module != "github.com/piusalfred/gotools" || info.Package == "" {
		t.Fatalf("info = %+v", info)
	}

	text := filepath.Join(t.TempDir(), "text")
	if err := os.WriteFile(text, []byte("hello"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := (binfo.FileReader{}).Read(text); err == nil {
		t.Fatal("read a text file")
	}
}
